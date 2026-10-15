package discussion

import (
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
	if serviceName("homelab-pilot") != "homelab-pilot-discussion" {
		t.Errorf("serviceName = %q", serviceName("homelab-pilot"))
	}
}
