package conversation

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/kubemoot/kmctl/internal/discussion"
)

func feed(m liveModel, events ...discussion.Event) liveModel {
	for _, e := range events {
		next, _ := m.Update(eventMsg{e: e, at: m.o.now()})
		m = next.(liveModel)
	}
	return m
}

func TestLiveModel_RowsSettleToSignals(t *testing.T) {
	m := newLiveModel(testOptions(ModeFull))
	m = feed(m,
		discussion.Event{Type: "phase", Agent: "pilot-k8s", Status: "triaging", GPU: "rig1-gpu0"},
		discussion.Event{Type: "phase", Agent: "pilot-gpu", Status: "triaging"},
	)
	running := ansi.Strip(m.render())
	for _, want := range []string{"conversation 6733c361", "homelab-pilot in crew-homelab-pilot", "pilot-k8s", "triaging", "[rig1-gpu0]"} {
		if !strings.Contains(running, want) {
			t.Errorf("live view missing %q:\n%s", want, running)
		}
	}
	m = feed(m,
		discussion.Event{Type: "phase", Agent: "pilot-k8s", Status: "evaluating", GPU: "rig1-gpu0"},
		discussion.Event{Type: "finding", Agent: "pilot-k8s", Signal: "agree", Content: "Three nodes are online."},
		discussion.Event{Type: "phase", Agent: "pilot-gpu", Status: "done", StoodAside: true, Reason: "gpu-busy"},
	)
	settled := ansi.Strip(m.render())
	for _, want := range []string{"\u2713", "agree", "Three nodes are online.", "(triage 3.0s, evaluation 1.0s)", "\u25cb", "stand_aside", "gpu-busy"} {
		if !strings.Contains(settled, want) {
			t.Errorf("settled view missing %q:\n%s", want, settled)
		}
	}
}

func TestLiveModel_EndQuits(t *testing.T) {
	m := newLiveModel(testOptions(ModeFull))
	next, cmd := m.Update(endMsg{err: errors.New("boom")})
	got := next.(liveModel)
	if !got.finished || got.err == nil || cmd == nil {
		t.Errorf("the stream's end finishes the view and quits: %+v", got)
	}
}

func TestLiveModel_CtrlCInterrupts(t *testing.T) {
	m := newLiveModel(testOptions(ModeFull))
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !next.(liveModel).interrupted || cmd == nil {
		t.Error("ctrl+c interrupts the live view")
	}
	next, cmd = m.Update(tea.KeyPressMsg{Code: 'x'})
	if next.(liveModel).interrupted || cmd != nil {
		t.Error("other keys are ignored")
	}
}

func TestLiveModel_SpinnerTicks(t *testing.T) {
	m := newLiveModel(testOptions(ModeFull))
	if m.Init() == nil {
		t.Fatal("the spinner starts on Init")
	}
	_, cmd := m.Update(spinner.TickMsg{ID: m.spin.ID()})
	if cmd == nil {
		t.Error("a spinner tick schedules the next one")
	}
	if v := m.View(); !strings.Contains(v.Content, "conversation") {
		t.Errorf("View renders the header: %q", v.Content)
	}
}

func TestRenderClosing_MarkdownThenFollowUp(t *testing.T) {
	tr := NewTracker(testStart)
	tr.Answer = "## Nodes\n\n**Notes:**\n\n- one is busy\n"
	out := ansi.Strip(renderClosing(tr, testOptions(ModeFull), "dark", 100))
	if strings.Contains(out, "**Notes:**") || !strings.Contains(out, "\u2022 one is busy") {
		t.Errorf("the answer is rendered, not raw markdown:\n%s", out)
	}
	notes, summary := strings.Index(out, "Notes:"), strings.Index(out, "How this answer was made")
	if notes < 0 || summary < notes {
		t.Errorf("the summary follows the answer's notes:\n%s", out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "Next: kmctl conversation ask homelab-pilot") {
		t.Errorf("last line is the follow-up command: %q", last)
	}
}

func TestRenderClosing_SuggestedCommandsAreBlueNotRed(t *testing.T) {
	tr := NewTracker(testStart)
	tr.Answer = "Fetch it with `kmctl conversation artifact x y`"
	for style, want := range map[string]string{"dark": "38;5;39", "light": "38;5;26"} {
		out := renderClosing(tr, testOptions(ModeFull), style, 100)
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if last := lines[len(lines)-1]; !strings.Contains(last, want) {
			t.Errorf("%s: the follow-up command is drawn in %s: %q", style, want, last)
		}
		if !strings.Contains(out, want+"m") && !strings.Contains(out, want+";") {
			t.Errorf("%s: inline code uses the command color", style)
		}
		if strings.Contains(out, "38;5;203") {
			t.Errorf("%s: no suggestion is drawn in the theme's red", style)
		}
	}
}

func TestRenderClosing_UnknownStyleFallsBackToRaw(t *testing.T) {
	tr := NewTracker(testStart)
	tr.Answer = "## Nodes"
	if out := renderClosing(tr, testOptions(ModeFull), "no-such-style", 80); !strings.Contains(out, "## Nodes") {
		t.Errorf("a renderer error leaves the markdown raw:\n%s", out)
	}
}

func TestTerminalHelpers_NonTerminal(t *testing.T) {
	var buf bytes.Buffer
	if w := terminalWidth(&buf); w != defaultWidth {
		t.Errorf("terminalWidth of a buffer = %d, want %d", w, defaultWidth)
	}
	t.Setenv("GLAMOUR_STYLE", "light")
	if s := glamourStyle(&buf); s != "light" {
		t.Errorf("GLAMOUR_STYLE wins: %q", s)
	}
	t.Setenv("GLAMOUR_STYLE", "")
	if s := glamourStyle(&buf); s != "dark" {
		t.Errorf("a non-terminal defaults to dark: %q", s)
	}
}

func TestRunFull_RendersToWriter(t *testing.T) {
	stream := strings.NewReader("data: {\"type\":\"phase\",\"agent\":\"a\",\"status\":\"triaging\"}\n\ndata: {\"type\":\"synthesis\",\"content\":\"All good.\"}\n\ndata: {\"type\":\"done\"}\n\n")
	var out bytes.Buffer
	if err := runFull(&out, stream, testOptions(ModeFull), tea.WithInput(nil)); err != nil {
		t.Fatalf("runFull: %v", err)
	}
	got := ansi.Strip(out.String())
	for _, want := range []string{"All good.", "How this answer was made", "Next: kmctl conversation ask"} {
		if !strings.Contains(got, want) {
			t.Errorf("full output missing %q:\n%s", want, got)
		}
	}
}

func TestLiveModel_ShowsSubcommitteeAndTokens(t *testing.T) {
	m := newLiveModel(testOptions(ModeFull))
	m = feed(m,
		discussion.Event{Type: "subcommittee", Subcommittee: &discussion.Subcommittee{Members: []discussion.SubcommitteeMember{{Agent: "pilot-k8s", Reason: "pods"}}}},
		discussion.Event{Type: "finding", Agent: "pilot-k8s", Signal: "agree", Content: "ok",
			SignalMeta: &discussion.SignalMeta{ModelName: "qwen3:14b", InputTokens: 1200, OutputTokens: 340}},
	)
	view := ansi.Strip(m.render())
	for _, want := range []string{"subcommittee: pilot-k8s (pods)", "1,540 tokens", "qwen3:14b"} {
		if !strings.Contains(view, want) {
			t.Errorf("live view missing %q:\n%s", want, view)
		}
	}
}
