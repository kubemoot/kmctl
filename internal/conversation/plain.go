package conversation

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/kubemoot/kmctl/internal/discussion"
)

// plainSink writes one line per event with the time since the turn began, then
// the raw markdown answer, the summary, and the follow-up command.
type plainSink struct {
	w io.Writer
	o Options
}

func (p *plainSink) begin() error {
	if !p.o.Announce {
		return nil
	}
	_, err := fmt.Fprintf(p.w, "conversation %s\n", p.o.ConversationID)
	return err
}

func (p *plainSink) event(t *Tracker, e discussion.Event, c Change, at time.Time) error {
	line := plainLine(e, c)
	if line == "" {
		return nil
	}
	_, err := fmt.Fprintf(p.w, "[%7s] %s\n", formatDuration(at.Sub(t.Start)), line)
	return err
}

func (p *plainSink) finish(t *Tracker) error {
	_, err := io.WriteString(p.w, closing(t, p.o))
	return err
}

// closing is the end of the plain output: the answer, the summary, and the
// follow-up command as the last line.
func closing(t *Tracker, o Options) string {
	var b strings.Builder
	if t.Answer != "" {
		b.WriteString("\nANSWER:\n" + strings.TrimRight(t.Answer, "\n") + "\n")
	}
	b.WriteString("\n" + o.summarize(t).Markdown())
	b.WriteString("\nNext: " + FollowUp(o.Crew, o.Namespace, o.ConversationID) + "\n")
	return b.String()
}

// plainLines formats each event type; a type without an entry prints nothing.
var plainLines = map[string]func(discussion.Event, Change) string{
	"connected":    func(discussion.Event, Change) string { return "connected" },
	"thread_found": func(e discussion.Event, _ Change) string { return "thread " + e.ThreadID },
	"phase":        func(e discussion.Event, c Change) string { return agentLine(e.Agent, phaseText(e, c)) },
	"finding":      func(e discussion.Event, c Change) string { return agentLine(e.Agent, findingText(e, c)) },
	"subcommittee": func(e discussion.Event, _ Change) string { return subcommitteeText(e.Subcommittee) },
	"activity":     func(e discussion.Event, _ Change) string { return "busy: " + busyText(e.Agents) },
	"synthesis":    func(discussion.Event, Change) string { return "answer ready" },
	"error":        func(e discussion.Event, _ Change) string { return "error: " + e.Error },
	"done":         func(discussion.Event, Change) string { return "done" },
}

func plainLine(e discussion.Event, c Change) string {
	if line, ok := plainLines[e.Type]; ok {
		return line(e, c)
	}
	return ""
}

// doingTexts words each activity step; a step without an entry is shown with its
// underscores as spaces.
var doingTexts = map[string]string{
	"waiting_for_accelerator": "waiting for an accelerator",
	"waiting_for_agents":      "waiting for agents",
}

// busyText is who is busy and on what: "compute evaluating on ollama-rig0,
// coordinator synthesizing".
func busyText(agents []discussion.Activity) string {
	parts := make([]string, 0, len(agents))
	for _, a := range agents {
		doing, ok := doingTexts[a.Doing]
		if !ok {
			doing = strings.ReplaceAll(a.Doing, "_", " ")
		}
		part := a.Agent + " " + doing
		if a.Model != "" && a.Doing == "waiting_for_accelerator" {
			part += " for " + a.Model
		} else if a.GPU != "" {
			part += " on " + a.GPU
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ", ")
}

func agentLine(agent, text string) string {
	return strings.TrimRight(fmt.Sprintf("%-22s %s", agent, text), " ")
}

// phaseText describes an agent's step: where it runs, what it waits for, or why
// it stood aside, with the duration of the step it just finished.
func phaseText(e discussion.Event, c Change) string {
	if e.StoodAside {
		return withReason("stood aside"+details(c, e.SignalMeta), e.Reason)
	}
	text := e.Status
	if e.Status == "waiting" {
		text = "waiting for GPU capacity"
		if e.Model != "" {
			text += " for " + e.Model
		}
		return withReason(text, e.Reason)
	}
	if e.GPU != "" {
		text += " on " + e.GPU
	}
	return text + durations(c)
}

// findingText is an agent's signal with its step duration, model and tokens, and
// short summary.
func findingText(e discussion.Event, c Change) string {
	a := c.Agent
	return withReason(a.Signal+details(c, e.SignalMeta), shownSummary(a))
}

// subcommitteeText is the convened agents and the start of the coordinator's brief.
func subcommitteeText(sc *discussion.Subcommittee) string {
	if sc == nil {
		return "subcommittee"
	}
	text := "subcommittee: " + memberList(sc)
	if sc.Brief != "" {
		text += "; brief: " + shortSummary(sc.Brief, summaryWidth)
	}
	return text
}

func withReason(text, reason string) string {
	if reason == "" {
		return text
	}
	return text + ": " + reason
}

// durations is " (triage 2.0s)" or " (evaluation 6.0s)" for the step an event
// finished, or empty.
func durations(c Change) string { return details(c, nil) }

// details is the step an event finished with its duration, and the model and tokens
// of the signal when it says: " (evaluation 6.0s, qwen3:14b, 1,540 tokens)".
func details(c Change, m *discussion.SignalMeta) string {
	var parts []string
	if c.Triage > 0 {
		parts = append(parts, "triage "+formatDuration(c.Triage))
	}
	if c.Evaluation > 0 {
		parts = append(parts, "evaluation "+formatDuration(c.Evaluation))
	}
	if m != nil {
		parts = appendNonEmpty(parts, m.ModelName, tokensText(m.InputTokens+m.OutputTokens))
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func appendNonEmpty(list []string, values ...string) []string {
	for _, v := range values {
		if v != "" {
			list = append(list, v)
		}
	}
	return list
}

// tokensText is "1,540 tokens", or empty for none.
func tokensText(n int64) string {
	if n <= 0 {
		return ""
	}
	return thousands(n) + " tokens"
}
