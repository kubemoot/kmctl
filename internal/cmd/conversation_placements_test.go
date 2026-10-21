package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/clierr"
)

const placementsStream = "data: {\"type\":\"connected\"}\n\ndata: {\"type\":\"synthesis\",\"content\":\"ok\"}\n\ndata: %s\n\n"

// withReceiptGateway serves conversation convA's stream, closing with the done event
// given (a JSON object).
func withReceiptGateway(s *fakeAPIServer, done string) {
	s.proxy[gatewayKey+"/"+convA+"/stream"] = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, strings.Replace(placementsStream, "%s", done, 1))
	}
}

func doneWithReceipt(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../placement/testdata/receipt.json")
	if err != nil {
		t.Fatal(err)
	}
	var receipt any
	if err := json.Unmarshal(b, &receipt); err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(map[string]any{"type": "done", "receipt": receipt})
	return string(out)
}

func TestConversationPlacementsCommand_Registered(t *testing.T) {
	assertSubcommands(t, newConversationCommand(client.NewFactory()), "ask", "watch", "list", "placements", "artifact")
}

func TestConversationPlacements_Table(t *testing.T) {
	s := newFakeAPIServer(t)
	withReceiptGateway(s, doneWithReceipt(t))
	out, _, err := s.kmctl(t, "conversation", "placements", "demo", convA)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "placements", out, "PLACEMENT", "qwen3:14b@ollama-gpu (cold)", "+2.4s (17%)",
		"reason: predicted 2.9s", "ollama-cpu  predicted 61.0s", "3 calls: 1 algorithm, 1 learned, 1 shadow.")
}

func TestConversationPlacements_JSONPassesCallsThrough(t *testing.T) {
	s := newFakeAPIServer(t)
	withReceiptGateway(s, doneWithReceipt(t))
	out, _, err := s.kmctl(t, "conversation", "placements", "demo", convA, "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		ConversationID string           `json:"conversationId"`
		Crew           string           `json:"crew"`
		Calls          []map[string]any `json:"calls"`
		Summary        struct {
			Calls int `json:"calls"`
		} `json:"summary"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if got.ConversationID != convA || got.Crew != "demo" || len(got.Calls) != 3 || got.Summary.Calls != 3 {
		t.Fatalf("unexpected output: %+v", got)
	}
	if got.Calls[0]["reason"] == nil || got.Calls[1]["coldStart"] != true {
		t.Errorf("calls were not passed through: %+v", got.Calls[:2])
	}
	if _, _, err := s.kmctl(t, "conversation", "placements", "demo", convA, "-o", "yaml"); err != nil {
		t.Errorf("yaml: %v", err)
	}
}

func TestConversationPlacements_NoCallsNote(t *testing.T) {
	s := newFakeAPIServer(t)
	withReceiptGateway(s, `{"type":"done","receipt":{"totalMs":5}}`)
	out, _, err := s.kmctl(t, "conversation", "placements", "demo", convA)
	if err != nil || !strings.Contains(out, "no placement calls") || strings.Count(out, "\n") != 1 {
		t.Fatalf("want a one-line note: %v\n%s", err, out)
	}
	out, _, err = s.kmctl(t, "conversation", "placements", "demo", convA, "-o", "json")
	if err != nil || !strings.Contains(out, `"calls": []`) {
		t.Fatalf("json without calls has an empty array: %v\n%s", err, out)
	}
}

func TestConversationPlacements_NoReceipt(t *testing.T) {
	s := newFakeAPIServer(t)
	withReceiptGateway(s, `{"type":"done"}`)
	out, _, err := s.kmctl(t, "conversation", "placements", "demo", convA)
	if err != nil || !strings.Contains(out, "closed without a receipt") {
		t.Fatalf("want the no-receipt note: %v\n%s", err, out)
	}
}

func TestConversationPlacements_Errors(t *testing.T) {
	s := newFakeAPIServer(t)
	withReceiptGateway(s, `{"type":"error","error":"boom"}`)
	if _, _, err := s.kmctl(t, "conversation", "placements", "demo", convA); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("an error event: %v", err)
	}
	s.proxy[gatewayKey+"/"+convA+"/stream"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "data: {\"type\":\"connected\"}\n\n")
	}
	_, _, err := s.kmctl(t, "conversation", "placements", "demo", convA)
	if ce, ok := clierr.As(err); !ok || !strings.Contains(ce.Message, "closed before the crew answered") || ce.Hint != "Reattach: kmctl conversation watch demo "+convA+" -n team-a" {
		t.Errorf("a stream that ends early names how to reattach: %v", err)
	}
	withReceiptGateway(s, `{"type":"error"}`)
	_, _, err = s.kmctl(t, "conversation", "placements", "demo", convA)
	if err == nil || strings.Contains(err.Error(), "Reattach") || !strings.Contains(err.Error(), "reported an error") {
		t.Errorf("an error event without a message is a reported error, not a cut stream: %v", err)
	}
}

func TestConversationPlacements_UsageErrors(t *testing.T) {
	s := newFakeAPIServer(t)
	withReceiptGateway(s, `{"type":"done"}`)
	if _, _, err := s.kmctl(t, "conversation", "placements", "demo", convA, "-o", "bogus"); err == nil {
		t.Error("an unknown output format is an error")
	}
	if _, _, err := s.kmctl(t, "conversation", "placements", "demo"); err == nil {
		t.Error("the conversation ID is required")
	}
}

// A turn that never closes ends at --timeout with the IDs and the reattach command.
func TestConversationPlacements_Timeout(t *testing.T) {
	s := newFakeAPIServer(t)
	s.proxy[gatewayKey+"/"+convA+"/stream"] = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"thread_found\",\"threadId\":\"t9\"}\n\n")
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		<-r.Context().Done()
	}
	_, _, err := s.kmctl(t, "conversation", "placements", "demo", convA, "--timeout", "300ms")
	if err == nil {
		t.Fatal("want a timeout error")
	}
	assertContains(t, "timeout", err.Error(), "stopped waiting after --timeout 300ms", "Conversation "+convA+", thread t9",
		"Reattach: kmctl conversation watch demo "+convA)
}

func TestConversationPlacements_WaitFlags(t *testing.T) {
	cmd := newConversationPlacementsCommand(client.NewFactory())
	if idle := cmd.Flags().Lookup("idle-timeout"); idle == nil || idle.DefValue != "5m0s" {
		t.Errorf("want --idle-timeout defaulting to 5m0s, got %+v", idle)
	}
	if overall := cmd.Flags().Lookup("timeout"); overall == nil || overall.DefValue != "0s" {
		t.Errorf("want --timeout defaulting to 0s, got %+v", overall)
	}
}

// With two closed threads in one stream, the later thread's receipt is shown.
func TestConversationPlacements_LatestReceiptWins(t *testing.T) {
	s := newFakeAPIServer(t)
	first := `{"type":"done","receipt":{"calls":[{"agent":"first-round","model":"m","server":"s","policy":"learned"}]}}`
	second := `{"type":"done","receipt":{"calls":[{"agent":"second-round","model":"m","server":"s","policy":"algorithm"}]}}`
	s.proxy[gatewayKey+"/"+convA+"/stream"] = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, ev := range []string{`{"type":"thread_found","threadId":"t1"}`, first, `{"type":"thread_found","threadId":"t2"}`, second} {
			_, _ = io.WriteString(w, "data: "+ev+"\n\n")
		}
	}
	out, _, err := s.kmctl(t, "conversation", "placements", "demo", convA)
	if err != nil || !strings.Contains(out, "second-round") || strings.Contains(out, "first-round") {
		t.Fatalf("want the second thread's receipt only: %v\n%s", err, out)
	}
}

func TestConversationPlacements_MalformedReceipt(t *testing.T) {
	s := newFakeAPIServer(t)
	withReceiptGateway(s, `{"type":"done","receipt":{"calls":[5]}}`)
	if _, _, err := s.kmctl(t, "conversation", "placements", "demo", convA); err == nil || !strings.Contains(err.Error(), "placement call 1") {
		t.Errorf("a call that is not an object: %v", err)
	}
}
