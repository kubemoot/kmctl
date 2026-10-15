package conversation

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kubemoot/kmctl/internal/discussion"
)

// fakeClock runs AfterFunc calls when Advance moves its time past them.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Duration
	pending []*fakeTimer
}

type fakeTimer struct {
	at      time.Duration
	f       func()
	stopped bool
}

func (t *fakeTimer) Stop() bool {
	was := !t.stopped
	t.stopped = true
	return was
}

func (c *fakeClock) AfterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{at: c.now + d, f: f}
	c.pending = append(c.pending, t)
	return t
}

// Advance moves the clock by d and runs every call that came due, in order.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now += d
	var due, rest []*fakeTimer
	for _, t := range c.pending {
		if t.at <= c.now && !t.stopped {
			due = append(due, t)
		} else if !t.stopped {
			rest = append(rest, t)
		}
	}
	c.pending = rest
	c.mu.Unlock()
	sort.Slice(due, func(i, j int) bool { return due[i].at < due[j].at })
	for _, t := range due {
		t.f()
	}
}

const idleLimit = 3 * time.Minute

func newTestIdle(limit time.Duration) (*IdleClock, *fakeClock, *int) {
	clock := &fakeClock{}
	fired := 0
	return NewIdleClock(limit, clock.AfterFunc, func() { fired++ }), clock, &fired
}

var activityEvent = discussion.Event{Type: "activity", Agents: []discussion.Activity{{Agent: "compute", Doing: "evaluating"}}}

// A gateway reporting activity every 15 seconds keeps kmctl waiting well past the
// old five-minute wall clock.
func TestIdleClockActivityKeepsItWaiting(t *testing.T) {
	idle, clock, fired := newTestIdle(idleLimit)
	for elapsed := time.Duration(0); elapsed < 10*time.Minute; elapsed += 15 * time.Second {
		clock.Advance(15 * time.Second)
		idle.Seen(activityEvent)
	}
	if *fired != 0 || idle.Fired() {
		t.Fatal("the idle clock fired while the crew reported activity")
	}
}

func TestIdleClockFiresOnceAfterSilence(t *testing.T) {
	idle, clock, fired := newTestIdle(idleLimit)
	clock.Advance(idleLimit - time.Second)
	if idle.Fired() {
		t.Fatal("fired before the limit")
	}
	clock.Advance(time.Second)
	if !idle.Fired() || *fired != 1 {
		t.Fatalf("fired = %v (%d calls), want once at the limit", idle.Fired(), *fired)
	}
	idle.Seen(activityEvent)
	clock.Advance(2 * idleLimit)
	if *fired != 1 {
		t.Errorf("fired %d times, want once", *fired)
	}
}

// Connection keep-alives say nothing about the crew, so they do not reset the clock.
func TestIdleClockIgnoresConnectionEvents(t *testing.T) {
	idle, clock, _ := newTestIdle(idleLimit)
	for i := 0; i < 7; i++ {
		clock.Advance(30 * time.Second)
		idle.Seen(discussion.Event{Type: "heartbeat"})
		idle.Seen(discussion.Event{Type: "connected"})
	}
	if !idle.Fired() {
		t.Fatal("heartbeats kept the idle clock from firing")
	}
}

// Against a gateway that sends no activity, each signal restarts the clock.
func TestIdleClockSignalsRestartIt(t *testing.T) {
	idle, clock, _ := newTestIdle(idleLimit)
	clock.Advance(2 * time.Minute)
	idle.Seen(discussion.Event{Type: "phase", Agent: "compute", Status: "evaluating"})
	clock.Advance(2 * time.Minute)
	if idle.Fired() {
		t.Fatal("fired 2 minutes after a signal")
	}
	clock.Advance(time.Minute)
	if !idle.Fired() {
		t.Fatal("did not fire 3 minutes after the last signal")
	}
}

func TestIdleClockStopAndZeroLimitNeverFire(t *testing.T) {
	idle, clock, _ := newTestIdle(idleLimit)
	idle.Stop()
	clock.Advance(time.Hour)
	if idle.Fired() {
		t.Error("a stopped clock fired")
	}
	idle, clock, _ = newTestIdle(0)
	clock.Advance(time.Hour)
	idle.Seen(activityEvent)
	if idle.Fired() || len(clock.pending) != 0 {
		t.Error("a zero limit scheduled or fired")
	}
	if idle.Limit() != 0 {
		t.Errorf("Limit() = %s", idle.Limit())
	}
}

func TestNewIdleClockDefaultsToTheWallClock(t *testing.T) {
	done := make(chan struct{})
	idle := NewIdleClock(time.Millisecond, nil, func() { close(done) })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the wall-clock idle limit never fired")
	}
	if !idle.Fired() {
		t.Error("Fired() is false after firing")
	}
}

// Rendering a stream of activity-only events for more than five minutes of crew
// time, with the idle clock fed by OnEvent, never gives up and shows each reading.
func TestRenderWithActivityPastFiveMinutesDoesNotGiveUp(t *testing.T) {
	var sse strings.Builder
	sse.WriteString(sseEvent(`{"type":"connected"}`) + sseEvent(`{"type":"thread_found","threadId":"t-1"}`))
	for i := 0; i < 28; i++ { // 28 readings 15 seconds apart: 7 minutes
		sse.WriteString(sseEvent(`{"type":"activity","at":"2026-10-15T12:00:00Z","agents":[{"agent":"compute","doing":"evaluating","gpu":"ollama-rig0"}]}`))
	}
	sse.WriteString(sseEvent(`{"type":"synthesis","content":"Three nodes."}`) + sseEvent(`{"type":"done"}`))

	idle, clock, _ := newTestIdle(idleLimit)
	var out bytes.Buffer
	o := Options{Mode: ModePlain, Crew: "pilot", ConversationID: "c-1", Start: time.Unix(0, 0), Now: func() time.Time { return time.Unix(0, 0) }}
	o.OnEvent = func(e discussion.Event) {
		clock.Advance(15 * time.Second)
		idle.Seen(e)
	}
	if err := Render(&out, strings.NewReader(sse.String()), o); err != nil {
		t.Fatal(err)
	}
	if idle.Fired() {
		t.Fatal("the idle clock fired during seven minutes of activity")
	}
	if got := strings.Count(out.String(), "busy: compute evaluating on ollama-rig0"); got != 28 {
		t.Errorf("%d busy lines, want 28:\n%s", got, out.String())
	}
}

func sseEvent(data string) string { return fmt.Sprintf("data: %s\n\n", data) }
