// Package discussion talks to a crew's discussion gateway through the API
// server's service proxy (so it uses the active kubeconfig credentials, never
// direct NATS). It starts a turn and streams the discussion as SSE.
package discussion

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/kubemoot/kmctl/internal/client"
	"k8s.io/client-go/rest"
)

// The per-crew gateway Service is "<crew>-discussion" on port 80 in the crew's
// namespace (see the operator's Crew status.discussionEndpoint).
const gatewayPort = "80"

// ServiceName is the name of a crew's discussion gateway Service.
func ServiceName(crew string) string { return crew + "-discussion" }

// Event mirrors the gateway's SSE event shape.
type Event struct {
	Type       string `json:"type"`
	Agent      string `json:"agent,omitempty"`
	Status     string `json:"status,omitempty"`
	GPU        string `json:"gpu,omitempty"`
	Signal     string `json:"signal,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Content    string `json:"content,omitempty"`
	ThreadID   string `json:"threadId,omitempty"`
	Error      string `json:"error,omitempty"`
	StoodAside bool   `json:"stood_aside,omitempty"`
	Model      string `json:"model,omitempty"`
	Reason     string `json:"reason,omitempty"`
	// Agents are the busy agents of an activity event, and At when the gateway
	// took that reading (RFC 3339).
	Agents []Activity `json:"agents,omitempty"`
	At     string     `json:"at,omitempty"`
}

// Activity is one busy agent in an activity event: what it is doing (waking,
// triaging, evaluating, waiting_for_accelerator, reasoning, waiting_for_agents,
// synthesizing), where, and since when.
type Activity struct {
	Agent string `json:"agent"`
	Doing string `json:"doing"`
	GPU   string `json:"gpu,omitempty"`
	Model string `json:"model,omitempty"`
	Since string `json:"since,omitempty"`
}

// Ask starts a turn against a crew and returns the conversationId. A non-empty
// conversationID continues an existing conversation.
func Ask(ctx context.Context, rc rest.Interface, namespace, crew, message, conversationID string) (string, error) {
	body, err := json.Marshal(map[string]string{"message": message, "conversationId": conversationID})
	if err != nil {
		return "", err
	}
	raw, err := client.ServiceProxy(rc.Post(), namespace, ServiceName(crew), gatewayPort).
		Suffix("api", "v1", "discussions", crew).
		Body(body).SetHeader("Content-Type", "application/json").
		DoRaw(ctx)
	if err != nil {
		return "", fmt.Errorf("start discussion with crew %q: %w", crew, err)
	}
	var resp struct {
		ConversationID string `json:"conversationId"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("parse gateway response: %w", err)
	}
	if resp.ConversationID == "" {
		return "", fmt.Errorf("gateway returned no conversationId: %s", string(raw))
	}
	return resp.ConversationID, nil
}

// Stream opens the SSE stream for a conversation.
func Stream(ctx context.Context, rc rest.Interface, namespace, crew, conversationID string) (io.ReadCloser, error) {
	return client.ServiceProxy(rc.Get(), namespace, ServiceName(crew), gatewayPort).
		Suffix("api", "v1", "discussions", crew, conversationID, "stream").
		Stream(ctx)
}

// ParseSSE reads an SSE stream, decoding each event's JSON data and passing it to
// handle. It stops when handle returns true or the stream ends. Non-JSON data
// lines (e.g. comment/heartbeat keep-alives) are skipped.
func ParseSSE(r io.Reader, handle func(Event) bool) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var data []string
	flush := func() bool {
		if len(data) == 0 {
			return false
		}
		payload := strings.Join(data, "\n")
		data = data[:0]
		var e Event
		if err := json.Unmarshal([]byte(payload), &e); err != nil {
			return false
		}
		return handle(e)
	}
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if flush() {
				return nil
			}
			continue
		}
		if val, ok := strings.CutPrefix(line, "data:"); ok {
			data = append(data, strings.TrimSpace(val))
		}
	}
	flush()
	return sc.Err()
}

// ShowsCrewActivity reports whether an event says the crew is working: every event
// except the connection's own (connected, and the heartbeat that only keeps the
// connection open).
func ShowsCrewActivity(e Event) bool { return e.Type != "heartbeat" && e.Type != "connected" }

// Terminal reports whether an event ends the stream.
func Terminal(e Event) bool { return e.Type == "done" || e.Type == "error" }
