package conversation

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kubemoot/kmctl/internal/discussion"
)

// signalOrder is the order signal kinds are listed in a summary; other kinds
// follow alphabetically.
var signalOrder = []string{signalAgree, signalConcern, signalBlock, signalFailure, signalStandAside}

// Summary is how an answer was made, from what the event stream carries. Fields
// the stream does not carry (tokens, models and the subcommittee from an older
// gateway; retries from any gateway) are absent rather than estimated.
type Summary struct {
	// TimeSource says where the step times come from: "received" means the moment
	// kmctl received each event; "published" means the timestamps the crew published.
	// The total time is always as received.
	TimeSource string `json:"timeSource"`
	// Since says when the clock started: "ask" when kmctl started the turn,
	// "watch" when kmctl attached to a turn already under way.
	Since              string   `json:"since"`
	TotalSeconds       float64  `json:"totalSeconds"`
	FirstSignalSeconds *float64 `json:"firstSignalSeconds,omitempty"`
	// CrewSeconds is from the thread's start to its answer, by published times.
	CrewSeconds *float64 `json:"crewSeconds,omitempty"`
	// Subcommittee is the agents the coordinator convened, when the gateway says.
	Subcommittee *discussion.Subcommittee `json:"subcommittee,omitempty"`
	Participants []string                 `json:"participants"`
	StoodAside   []string                 `json:"stoodAside,omitempty"`
	Signals      map[string]int           `json:"signals,omitempty"`
	Accelerators []string                 `json:"accelerators,omitempty"`
	Models       []string                 `json:"models,omitempty"`
	WaitedForGPU []string                 `json:"waitedForGpu,omitempty"`
	InputTokens  int64                    `json:"inputTokens,omitempty"`
	OutputTokens int64                    `json:"outputTokens,omitempty"`
	ToolErrors   []string                 `json:"toolErrors,omitempty"`
	Errors       []string                 `json:"errors,omitempty"`
	Artifacts    []ArtifactReport         `json:"artifacts,omitempty"`
	Agents       []AgentReport            `json:"agents"`
}

// ArtifactReport is an artifact an agent attached, with the command that fetches it.
type ArtifactReport struct {
	Agent string `json:"agent"`
	Key   string `json:"key"`
	Bytes int64  `json:"bytes"`
	Fetch string `json:"fetch,omitempty"`
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
	InferenceSeconds  *float64  `json:"inferenceSeconds,omitempty"`
	InputTokens       int64     `json:"inputTokens,omitempty"`
	OutputTokens      int64     `json:"outputTokens,omitempty"`
	ToolsEnabled      []string  `json:"toolsEnabled,omitempty"`
	ToolError         string    `json:"toolError,omitempty"`
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
		TimeSource:   timeSource(t.Stamped),
		Since:        since,
		TotalSeconds: seconds(t.End.Sub(t.Start)),
		Participants: []string{},
		Agents:       []AgentReport{},
		Errors:       t.Errors,
		Subcommittee: t.Subcommittee,
	}
	if !t.FirstSignal.IsZero() {
		s.FirstSignalSeconds = optionalSeconds(t.FirstSignal.Sub(t.Start))
	}
	if !t.ThreadAt.IsZero() && !t.AnswerAt.IsZero() {
		s.CrewSeconds = optionalSeconds(t.AnswerAt.Sub(t.ThreadAt))
	}
	if counts := t.signalCounts(); len(counts) > 0 {
		s.Signals = counts
	}
	accelerators, models := set{}, set{}
	for _, a := range t.agents {
		s.addAgent(a)
		accelerators.add(a.GPU)
		models.add(a.Model)
	}
	s.Accelerators, s.Models = accelerators.sorted(), models.sorted()
	return s
}

// addAgent adds one agent's part: its name, stand-aside, wait, tokens, tool error,
// artifact, and report.
func (s *Summary) addAgent(a *Agent) {
	s.Participants = append(s.Participants, a.Name)
	s.StoodAside = appendIf(s.StoodAside, a.StoodAside, a.Name)
	s.WaitedForGPU = appendIf(s.WaitedForGPU, a.Waited, a.Name)
	s.ToolErrors = appendIf(s.ToolErrors, a.ToolError != "", a.Name+": "+a.ToolError)
	s.InputTokens += a.InputTokens
	s.OutputTokens += a.OutputTokens
	if a.Artifact != nil {
		s.Artifacts = append(s.Artifacts, ArtifactReport{Agent: a.Name, Key: a.Artifact.Key, Bytes: a.Artifact.Bytes})
	}
	s.Agents = append(s.Agents, reportAgent(a))
}

func timeSource(stamped bool) string {
	if stamped {
		return "published"
	}
	return "received"
}

// WithFetchCommands fills in the command that fetches each artifact.
func (s Summary) WithFetchCommands(crew, namespace string) Summary {
	for i := range s.Artifacts {
		s.Artifacts[i].Fetch = ArtifactFetch(crew, namespace, s.Artifacts[i].Key)
	}
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
		InferenceSeconds:  optionalSeconds(a.Inference),
		InputTokens:       a.InputTokens, OutputTokens: a.OutputTokens,
		ToolsEnabled: a.Tools, ToolError: a.ToolError,
	}
}

// Markdown renders the summary as a short markdown section, written to read as
// a continuation of the answer's own closing notes.
func (s Summary) Markdown() string {
	lines := []string{s.totalLine()}
	optional := []string{
		s.crewTimeLine(), s.subcommitteeLine(), s.agentsLine(), s.signalsLine(), s.tokensLine(),
		joinLine("Accelerators", s.Accelerators), joinLine("Models", s.Models),
		joinLine("Waited for GPU capacity", s.WaitedForGPU), joinLine("Tool errors", s.ToolErrors),
		joinLine("Errors", s.Errors),
	}
	optional = append(optional, s.artifactLines()...)
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

// crewTimeLine is the crew's own time from the thread's start to its answer.
func (s Summary) crewTimeLine() string {
	if s.CrewSeconds == nil {
		return ""
	}
	return "Crew time (thread start to answer, as the crew published): " + formatSeconds(*s.CrewSeconds)
}

// subcommitteeLine names the convened agents, each with its reason when given.
func (s Summary) subcommitteeLine() string {
	sc := s.Subcommittee
	if sc == nil {
		return ""
	}
	if sc.AllAgents || len(sc.Members) == 0 {
		return "Subcommittee: " + memberList(sc)
	}
	return fmt.Sprintf("Subcommittee: %d convened (%s)", len(sc.Members), memberList(sc))
}

// memberList names the convened agents, each with its reason when given, or says
// none were convened.
func memberList(sc *discussion.Subcommittee) string {
	if sc.AllAgents || len(sc.Members) == 0 {
		return "none convened; every agent could take part"
	}
	names := make([]string, 0, len(sc.Members))
	for _, m := range sc.Members {
		names = append(names, withParens(m.Agent, m.Reason))
	}
	return strings.Join(names, ", ")
}

func withParens(text, note string) string {
	if note == "" {
		return text
	}
	return text + " (" + note + ")"
}

func (s Summary) tokensLine() string {
	if s.InputTokens == 0 && s.OutputTokens == 0 {
		return ""
	}
	return fmt.Sprintf("Tokens: %s in, %s out", thousands(s.InputTokens), thousands(s.OutputTokens))
}

// artifactLines are one line per artifact with its fetch command, once filled in.
func (s Summary) artifactLines() []string {
	var lines []string
	for _, a := range s.Artifacts {
		if a.Fetch != "" {
			lines = append(lines, fmt.Sprintf("Artifact from %s (%s): `%s`", a.Agent, formatBytes(a.Bytes), a.Fetch))
		}
	}
	return lines
}

// thousands writes n with comma separators: 12,345.
func thousands(n int64) string {
	digits := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 && digits[i-1] != '-' {
			b.WriteByte(',')
		}
		b.WriteRune(d)
	}
	return b.String()
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

// ArtifactFetch is the command that writes an artifact to stdout.
func ArtifactFetch(crew, namespace, key string) string {
	cmd := "kmctl conversation artifact " + crew + " " + key
	if namespace != "" {
		cmd += " -n " + namespace
	}
	return cmd
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
