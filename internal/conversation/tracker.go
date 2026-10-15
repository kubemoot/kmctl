// Package conversation renders a crew's discussion stream for the terminal and
// for scripts: a live view (full), line-by-line text (plain), or newline-delimited
// JSON (json), each ending with the answer, a summary of how it was made, and the
// command that continues the conversation.
package conversation

import (
	"fmt"
	"time"

	"github.com/kubemoot/kmctl/internal/discussion"
)

// Signal kinds an agent can contribute.
const (
	signalAgree      = "agree"
	signalConcern    = "concern"
	signalBlock      = "block"
	signalFailure    = "failure"
	signalStandAside = "stand_aside"
)

// Agent is what the stream has shown about one agent so far.
type Agent struct {
	Name    string
	Step    string
	GPU     string
	Model   string
	Signal  string
	Summary string
	// Reason is why the agent stood aside; WaitReason is why it last waited for
	// GPU capacity. They are kept apart because the stream gives them separately.
	Reason     string
	WaitReason string
	Artifact   *Artifact
	StoodAside bool
	// Waited reports whether the agent waited for GPU capacity at least once.
	Waited bool
	// Triage and Evaluation are how long the agent spent in each step; zero when
	// the stream did not show both ends of the step.
	Triage     time.Duration
	Evaluation time.Duration
	// InputTokens, OutputTokens and Inference add up the agent's signals; Tools are
	// the tools it has enabled and ToolError what failed, as its signals report them.
	InputTokens  int64
	OutputTokens int64
	Inference    time.Duration
	Tools        []string
	ToolError    string

	triageAt mark
	evalAt   mark
}

// mark is when a step began, and whether that time is a published timestamp or the
// time kmctl received the event. A step is timed only between marks of one kind.
type mark struct {
	at        time.Time
	published bool
}

// Done reports whether the agent has given its signal.
func (a *Agent) Done() bool { return a.Signal != "" }

// Tracker accumulates a discussion's state from its events. Start, End and
// FirstSignal are when kmctl received events; step durations come from the
// timestamps the crew published when the gateway forwards them, else from when kmctl
// received each event.
type Tracker struct {
	Start       time.Time
	End         time.Time
	FirstSignal time.Time
	ThreadID    string
	Answer      string
	Errors      []string
	// Busy is who the latest activity event said is working, until the answer.
	Busy []discussion.Activity
	// Subcommittee is the agents the coordinator convened, once it says.
	Subcommittee *discussion.Subcommittee
	// Stamped reports whether any event carried a timestamp; ThreadAt and AnswerAt
	// are the published times of the thread's start and of its answer.
	Stamped  bool
	ThreadAt time.Time
	AnswerAt time.Time

	agents []*Agent
	byName map[string]*Agent
	// receivedAt is when kmctl received the event being applied, and published
	// whether the time the handlers see is that event's published timestamp.
	receivedAt time.Time
	published  bool
}

// NewTracker starts tracking a discussion at start.
func NewTracker(start time.Time) *Tracker {
	return &Tracker{Start: start, End: start, byName: map[string]*Agent{}}
}

// Agents is every agent seen, in the order each first appeared.
func (t *Tracker) Agents() []*Agent { return t.agents }

// Change is what one event changed: the agent it concerns, if any, and the step
// duration it completed.
type Change struct {
	Agent      *Agent
	Triage     time.Duration
	Evaluation time.Duration
}

// trackers applies each event type; a type without an entry changes nothing.
var trackers = map[string]func(*Tracker, discussion.Event, time.Time) Change{
	"thread_found": func(t *Tracker, e discussion.Event, at time.Time) Change {
		t.ThreadID = e.ThreadID
		if t.published {
			t.ThreadAt = at
		}
		return Change{}
	},
	"phase":        (*Tracker).applyPhase,
	"finding":      (*Tracker).applyFinding,
	"subcommittee": (*Tracker).applySubcommittee,
	"synthesis": func(t *Tracker, e discussion.Event, at time.Time) Change {
		t.Answer, t.Busy = e.Content, nil
		if t.published {
			t.AnswerAt = at
		}
		return Change{}
	},
	"activity": (*Tracker).applyActivity,
	"error": func(t *Tracker, e discussion.Event, _ time.Time) Change {
		t.Errors = append(t.Errors, e.Error)
		return Change{}
	},
}

// Apply records event e received at at. Steps are timed by the event's own
// timestamp when it has one.
func (t *Tracker) Apply(e discussion.Event, at time.Time) Change {
	if at.After(t.End) {
		t.End = at
	}
	apply, ok := trackers[e.Type]
	if !ok {
		return Change{}
	}
	t.receivedAt = at
	stamp, stamped := publishedAt(e)
	t.published = stamped
	if !stamped {
		return apply(t, e, at)
	}
	t.Stamped = true
	return apply(t, e, stamp)
}

// publishedAt is the event's timestamp, when it has a valid one.
func publishedAt(e discussion.Event) (time.Time, bool) {
	if e.Timestamp == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, e.Timestamp)
	return t, err == nil
}

func (t *Tracker) agent(name string) *Agent {
	if a, ok := t.byName[name]; ok {
		return a
	}
	a := &Agent{Name: name}
	t.byName[name] = a
	t.agents = append(t.agents, a)
	return a
}

// phaseSteps applies each phase status to its agent.
var phaseSteps = map[string]func(*Tracker, *Agent, discussion.Event, time.Time) Change{
	"triaging":   applyTriaging,
	"evaluating": applyEvaluating,
	"waiting":    applyWaiting,
}

func (t *Tracker) applyPhase(e discussion.Event, at time.Time) Change {
	a := t.agent(e.Agent)
	setIfPresent(&a.GPU, e.GPU)
	if e.StoodAside {
		a.StoodAside, a.Reason = true, e.Reason
		a.addSignalMeta(e.SignalMeta)
		return t.settle(a, signalStandAside, "", at)
	}
	a.Step = e.Status
	if step, ok := phaseSteps[e.Status]; ok {
		return step(t, a, e, at)
	}
	return Change{Agent: a}
}

// applyTriaging starts a triage step; a triage that starts again replaces any
// step still open.
func applyTriaging(t *Tracker, a *Agent, _ discussion.Event, at time.Time) Change {
	a.triageAt, a.evalAt = t.mark(at), mark{}
	return Change{Agent: a}
}

func applyEvaluating(t *Tracker, a *Agent, _ discussion.Event, at time.Time) Change {
	a.evalAt = t.mark(at)
	return Change{Agent: a, Triage: closeStep(&a.triageAt, &a.Triage, t.mark(at))}
}

// mark is at, of the kind of time the event being applied carries.
func (t *Tracker) mark(at time.Time) mark { return mark{at: at, published: t.published} }

func applyWaiting(_ *Tracker, a *Agent, e discussion.Event, _ time.Time) Change {
	a.Waited = true
	setIfPresent(&a.Model, e.Model)
	a.WaitReason = e.Reason
	return Change{Agent: a}
}

// applyActivity records who is busy and fills in the accelerator and model of the
// agents already shown. Once the answer is in, no one is shown as busy.
func (t *Tracker) applyActivity(e discussion.Event, _ time.Time) Change {
	if t.Answer != "" {
		return Change{}
	}
	t.Busy = e.Agents
	for _, b := range e.Agents {
		if a, ok := t.byName[b.Agent]; ok {
			setIfPresent(&a.GPU, b.GPU)
			setIfPresent(&a.Model, b.Model)
		}
	}
	return Change{}
}

func (t *Tracker) applyFinding(e discussion.Event, at time.Time) Change {
	a := t.agent(e.Agent)
	setIfPresent(&a.GPU, e.GPU)
	text := e.Content
	if text == "" {
		text = e.Summary
	}
	a.Artifact = artifactOf(e.SignalMeta, text)
	a.addSignalMeta(e.SignalMeta)
	return t.settle(a, e.Signal, text, at)
}

// addSignalMeta adds what a signal says about how it was made.
func (a *Agent) addSignalMeta(m *discussion.SignalMeta) {
	if m == nil {
		return
	}
	setIfPresent(&a.Model, m.ModelName)
	a.InputTokens += m.InputTokens
	a.OutputTokens += m.OutputTokens
	a.Inference += time.Duration(m.InferenceMs) * time.Millisecond
	if len(m.ToolsUsed) > 0 {
		a.Tools = m.ToolsUsed
	}
	if err := toolError(m); err != "" {
		a.ToolError = err
	}
}

// toolError describes a failure's tool error: the tool, how many calls failed, and
// the last error, as far as the signal says.
func toolError(m *discussion.SignalMeta) string {
	if m.FailedTool == "" && m.LastError == "" {
		return ""
	}
	text := firstNonEmpty(m.FailedTool, m.FailureType)
	if m.FailureCount > 1 {
		text += fmt.Sprintf(" failed %d times", m.FailureCount)
	}
	return withReason(text, shortSummary(m.LastError, summaryWidth))
}

// applySubcommittee records the agents the coordinator convened. A later selection
// replaces an earlier one; the reasons of an earlier triage selection stay with the
// agents that are still convened.
func (t *Tracker) applySubcommittee(e discussion.Event, _ time.Time) Change {
	if e.Subcommittee == nil {
		return Change{}
	}
	next := *e.Subcommittee
	next.Members = withEarlierReasons(next.Members, t.Subcommittee)
	if earlier := t.Subcommittee; earlier != nil {
		setIfPresent(&next.Brief, firstNonEmpty(next.Brief, earlier.Brief))
		if len(next.Technologies) == 0 {
			next.Technologies = earlier.Technologies
		}
	}
	t.Subcommittee = &next
	return Change{}
}

func withEarlierReasons(members []discussion.SubcommitteeMember, earlier *discussion.Subcommittee) []discussion.SubcommitteeMember {
	if earlier == nil {
		return members
	}
	reasons := map[string]string{}
	for _, m := range earlier.Members {
		reasons[m.Agent] = m.Reason
	}
	out := make([]discussion.SubcommitteeMember, len(members))
	for i, m := range members {
		if m.Reason == "" {
			m.Reason = reasons[m.Agent]
		}
		out[i] = m
	}
	return out
}

// settle records an agent's signal and closes the step it was in.
func (t *Tracker) settle(a *Agent, signal, text string, at time.Time) Change {
	a.Signal, a.Step = signal, signal
	a.Summary = shortSummary(text, summaryWidth)
	if a.Artifact != nil {
		a.Summary = a.Artifact.describe()
	}
	if t.FirstSignal.IsZero() {
		t.FirstSignal = t.receivedAt
	}
	return Change{
		Agent:      a,
		Triage:     closeStep(&a.triageAt, &a.Triage, t.mark(at)),
		Evaluation: closeStep(&a.evalAt, &a.Evaluation, t.mark(at)),
	}
}

// closeStep ends the step that began at *began, if one is open: it records the
// step's duration in *took, clears *began, and returns the duration. It returns
// zero when no step was open, when the two ends are different kinds of time
// (published and received), or when the end is before the start.
func closeStep(began *mark, took *time.Duration, end mark) time.Duration {
	start := *began
	*began = mark{}
	if start.at.IsZero() || start.published != end.published || end.at.Before(start.at) {
		return 0
	}
	*took = end.at.Sub(start.at)
	return *took
}

// signalCounts counts each agent's latest signal, so an agent that signals again
// is counted once.
func (t *Tracker) signalCounts() map[string]int {
	counts := map[string]int{}
	for _, a := range t.agents {
		if a.Signal != "" {
			counts[a.Signal]++
		}
	}
	return counts
}

func setIfPresent(dst *string, v string) {
	if v != "" {
		*dst = v
	}
}
