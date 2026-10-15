package conversation

import (
	"testing"
	"time"

	"github.com/kubemoot/kmctl/internal/discussion"
)

func TestTracker_StepDurations(t *testing.T) {
	tr := NewTracker(testStart)
	at := func(s int) time.Time { return testStart.Add(time.Duration(s) * time.Second) }
	tr.Apply(discussion.Event{Type: "phase", Agent: "a", Status: "triaging", GPU: "g0"}, at(1))
	c := tr.Apply(discussion.Event{Type: "phase", Agent: "a", Status: "evaluating"}, at(4))
	if c.Triage != 3*time.Second {
		t.Errorf("triage = %s, want 3s", c.Triage)
	}
	c = tr.Apply(discussion.Event{Type: "finding", Agent: "a", Signal: "agree", Content: "ok"}, at(10))
	if c.Evaluation != 6*time.Second || c.Agent.GPU != "g0" {
		t.Errorf("evaluation = %s on %q, want 6s on g0", c.Evaluation, c.Agent.GPU)
	}
	tr.Apply(discussion.Event{Type: "phase", Agent: "b", Status: "triaging"}, at(2))
	c = tr.Apply(discussion.Event{Type: "phase", Agent: "b", Status: "done", StoodAside: true, Reason: "not relevant"}, at(5))
	if c.Triage != 3*time.Second || c.Agent.Signal != signalStandAside {
		t.Errorf("standing aside after triage closes the triage step: %+v", c)
	}
	if !tr.FirstSignal.Equal(at(10)) {
		t.Errorf("first signal at %s, want the first finding", tr.FirstSignal)
	}
}

func TestTracker_NoStartNoDuration(t *testing.T) {
	tr := NewTracker(testStart)
	c := tr.Apply(discussion.Event{Type: "finding", Agent: "a", Signal: "concern", Summary: "hmm"}, testStart.Add(time.Second))
	if c.Triage != 0 || c.Evaluation != 0 {
		t.Errorf("a signal without a seen step has no duration: %+v", c)
	}
	if c.Agent.Summary != "hmm" {
		t.Errorf("a concern without content uses its summary: %q", c.Agent.Summary)
	}
}

func TestTracker_IgnoresUnknownEvents(t *testing.T) {
	tr := NewTracker(testStart)
	if c := tr.Apply(discussion.Event{Type: "something-new"}, testStart); c.Agent != nil {
		t.Errorf("an unknown event changes nothing: %+v", c)
	}
	if len(tr.Agents()) != 0 {
		t.Error("an unknown event adds no agent")
	}
}

func TestTracker_RepeatedStepsDoNotGoStale(t *testing.T) {
	tr := NewTracker(testStart)
	at := func(s int) time.Time { return testStart.Add(time.Duration(s) * time.Second) }
	tr.Apply(discussion.Event{Type: "phase", Agent: "a", Status: "triaging"}, at(1))
	tr.Apply(discussion.Event{Type: "phase", Agent: "a", Status: "evaluating"}, at(2))
	tr.Apply(discussion.Event{Type: "finding", Agent: "a", Signal: "concern", Summary: "first"}, at(3))
	tr.Apply(discussion.Event{Type: "phase", Agent: "a", Status: "triaging"}, at(10))
	c := tr.Apply(discussion.Event{Type: "finding", Agent: "a", Signal: "agree", Content: "second"}, at(12))
	if c.Evaluation != 0 || c.Triage != 2*time.Second {
		t.Errorf("a second round closes its own triage, not the old evaluation: %+v", c)
	}
	c = tr.Apply(discussion.Event{Type: "phase", Agent: "a", Status: "evaluating"}, at(13))
	if c.Triage != 0 {
		t.Errorf("evaluating without an open triage has no triage duration: %s", c.Triage)
	}
	if got := tr.signalCounts(); len(got) != 1 || got["agree"] != 1 {
		t.Errorf("an agent that signals again is counted once, by its latest signal: %v", got)
	}
}

func TestTracker_WaitReasonIsNotStandAsideReason(t *testing.T) {
	tr := NewTracker(testStart)
	tr.Apply(discussion.Event{Type: "phase", Agent: "a", Status: "waiting", Model: "m", Reason: "gpu-busy"}, testStart)
	c := tr.Apply(discussion.Event{Type: "finding", Agent: "a", Signal: "agree", Content: ""}, testStart)
	if c.Agent.Reason != "" || c.Agent.WaitReason != "gpu-busy" || !c.Agent.Waited {
		t.Errorf("the wait reason is kept apart: %+v", c.Agent)
	}
	if rowNote(c.Agent) != "" {
		t.Errorf("a finished agent does not show its old wait reason: %q", rowNote(c.Agent))
	}
}
