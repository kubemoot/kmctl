package cmd

import (
	"net/http"
	"strings"
	"testing"
)

const stopBase = controlBase + "/stop"

func stopBody(outcome, message string) string {
	return `{"conversationId":"` + convID + `","action":"stop_requested","threads":["t-1"],"outcome":"` + outcome + `","message":"` + message + `"}`
}

func TestConversationStop_SaysWhatHappened(t *testing.T) {
	cases := map[string]struct {
		code    int
		outcome string
		message string
		want    string
	}{
		"stopped":         {200, "stopped", "Stopped. The crew abandoned the turn.", "stopped conversation " + convID},
		"cancelled":       {200, "cancelled", "Cancelled a question that was still waiting in the queue.", "cancelled the queued question of conversation " + convID},
		"nothing_to_stop": {200, "nothing_to_stop", "Nothing to stop: every turn had already finished.", "nothing to stop in conversation " + convID},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := newFakeAPIServer(t)
			serveControl(s, stopBase, c.code, stopBody(c.outcome, c.message))
			out, _, err := s.kmctl(t, "conversation", "stop", "demo", convID)
			if err != nil {
				t.Fatalf("%s succeeds, got %v", name, err)
			}
			assertContains(t, name, out, c.want, c.message)
			if name != "stopped" && strings.Contains(out, "stopped conversation") {
				t.Errorf("%s is worded as a stop:\n%s", name, out)
			}
		})
	}
}

func TestConversationStop_UnconfirmedFails(t *testing.T) {
	s := newFakeAPIServer(t)
	serveControl(s, stopBase, http.StatusAccepted, stopBody("unconfirmed", "Asked the crew to stop, and it did not confirm in time; the work may still be running."))
	out, _, err := s.kmctl(t, "conversation", "stop", "demo", convID)
	if err == nil {
		t.Fatal("an unconfirmed stop must exit non-zero")
	}
	assertContains(t, "stdout", out, "not confirmed", "the work may still be running")
	assertContains(t, "error", errorText(err), "did not confirm the stop", "kmctl conversation get demo "+convID)
}

func TestConversationStop_JustTakenFails(t *testing.T) {
	s := newFakeAPIServer(t)
	serveControl(s, stopBase, http.StatusConflict, "The crew has just taken the question and its discussion has not begun, so there is no turn to stop yet; stop again in a moment.")
	_, _, err := s.kmctl(t, "conversation", "stop", "demo", convID)
	if err == nil {
		t.Fatal("a stop of a question the crew just took must exit non-zero")
	}
	assertContains(t, "error", errorText(err), "no turn to stop yet", "Stop again in a moment")
}

// A queued question is stopped by its full ID, which the gateway has never listed; the answer is
// the gateway's, not a claim that the gateway was not found.
func TestConversationStop_QueuedQuestionIsNotAGatewayError(t *testing.T) {
	s := newFakeAPIServer(t)
	serveControl(s, stopBase, 200, stopBody("cancelled", "Cancelled a question that was still waiting in the queue; the crew never saw it."))
	out, _, err := s.kmctl(t, "conversation", "stop", "demo", convID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "was not found") {
		t.Errorf("a queued question is not a missing gateway:\n%s", out)
	}
	assertContains(t, "out", out, "never saw it")
}

func TestConversationStop_UnknownConversationIsNotAMissingGateway(t *testing.T) {
	s := newFakeAPIServer(t)
	serveControl(s, stopBase, 404, "conversation not found")
	_, _, err := s.kmctl(t, "conversation", "stop", "demo", convID)
	if err == nil {
		t.Fatal("want an error")
	}
	text := errorText(err)
	assertContains(t, "error", text, "conversation not found on crew")
	if strings.Contains(text, "gateway of crew") && strings.Contains(text, "was not found") {
		t.Errorf("says the gateway is missing:\n%s", text)
	}
}

func TestConversationStop_OlderGatewayKeepsTheOldWording(t *testing.T) {
	s := newFakeAPIServer(t)
	serveControl(s, stopBase, 200, `{"conversationId":"`+convID+`","action":"stop_requested","threads":["t-1"]}`)
	out, _, err := s.kmctl(t, "conversation", "stop", "demo", convID)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "out", out, "stopped conversation "+convID, "1 thread(s) (t-1)")
}

const stateBase = controlBase + "/state"

const stateBody = `{"conversationId":"` + convID + `","state":"running","turns":[` +
	`{"threadId":"t-1","question":"what is failing?","state":"stopped","startedAt":"2026-11-24T10:00:00Z","endedAt":"2026-11-24T10:01:00Z"},` +
	`{"threadId":"t-2","question":"and now?","state":"running","stopRequested":true},` +
	`{"question":"third question","state":"queued","queuePosition":2}]}`

func TestConversationGet_ShowsEachTurnsState(t *testing.T) {
	s := newFakeAPIServer(t)
	serveControl(s, stateBase, 200, stateBody)
	out, _, err := s.kmctl(t, "conversation", "get", "demo", convID)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "table", out, "conversation "+convID+": running", "t-1", "stopped", "running (stop requested)", "queued (position 2)", "third question")
	js, _, err := s.kmctl(t, "conversation", "get", "demo", convID, "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "json", js, `"state": "running"`, `"queuePosition": 2`)
}

func TestConversationGet_Errors(t *testing.T) {
	s := newFakeAPIServer(t)
	serveControl(s, stateBase, 404, "conversation not found")
	_, _, err := s.kmctl(t, "conversation", "get", "demo", convID)
	assertContains(t, "unknown", errorText(err), "conversation not found on crew")

	s = newFakeAPIServer(t)
	serveControl(s, stateBase, 404, "404 page not found")
	_, _, err = s.kmctl(t, "conversation", "get", "demo", convID)
	assertContains(t, "old gateway", errorText(err), "has no state read", "older than this kmctl")
}
