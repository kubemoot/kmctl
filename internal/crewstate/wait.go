package crewstate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Question is what a wait is for.
type Question int

const (
	// ForAnswer waits until the crew can answer: reconciled, phase Ready, and
	// CapacityAvailable True. Use it wherever kmctl is about to depend on an answer.
	ForAnswer Question = iota
	// ForReconciled waits only until the crew is reconciled as declared (status.ready,
	// current generation), whether or not a model is available. Use it for a rollout.
	ForReconciled
)

// DefaultInterval is how often Wait reads the crew. It paces the reads; the wait
// ends on the crew's state, and the caller's context bounds it as a safety net.
const DefaultInterval = 2 * time.Second

// Getter reads the crew's current object.
type Getter func(ctx context.Context) (*unstructured.Unstructured, error)

// Options tunes Wait.
type Options struct {
	Interval time.Duration
	// Progress is told each time what the crew waits for changes; it may be nil.
	Progress func(waiting string)
}

// ErrNotReady is wrapped by the error Wait returns when its context ends first.
var ErrNotReady = errors.New("crew not ready")

// Wait reads the crew until it satisfies q. When the context ends first it returns
// an error that names the last thing the crew was waiting for, such as the
// scheduler's message about a missing model.
func Wait(ctx context.Context, get Getter, q Question, opts Options) (State, error) {
	interval := opts.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	var last string
	for {
		obj, err := get(ctx)
		if err != nil {
			return State{}, readFailed(ctx, err, last)
		}
		st := Read(obj.Object)
		ok, why := check(st, q)
		if ok {
			return st, nil
		}
		if why != last && opts.Progress != nil {
			opts.Progress(why)
		}
		last = why
		select {
		case <-ctx.Done():
			return st, notReady(ctx, last)
		case <-time.After(interval):
		}
	}
}

// notReady is the error for a context that ended while the crew still waited.
func notReady(ctx context.Context, waiting string) error {
	return fmt.Errorf("%w: %s (%v)", ErrNotReady, waiting, ctx.Err())
}

// readFailed returns a failed read as it is, unless the context ended during it
// after the crew had been seen waiting: then the wait's own error names what for.
func readFailed(ctx context.Context, err error, waiting string) error {
	if ctx.Err() != nil && waiting != "" {
		return notReady(ctx, waiting)
	}
	return err
}

func check(st State, q Question) (bool, string) {
	if q == ForReconciled {
		return st.Reconciled()
	}
	return st.CanAnswer()
}

// IsNotReady reports whether err is the error Wait returns when its context ends
// before the crew satisfies the question.
func IsNotReady(err error) bool { return errors.Is(err, ErrNotReady) }
