package discussion

import (
	"testing"

	"github.com/kubemoot/kubemoot/operator/pkg/bus"
)

func TestQueuePosition(t *testing.T) {
	running := bus.TurnStatus{State: bus.TurnStateRunning}
	queued := bus.TurnStatus{State: bus.TurnStateQueued, QueuePosition: 3}
	cases := map[string][]bus.TurnStatus{"none": nil, "running only": {running}}
	for name, turns := range cases {
		if got := QueuePosition(bus.ConversationState{Turns: turns}); got != 0 {
			t.Errorf("%s: position %d, want 0", name, got)
		}
	}
	if got := QueuePosition(bus.ConversationState{Turns: []bus.TurnStatus{running, queued}}); got != 3 {
		t.Errorf("position %d, want 3", got)
	}
}

func TestEvent_Stopped(t *testing.T) {
	if !(Event{Outcome: OutcomeStopped}).Stopped() || (Event{}).Stopped() {
		t.Error("only outcome stopped is a stop")
	}
}
