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

// The only default clock is the idle one; --timeout is an opt-in overall cap.
func TestConversationCommands_WaitFlags(t *testing.T) {
	f := client.NewFactory()
	for name, cmd := range map[string]*cobra.Command{"ask": newAskCommand(f), "watch": newWatchCommand(f)} {
		if idle := cmd.Flags().Lookup("idle-timeout"); idle == nil || idle.DefValue != "5m0s" {
			t.Errorf("%s: want --idle-timeout defaulting to 5m0s, got %+v", name, idle)
		}
		if overall := cmd.Flags().Lookup("timeout"); overall == nil || overall.DefValue != "0s" {
			t.Errorf("%s: want --timeout defaulting to 0s (no cap), got %+v", name, overall)
		}
		if !strings.Contains(cmd.Long, "--idle-timeout") || !strings.Contains(cmd.Long, "kmctl conversation watch") {
			t.Errorf("%s: help does not explain how long it waits", name)
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

func TestConversationCommands_HaveOutputAndQuiet(t *testing.T) {
	f := client.NewFactory()
	for name, cmd := range map[string]*cobra.Command{"ask": newAskCommand(f), "watch": newWatchCommand(f)} {
		out := cmd.Flags().Lookup("output")
		if out == nil || out.Shorthand != "o" || out.DefValue != "full" {
			t.Errorf("%s: want -o/--output defaulting to full, got %+v", name, out)
		}
		if q := cmd.Flags().Lookup("quiet"); q == nil || q.Shorthand != "q" {
			t.Errorf("%s: want -q/--quiet", name)
		}
		complete, ok := cmd.GetFlagCompletionFunc("output")
		if !ok {
			t.Fatalf("%s: --output has no completion", name)
		}
		modes, directive := complete(cmd, nil, "")
		if strings.Join(modes, ",") != "full,plain,json,quiet" || directive != cobra.ShellCompDirectiveNoFileComp {
			t.Errorf("%s: --output completes %v (%v)", name, modes, directive)
		}
	}
}
