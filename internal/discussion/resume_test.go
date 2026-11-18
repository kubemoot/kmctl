package discussion

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// fakeGateway serves scripted streams: each open returns the next body and records the
// Last-Event-ID it was given. A nil body is a refused open.
type fakeGateway struct {
	bodies []*string
	opened []string
}

func (g *fakeGateway) open(_ context.Context, lastID string) (io.ReadCloser, error) {
	g.opened = append(g.opened, lastID)
	i := len(g.opened) - 1
	if i >= len(g.bodies) || g.bodies[i] == nil {
		return nil, errors.New("gateway unreachable")
	}
	return io.NopCloser(strings.NewReader(*g.bodies[i])), nil
}

func str(s string) *string { return &s }

func fast(t *testing.T, attempts int) {
	t.Helper()
	oa, op := resumeAttempts, resumePause
	resumeAttempts, resumePause = attempts, time.Millisecond
	t.Cleanup(func() { resumeAttempts, resumePause = oa, op })
}

func collect(t *testing.T, g *fakeGateway) ([]Event, error) {
	t.Helper()
	s, err := newResuming(context.Background(), g.open)
	if err != nil {
		t.Fatal(err)
	}
	var got []Event
	err = ParseSSE(s, func(e Event) bool {
		got = append(got, e)
		return Terminal(e)
	})
	return got, err
}

func TestStreamResumesAfterTheLastEventWhenItDropsMidAnswer(t *testing.T) {
	fast(t, 3)
	g := &fakeGateway{bodies: []*string{
		// cut after a whole event and in the middle of the next one
		str("id: t1:5\ndata: {\"type\":\"thread_found\",\"threadId\":\"t1\"}\n\nid: t1:6\ndata: {\"type\":\"phase\",\"ag"),
		str("id: t1:9\ndata: {\"type\":\"synthesis\",\"content\":\"the answer\"}\n\nid: t1:10\ndata: {\"type\":\"done\"}\n\n"),
	}}

	got, err := collect(t, g)

	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, e := range got {
		types = append(types, e.Type)
	}
	if strings.Join(types, ",") != "thread_found,synthesis,done" {
		t.Fatalf("events = %v: the partial event must be dropped, the answer and done must arrive", types)
	}
	if len(g.opened) != 2 || g.opened[0] != "" || g.opened[1] != "t1:5" {
		t.Fatalf("opens = %q, want a resume after the last whole event t1:5", g.opened)
	}
}

func TestStreamEndsNormallyAtDoneWithoutReopening(t *testing.T) {
	g := &fakeGateway{bodies: []*string{str("data: {\"type\":\"done\"}\n\n")}}
	if _, err := collect(t, g); err != nil || len(g.opened) != 1 {
		t.Fatalf("err = %v, opens = %d", err, len(g.opened))
	}
}

func TestStreamThatCannotBeResumedEndsWithErrStreamLost(t *testing.T) {
	fast(t, 3)
	g := &fakeGateway{bodies: []*string{str("id: t1:5\ndata: {\"type\":\"thread_found\"}\n\n"), nil, nil, nil}}

	got, err := collect(t, g)

	if !errors.Is(err, ErrStreamLost) {
		t.Fatalf("err = %v, want ErrStreamLost", err)
	}
	if len(got) != 1 || len(g.opened) != 4 {
		t.Fatalf("events = %d, opens = %d, want 1 event and 1+3 opens", len(got), len(g.opened))
	}
}

func TestStreamResumesRepeatedlyAndTheCountResetsOnSuccess(t *testing.T) {
	fast(t, 1)
	g := &fakeGateway{bodies: []*string{
		str("id: a:1\ndata: {\"type\":\"phase\"}\n\n"),
		str("id: a:2\ndata: {\"type\":\"phase\"}\n\n"),
		str("id: a:3\ndata: {\"type\":\"done\"}\n\n"),
	}}
	got, err := collect(t, g)
	if err != nil || len(got) != 3 || g.opened[2] != "a:2" {
		t.Fatalf("err = %v, events = %d, opens = %q", err, len(got), g.opened)
	}
}

func TestStreamStopsWaitingWhenTheContextEnds(t *testing.T) {
	fast(t, 3)
	g := &fakeGateway{bodies: []*string{str("data: {\"type\":\"phase\"}\n\n")}}
	ctx, cancel := context.WithCancel(context.Background())
	s, _ := newResuming(ctx, g.open)
	cancel()
	if _, err := io.ReadAll(s); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestStreamThatKeepsEndingWithNothingNewIsLost(t *testing.T) {
	fast(t, 2)
	same := "id: a:1\ndata: {\"type\":\"phase\"}\n\n"
	g := &fakeGateway{bodies: []*string{str(same), str(""), str(""), str(""), str("")}}

	_, err := collect(t, g)

	if !errors.Is(err, ErrStreamLost) {
		t.Fatalf("err = %v, want ErrStreamLost: reopens that deliver nothing must not loop forever", err)
	}
}

func TestAnErrorEventEndsTheStreamAtOnceWithoutReopening(t *testing.T) {
	fast(t, 3)
	g := &fakeGateway{bodies: []*string{str("id: a:1\ndata: {\"type\":\"error\",\"error\":\"boom\"}\n\n")}}

	got, err := collect(t, g)

	if err != nil || len(got) != 1 || got[0].Type != "error" || len(g.opened) != 1 {
		t.Fatalf("err = %v, events = %v, opens = %d: an error event is terminal", err, got, len(g.opened))
	}
}

func TestRefusedReopenStopsAtOnceAndKeepsTheStatusError(t *testing.T) {
	for _, code := range []int32{401, 403, 404} {
		fast(t, 5)
		opens := 0
		open := func(_ context.Context, _ string) (io.ReadCloser, error) {
			opens++
			if opens == 1 {
				return io.NopCloser(strings.NewReader("id: a:1\ndata: {\"type\":\"phase\"}\n\n")), nil
			}
			return nil, &apierrors.StatusError{ErrStatus: metav1.Status{Code: code, Reason: metav1.StatusReasonUnknown}}
		}
		s, _ := newResuming(context.Background(), open)

		_, err := io.ReadAll(s)

		var se *apierrors.StatusError
		if !errors.As(err, &se) || se.Status().Code != code || opens != 2 {
			t.Fatalf("code %d: err = %v, opens = %d, want the status error after one reopen", code, err, opens)
		}
	}
}
