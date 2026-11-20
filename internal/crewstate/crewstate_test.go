package crewstate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const noModel = "coordinator c is unschedulable: no model in the cluster catalog (2 available) satisfies the require of phase=mulling"

// crew builds a Crew map. capacity is "" for a Kubemoot without the condition.
func crew(generation, observed int64, phase string, ready bool, capacity, reason, message string) map[string]any {
	status := map[string]any{"phase": phase, "ready": ready}
	if observed > 0 {
		status["observedGeneration"] = observed
	}
	if capacity != "" {
		status["conditions"] = []any{
			map[string]any{"type": "Ready", "status": "True"},
			map[string]any{"type": ConditionCapacity, "status": capacity, "reason": reason, "message": message},
		}
	}
	return map[string]any{"metadata": map[string]any{"generation": generation}, "status": status}
}

func withStatusMessage(obj map[string]any, message string) map[string]any {
	obj["status"].(map[string]any)["message"] = message
	return obj
}

func TestCanAnswer(t *testing.T) {
	cases := map[string]struct {
		obj  map[string]any
		ok   bool
		want string // substring of what it waits for
	}{
		"available":              {crew(3, 3, "Ready", true, "True", "ModelAvailable", ""), true, ""},
		"no model":               {crew(3, 3, "WaitingForCapacity", true, "False", "NoModelAvailable", noModel), false, "waiting for an accelerator: " + noModel},
		"capacity false only":    {crew(3, 3, "Ready", true, "False", "NoModelAvailable", noModel), false, "waiting for an accelerator"},
		"coordinator not run":    {crew(3, 3, "Pending", false, "Unknown", "CoordinatorNotRunning", ""), false, "waiting for the crew to reconcile"},
		"capacity unknown ready": {crew(3, 3, "Ready", true, "Unknown", "CoordinatorNotRunning", ""), false, "waiting for the coordinator to run"},
		"stale status":           {crew(4, 3, "Ready", true, "True", "ModelAvailable", ""), false, "latest change"},
		"reconcile error":        {crew(3, 3, "Error", false, "", "", ""), false, "failed to reconcile"},
		"0.95 ready":             {crew(3, 0, "Ready", true, "", "", ""), true, ""},
		"0.95 pending":           {crew(3, 0, "Pending", false, "", "", ""), false, "waiting for the crew to reconcile"},
		"0.95 no phase":          {crew(3, 0, "", true, "", "", ""), true, ""},
		"waiting phase alone":    {withStatusMessage(crew(3, 3, "WaitingForCapacity", true, "", "", ""), "no model"), false, "waiting for an accelerator: no model"},
		"odd phase":              {crew(3, 3, "Draining", true, "", "", ""), false, "phase Draining"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			ok, why := Read(c.obj).CanAnswer()
			if ok != c.ok || !strings.Contains(why, c.want) {
				t.Errorf("CanAnswer = %v, %q; want %v containing %q", ok, why, c.ok, c.want)
			}
		})
	}
}

func TestReconciledIgnoresCapacity(t *testing.T) {
	waiting := crew(3, 3, "WaitingForCapacity", true, "False", "NoModelAvailable", noModel)
	if ok, why := Read(waiting).Reconciled(); !ok {
		t.Errorf("a crew waiting for capacity is reconciled; got %q", why)
	}
	if ok, _ := Read(crew(4, 3, "Ready", true, "True", "", "")).Reconciled(); ok {
		t.Error("a status of an older generation is not reconciled")
	}
	if ok, _ := Read(crew(3, 0, "Ready", true, "", "", "")).Reconciled(); !ok {
		t.Error("0.95 status without observedGeneration is reconciled when ready")
	}
	if ok, _ := Read(map[string]any{}).Reconciled(); ok {
		t.Error("an empty status is not reconciled")
	}
}

// sequence serves the crews in order, repeating the last one, and counts the reads.
func sequence(objs ...map[string]any) (Getter, *int) {
	n := 0
	return func(context.Context) (*unstructured.Unstructured, error) {
		i := min(n, len(objs)-1)
		n++
		return &unstructured.Unstructured{Object: objs[i]}, nil
	}, &n
}

func TestWaitThenAvailable(t *testing.T) {
	get, reads := sequence(
		crew(1, 1, "Pending", false, "", "", ""),
		crew(1, 1, "WaitingForCapacity", true, "False", "NoModelAvailable", noModel),
		crew(1, 1, "WaitingForCapacity", true, "False", "NoModelAvailable", noModel),
		crew(1, 1, "Ready", true, "True", "ModelAvailable", ""),
	)
	var said []string
	st, err := Wait(context.Background(), get, ForAnswer, Options{Interval: time.Millisecond, Progress: func(s string) { said = append(said, s) }})
	if err != nil || st.Phase != PhaseReady {
		t.Fatalf("Wait = %+v, %v", st, err)
	}
	if *reads != 4 {
		t.Errorf("reads = %d, want 4", *reads)
	}
	if len(said) != 2 || said[1] != "waiting for an accelerator: "+noModel {
		t.Errorf("progress said %q: each change once, the repeat not again", said)
	}
}

func TestWaitNeverAvailableNamesTheMessage(t *testing.T) {
	get, _ := sequence(crew(1, 1, "WaitingForCapacity", true, "False", "NoModelAvailable", noModel))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := Wait(ctx, get, ForAnswer, Options{Interval: 5 * time.Millisecond})
	if !IsNotReady(err) || !strings.Contains(err.Error(), "waiting for an accelerator: "+noModel) {
		t.Fatalf("err = %v, want not-ready naming the scheduler's message", err)
	}
}

func TestWaitReconciledDoesNotWaitForCapacity(t *testing.T) {
	get, reads := sequence(crew(1, 1, "WaitingForCapacity", true, "False", "NoModelAvailable", noModel))
	if _, err := Wait(context.Background(), get, ForReconciled, Options{Interval: time.Millisecond}); err != nil || *reads != 1 {
		t.Errorf("err = %v after %d reads, want one read", err, *reads)
	}
}

func TestWait095Shape(t *testing.T) {
	get, _ := sequence(crew(1, 0, "Pending", false, "", "", ""), crew(1, 0, "Ready", true, "", "", ""))
	if _, err := Wait(context.Background(), get, ForAnswer, Options{Interval: time.Millisecond}); err != nil {
		t.Errorf("a 0.95 crew that is Ready can answer: %v", err)
	}
}

func TestWaitReadErrorIsReturned(t *testing.T) {
	boom := errors.New("boom")
	_, err := Wait(context.Background(), func(context.Context) (*unstructured.Unstructured, error) { return nil, boom }, ForAnswer, Options{})
	if !errors.Is(err, boom) || IsNotReady(err) {
		t.Errorf("err = %v, want the read error", err)
	}
}
