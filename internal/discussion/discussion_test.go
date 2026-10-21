package discussion

import (
	"errors"
	"strings"
	"testing"
)

const sampleStream = `data: {"type":"connected"}

data: {"type":"thread_found","threadId":"t1"}

data: {"type":"phase","agent":"k8s","status":"triaging","gpu":"rtx5090"}

data: {"type":"finding","agent":"k8s","signal":"agree","summary":"3 pods"}

data: {"type":"synthesis","content":"There are 3 pods."}

data: {"type":"done"}

data: {"type":"phase","agent":"late","status":"ready"}

`

func TestParseSSE_CollectsAndStopsAtTerminal(t *testing.T) {
	var got []Event
	err := ParseSSE(strings.NewReader(sampleStream), func(e Event) bool {
		got = append(got, e)
		return Terminal(e)
	})
	if err != nil {
		t.Fatalf("ParseSSE: %v", err)
	}
	// connected, thread_found, phase, finding, synthesis, done = 6; the post-done
	// event must NOT be delivered because handle returned true on done.
	if len(got) != 6 {
		t.Fatalf("got %d events, want 6 (stop at done): %+v", len(got), got)
	}
	if got[0].Type != "connected" || got[len(got)-1].Type != "done" {
		t.Errorf("unexpected first/last: %q / %q", got[0].Type, got[len(got)-1].Type)
	}
	if got[4].Type != "synthesis" || got[4].Content != "There are 3 pods." {
		t.Errorf("synthesis event wrong: %+v", got[4])
	}
}

func TestParseSSE_SkipsNonJSON(t *testing.T) {
	stream := ": keep-alive comment\ndata: not-json\n\ndata: {\"type\":\"done\"}\n\n"
	var got []Event
	if err := ParseSSE(strings.NewReader(stream), func(e Event) bool { got = append(got, e); return false }); err != nil {
		t.Fatalf("ParseSSE: %v", err)
	}
	if len(got) != 1 || got[0].Type != "done" {
		t.Errorf("expected only the valid 'done' event, got %+v", got)
	}
}

func TestParseSSE_DecodesWaitAndStandAsideFields(t *testing.T) {
	stream := "data: {\"type\":\"phase\",\"agent\":\"k8s\",\"status\":\"waiting\",\"model\":\"qwen3:8b\",\"reason\":\"gpu-busy\"}\n\n"
	var got Event
	if err := ParseSSE(strings.NewReader(stream), func(e Event) bool { got = e; return true }); err != nil {
		t.Fatalf("ParseSSE: %v", err)
	}
	if got.Model != "qwen3:8b" || got.Reason != "gpu-busy" {
		t.Errorf("model and reason not decoded: %+v", got)
	}
}

func TestShowsCrewActivity(t *testing.T) {
	for typ, want := range map[string]bool{
		"heartbeat": false, "connected": false,
		"activity": true, "phase": true, "finding": true, "thread_found": true, "synthesis": true, "done": true,
	} {
		if got := ShowsCrewActivity(Event{Type: typ}); got != want {
			t.Errorf("ShowsCrewActivity(%s) = %v, want %v", typ, got, want)
		}
	}
}

func TestParseSSE_DecodesActivity(t *testing.T) {
	stream := `data: {"type":"activity","at":"2026-10-15T12:00:00Z","agents":[{"agent":"compute","doing":"evaluating","gpu":"g0","since":"2026-10-15T11:59:00Z"}]}` + "\n\n"
	var got Event
	_ = ParseSSE(strings.NewReader(stream), func(e Event) bool { got = e; return true })
	if got.At != "2026-10-15T12:00:00Z" || len(got.Agents) != 1 ||
		got.Agents[0] != (Activity{Agent: "compute", Doing: "evaluating", GPU: "g0", Since: "2026-10-15T11:59:00Z"}) {
		t.Errorf("decoded %+v", got)
	}
}

func TestServiceName(t *testing.T) {
	if ServiceName("homelab-pilot") != "homelab-pilot-discussion" {
		t.Errorf("ServiceName = %q", ServiceName("homelab-pilot"))
	}
}

func sse(events ...string) *strings.Reader {
	return strings.NewReader("data: " + strings.Join(events, "\n\ndata: ") + "\n\n")
}

func TestReceiptFromStream(t *testing.T) {
	got, err := ReceiptFromStream(sse(`{"type":"connected"}`, `{"type":"thread_found","threadId":"t1"}`,
		`{"type":"done","receipt":{"calls":[{"model":"m"}]}}`), nil)
	if err != nil || !got.Done || got.ThreadID != "t1" || string(got.Receipt) != `{"calls":[{"model":"m"}]}` {
		t.Fatalf("got %+v, %v", got, err)
	}
	got, err = ReceiptFromStream(sse(`{"type":"thread_found","threadId":"t1"}`, `{"type":"done"}`), nil)
	if err != nil || !got.Done || got.Receipt != nil {
		t.Errorf("a done event without a receipt: %+v, %v", got, err)
	}
}

// Of two closed threads in one stream, the later thread's receipt is used; a thread
// announced after a close, and not yet closed, leaves no receipt.
func TestReceiptFromStream_LatestThreadWins(t *testing.T) {
	two := sse(`{"type":"thread_found","threadId":"t1"}`, `{"type":"done","receipt":{"totalMs":1}}`,
		`{"type":"thread_found","threadId":"t2"}`, `{"type":"done","receipt":{"totalMs":2}}`)
	got, err := ReceiptFromStream(two, nil)
	if err != nil || got.ThreadID != "t2" || string(got.Receipt) != `{"totalMs":2}` {
		t.Fatalf("two closes: %+v, %v", got, err)
	}
	open := sse(`{"type":"thread_found","threadId":"t1"}`, `{"type":"done","receipt":{"totalMs":1}}`, `{"type":"thread_found","threadId":"t2"}`)
	if got, err := ReceiptFromStream(open, nil); !errors.Is(err, ErrNoClose) || got.ThreadID != "t2" || got.Receipt != nil {
		t.Errorf("second thread still open: %+v, %v", got, err)
	}
}

func TestReceiptFromStream_Failures(t *testing.T) {
	_, err := ReceiptFromStream(sse(`{"type":"connected"}`), nil)
	if !errors.Is(err, ErrNoClose) {
		t.Errorf("a stream that ends before done: %v", err)
	}
	_, err = ReceiptFromStream(sse(`{"type":"error","error":"boom"}`), nil)
	if !errors.Is(err, ErrStreamReported) || errors.Is(err, ErrNoClose) || !strings.Contains(err.Error(), "boom") {
		t.Errorf("an error event: %v", err)
	}
	_, err = ReceiptFromStream(sse(`{"type":"error"}`), nil)
	if !errors.Is(err, ErrStreamReported) || errors.Is(err, ErrNoClose) {
		t.Errorf("an error event without a message is still a reported error: %v", err)
	}
}

func TestReceiptFromStream_ShowsEveryEvent(t *testing.T) {
	var types []string
	_, _ = ReceiptFromStream(sse(`{"type":"connected"}`, `{"type":"done"}`), func(e Event) { types = append(types, e.Type) })
	if strings.Join(types, ",") != "connected,done" {
		t.Errorf("onEvent saw %v", types)
	}
}
