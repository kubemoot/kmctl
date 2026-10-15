package conversation

import (
	"sync"
	"time"

	"github.com/kubemoot/kmctl/internal/discussion"
)

// Timer is a pending call that Stop cancels.
type Timer interface{ Stop() bool }

// AfterFunc calls f once d has passed and returns the pending call.
type AfterFunc func(d time.Duration, f func()) Timer

// RealAfterFunc is AfterFunc on the wall clock.
func RealAfterFunc(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) }

// IdleClock calls onIdle once the crew has shown no activity for limit. Every event
// that shows crew activity (discussion.ShowsCrewActivity) starts the limit again;
// the connection's own keep-alives do not. A limit of zero or less never fires.
type IdleClock struct {
	limit  time.Duration
	after  AfterFunc
	onIdle func()

	mu      sync.Mutex
	timer   Timer
	gen     int
	fired   bool
	stopped bool
}

// NewIdleClock starts an idle clock. A nil after means RealAfterFunc.
func NewIdleClock(limit time.Duration, after AfterFunc, onIdle func()) *IdleClock {
	if after == nil {
		after = RealAfterFunc
	}
	c := &IdleClock{limit: limit, after: after, onIdle: onIdle}
	c.mu.Lock()
	c.restartLocked()
	c.mu.Unlock()
	return c
}

// Limit is how long the crew may show no activity.
func (c *IdleClock) Limit() time.Duration { return c.limit }

// Seen restarts the clock when e shows crew activity.
func (c *IdleClock) Seen(e discussion.Event) {
	if !discussion.ShowsCrewActivity(e) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.fired && !c.stopped {
		c.restartLocked()
	}
}

// Stop ends the clock without firing.
func (c *IdleClock) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = true
	if c.timer != nil {
		c.timer.Stop()
	}
}

// Fired reports whether the limit passed with no crew activity.
func (c *IdleClock) Fired() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fired
}

// restartLocked replaces the pending call with one for the full limit. A call
// already under way for an earlier start finds its generation outdated and does
// nothing.
func (c *IdleClock) restartLocked() {
	if c.limit <= 0 {
		return
	}
	if c.timer != nil {
		c.timer.Stop()
	}
	c.gen++
	gen := c.gen
	c.timer = c.after(c.limit, func() { c.fire(gen) })
}

func (c *IdleClock) fire(gen int) {
	c.mu.Lock()
	if gen != c.gen || c.fired || c.stopped {
		c.mu.Unlock()
		return
	}
	c.fired = true
	c.mu.Unlock()
	c.onIdle()
}
