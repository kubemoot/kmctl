package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

const replayedID = "9999c361-aa0c-4bde-85c6-e143af23b251"

// withReplayGateway serves a gateway that lists convA and convB, records the message and
// conversation ID of each turn it is asked to start, and streams the new conversation.
func withReplayGateway(s *fakeAPIServer, asked *[]map[string]string) {
	s.proxy[gatewayKey] = func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, map[string]any{"conversations": []map[string]any{
				{"conversationId": convA, "question": "Describe the proxmox nodes.", "turns": 2},
				{"conversationId": convB, "turns": 1},
			}})
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		*asked = append(*asked, body)
		writeJSON(w, http.StatusOK, map[string]string{"conversationId": replayedID})
	}
	s.proxy[gatewayKey+"/"+replayedID+"/stream"] = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, discussionEvents)
	}
}

func TestConversationReplayAsksTheFirstQuestionInANewConversation(t *testing.T) {
	s := newFakeAPIServer(t)
	var asked []map[string]string
	withReplayGateway(s, &asked)
	out, _, err := s.kmctl(t, "conversation", "replay", "demo", "6733c", "-o", "plain")
	if err != nil {
		t.Fatalf("replay: %v\n%s", err, out)
	}
	if len(asked) != 1 || asked[0]["message"] != "Describe the proxmox nodes." || asked[0]["conversationId"] != "" {
		t.Fatalf("asked = %v, want the first question with no conversation ID", asked)
	}
	if !strings.Contains(out, replayedID) {
		t.Errorf("output does not name the new conversation:\n%s", out)
	}
}

func TestConversationReplayErrors(t *testing.T) {
	s := newFakeAPIServer(t)
	var asked []map[string]string
	withReplayGateway(s, &asked)
	for name, args := range map[string][]string{
		"no recorded question": {"conversation", "replay", "demo", convB},
		"unknown full id":      {"conversation", "replay", "demo", "ffffffff-aa0c-4bde-85c6-e143af23b251"},
		"unknown prefix":       {"conversation", "replay", "demo", "ffff"},
		"missing id":           {"conversation", "replay", "demo"},
	} {
		if _, _, err := s.kmctl(t, args...); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if len(asked) != 0 {
		t.Errorf("a failed replay asked the crew: %v", asked)
	}
}

func TestConversationReplayNeedsAGatewayThatLists(t *testing.T) {
	s := newFakeAPIServer(t)
	withOlderGateway(s)
	_, _, err := s.kmctl(t, "conversation", "replay", "demo", convA)
	if err == nil || !strings.Contains(err.Error(), "does not list conversations") {
		t.Fatalf("err = %v", err)
	}
}
