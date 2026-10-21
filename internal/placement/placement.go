// Package placement reads the scheduler's placement decisions from a discussion
// receipt and renders them: one row per model call, then, per call, why it was
// placed there and what else was weighed.
//
// The types mirror PlacementCall in the Kubemoot bus contract (the receipt's
// "calls" array). They are declared here until kmctl moves to a Kubemoot release
// that ships the generated type.
package placement

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Prediction is the completion time a placement predicted, by part, in milliseconds.
type Prediction struct {
	WaitMs   int64 `json:"waitMs"`
	LoadMs   int64 `json:"loadMs"`
	PromptMs int64 `json:"promptMs"`
	// FirstTokenMs is the predicted time to the first token (wait, load, and prompt); 0 when the receipt gives none.
	FirstTokenMs int64 `json:"firstTokenMs,omitempty"`
	GenerateMs   int64 `json:"generateMs"`
	TotalMs      int64 `json:"totalMs"`
}

// Option is one server the model could have run on.
type Option struct {
	Server    string      `json:"server"`
	Model     string      `json:"model,omitempty"`
	Warm      bool        `json:"warm"`
	StartsNow bool        `json:"startsNow"`
	Predicted *Prediction `json:"predicted,omitempty"`
}

// Actual is what the first request of a placement measured.
type Actual struct {
	OK           bool  `json:"ok"`
	WaitMs       int64 `json:"waitMs,omitempty"`
	FirstTokenMs int64 `json:"firstTokenMs,omitempty"`
	TotalMs      int64 `json:"totalMs"`
	InputTokens  int64 `json:"inputTokens"`
	OutputTokens int64 `json:"outputTokens"`
	Requests     int64 `json:"requests"`
}

// Error is how far the prediction was from the first request.
type Error struct {
	TotalAbsMs         int64   `json:"totalAbsMs"`
	TotalRelative      float64 `json:"totalRelative"`
	TotalSignedMs      int64   `json:"totalSignedMs"`
	FirstTokenAbsMs    int64   `json:"firstTokenAbsMs,omitempty"`
	FirstTokenRelative float64 `json:"firstTokenRelative,omitempty"`
}

// Call is one model call the scheduler placed.
type Call struct {
	Agent        string      `json:"agent,omitempty"`
	Phase        string      `json:"phase,omitempty"`
	Kind         string      `json:"kind,omitempty"`
	At           int64       `json:"at,omitempty"`
	Model        string      `json:"model"`
	Server       string      `json:"server"`
	ColdStart    bool        `json:"coldStart,omitempty"`
	Policy       string      `json:"policy"`
	Reason       string      `json:"reason,omitempty"`
	Alternatives []Option    `json:"alternatives,omitempty"`
	Predicted    *Prediction `json:"predicted,omitempty"`
	Actual       *Actual     `json:"actual,omitempty"`
	Error        *Error      `json:"error,omitempty"`
}

// Decoded is a receipt's calls, both as the receipt carried them (Raw, for output
// that must not change them) and decoded (Calls, for rendering).
type Decoded struct {
	Raw   []json.RawMessage
	Calls []Call
}

// Decode reads the "calls" array of a discussion receipt. A receipt that is empty,
// has no calls, or has an empty array decodes to no calls and no error; a receipt
// that is not JSON, or a call that is not an object, is an error.
func Decode(receipt json.RawMessage) (Decoded, error) {
	if len(receipt) == 0 {
		return Decoded{}, nil
	}
	var r struct {
		Calls []json.RawMessage `json:"calls"`
	}
	if err := json.Unmarshal(receipt, &r); err != nil {
		return Decoded{}, fmt.Errorf("read the receipt: %w", err)
	}
	d := Decoded{Raw: r.Calls}
	for i, raw := range r.Calls {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return Decoded{}, fmt.Errorf("placement call %d of the receipt is null", i+1)
		}
		var c Call
		if err := json.Unmarshal(raw, &c); err != nil {
			return Decoded{}, fmt.Errorf("read placement call %d of the receipt: %w", i+1, err)
		}
		d.Calls = append(d.Calls, c)
	}
	return d, nil
}

// Place is "model@server".
func (c Call) Place() string { return c.Model + "@" + c.Server }

// Stats totals a receipt's calls.
type Stats struct {
	Calls    int            `json:"calls"`
	ByPolicy map[string]int `json:"byPolicy"`
	Measured int            `json:"withError"`
	// MeanAbsErrorMs is the mean absolute total error over the calls that have one.
	MeanAbsErrorMs float64 `json:"meanAbsErrorMs"`
}

// Summarize counts the calls per policy and averages their absolute error.
func Summarize(calls []Call) Stats {
	s := Stats{Calls: len(calls), ByPolicy: map[string]int{}}
	var sum int64
	for _, c := range calls {
		s.ByPolicy[policyName(c.Policy)]++
		if c.Error != nil {
			s.Measured++
			sum += c.Error.TotalAbsMs
		}
	}
	if s.Measured > 0 {
		s.MeanAbsErrorMs = float64(sum) / float64(s.Measured)
	}
	return s
}

func policyName(p string) string {
	if p == "" {
		return "unknown"
	}
	return p
}
