package discussion

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kubemoot/kubemoot/operator/pkg/bus"
	"k8s.io/client-go/rest"
)

// State reads a conversation's state from the crew's gateway: the conversation as a whole and
// each turn (queued with its position, running, paused, stopped or finished). It reads what the
// bus holds, so it also answers for a conversation another client started.
func State(ctx context.Context, rc rest.Interface, crew, conversationID string) (bus.ConversationState, error) {
	var state bus.ConversationState
	raw, err := rc.Get().Suffix("api", "v1", "discussions", crew, conversationID, "state").DoRaw(ctx)
	if err != nil {
		return state, fmt.Errorf("read the state of conversation %s of crew %q: %w", conversationID, crew, err)
	}
	if err := json.Unmarshal(raw, &state); err != nil || state.ConversationID == "" {
		return state, errors.New("the gateway's answer is not a conversation state (a gateway older than this kmctl has no state read)")
	}
	return state, nil
}

// QueuePosition is where a conversation's first waiting question stands in the crew's queue, or
// 0 when none waits. 1 is the next to be taken when the crew is idle.
func QueuePosition(state bus.ConversationState) int {
	for _, t := range state.Turns {
		if t.State == bus.TurnStateQueued {
			return int(t.QueuePosition)
		}
	}
	return 0
}
