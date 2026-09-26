package cmd

import (
	"testing"

	"github.com/kubemoot/kmctl/internal/client"
)

func TestCrewCommand_Subcommands(t *testing.T) {
	cmd := newCrewCommand(client.NewFactory())
	if cmd.Use != "crew" {
		t.Errorf("Use = %q, want %q", cmd.Use, "crew")
	}
	want := map[string]bool{"list": false, "get": false}
	for _, c := range cmd.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("crew missing subcommand %q", name)
		}
	}
}
