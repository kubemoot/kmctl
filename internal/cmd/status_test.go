package cmd

import (
	"bytes"
	"testing"

	"github.com/javajon-homelab/kmctl/internal/client"
)

func TestStatusCommand_Construction(t *testing.T) {
	cmd := newStatusCommand(client.NewFactory())
	if cmd.Use != "status" {
		t.Errorf("Use = %q, want %q", cmd.Use, "status")
	}
}

func TestStatusCommand_RejectsArgs(t *testing.T) {
	// cobra validates Args before RunE, so this never reaches the cluster call.
	cmd := newStatusCommand(client.NewFactory())
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"extra"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error for unexpected positional arg, got nil")
	}
}
