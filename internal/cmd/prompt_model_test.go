package cmd

import (
	"testing"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/spf13/cobra"
)

func assertSubcommands(t *testing.T, parent *cobra.Command, names ...string) {
	t.Helper()
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = false
	}
	for _, c := range parent.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s missing subcommand %q", parent.Name(), name)
		}
	}
}

func TestPromptCommand_Subcommands(t *testing.T) {
	cmd := newPromptCommand(client.NewFactory())
	if cmd.Use != "prompt" {
		t.Errorf("Use = %q, want %q", cmd.Use, "prompt")
	}
	assertSubcommands(t, cmd, "list", "get")
}

func TestModelCommand_Subcommands(t *testing.T) {
	cmd := newModelCommand(client.NewFactory())
	if cmd.Use != "model" {
		t.Errorf("Use = %q, want %q", cmd.Use, "model")
	}
	assertSubcommands(t, cmd, "list", "get", "footprint")
}
