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

func serviceName(crew string) string { return crew + "-discussion" }

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
}

// Ask starts a turn against a crew and returns the conversationId. A non-empty
// conversationID continues an existing conversation.
func Ask(ctx context.Context, rc rest.Interface, namespace, crew, message, conversationID string) (string, error) {
	body, err := json.Marshal(map[string]string{"message": message, "conversationId": conversationID})
	if err != nil {
		return "", err
	}
	raw, err := client.ServiceProxy(rc.Post(), namespace, serviceName(crew), gatewayPort).
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
	return client.ServiceProxy(rc.Get(), namespace, serviceName(crew), gatewayPort).
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

// renderers formats each event type; a type without an entry is skipped.
var renderers = map[string]func(Event) string{
	"connected":    func(Event) string { return "* connected" },
	"thread_found": func(e Event) string { return "* thread " + e.ThreadID },
	"phase":        renderPhase,
	"finding":      renderFinding,
	"synthesis":    func(e Event) string { return "\nANSWER:\n" + e.Content },
	"error":        func(e Event) string { return "ERROR: " + e.Error },
	"done":         func(Event) string { return "* done" },
}

// Render formats an event as a human-readable line (empty string = skip).
func Render(e Event) string {
	if render, ok := renderers[e.Type]; ok {
		return render(e)
	}
	return ""
}

// renderPhase shows an agent's phase, with the GPU it ran on and whether it stood aside.
func renderPhase(e Event) string {
	line := fmt.Sprintf("  %-22s %s", e.Agent, e.Status)
	if e.GPU != "" {
		line += " [" + e.GPU + "]"
	}
	if e.StoodAside {
		line += " (stood aside)"
	}
	return line
}

// renderFinding shows an agent's signal with its summary, or the first line of its
// content when it gave no summary.
func renderFinding(e Event) string {
	summary := e.Summary
	if summary == "" {
		summary = firstLine(e.Content)
	}
	return fmt.Sprintf("  %-22s %s: %s", e.Agent, e.Signal, summary)
}

// Terminal reports whether an event ends the stream.
func Terminal(e Event) bool { return e.Type == "done" || e.Type == "error" }

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
