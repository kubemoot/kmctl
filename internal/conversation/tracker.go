// Package conversation renders a crew's discussion stream for the terminal and
// for scripts: a live view (full), line-by-line text (plain), or newline-delimited
// JSON (json), each ending with the answer, a summary of how it was made, and the
// command that continues the conversation.
package conversation

import (
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

	triageAt time.Time
	evalAt   time.Time
}

// Done reports whether the agent has given its signal.
func (a *Agent) Done() bool { return a.Signal != "" }

// Tracker accumulates a discussion's state from its events. Times are when kmctl
// received each event, since the gateway's events carry no timestamps.
type Tracker struct {
	Start       time.Time
	End         time.Time
	FirstSignal time.Time
	ThreadID    string
	Answer      string
	Errors      []string
	// Busy is who the latest activity event said is working, until the answer.
	Busy []discussion.Activity

	agents []*Agent
	byName map[string]*Agent
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
	"thread_found": func(t *Tracker, e discussion.Event, _ time.Time) Change { t.ThreadID = e.ThreadID; return Change{} },
	"phase":        (*Tracker).applyPhase,
	"finding":      (*Tracker).applyFinding,
	"synthesis": func(t *Tracker, e discussion.Event, _ time.Time) Change {
		t.Answer, t.Busy = e.Content, nil
		return Change{}
	},
	"activity": (*Tracker).applyActivity,
	"error": func(t *Tracker, e discussion.Event, _ time.Time) Change {
		t.Errors = append(t.Errors, e.Error)
		return Change{}
	},
}

// Apply records event e received at at.
func (t *Tracker) Apply(e discussion.Event, at time.Time) Change {
	if at.After(t.End) {
		t.End = at
	}
	if apply, ok := trackers[e.Type]; ok {
		return apply(t, e, at)
	}
	return Change{}
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
func applyTriaging(_ *Tracker, a *Agent, _ discussion.Event, at time.Time) Change {
	a.triageAt, a.evalAt = at, time.Time{}
	return Change{Agent: a}
}

func applyEvaluating(_ *Tracker, a *Agent, _ discussion.Event, at time.Time) Change {
	a.evalAt = at
	return Change{Agent: a, Triage: closeStep(&a.triageAt, &a.Triage, at)}
}

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
	text := e.Content
	if text == "" {
		text = e.Summary
	}
	a.Artifact = parseArtifact(text)
	return t.settle(a, e.Signal, text, at)
}

// settle records an agent's signal and closes the step it was in.
func (t *Tracker) settle(a *Agent, signal, text string, at time.Time) Change {
	a.Signal, a.Step = signal, signal
	a.Summary = shortSummary(text, summaryWidth)
	if a.Artifact != nil {
		a.Summary = a.Artifact.describe()
	}
	if t.FirstSignal.IsZero() {
		t.FirstSignal = at
	}
	return Change{
		Agent:      a,
		Triage:     closeStep(&a.triageAt, &a.Triage, at),
		Evaluation: closeStep(&a.evalAt, &a.Evaluation, at),
	}
}

// closeStep ends the step that began at *began, if one is open: it records the
// step's duration in *took, clears *began, and returns the duration (zero when
// no step was open).
func closeStep(began *time.Time, took *time.Duration, at time.Time) time.Duration {
	if began.IsZero() {
		return 0
	}
	*took = at.Sub(*began)
	*began = time.Time{}
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
