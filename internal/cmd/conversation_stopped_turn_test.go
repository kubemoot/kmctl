package cmd

import (
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kubemoot/kmctl/internal/conversation"
)

const askedID = "11111111-1111-4111-8111-111111111111"
const askedBase = "demo-discussion/api/v1/discussions/demo"

const stoppedEvents = `data: {"type":"connected"}

data: {"type":"phase","agent":"demo-workloads","status":"gathering"}

data: {"type":"synthesis","content":"Stopped by the person who asked; this is not an answer.","outcome":"stopped"}

data: {"type":"done","outcome":"stopped"}

`

func serveAsk(s *fakeAPIServer, stream string) {
	s.proxy[askedBase] = func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"conversationId": askedID})
	}
	s.proxy[askedBase+"/"+askedID+"/stream"] = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, stream)
	}
}

func TestConversationAsk_StoppedTurnIsNotAnAnswer(t *testing.T) {
	s := newFakeAPIServer(t)
	serveAsk(s, stoppedEvents)

	out, _, err := s.kmctl(t, "conversation", "ask", "demo", "gpu temperatures?", "-o", "plain")
	if err == nil || !strings.Contains(err.Error(), "stopped before the crew answered") {
		t.Fatalf("a stopped turn is not a success, got %v", err)
	}
	assertContains(t, "plain", out, "] stopped by you", "] done (stopped)", "STOPPED: Stopped by you.")
	if strings.Contains(out, "ANSWER:") || strings.Contains(out, "answer ready") {
		t.Errorf("a stopped turn is shown as an answer:\n%s", out)
	}

	out, _, err = s.kmctl(t, "conversation", "ask", "demo", "again", "-o", "json")
	if err == nil {
		t.Fatal("json: a stopped turn exits non-zero")
	}
	assertContains(t, "json", out, `"type":"result"`, `"answer":""`, `"outcome":"stopped"`, `"message":"Stopped by you.`)

	out, _, err = s.kmctl(t, "conversation", "ask", "demo", "again", "-q")
	if err == nil || out != "" {
		t.Errorf("quiet prints no answer for a stopped turn: %q, %v", out, err)
	}
}

func TestConversationWatch_AStoppedTurnWithoutSynthesis(t *testing.T) {
	s := newFakeAPIServer(t)
	s.proxy[askedBase+"/"+askedID+"/stream"] = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"done\",\"outcome\":\"stopped\"}\n\n")
	}
	out, _, err := s.kmctl(t, "conversation", "watch", "demo", askedID, "-o", "plain")
	if err == nil {
		t.Fatal("a stopped turn exits non-zero")
	}
	assertContains(t, "watch", out, "STOPPED:", "done (stopped)")
}

// A question that waits behind others shows its place instead of silence, and the line goes to
// stderr so the answer and the JSON stay clean.
func TestConversationAsk_ShowsTheQueuePosition(t *testing.T) {
	defer func(d time.Duration) { queuePollEvery = d }(queuePollEvery)
	queuePollEvery = 5 * time.Millisecond
	s := newFakeAPIServer(t)
	serveAsk(s, "")
	var polls atomic.Int32
	release := make(chan struct{})
	s.proxy[askedBase+"/"+askedID+"/state"] = func(w http.ResponseWriter, _ *http.Request) {
		position := 2
		if polls.Add(1) > 2 {
			position = 1
			select {
			case <-release:
			default:
				close(release)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"conversationId": askedID, "state": "queued",
			"turns": []map[string]any{{"question": "q", "state": "queued", "queuePosition": position}}})
	}
	s.proxy[askedBase+"/"+askedID+"/stream"] = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"connected\"}\n\n")
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(w, discussionEvents)
	}
	out, errOut, err := s.kmctl(t, "conversation", "ask", "demo", "hi", "-o", "plain")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "stderr", errOut, conversation.QueueText(2), conversation.QueueText(1))
	if strings.Contains(out, "queued") {
		t.Errorf("the queue line is on stdout:\n%s", out)
	}
	assertContains(t, "stdout", out, "ANSWER:")
}

func TestConversationAsk_NoStateReadIsNoNoise(t *testing.T) {
	s := newFakeAPIServer(t)
	var asked string
	withGateway(s, &asked) // serves no /state: an older gateway
	_, errOut, err := s.kmctl(t, "conversation", "ask", "demo", "hi", "-o", "plain")
	if err != nil || errOut != "" {
		t.Errorf("an older gateway runs as before: %q, %v", errOut, err)
	}
}
