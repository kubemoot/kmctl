package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kubemoot/kmctl/internal/client"
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

// The global flags every command shares reach the factory: the namespace and
// context a command resolves come from them, and -v is read from the root.
func TestRootCommand_GlobalFlagsReachTheFactory(t *testing.T) {
	s := newFakeAPIServer(t)
	f := client.NewFactory()
	root := newRootCommand(f)
	args := []string{"-n", "team-b", "--context", "fake", "--kubeconfig", s.kubeconfig, "-v"}
	if err := root.ParseFlags(args); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if got := f.Namespace(); got != "team-b" {
		t.Errorf("namespace = %q, want team-b", got)
	}
	if got := f.CurrentContext(); got != "fake" {
		t.Errorf("context = %q, want the --context flag's fake", got)
	}
	if got := kubeconfigFlag(f); got != s.kubeconfig {
		t.Errorf("kubeconfig = %q, want the flag's path", got)
	}
	if v, err := root.PersistentFlags().GetBool(verboseFlag); err != nil || !v {
		t.Errorf("-v = %v, %v; want true", v, err)
	}
}

func TestRootCommand_UnknownFlagIsAnError(t *testing.T) {
	root := newRootCommand(client.NewFactory())
	if err := root.ParseFlags([]string{"--no-such-flag"}); err == nil {
		t.Fatal("expected an error for an unknown global flag")
	}
}

// With no -n the namespace is the kubeconfig context's, and a kubeconfig that is
// not there is reported with the path it looked at instead of a stack of client errors.
func TestRootCommand_NamespaceComesFromTheKubeconfigContext(t *testing.T) {
	s := newFakeAPIServer(t)
	f := client.NewFactory()
	root := newRootCommand(f)
	if err := root.ParseFlags([]string{"--kubeconfig", s.kubeconfig}); err != nil {
		t.Fatal(err)
	}
	if got := f.Namespace(); got != contextNS {
		t.Errorf("namespace = %q, want the context's %q", got, contextNS)
	}
	if got := f.CurrentContext(); got != "fake" {
		t.Errorf("context = %q, want fake", got)
	}
}

func TestRootCommand_MissingKubeconfigIsReportedNotPanicked(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone")
	f := client.NewFactory()
	root := newRootCommand(f)
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	root.SetArgs([]string{"--kubeconfig", missing, "crew", "list"})
	var stderr bytes.Buffer
	if code := report(root, f, &stderr, false, func(string) string { return "" }); code == 0 {
		t.Fatalf("exit code 0 for a missing kubeconfig; stderr: %s", stderr.String())
	}
	if stderr.Len() == 0 {
		t.Error("nothing was said about the missing kubeconfig")
	}
}

// Execute is the process entry point: it returns the exit code from os.Args.
func TestExecute_ExitCodes(t *testing.T) {
	old := os.Args
	t.Cleanup(func() { os.Args = old })
	os.Args = []string{"kmctl", "--help"}
	if code := Execute(); code != 0 {
		t.Errorf("--help exit code = %d, want 0", code)
	}
	os.Args = []string{"kmctl", "no-such-command"}
	if code := Execute(); code == 0 {
		t.Error("an unknown command exited 0")
	}
}
