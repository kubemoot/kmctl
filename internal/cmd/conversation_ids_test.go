package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	convA      = "6733c361-aa0c-4bde-85c6-e143af23b251"
	convB      = "6733f000-1111-4bde-85c6-e143af23b251"
	gatewayKey = "demo-discussion/api/v1/discussions/demo"
	artifactID = "team-a/demo/t-1/demo-workloads/agree-1"
)

// withListingGateway serves a gateway that lists conversations convA and convB, starts
// turns (recording the conversation ID it was sent), and streams convA's discussion.
func withListingGateway(s *fakeAPIServer, sent *string) {
	s.proxy[gatewayKey] = func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, map[string]any{"conversations": []map[string]any{
				{"conversationId": convA, "question": "Describe the proxmox nodes.", "turns": 2, "lastTurnAt": "2026-10-15T09:00:00Z"},
				{"conversationId": convB, "question": "What is failing?", "turns": 1, "lastTurnAt": "2026-10-15T08:00:00Z"},
			}})
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		*sent = body["conversationId"]
		writeJSON(w, http.StatusOK, map[string]string{"conversationId": body["conversationId"]})
	}
	s.proxy[gatewayKey+"/"+convA+"/stream"] = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, discussionEvents)
	}
}

// withOlderGateway serves a gateway that only starts turns: a GET on the list path is
// answered 405, as Go's router answers a method it has no route for.
func withOlderGateway(s *fakeAPIServer) {
	s.proxy[gatewayKey] = func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"conversationId": convA})
	}
	s.proxy[gatewayKey+"/"+convA+"/stream"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, discussionEvents)
	}
}

func TestConversationPrefixResolves(t *testing.T) {
	s := newFakeAPIServer(t)
	var sent string
	withListingGateway(s, &sent)
	out, _, err := s.kmctl(t, "conversation", "watch", "demo", "6733c", "-o", "plain")
	if err != nil || !strings.Contains(out, "--conversation-id "+convA) {
		t.Fatalf("watch with a prefix: %v\n%s", err, out)
	}
	if _, _, err := s.kmctl(t, "conversation", "ask", "demo", "and their memory?", "--conversation-id", "6733c", "-q"); err != nil || sent != convA {
		t.Fatalf("ask sends the full ID: sent %q, %v", sent, err)
	}
}

func TestConversationPrefixAmbiguousOrUnknown(t *testing.T) {
	s := newFakeAPIServer(t)
	var sent string
	withListingGateway(s, &sent)
	_, _, err := s.kmctl(t, "conversation", "watch", "demo", "6733")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") || !strings.Contains(err.Error(), "What is failing?") {
		t.Fatalf("ambiguous: %v", err)
	}
	if _, _, err := s.kmctl(t, "conversation", "ask", "demo", "hi", "--conversation-id", "ffff"); err == nil || !strings.Contains(err.Error(), "no recent conversation") || sent != "" {
		t.Fatalf("unknown prefix: %v, sent %q", err, sent)
	}
}

func TestConversationOlderGatewayNeedsTheFullID(t *testing.T) {
	s := newFakeAPIServer(t)
	withOlderGateway(s)
	_, _, err := s.kmctl(t, "conversation", "watch", "demo", "6733c")
	if err == nil || !strings.Contains(err.Error(), "does not list conversations") || !strings.Contains(err.Error(), "pass the full ID") {
		t.Fatalf("prefix on an older gateway: %v", err)
	}
	if out, _, err := s.kmctl(t, "conversation", "watch", "demo", convA, "-o", "plain"); err != nil || !strings.Contains(out, "ANSWER:") {
		t.Fatalf("full ID on an older gateway: %v\n%s", err, out)
	}
	if _, _, err := s.kmctl(t, "conversation", "list", "demo"); err == nil || !strings.Contains(err.Error(), "does not list conversations") {
		t.Fatalf("list on an older gateway: %v", err)
	}
}

func TestConversationList(t *testing.T) {
	s := newFakeAPIServer(t)
	var sent string
	withListingGateway(s, &sent)
	out, _, err := s.kmctl(t, "conversation", "list", "demo")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "list", out, "CONVERSATION ID", convA, "Describe the proxmox nodes.", "2026-10-15T09:00:00Z")
	out, _, err = s.kmctl(t, "conversation", "list", "demo", "-o", "json")
	if err != nil || !strings.Contains(out, `"conversationId": "`+convB+`"`) {
		t.Fatalf("json: %v\n%s", err, out)
	}
}

// Completion offers the crew's recent conversations with their first questions, for
// watch's second argument and ask's --conversation-id.
func TestConversationIDCompletion(t *testing.T) {
	s := newFakeAPIServer(t)
	var sent string
	withListingGateway(s, &sent)
	for _, args := range [][]string{
		{"__complete", "conversation", "watch", "demo", "6733"},
		{"__complete", "conversation", "ask", "demo", "q", "--conversation-id", "6733"},
	} {
		out, _, err := s.kmctl(t, args...)
		if err != nil {
			t.Fatal(err)
		}
		assertContains(t, strings.Join(args, " "), out, convA+"\tDescribe the proxmox nodes.", convB+"\tWhat is failing?")
	}
	out, _, _ := s.kmctl(t, "__complete", "conversation", "watch", "demo", "9")
	if strings.Contains(out, convA) {
		t.Errorf("completion filters by the typed prefix:\n%s", out)
	}
}

func TestConversationIDCompletionOnAnOlderGatewayIsEmpty(t *testing.T) {
	s := newFakeAPIServer(t)
	withOlderGateway(s)
	out, _, err := s.kmctl(t, "__complete", "conversation", "watch", "demo", "")
	if err != nil || strings.Contains(out, convA) {
		t.Fatalf("got %v\n%s", err, out)
	}
}

func TestConversationArtifact(t *testing.T) {
	s := newFakeAPIServer(t)
	var key string
	s.proxy[gatewayKey+"/artifact"] = func(w http.ResponseWriter, r *http.Request) {
		key = r.URL.Query().Get("key")
		_, _ = io.WriteString(w, "pve1 online\n")
	}
	out, _, err := s.kmctl(t, "conversation", "artifact", "demo", artifactID)
	if err != nil || out != "pve1 online\n" || key != artifactID {
		t.Fatalf("stdout: %q, key %q, %v", out, key, err)
	}
	file := filepath.Join(t.TempDir(), "a.txt")
	if out, _, err := s.kmctl(t, "conversation", "artifact", "demo", artifactID, "-f", file); err != nil || out != "" {
		t.Fatalf("-f: %q, %v", out, err)
	}
	if data, _ := os.ReadFile(file); string(data) != "pve1 online\n" {
		t.Fatalf("file holds %q", data)
	}
}

func TestConversationArtifactGone(t *testing.T) {
	s := newFakeAPIServer(t)
	s.proxy[gatewayKey+"/artifact"] = func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "artifact not found"})
	}
	_, _, err := s.kmctl(t, "conversation", "artifact", "demo", artifactID)
	if err == nil || !strings.Contains(err.Error(), "deleted after the thread closes") {
		t.Fatalf("got %v", err)
	}
}

type failingBody struct{ sent bool }

func (b *failingBody) Read(p []byte) (int, error) {
	if b.sent {
		return 0, io.ErrUnexpectedEOF
	}
	b.sent = true
	return copy(p, "partial"), nil
}

// A copy that fails partway leaves no file that looks like the whole artifact.
func TestWriteArtifactRemovesAPartialFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "a.txt")
	if err := writeArtifact(io.Discard, file, &failingBody{}); err == nil {
		t.Fatal("want the copy error")
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("the partial file is still there: %v", err)
	}
	if err := writeArtifact(io.Discard, filepath.Join(t.TempDir(), "missing", "a.txt"), strings.NewReader("x")); err == nil {
		t.Fatal("a file that cannot be created is an error")
	}
}
