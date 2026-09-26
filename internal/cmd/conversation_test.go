package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/spf13/cobra"
)

func TestConversationCommand_Subcommands(t *testing.T) {
	cmd := newConversationCommand(client.NewFactory())
	if cmd.Use != "conversation" {
		t.Errorf("Use = %q, want %q", cmd.Use, "conversation")
	}
	assertSubcommands(t, cmd, "ask", "watch")
}

func TestConversationCommands_HaveTimeout(t *testing.T) {
	f := client.NewFactory()
	for name, cmd := range map[string]*cobra.Command{"ask": newAskCommand(f), "watch": newWatchCommand(f)} {
		if cmd.Flags().Lookup("timeout") == nil {
			t.Errorf("%s command has no --timeout flag (would hang forever on a stalled discussion)", name)
		}
	}
}

func TestAskCommand_ArgValidation(t *testing.T) {
	for _, args := range [][]string{{}, {"crew"}, {"crew", "msg", "extra"}} {
		cmd := newAskCommand(client.NewFactory())
		cmd.SetOut(new(bytes.Buffer))
		cmd.SetErr(new(bytes.Buffer))
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Errorf("ask %v: expected error, got nil", args)
		}
	}
}

func TestWatchCommand_RequiresCrewAndID(t *testing.T) {
	cmd := newWatchCommand(client.NewFactory())
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"only-crew"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error: watch needs CREW and CONVERSATION_ID")
	}
}

func TestRenderStream_QuietPrintsOnlyAnswer(t *testing.T) {
	stream := strings.NewReader(`data: {"type":"phase","agent":"a","status":"ready"}

data: {"type":"synthesis","content":"the answer"}

data: {"type":"done"}

`)
	buf := new(bytes.Buffer)
	if err := renderStream(buf, stream, true); err != nil {
		t.Fatalf("renderStream: %v", err)
	}
	got := strings.TrimSpace(buf.String())
	if got != "the answer" {
		t.Errorf("quiet output = %q, want just the answer", got)
	}
}

func TestRenderStream_VerbosePrintsEvents(t *testing.T) {
	stream := strings.NewReader(`data: {"type":"connected"}

data: {"type":"synthesis","content":"hi"}

data: {"type":"done"}

`)
	buf := new(bytes.Buffer)
	if err := renderStream(buf, stream, false); err != nil {
		t.Fatalf("renderStream: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"connected", "ANSWER", "hi", "done"} {
		if !strings.Contains(out, want) {
			t.Errorf("verbose output missing %q\n%s", want, out)
		}
	}
}
