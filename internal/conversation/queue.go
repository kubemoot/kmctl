package conversation

import (
	"fmt"
	"sync"
)

// QueueStatus is where the question stands in the crew's request queue while it waits there. The
// command that polls the gateway's state read sets it; the live view shows it, so a question that
// waits behind others is not shown as silence.
type QueueStatus struct {
	mu       sync.Mutex
	position int
}

// Set records the position: 1 is the next question to be taken, 0 means none waits.
func (q *QueueStatus) Set(position int) {
	q.mu.Lock()
	q.position = position
	q.mu.Unlock()
}

// Position is the last position set, or 0.
func (q *QueueStatus) Position() int {
	if q == nil {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.position
}

// QueueText says where a waiting question stands: "queued, next to be taken", or "queued, 2
// questions ahead". It is empty for position 0.
func QueueText(position int) string {
	switch {
	case position <= 0:
		return ""
	case position == 1:
		return "queued, next to be taken"
	}
	return fmt.Sprintf("queued, %d questions ahead", position-1)
}
