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

// A malformed timestamp is ignored: the step is timed by receipt.
func TestTracker_BadTimestampFallsBackToReceipt(t *testing.T) {
	tr := NewTracker(testStart)
	tr.Apply(discussion.Event{Type: "phase", Agent: "a", Status: "triaging", Timestamp: "soon"}, testStart.Add(time.Second))
	c := tr.Apply(discussion.Event{Type: "phase", Agent: "a", Status: "evaluating", Timestamp: "later"}, testStart.Add(3*time.Second))
	if c.Triage != 2*time.Second || tr.Stamped {
		t.Errorf("triage = %s, stamped %v", c.Triage, tr.Stamped)
	}
}

func TestTracker_SubcommitteeOfEveryAgent(t *testing.T) {
	tr := NewTracker(testStart)
	tr.Apply(discussion.Event{Type: "subcommittee"}, testStart)
	if tr.Subcommittee != nil {
		t.Fatal("an event without content records nothing")
	}
	tr.Apply(discussion.Event{Type: "subcommittee", Subcommittee: &discussion.Subcommittee{AllAgents: true}}, testStart)
	if got := tr.Summarize(SinceAsk).subcommitteeLine(); got != "Subcommittee: none convened; every agent could take part" {
		t.Errorf("got %q", got)
	}
	if got := subcommitteeText(nil); got != "subcommittee" {
		t.Errorf("got %q", got)
	}
}

func TestToolError(t *testing.T) {
	cases := []struct {
		in   discussion.SignalMeta
		want string
	}{
		{discussion.SignalMeta{FailureType: "model_timeout"}, ""},
		{discussion.SignalMeta{FailureType: "model_timeout", LastError: "read timed out"}, "model_timeout: read timed out"},
		{discussion.SignalMeta{FailedTool: "pods_log", FailureCount: 1}, "pods_log"},
		{discussion.SignalMeta{FailedTool: "pods_log", FailureCount: 2, LastError: "403"}, "pods_log failed 2 times: 403"},
	}
	for _, c := range cases {
		if got := toolError(&c.in); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.in, got, c.want)
		}
	}
}

// A step whose start is a published time and whose end is a receipt time (or the
// other way round) is not timed: the two clocks differ.
func TestTracker_MixedClocksDoNotTimeAStep(t *testing.T) {
	tr := NewTracker(testStart)
	tr.Apply(discussion.Event{Type: "phase", Agent: "a", Status: "triaging", Timestamp: "2026-10-15T08:00:00Z"}, testStart.Add(time.Second))
	c := tr.Apply(discussion.Event{Type: "phase", Agent: "a", Status: "evaluating"}, testStart.Add(3*time.Second))
	if c.Triage != 0 {
		t.Errorf("triage across clocks = %s, want none", c.Triage)
	}
	tr.Apply(discussion.Event{Type: "thread_found", ThreadID: "t", Timestamp: "bad"}, testStart)
	if !tr.ThreadAt.IsZero() {
		t.Error("a thread start without a valid timestamp has no published time")
	}
}

// A later subcommittee event keeps the brief, technologies, and reasons of an
// earlier one when it does not carry its own.
func TestTracker_SubcommitteeKeepsEarlierDetail(t *testing.T) {
	tr := NewTracker(testStart)
	tr.Apply(discussion.Event{Type: "subcommittee", Subcommittee: &discussion.Subcommittee{
		Source: "advisory", Brief: "Check the nodes.", Technologies: []string{"proxmox"},
		Members: []discussion.SubcommitteeMember{{Agent: "a", Reason: "nodes"}, {Agent: "b"}},
	}}, testStart)
	tr.Apply(discussion.Event{Type: "subcommittee", Subcommittee: &discussion.Subcommittee{
		Source: "triage", Members: []discussion.SubcommitteeMember{{Agent: "a"}, {Agent: "c", Reason: "disks"}},
	}}, testStart)
	sc := tr.Subcommittee
	if sc.Brief != "Check the nodes." || len(sc.Technologies) != 1 || sc.Members[0].Reason != "nodes" || sc.Members[1].Reason != "disks" || len(sc.Members) != 2 {
		t.Errorf("got %+v", sc)
	}
}

func TestSmallHelpers(t *testing.T) {
	if tokensText(0) != "" || tokensText(-5) != "" || tokensText(1234) != "1,234 tokens" {
		t.Error("tokensText")
	}
	if artifactOf(nil, "plain text") != nil || artifactOf(&discussion.SignalMeta{Artifact: &discussion.ArtifactRef{}}, "") != nil {
		t.Error("no artifact without a key")
	}
	if a := artifactOf(&discussion.SignalMeta{Artifact: &discussion.ArtifactRef{Key: "k", Bytes: 3}}, ""); a == nil || a.Key != "k" {
		t.Error("artifact from metadata")
	}
}

func TestSummaryHelpers(t *testing.T) {
	if timeSource(true) != "published" || timeSource(false) != "received" {
		t.Error("timeSource")
	}
	s := Summary{Artifacts: []ArtifactReport{{Key: "k"}}}.WithFetchCommands("crew", "")
	if s.Artifacts[0].Fetch != "kmctl conversation artifact crew k" {
		t.Errorf("fetch without a namespace: %q", s.Artifacts[0].Fetch)
	}
}
