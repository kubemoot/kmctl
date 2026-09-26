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

func TestRender_Exact(t *testing.T) {
	cases := map[Event]string{
		{Type: "connected"}:                "* connected",
		{Type: "done"}:                     "* done",
		{Type: "error", Error: "boom"}:     "ERROR: boom",
		{Type: "heartbeat"}:                "",
		{Type: "synthesis", Content: "hi"}: "\nANSWER:\nhi",
	}
	for e, want := range cases {
		if got := Render(e); got != want {
			t.Errorf("Render(%s) = %q, want %q", e.Type, got, want)
		}
	}
}

func TestRender_Padded(t *testing.T) {
	phase := Render(Event{Type: "phase", Agent: "k8s", Status: "ready", GPU: "rtx5090"})
	for _, want := range []string{"k8s", "ready", "[rtx5090]"} {
		if !strings.Contains(phase, want) {
			t.Errorf("phase render %q missing %q", phase, want)
		}
	}
	finding := Render(Event{Type: "finding", Agent: "k8s", Signal: "agree", Summary: "3 pods"})
	for _, want := range []string{"k8s", "agree", "3 pods"} {
		if !strings.Contains(finding, want) {
			t.Errorf("finding render %q missing %q", finding, want)
		}
	}
}

func TestServiceName(t *testing.T) {
	if serviceName("homelab-pilot") != "homelab-pilot-discussion" {
		t.Errorf("serviceName = %q", serviceName("homelab-pilot"))
	}
}
