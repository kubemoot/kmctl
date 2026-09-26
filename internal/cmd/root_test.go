package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootCommand_Help(t *testing.T) {
	root := NewRootCommand()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"--help"})

	if err := root.Execute(); err != nil {
		t.Fatalf("--help returned error: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"kmctl", "crews and Kubemoot", "version"} {
		if !strings.Contains(out, want) {
			t.Errorf("help output missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestRootCommand_UnknownSubcommand(t *testing.T) {
	root := NewRootCommand()
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	root.SetArgs([]string{"does-not-exist"})

	if err := root.Execute(); err == nil {
		t.Fatal("expected error for unknown subcommand, got nil")
	}
}

func TestRootCommand_ConnectionFlags(t *testing.T) {
	root := NewRootCommand()
	for _, flag := range []string{"kubeconfig", "context", "namespace"} {
		if root.PersistentFlags().Lookup(flag) == nil {
			t.Errorf("expected persistent --%s flag", flag)
		}
	}
}

func TestRootCommand_Subcommands(t *testing.T) {
	root := NewRootCommand()
	want := map[string]bool{"version": false, "info": false, "status": false, "create": false, "crew": false, "agent": false, "prompt": false, "model": false, "conversation": false, "fitness": false, "apply": false, "delete": false}
	for _, c := range root.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("expected subcommand %q", name)
		}
	}
}

// The built-in completion command is the shell-autocomplete entry point. Cobra
// adds it lazily during Execute, so initialize it explicitly before asserting it
// is present - this catches a regression that disables default completion.
func TestRootCommand_HasCompletion(t *testing.T) {
	root := NewRootCommand()
	if root.CompletionOptions.DisableDefaultCmd {
		t.Fatal("default completion command is disabled")
	}
	root.InitDefaultCompletionCmd()
	for _, c := range root.Commands() {
		if c.Name() == "completion" {
			return
		}
	}
	t.Error("expected the built-in completion command to be present")
}
