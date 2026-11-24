package conversation

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kubemoot/kmctl/internal/discussion"
)

func TestTracker_StoppedSynthesisIsNotAnAnswer(t *testing.T) {
	tr := NewTracker(time.Now())
	tr.Apply(discussion.Event{Type: "synthesis", Content: "Stopped by the person who asked.", Outcome: "stopped"}, time.Now())
	if tr.Answer != "" || !tr.Stopped || tr.StoppedText == "" {
		t.Errorf("answer %q stopped %v text %q", tr.Answer, tr.Stopped, tr.StoppedText)
	}
}

func TestTracker_StoppedDoneAloneStopsTheTurn(t *testing.T) {
	tr := NewTracker(time.Now())
	tr.Apply(discussion.Event{Type: "done", Outcome: "stopped"}, time.Now())
	if !tr.Stopped {
		t.Error("a done event with outcome stopped stops the turn")
	}
}

func TestTracker_AnAnswerIsNotStopped(t *testing.T) {
	tr := NewTracker(time.Now())
	tr.Apply(discussion.Event{Type: "synthesis", Content: "All good."}, time.Now())
	tr.Apply(discussion.Event{Type: "done"}, time.Now())
	if tr.Answer != "All good." || tr.Stopped {
		t.Errorf("answer %q stopped %v", tr.Answer, tr.Stopped)
	}
}

func TestRender_StoppedTurnPerMode(t *testing.T) {
	stream := `data: {"type":"synthesis","content":"Stopped.","outcome":"stopped"}

data: {"type":"done","outcome":"stopped"}

`
	for _, mode := range []Mode{ModePlain, ModeJSON, ModeQuiet} {
		var out bytes.Buffer
		err := Render(&out, strings.NewReader(stream), Options{Mode: mode, Crew: "c", ConversationID: "id"})
		if !errors.Is(err, ErrStopped) {
			t.Errorf("mode %v: err = %v, want ErrStopped", mode, err)
		}
		if strings.Contains(out.String(), "ANSWER:") || (mode == ModeQuiet && out.Len() != 0) {
			t.Errorf("mode %v shows the stopped turn as an answer:\n%s", mode, out.String())
		}
	}
}

func TestQueueText(t *testing.T) {
	for position, want := range map[int]string{0: "", -1: "", 1: "queued, next to be taken", 3: "queued, 2 questions ahead"} {
		if got := QueueText(position); got != want {
			t.Errorf("QueueText(%d) = %q, want %q", position, got, want)
		}
	}
	var q *QueueStatus
	if q.Position() != 0 {
		t.Error("a nil status has no position")
	}
}
