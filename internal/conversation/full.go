package conversation

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
	"github.com/kubemoot/kmctl/internal/discussion"
	"golang.org/x/term"
)

// errInterrupted ends a live view the person stopped with ctrl+c.
var errInterrupted = errors.New("interrupted")

// defaultWidth is the word-wrap width when the terminal size is unknown.
const defaultWidth = 100

var (
	styleBold   = lipgloss.NewStyle().Bold(true)
	styleDim    = lipgloss.NewStyle().Faint(true)
	styleAgent  = lipgloss.NewStyle().Bold(true).Width(24)
	styleSignal = map[string]lipgloss.Style{
		signalAgree:      lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		signalConcern:    lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		signalBlock:      lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		signalFailure:    lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		signalStandAside: lipgloss.NewStyle().Faint(true),
	}
	signalMark = map[string]string{
		signalAgree: "\u2713", signalConcern: "!", signalBlock: "\u2717", signalFailure: "\u2717", signalStandAside: "\u25cb",
	}
)

// eventMsg is one discussion event delivered to the live view.
type eventMsg struct {
	e  discussion.Event
	at time.Time
}

// endMsg says the stream ended, with the error that ended it, if any.
type endMsg struct{ err error }

// liveModel is the full mode's live view: a header with the elapsed time and one
// row per agent with a spinner while it works.
type liveModel struct {
	o           Options
	t           *Tracker
	spin        spinner.Model
	finished    bool
	interrupted bool
	err         error
}

func newLiveModel(o Options) liveModel {
	return liveModel{
		o:    o,
		t:    NewTracker(o.start()),
		spin: spinner.New(spinner.WithSpinner(spinner.Dot)),
	}
}

func (m liveModel) Init() tea.Cmd { return m.spin.Tick }

func (m liveModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case eventMsg:
		m.t.Apply(msg.e, msg.at)
	case endMsg:
		m.finished, m.err = true, msg.err
		return m, tea.Quit
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			m.interrupted = true
			return m, tea.Quit
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m liveModel) View() tea.View { return tea.NewView(m.render()) }

// render is the header and the agent rows.
func (m liveModel) render() string {
	lines := []string{m.header()}
	if sc := m.t.Subcommittee; sc != nil {
		lines = append(lines, styleDim.Render("subcommittee: "+memberList(sc)))
	}
	for _, a := range m.t.Agents() {
		lines = append(lines, m.row(a))
	}
	return strings.Join(lines, "\n") + "\n"
}

func (m liveModel) header() string {
	end := m.t.End
	if !m.finished {
		end = m.o.now()
	}
	where := m.o.Crew
	if m.o.Namespace != "" {
		where += " in " + m.o.Namespace
	}
	status := fmt.Sprintf("%s | %s", where, formatDuration(end.Sub(m.t.Start)))
	if len(m.t.Busy) > 0 && !m.finished {
		status += " | busy: " + busyText(m.t.Busy)
	}
	return styleBold.Render("conversation "+m.o.ConversationID) + "  " + styleDim.Render(status)
}

// row is one agent: a spinner or its colored result, its step, where it runs,
// how long each step took, and its summary once it has signalled.
func (m liveModel) row(a *Agent) string {
	mark := m.spin.View()
	step := a.Step
	if a.Done() {
		style := signalStyle(a.Signal)
		mark, step = style.Render(signalMarkFor(a.Signal)), style.Render(a.Signal)
	}
	parts := []string{mark, styleAgent.Render(a.Name), step}
	if where := runsOn(a); where != "" {
		parts = append(parts, styleDim.Render(where))
	}
	if d := stepTimes(a); d != "" {
		parts = append(parts, styleDim.Render(d))
	}
	if tokens := tokensText(a.InputTokens + a.OutputTokens); tokens != "" {
		parts = append(parts, styleDim.Render(tokens))
	}
	if text := rowNote(a); text != "" {
		parts = append(parts, text)
	}
	return strings.Join(parts, " ")
}

// rowNote is the agent's summary or stand-aside reason once it has signalled,
// or why it waits while it waits for GPU capacity.
func rowNote(a *Agent) string {
	if a.Done() {
		return firstNonEmpty(shownSummary(a), a.Reason)
	}
	if a.Step == "waiting" {
		return a.WaitReason
	}
	return ""
}

func signalStyle(signal string) lipgloss.Style {
	if s, ok := styleSignal[signal]; ok {
		return s
	}
	return lipgloss.NewStyle()
}

func signalMarkFor(signal string) string {
	if mark, ok := signalMark[signal]; ok {
		return mark
	}
	return "*"
}

// runsOn is the accelerator and model an agent runs on, as far as the stream says.
func runsOn(a *Agent) string {
	parts := []string{}
	for _, v := range []string{a.GPU, a.Model} {
		if v != "" {
			parts = append(parts, v)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// stepTimes is the agent's finished step durations, or empty.
func stepTimes(a *Agent) string {
	return strings.TrimSpace(durations(Change{Triage: a.Triage, Evaluation: a.Evaluation}))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// runFull runs the live view until the stream ends, then prints the rendered
// answer, the summary, and the follow-up command below it.
func runFull(w io.Writer, stream io.Reader, o Options, extra ...tea.ProgramOption) error {
	p := tea.NewProgram(newLiveModel(o), append([]tea.ProgramOption{tea.WithOutput(w)}, extra...)...)
	// The reader keeps reading after ctrl+c until the stream closes; the caller
	// closes the stream and cancels its context when this returns, which ends it.
	go func() {
		err := discussion.ParseSSE(stream, func(e discussion.Event) bool {
			o.seen(e)
			p.Send(eventMsg{e: e, at: o.now()})
			return discussion.Terminal(e)
		})
		p.Send(endMsg{err: err})
	}()
	final, err := p.Run()
	if err != nil {
		return err
	}
	m, _ := final.(liveModel)
	if m.interrupted {
		return errInterrupted
	}
	if m.err != nil {
		return m.err
	}
	_, err = lipgloss.Fprint(w, renderClosing(m.t, o, glamourStyle(w), terminalWidth(w)))
	return err
}

// renderClosing renders the answer and the summary as markdown in the given
// glamour style, followed by the follow-up command.
func renderClosing(t *Tracker, o Options, style string, width int) string {
	doc := o.summarize(t).Markdown()
	if t.Answer != "" {
		doc = strings.TrimRight(t.Answer, "\n") + "\n\n" + doc
	}
	out := doc
	r, err := glamour.NewTermRenderer(glamour.WithStandardStyle(style), glamour.WithWordWrap(width))
	if err == nil {
		if rendered, rerr := r.Render(doc); rerr == nil {
			out = rendered
		}
	}
	return out + styleDim.Render("Next: ") + FollowUp(o.Crew, o.Namespace, o.ConversationID) + "\n"
}

// glamourStyle is GLAMOUR_STYLE when set, else dark or light to match the
// terminal background.
func glamourStyle(w io.Writer) string {
	if s := os.Getenv("GLAMOUR_STYLE"); s != "" {
		return s
	}
	if f, ok := w.(*os.File); ok && !lipgloss.HasDarkBackground(os.Stdin, f) {
		return "light"
	}
	return "dark"
}

// terminalWidth is the width of the terminal w writes to, or defaultWidth.
func terminalWidth(w io.Writer) int {
	if f, ok := w.(*os.File); ok {
		if width, _, err := term.GetSize(int(f.Fd())); err == nil && width > 0 {
			return width
		}
	}
	return defaultWidth
}
