package conversation

import (
	"encoding/json"
	"io"
	"time"

	"github.com/kubemoot/kmctl/internal/discussion"
)

// SchemaVersion is the version of the -o json line shapes. It changes when a
// field is removed or changes meaning; new fields keep the version.
const SchemaVersion = 1

// jsonConversation is the first line: which conversation the stream follows.
type jsonConversation struct {
	SchemaVersion  int    `json:"schemaVersion"`
	Type           string `json:"type"`
	ConversationID string `json:"conversationId"`
	Crew           string `json:"crew"`
	Namespace      string `json:"namespace,omitempty"`
}

// jsonEvent is one gateway event with what kmctl derived from it.
type jsonEvent struct {
	SchemaVersion int `json:"schemaVersion"`
	discussion.Event
	ElapsedMs    int64     `json:"elapsedMs"`
	TriageMs     int64     `json:"triageMs,omitempty"`
	EvaluationMs int64     `json:"evaluationMs,omitempty"`
	Artifact     *Artifact `json:"artifact,omitempty"`
}

// jsonResult is the last line: the answer, the summary, and the follow-up command.
type jsonResult struct {
	SchemaVersion  int     `json:"schemaVersion"`
	Type           string  `json:"type"`
	ConversationID string  `json:"conversationId"`
	Answer         string  `json:"answer"`
	Summary        Summary `json:"summary"`
	Next           string  `json:"next"`
}

// jsonSink writes newline-delimited JSON: a conversation line, one line per
// event (heartbeats skipped), and a result line.
type jsonSink struct {
	enc *json.Encoder
	o   Options
}

func newJSONSink(w io.Writer, o Options) *jsonSink {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &jsonSink{enc: enc, o: o}
}

func (j *jsonSink) begin() error {
	return j.enc.Encode(jsonConversation{
		SchemaVersion: SchemaVersion, Type: "conversation",
		ConversationID: j.o.ConversationID, Crew: j.o.Crew, Namespace: j.o.Namespace,
	})
}

func (j *jsonSink) event(t *Tracker, e discussion.Event, c Change, at time.Time) error {
	if e.Type == "heartbeat" {
		return nil
	}
	out := jsonEvent{
		SchemaVersion: SchemaVersion, Event: e, ElapsedMs: millis(at.Sub(t.Start)),
		TriageMs: millis(c.Triage), EvaluationMs: millis(c.Evaluation),
	}
	if e.Type == "finding" && c.Agent != nil {
		out.Artifact = c.Agent.Artifact
	}
	return j.enc.Encode(out)
}

func (j *jsonSink) finish(t *Tracker) error {
	return j.enc.Encode(jsonResult{
		SchemaVersion: SchemaVersion, Type: "result", ConversationID: j.o.ConversationID,
		Answer: t.Answer, Summary: t.Summarize(j.o.since()),
		Next: FollowUp(j.o.Crew, j.o.Namespace, j.o.ConversationID),
	})
}
