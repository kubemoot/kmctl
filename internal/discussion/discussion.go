// Package discussion talks to a crew's discussion gateway over HTTP, through a
// client for the gateway Service (client.Factory.ServiceClient), never direct
// NATS. It starts a turn and streams the discussion as SSE.
package discussion

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"k8s.io/client-go/rest"
)

// OutcomeStopped is the outcome of a turn the person stopped.
const OutcomeStopped = "stopped"

// GatewayPort is the port of the per-crew gateway Service, "<crew>-discussion" in
// the crew's namespace (see the operator's Crew status.discussionEndpoint).
const GatewayPort = "80"

// ServiceName is the name of a crew's discussion gateway Service.
func ServiceName(crew string) string { return crew + "-discussion" }

// Event mirrors the gateway's SSE event shape.
type Event struct {
	Type        string `json:"type"`
	Agent       string `json:"agent,omitempty"`
	Status      string `json:"status,omitempty"`
	Accelerator string `json:"accelerator,omitempty"`
	// GPU is the deprecated old name of Accelerator; a gateway older than this release sends only it.
	GPU        string `json:"gpu,omitempty"`
	Signal     string `json:"signal,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Content    string `json:"content,omitempty"`
	ThreadID   string `json:"threadId,omitempty"`
	Error      string `json:"error,omitempty"`
	StoodAside bool   `json:"stood_aside,omitempty"`
	Model      string `json:"model,omitempty"`
	Reason     string `json:"reason,omitempty"`
	// Outcome is how the turn ended, on a synthesis or done event: OutcomeStopped when the person
	// stopped it, so its content is the text that says so and not an answer. Empty from a gateway
	// older than the stop outcome.
	Outcome string `json:"outcome,omitempty"`
	// Agents are the busy agents of an activity event, and At when the gateway
	// took that reading (RFC 3339).
	Agents []Activity `json:"agents,omitempty"`
	At     string     `json:"at,omitempty"`
	// Timestamp is when the crew published the message behind the event (RFC 3339);
	// absent on events from an older gateway and on the gateway's own events.
	Timestamp string `json:"timestamp,omitempty"`
	// Receipt is the thread's receipt (the bus contract's DiscussionReceipt) on the
	// done event of a gateway that forwards it; kept as the gateway sent it.
	Receipt json.RawMessage `json:"receipt,omitempty"`
	// SignalMeta is the signal metadata of a finding or stand-aside event.
	*SignalMeta
	// Subcommittee is the content of a subcommittee event.
	*Subcommittee
}

// SignalMeta is how an agent's signal was made: the model, the tokens and inference
// time of its call, the tools it has enabled, a workspace object it stored, and for a
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

// ArtifactRef names a contribution an agent stored in the discussion's workspace. The gateway
// still carries it as metadata.artifact.
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
	Agent       string `json:"agent"`
	Doing       string `json:"doing"`
	Accelerator string `json:"accelerator,omitempty"`
	// GPU is the deprecated old name of Accelerator; a gateway older than this release sends only it.
	GPU   string `json:"gpu,omitempty"`
	Model string `json:"model,omitempty"`
	Since string `json:"since,omitempty"`
}

// AcceleratorName is the accelerator the event names: Accelerator, else the deprecated GPU.
func (e Event) AcceleratorName() string { return firstSet(e.Accelerator, e.GPU) }

// AcceleratorName is the accelerator the activity names: Accelerator, else the deprecated GPU.
func (a Activity) AcceleratorName() string { return firstSet(a.Accelerator, a.GPU) }

// fillAcceleratorNames sets Accelerator and the deprecated GPU of the event and its agents to
// the same name, so a reader of either field, and the JSON kmctl prints, see the name whichever
// one the gateway sent. Remove it with the deprecated GPU fields in the release after this one.
func (e *Event) fillAcceleratorNames() {
	e.Accelerator = e.AcceleratorName()
	e.GPU = e.Accelerator
	for i := range e.Agents {
		e.Agents[i].Accelerator = e.Agents[i].AcceleratorName()
		e.Agents[i].GPU = e.Agents[i].Accelerator
	}
}

func firstSet(current, deprecated string) string {
	if current != "" {
		return current
	}
	return deprecated
}

// Ask starts a turn against a crew and returns the conversationId. A non-empty
// conversationID continues an existing conversation. The question carries this
// machine's time zone and local date and time, so the crew's tools use the date of
// the person asking, not the date where the crew runs.
func Ask(ctx context.Context, rc rest.Interface, crew, message, conversationID string) (string, error) {
	body, err := json.Marshal(askBody(message, conversationID, CurrentRequester(time.Now())))
	if err != nil {
		return "", err
	}
	raw, err := rc.Post().
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
func Stream(ctx context.Context, rc rest.Interface, crew, conversationID string) (io.ReadCloser, error) {
	return rc.Get().
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
		e.fillAcceleratorNames()
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

// Stopped reports whether the event says the person stopped the turn.
func (e Event) Stopped() bool { return e.Outcome == OutcomeStopped }

// Terminal reports whether an event ends the stream.
func Terminal(e Event) bool { return e.Type == "done" || e.Type == "error" }

// ErrNoClose means the discussion stream ended before the thread closed.
var ErrNoClose = errors.New("the discussion stream ended before the thread closed")

// ErrStreamReported means the gateway sent an error event.
var ErrStreamReported = errors.New("the discussion stream reported an error")

// reportedErr is ErrStreamReported with the gateway's message, when it gave one.
func reportedErr(message string) error {
	if message == "" {
		return ErrStreamReported
	}
	return fmt.Errorf("%w: %s", ErrStreamReported, message)
}

// Closed is what a discussion stream says about the turn's closing thread.
type Closed struct {
	// ThreadID is the last thread the stream announced, "" when it announced none.
	ThreadID string
	// Receipt is the receipt on the closing event of the latest thread, nil when that
	// event carries none or no thread closed.
	Receipt json.RawMessage
	// Done is set once a thread has closed.
	Done bool
}

// FetchReceipt follows a conversation's discussion stream to its end and returns what
// it says about the closing thread. A turn that already closed replays from the
// discussion stream, which keeps about a day of discussions. onEvent, when set, sees
// every event as it arrives. On an error the Closed value still names the thread
// seen so far.
func FetchReceipt(ctx context.Context, rc rest.Interface, crew, conversationID string, onEvent func(Event)) (Closed, error) {
	stream, err := Stream(ctx, rc, crew, conversationID)
	if err != nil {
		return Closed{}, err
	}
	defer func() { _ = stream.Close() }()
	return ReceiptFromStream(stream, onEvent)
}

// ReceiptFromStream reads an SSE stream to its end. The receipt is that of the latest
// thread: a done event sets it, and a later thread_found (the coordinator starting the
// turn's thread again) clears it until that thread closes. A stream that is cut after
// a thread closed still returns that thread's receipt.
func ReceiptFromStream(r io.Reader, onEvent func(Event)) (Closed, error) {
	var c Closed
	var failure *string
	err := ParseSSE(r, func(e Event) bool {
		if onEvent != nil {
			onEvent(e)
		}
		switch e.Type {
		case "thread_found":
			c = Closed{ThreadID: e.ThreadID}
		case "done":
			c.Done, c.Receipt = true, e.Receipt
		case "error":
			failure = &e.Error
			return true
		}
		return false
	})
	switch {
	case failure != nil:
		return c, reportedErr(*failure)
	case c.Done:
		return c, nil
	case err != nil:
		return c, err
	}
	return c, ErrNoClose
}
