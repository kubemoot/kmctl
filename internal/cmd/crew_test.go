package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/crewstate"
	"github.com/kubemoot/kmctl/internal/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
)

func TestCrewCommand_Subcommands(t *testing.T) {
	cmd := newCrewCommand(client.NewFactory())
	if cmd.Use != "crew" {
		t.Errorf("Use = %q, want %q", cmd.Use, "crew")
	}
	want := map[string]bool{"list": false, "get": false, "wait": false}
	for _, c := range cmd.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("crew missing subcommand %q", name)
		}
	}
}

func crewWithStatus(status map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kubemoot.ai/v1alpha1", "kind": "Crew",
		"metadata": map[string]any{"name": "demo", "namespace": "ns", "generation": int64(1)},
		"status":   status,
	}}
}

func noCapacity() map[string]any {
	return map[string]any{"ready": true, "phase": "WaitingForCapacity", "observedGeneration": int64(1),
		"conditions": []any{map[string]any{"type": "CapacityAvailable", "status": "False", "reason": "NoModelAvailable", "message": "no model fits"}}}
}

func answers() map[string]any {
	return map[string]any{"ready": true, "phase": "Ready", "observedGeneration": int64(1),
		"conditions": []any{map[string]any{"type": "CapacityAvailable", "status": "True", "reason": "ModelAvailable"}}}
}

func runWait(t *testing.T, dc runtime.Object, q crewstate.Question, timeout time.Duration) (stdout, stderr string, err error) {
	t.Helper()
	cmd := newCrewWaitCommand(client.NewFactory())
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	err = runCrewWait(context.Background(), fakeDyn(dc), "ns", "demo", q, timeout, cmd)
	return out.String(), errb.String(), err
}

func TestCrewWaitAnswers(t *testing.T) {
	out, _, err := runWait(t, crewWithStatus(answers()), crewstate.ForAnswer, time.Second)
	if err != nil || out != "crew/demo can answer\n" {
		t.Errorf("out %q err %v", out, err)
	}
}

func TestCrewWaitTimesOutNamingTheMessage(t *testing.T) {
	_, errOut, err := runWait(t, crewWithStatus(noCapacity()), crewstate.ForAnswer, 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "not ready after") {
		t.Fatalf("err = %v", err)
	}
	for name, got := range map[string]string{"progress": errOut, "error cause": errString(err)} {
		if !strings.Contains(got, "waiting for an accelerator: no model fits") {
			t.Errorf("%s lacks the message: %q", name, got)
		}
	}
}

func errString(err error) string {
	var b strings.Builder
	for ; err != nil; err = unwrap(err) {
		b.WriteString(err.Error() + "\n")
	}
	return b.String()
}

func unwrap(err error) error {
	u, ok := err.(interface{ Unwrap() error })
	if !ok {
		return nil
	}
	return u.Unwrap()
}

func TestCrewWaitReconciledKeepsStatusReady(t *testing.T) {
	out, _, err := runWait(t, crewWithStatus(noCapacity()), crewstate.ForReconciled, time.Second)
	if err != nil || out != "crew/demo is reconciled\n" {
		t.Errorf("out %q err %v", out, err)
	}
}

func TestCrewWaitFor(t *testing.T) {
	for _, bad := range []string{"", "ready", "capacity"} {
		if _, err := waitQuestion(bad); err == nil {
			t.Errorf("--for %q: want an error", bad)
		}
	}
	for forWhat, want := range map[string]crewstate.Question{"answer": crewstate.ForAnswer, "reconciled": crewstate.ForReconciled} {
		if got, err := waitQuestion(forWhat); err != nil || got != want {
			t.Errorf("--for %q = %v, %v", forWhat, got, err)
		}
	}
}

func TestCrewWait095Shape(t *testing.T) {
	old := map[string]any{"ready": true, "phase": "Ready"}
	if _, _, err := runWait(t, crewWithStatus(old), crewstate.ForAnswer, time.Second); err != nil {
		t.Errorf("a 0.95 crew that is Ready can answer: %v", err)
	}
}

func TestCrewWaitSequence(t *testing.T) {
	dc := fakeDyn()
	reads := 0
	dc.PrependReactor("get", "crews", func(k8stesting.Action) (bool, runtime.Object, error) {
		reads++
		if reads < 3 {
			return true, crewWithStatus(noCapacity()), nil
		}
		return true, crewWithStatus(answers()), nil
	})
	cmd := newCrewWaitCommand(client.NewFactory())
	var errb bytes.Buffer
	cmd.SetErr(&errb)
	cmd.SetOut(&bytes.Buffer{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	get := func(ctx context.Context) (*unstructured.Unstructured, error) {
		return resource.Get(ctx, dc, resource.Crew, "ns", "demo")
	}
	if _, err := crewstate.Wait(ctx, get, crewstate.ForAnswer, crewstate.Options{Interval: time.Millisecond}); err != nil || reads != 3 {
		t.Errorf("reads %d err %v", reads, err)
	}
}

func TestGetCrewShowsCapacityAndWide(t *testing.T) {
	obj := crewWithStatus(noCapacity())
	var buf bytes.Buffer
	if err := printObject(&buf, resource.Crew, obj, "wide"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"CAPACITY-REASON", "NoModelAvailable", "Capacity:  False (NoModelAvailable)", "no model fits"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("wide output lacks %q:\n%s", want, buf.String())
		}
	}
	buf.Reset()
	if err := printObject(&buf, resource.Crew, obj, ""); err != nil || strings.Contains(buf.String(), "CAPACITY-REASON") {
		t.Errorf("default output must not carry the wide column: %v\n%s", err, buf.String())
	}
	if err := checkTableOutput("wide"); err != nil || checkOutput("wide") == nil {
		t.Error("wide is a table format of get and list only")
	}
}

func TestNoteIfCannotAnswer(t *testing.T) {
	cases := map[string]struct {
		status map[string]any
		want   string
	}{
		"waiting": {noCapacity(), "waiting for an accelerator: no model fits"},
		"answers": {answers(), ""},
		"0.95":    {map[string]any{"ready": false, "phase": "Pending"}, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var w bytes.Buffer
			noteWith(&w, crewWithStatus(c.status))
			if c.want == "" && w.Len() != 0 || !strings.Contains(w.String(), c.want) {
				t.Errorf("note = %q, want %q", w.String(), c.want)
			}
		})
	}
}
