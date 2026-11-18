package discussion

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"k8s.io/client-go/rest"
)

// ErrStreamLost means the stream ended before a done or error event and could not be
// resumed within the attempt bound. The crew may still answer.
var ErrStreamLost = errors.New("the discussion stream ended before the crew answered and could not be resumed")

// Resume bounds: how many times a dropped stream is reopened in a row, and the pause
// before the first reopen (it doubles up to resumeMaxPause). The bound is a safety net
// for a gateway that stays away; a stream that comes back resets the count.
var (
	resumeAttempts = 6
	resumePause    = time.Second
)

const resumeMaxPause = 10 * time.Second

// OpenFunc opens the stream of a conversation, after the event with lastEventID when it
// is not empty (the standard SSE Last-Event-ID).
type OpenFunc func(ctx context.Context, lastEventID string) (io.ReadCloser, error)

// StreamResuming opens the conversation's stream like Stream and, when the stream ends
// without a done or error event (the gateway restarted, a port-forward dropped), reopens
// it after the last event received, so the reader sees one stream. When it cannot be
// reopened within the bound, Read returns ErrStreamLost.
func StreamResuming(ctx context.Context, rc rest.Interface, crew, conversationID string) (io.ReadCloser, error) {
	open := func(ctx context.Context, lastEventID string) (io.ReadCloser, error) {
		req := rc.Get().Suffix("api", "v1", "discussions", crew, conversationID, "stream")
		if lastEventID != "" {
			req = req.SetHeader("Last-Event-ID", lastEventID)
		}
		return req.Stream(ctx)
	}
	return newResuming(ctx, open)
}

func newResuming(ctx context.Context, open OpenFunc) (io.ReadCloser, error) {
	first, err := open(ctx, "")
	if err != nil {
		return nil, err
	}
	return &resuming{ctx: ctx, open: open, cur: first, in: bufio.NewReader(first)}, nil
}

// resuming hands the reader whole events only: a partial event at a cut is dropped, and
// the reopened stream, which starts after the last whole event, supplies it again.
type resuming struct {
	ctx  context.Context
	open OpenFunc
	cur  io.ReadCloser
	in   *bufio.Reader

	out       bytes.Buffer // whole events ready to read
	event     bytes.Buffer // the event being read
	eventID   string       // the id line of that event
	terminal  bool         // that event is a done or error
	lastID    string       // id of the last whole event handed on
	fruitless int          // reopens in a row after which no event with a new id arrived
	idAtOpen  string       // lastID when the current stream was opened
	done      bool         // a terminal event was handed on
}

func (r *resuming) Read(p []byte) (int, error) {
	for r.out.Len() == 0 {
		if r.done {
			return 0, io.EOF
		}
		if err := r.fill(); err != nil {
			return 0, err
		}
	}
	return r.out.Read(p)
}

func (r *resuming) Close() error { return r.cur.Close() }

// fill reads lines until one whole event is ready, reopening the stream when it ends.
func (r *resuming) fill() error {
	for r.out.Len() == 0 {
		line, err := r.in.ReadString('\n')
		if err != nil {
			if r.ctx.Err() != nil {
				return r.ctx.Err()
			}
			if rerr := r.reopen(err); rerr != nil {
				return rerr
			}
			continue
		}
		r.addLine(line)
	}
	return nil
}

// addLine adds one line to the current event; a blank line completes it.
func (r *resuming) addLine(line string) {
	trimmed := strings.TrimRight(line, "\r\n")
	if trimmed != "" {
		r.event.WriteString(line)
		r.noteField(trimmed)
		return
	}
	if r.event.Len() == 0 {
		return
	}
	r.event.WriteString(line)
	r.out.Write(r.event.Bytes())
	r.event.Reset()
	if r.eventID != "" {
		r.lastID = r.eventID
	}
	r.done = r.done || r.terminal
	r.eventID, r.terminal = "", false
}

// noteField records the id of the event and whether its data is a terminal event.
func (r *resuming) noteField(line string) {
	if id, ok := strings.CutPrefix(line, "id:"); ok {
		r.eventID = strings.TrimSpace(id)
	} else if data, ok := strings.CutPrefix(line, "data:"); ok {
		var e Event
		if json.Unmarshal([]byte(strings.TrimSpace(data)), &e) == nil && Terminal(e) {
			r.terminal = true
		}
	}
}

// reopen replaces the ended stream with one after the last whole event, trying up to
// resumeAttempts times. cause is why the old stream ended.
func (r *resuming) reopen(cause error) error {
	_ = r.cur.Close()
	// A stream that gives nothing new before it ends again does not count as resumed.
	if r.lastID == r.idAtOpen {
		r.fruitless++
	} else {
		r.fruitless = 0
	}
	if r.fruitless >= resumeAttempts {
		return fmt.Errorf("%w (%d reopens delivered nothing new): %v", ErrStreamLost, r.fruitless, cause)
	}
	r.idAtOpen = r.lastID
	r.event.Reset()
	r.eventID, r.terminal = "", false
	pause := resumePause
	last := cause
	for attempt := 1; attempt <= resumeAttempts; attempt++ {
		select {
		case <-r.ctx.Done():
			return r.ctx.Err()
		case <-time.After(pause):
		}
		next, err := r.open(r.ctx, r.lastID)
		if err == nil {
			r.cur, r.in = next, bufio.NewReader(next)
			return nil
		}
		last = err
		pause = min(pause*2, resumeMaxPause)
	}
	return fmt.Errorf("%w (%d attempts): %v", ErrStreamLost, resumeAttempts, last)
}

// SetResumeBounds replaces the resume bounds and returns a function that restores them;
// for tests of the commands that stream.
func SetResumeBounds(attempts int, pause time.Duration) (restore func()) {
	oa, op := resumeAttempts, resumePause
	resumeAttempts, resumePause = attempts, pause
	return func() { resumeAttempts, resumePause = oa, op }
}
