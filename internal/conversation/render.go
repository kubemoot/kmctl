package conversation

import (
	"fmt"
	"io"
	"time"

	"github.com/kubemoot/kmctl/internal/discussion"
)

// Options says how to show one conversation's discussion stream.
type Options struct {
	Mode           Mode
	Crew           string
	Namespace      string
	ConversationID string
	// Announce prints the conversation ID first, for a turn kmctl just started.
	Announce bool
	// Start is when the turn began; zero means when rendering starts.
	Start time.Time
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// OnEvent, when set, sees every event as it arrives, before it is shown.
	OnEvent func(discussion.Event)
}

func (o Options) seen(e discussion.Event) {
	if o.OnEvent != nil {
		o.OnEvent(e)
	}
}

func (o Options) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func (o Options) start() time.Time {
	if o.Start.IsZero() {
		return o.now()
	}
	return o.Start
}

// since is when the turn's clock started: at the ask, or when kmctl attached.
func (o Options) since() string {
	if o.Announce {
		return SinceAsk
	}
	return SinceWatch
}

// sink writes one output mode: a start, one call per event, and an end.
type sink interface {
	begin() error
	event(t *Tracker, e discussion.Event, c Change, at time.Time) error
	finish(t *Tracker) error
}

// Render reads the discussion stream until a terminal event or its end and writes
// it to w in the mode o asks for.
func Render(w io.Writer, stream io.Reader, o Options) error {
	switch o.Mode {
	case ModeQuiet:
		return runSink(stream, o, quietSink{w: w})
	case ModeJSON:
		return runSink(stream, o, newJSONSink(w, o))
	case ModeFull:
		return runFull(w, stream, o)
	default:
		return runSink(stream, o, &plainSink{w: w, o: o})
	}
}

// quietSink prints only the answer, as soon as it arrives.
type quietSink struct{ w io.Writer }

func (quietSink) begin() error { return nil }

func (q quietSink) event(_ *Tracker, e discussion.Event, _ Change, _ time.Time) error {
	if e.Type != "synthesis" {
		return nil
	}
	_, err := fmt.Fprintln(q.w, e.Content)
	return err
}

func (quietSink) finish(*Tracker) error { return nil }

// runSink feeds each event to s as it arrives and finishes once the stream ends.
func runSink(stream io.Reader, o Options, s sink) error {
	t := NewTracker(o.start())
	if err := s.begin(); err != nil {
		return err
	}
	var writeErr error
	parseErr := discussion.ParseSSE(stream, func(e discussion.Event) bool {
		o.seen(e)
		at := o.now()
		writeErr = s.event(t, e, t.Apply(e, at), at)
		return writeErr != nil || discussion.Terminal(e)
	})
	if writeErr != nil {
		return writeErr
	}
	if parseErr != nil {
		return parseErr
	}
	return s.finish(t)
}
