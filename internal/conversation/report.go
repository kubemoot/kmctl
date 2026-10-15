package conversation

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// signalOrder is the order signal kinds are listed in a summary; other kinds
// follow alphabetically.
var signalOrder = []string{signalAgree, signalConcern, signalBlock, signalFailure, signalStandAside}

// Summary is how an answer was made, from what the event stream carries. Fields
// the stream does not carry (tokens, tool calls, retries, which agents the
// coordinator convened and why) are absent rather than estimated.
type Summary struct {
	// TimeSource says where the times come from: "received" means the moment
	// kmctl received each event.
	TimeSource string `json:"timeSource"`
	// Since says when the clock started: "ask" when kmctl started the turn,
	// "watch" when kmctl attached to a turn already under way.
	Since              string         `json:"since"`
	TotalSeconds       float64        `json:"totalSeconds"`
	FirstSignalSeconds *float64       `json:"firstSignalSeconds,omitempty"`
	Participants       []string       `json:"participants"`
	StoodAside         []string       `json:"stoodAside,omitempty"`
	Signals            map[string]int `json:"signals,omitempty"`
	Accelerators       []string       `json:"accelerators,omitempty"`
	Models             []string       `json:"models,omitempty"`
	WaitedForGPU       []string       `json:"waitedForGpu,omitempty"`
	Errors             []string       `json:"errors,omitempty"`
	Agents             []AgentReport  `json:"agents"`
}

// AgentReport is one agent's part in a Summary.
type AgentReport struct {
	Name              string    `json:"name"`
	Signal            string    `json:"signal,omitempty"`
	Summary           string    `json:"summary,omitempty"`
	Reason            string    `json:"reason,omitempty"`
	GPU               string    `json:"gpu,omitempty"`
	Model             string    `json:"model,omitempty"`
	TriageSeconds     *float64  `json:"triageSeconds,omitempty"`
	EvaluationSeconds *float64  `json:"evaluationSeconds,omitempty"`
	Artifact          *Artifact `json:"artifact,omitempty"`
}

// Clock starts: when kmctl started the turn, or when it attached to one.
const (
	SinceAsk   = "ask"
	SinceWatch = "watch"
)

// Summarize builds the Summary of what the tracker saw; since is SinceAsk or
// SinceWatch.
func (t *Tracker) Summarize(since string) Summary {
	s := Summary{
		TimeSource:   "received",
		Since:        since,
		TotalSeconds: seconds(t.End.Sub(t.Start)),
		Participants: []string{},
		Agents:       []AgentReport{},
		Errors:       t.Errors,
	}
	if !t.FirstSignal.IsZero() {
		s.FirstSignalSeconds = optionalSeconds(t.FirstSignal.Sub(t.Start))
	}
	if counts := t.signalCounts(); len(counts) > 0 {
		s.Signals = counts
	}
	accelerators, models := set{}, set{}
	for _, a := range t.agents {
		s.Participants = append(s.Participants, a.Name)
		s.StoodAside = appendIf(s.StoodAside, a.StoodAside, a.Name)
		s.WaitedForGPU = appendIf(s.WaitedForGPU, a.Waited, a.Name)
		accelerators.add(a.GPU)
		models.add(a.Model)
		s.Agents = append(s.Agents, reportAgent(a))
	}
	s.Accelerators, s.Models = accelerators.sorted(), models.sorted()
	return s
}

func appendIf(list []string, ok bool, v string) []string {
	if ok {
		return append(list, v)
	}
	return list
}

func reportAgent(a *Agent) AgentReport {
	return AgentReport{
		Name: a.Name, Signal: a.Signal, Summary: a.Summary, Reason: a.Reason,
		GPU: a.GPU, Model: a.Model, Artifact: a.Artifact,
		TriageSeconds:     optionalSeconds(a.Triage),
		EvaluationSeconds: optionalSeconds(a.Evaluation),
	}
}

// Markdown renders the summary as a short markdown section, written to read as
// a continuation of the answer's own closing notes.
func (s Summary) Markdown() string {
	lines := []string{s.totalLine()}
	optional := []string{
		s.agentsLine(), s.signalsLine(), joinLine("Accelerators", s.Accelerators), joinLine("Models", s.Models),
		joinLine("Waited for GPU capacity", s.WaitedForGPU), joinLine("Errors", s.Errors),
	}
	for _, line := range optional {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return "**How this answer was made**\n\n- " + strings.Join(lines, "\n- ") + "\n"
}

// totalLine is the elapsed time, labelled with when the clock started.
func (s Summary) totalLine() string {
	label := "Total time (as received by kmctl): "
	if s.Since == SinceWatch {
		label = "Time since watch began (as received by kmctl): "
	}
	line := label + formatSeconds(s.TotalSeconds)
	if s.FirstSignalSeconds != nil {
		line += "; first signal after " + formatSeconds(*s.FirstSignalSeconds)
	}
	return line
}

func (s Summary) agentsLine() string {
	if len(s.Participants) == 0 {
		return ""
	}
	line := fmt.Sprintf("Agents: %d took part (%s)", len(s.Participants), strings.Join(s.Participants, ", "))
	if len(s.StoodAside) > 0 {
		line += fmt.Sprintf("; %d stood aside (%s)", len(s.StoodAside), strings.Join(s.StoodAside, ", "))
	}
	return line
}

func (s Summary) signalsLine() string {
	if len(s.Signals) == 0 {
		return ""
	}
	parts := make([]string, 0, len(s.Signals))
	for _, kind := range orderedKinds(s.Signals) {
		parts = append(parts, fmt.Sprintf("%d %s", s.Signals[kind], kind))
	}
	return "Signals: " + strings.Join(parts, ", ")
}

func joinLine(label string, values []string) string {
	if len(values) == 0 {
		return ""
	}
	return label + ": " + strings.Join(values, ", ")
}

// orderedKinds lists the signal kinds present, known kinds first.
func orderedKinds(counts map[string]int) []string {
	known := map[string]bool{}
	var kinds []string
	for _, k := range signalOrder {
		known[k] = true
		if counts[k] > 0 {
			kinds = append(kinds, k)
		}
	}
	var other []string
	for k := range counts {
		if !known[k] {
			other = append(other, k)
		}
	}
	sort.Strings(other)
	return append(kinds, other...)
}

// FollowUp is the command that asks the next question in the same conversation.
func FollowUp(crew, namespace, conversationID string) string {
	cmd := fmt.Sprintf("kmctl conversation ask %s \"<next question>\"", crew)
	if namespace != "" {
		cmd += " -n " + namespace
	}
	return cmd + " --conversation-id " + conversationID
}

// Reattach is the command that follows the conversation's current turn again.
func Reattach(crew, namespace, conversationID string) string {
	cmd := "kmctl conversation watch " + crew + " " + conversationID
	if namespace != "" {
		cmd += " -n " + namespace
	}
	return cmd
}

func seconds(d time.Duration) float64 { return float64(millis(d)) / 1000 }

func optionalSeconds(d time.Duration) *float64 {
	if d <= 0 {
		return nil
	}
	s := seconds(d)
	return &s
}

func formatSeconds(s float64) string {
	return formatDuration(time.Duration(s * float64(time.Second)))
}

// set collects distinct non-empty strings.
type set map[string]struct{}

func (s set) add(v string) {
	if v != "" {
		s[v] = struct{}{}
	}
}

func (s set) sorted() []string {
	if len(s) == 0 {
		return nil
	}
	out := make([]string, 0, len(s))
	for v := range s {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
