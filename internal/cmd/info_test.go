package cmd

import (
	"bytes"
	"testing"

	"github.com/kubemoot/kmctl/internal/client"
)

func TestInfoCommand_Construction(t *testing.T) {
	cmd := newInfoCommand(client.NewFactory())
	if cmd.Use != "info" {
		t.Errorf("Use = %q, want %q", cmd.Use, "info")
	}
	if cmd.Flags().Lookup("output") == nil {
		t.Error("expected an --output flag")
	}
}

func TestInfoCommand_RejectsArgs(t *testing.T) {
	cmd := newInfoCommand(client.NewFactory())
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"extra"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for unexpected positional arg, got nil")
	}
}
