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
	// Timestamp is when the crew published the message behind the event (RFC 3339);
	// absent on events from an older gateway and on the gateway's own events.
	Timestamp string `json:"timestamp,omitempty"`
	// SignalMeta is the signal metadata of a finding or stand-aside event.
	*SignalMeta
	// Subcommittee is the content of a subcommittee event.
	*Subcommittee
}

// SignalMeta is how an agent's signal was made: the model, the tokens and inference
// time of its call, the tools it has enabled, an artifact it spilled, and for a
// failure, what failed. A gateway older than these fields sends none of them.
type SignalMeta struct {
	ModelName    string       `json:"modelName,omitempty"`
	InputTokens  int64        `json:"inputTokens,omitempty"`
	OutputTokens int64        `json:"outputTokens,omitempty"`
	TotalTokens  int64        `json:"totalTokens,omitempty"`
	InferenceMs  int64        `json:"inferenceMs,omitempty"`
	ToolsUsed    []string     `json:"toolsUsed,omitempty"`
	FailureType  string       `json:"failureType,omitempty"`
	FailedTool   string       `json:"failedTool,omitempty"`
	FailureCount int64        `json:"failureCount,omitempty"`
	LastError    string       `json:"lastError,omitempty"`
	Artifact     *ArtifactRef `json:"artifact,omitempty"`
}

// ArtifactRef names a contribution an agent spilled to the crew's artifact store.
type ArtifactRef struct {
	Key   string `json:"key"`
	Bytes int64  `json:"bytes,omitempty"`
}

// Subcommittee is the agents the coordinator convened: from its advisory, with the
// brief, or from a triage selection, with each agent's reason.
type Subcommittee struct {
	Source       string               `json:"source,omitempty"`
	Members      []SubcommitteeMember `json:"members,omitempty"`
	AllAgents    bool                 `json:"allAgents,omitempty"`
	Brief        string               `json:"brief,omitempty"`
	Confidence   float64              `json:"confidence,omitempty"`
	Technologies []string             `json:"technologies,omitempty"`
}

// SubcommitteeMember is one convened agent.
type SubcommitteeMember struct {
	Agent      string  `json:"agent"`
	Reason     string  `json:"reason,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
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
