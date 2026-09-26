package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/kubemoot/kmctl/internal/version"
)

func TestVersionCommand_Default(t *testing.T) {
	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("version returned error: %v", err)
	}
	if !strings.Contains(buf.String(), version.Version) {
		t.Errorf("version output %q missing version %q", buf.String(), version.Version)
	}
}

func TestVersionCommand_Short(t *testing.T) {
	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"version", "--short"})

	if err := root.Execute(); err != nil {
		t.Fatalf("version --short returned error: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != version.Version {
		t.Errorf("version --short = %q, want %q", got, version.Version)
	}
}

func TestVersionCommand_RejectsArgs(t *testing.T) {
	root := NewRootCommand()
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	root.SetArgs([]string{"version", "extra-arg"})

	if err := root.Execute(); err == nil {
		t.Fatal("expected error when passing positional args to version, got nil")
	}
}
