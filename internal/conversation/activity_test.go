package conversation

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/kubemoot/kmctl/internal/discussion"
)

var busyEvent = discussion.Event{Type: "activity", At: "2026-10-15T12:04:37Z", Agents: []discussion.Activity{
	{Agent: "compute", Doing: "evaluating", GPU: "ollama-rig0"},
	{Agent: "rules", Doing: "waiting_for_accelerator", Model: "qwen3:14b", GPU: "ollama-rig1"},
	{Agent: "coordinator", Doing: "waiting_for_agents"},
	{Agent: "next", Doing: "some_new_step"},
}}

func TestBusyText(t *testing.T) {
	want := "compute evaluating on ollama-rig0, rules waiting for an accelerator for qwen3:14b, " +
		"coordinator waiting for agents, next some new step"
	if got := busyText(busyEvent.Agents); got != want {
		t.Errorf("busyText = %q, want %q", got, want)
	}
	if got := busyText(nil); got != "" {
		t.Errorf("busyText(nil) = %q", got)
	}
}

func TestTrackerActivityFillsRowsAndClearsOnTheAnswer(t *testing.T) {
	tr := NewTracker(testOptions(ModePlain).start())
	tr.Apply(discussion.Event{Type: "phase", Agent: "compute", Status: "evaluating"}, tr.Start)
	tr.Apply(busyEvent, tr.Start)
	if len(tr.Busy) != 4 || tr.byName["compute"].GPU != "ollama-rig0" {
		t.Fatalf("busy = %+v, compute = %+v", tr.Busy, tr.byName["compute"])
	}
	if _, ok := tr.byName["coordinator"]; ok {
		t.Error("an activity reading adds no agent rows")
	}
	tr.Apply(discussion.Event{Type: "synthesis", Content: "done"}, tr.Start)
	tr.Apply(busyEvent, tr.Start)
	if tr.Busy != nil {
		t.Errorf("busy after the answer: %+v", tr.Busy)
	}
}

func TestLiveModelHeaderSaysWhoIsBusy(t *testing.T) {
	m := feed(newLiveModel(testOptions(ModeFull)),
		discussion.Event{Type: "phase", Agent: "compute", Status: "evaluating"}, busyEvent)
	if header := ansi.Strip(m.header()); !strings.Contains(header, "| busy: compute evaluating on ollama-rig0") {
		t.Errorf("header = %q", header)
	}
	if row := ansi.Strip(m.row(m.t.Agents()[0])); !strings.Contains(row, m.spin.View()) {
		t.Errorf("a busy agent's row keeps its spinner: %q", row)
	}
	m.finished = true
	if header := ansi.Strip(m.header()); strings.Contains(header, "busy") {
		t.Errorf("a finished view says no one is busy: %q", header)
	}
}

func TestPlainAndJSONShowActivity(t *testing.T) {
	stream := sseEvent(`{"type":"activity","at":"2026-10-15T12:04:37Z","agents":[{"agent":"compute","doing":"evaluating","gpu":"ollama-rig0"}]}`) +
		sseEvent(`{"type":"heartbeat"}`) + sseEvent(`{"type":"done"}`)

	var plain bytes.Buffer
	if err := Render(&plain, strings.NewReader(stream), testOptions(ModePlain)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plain.String(), "] busy: compute evaluating on ollama-rig0\n") {
		t.Errorf("plain has no busy line:\n%s", plain.String())
	}

	var js bytes.Buffer
	if err := Render(&js, strings.NewReader(stream), testOptions(ModeJSON)); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, line := range strings.Split(strings.TrimSpace(js.String()), "\n") {
		var e struct {
			Type   string                `json:"type"`
			At     string                `json:"at"`
			Agents []discussion.Activity `json:"agents"`
		}
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("not JSON: %s", line)
		}
		if e.Type == "activity" {
			found = e.At == "2026-10-15T12:04:37Z" && len(e.Agents) == 1 && e.Agents[0].GPU == "ollama-rig0"
		}
	}
	if !found {
		t.Errorf("json has no activity line with its agents:\n%s", js.String())
	}
}

func TestReattach(t *testing.T) {
	if got := Reattach("homelab-pilot", "crew-homelab-pilot", "c-1"); got != "kmctl conversation watch homelab-pilot c-1 -n crew-homelab-pilot" {
		t.Errorf("Reattach = %q", got)
	}
	if got := Reattach("demo", "", "c-1"); got != "kmctl conversation watch demo c-1" {
		t.Errorf("Reattach without a namespace = %q", got)
	}
}
