package cmd

import (
	"testing"

	"github.com/javajon-homelab/kmctl/internal/client"
)

func TestAgentCommand_Subcommands(t *testing.T) {
	cmd := newAgentCommand(client.NewFactory())
	if cmd.Use != "agent" {
		t.Errorf("Use = %q, want %q", cmd.Use, "agent")
	}
	want := map[string]bool{"list": false, "get": false}
	for _, c := range cmd.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("agent missing subcommand %q", name)
		}
	}
}
