package cmd

import (
	"bytes"
	"testing"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/resource"
)

func TestListCommand_Flags(t *testing.T) {
	cmd := newListCommand(client.NewFactory(), resource.Crew)
	if cmd.Flags().Lookup("all-namespaces") == nil {
		t.Error("expected --all-namespaces flag")
	}
	if cmd.Flags().Lookup("output") == nil {
		t.Error("expected --output flag")
	}
}

func TestGetCommand_RequiresExactlyOneName(t *testing.T) {
	for _, args := range [][]string{{}, {"a", "b"}} {
		cmd := newGetCommand(client.NewFactory(), resource.Agent)
		cmd.SetOut(new(bytes.Buffer))
		cmd.SetErr(new(bytes.Buffer))
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Errorf("get with args %v: expected error, got nil", args)
		}
	}
}
