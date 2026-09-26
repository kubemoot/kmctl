package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/scaffold"
)

func TestWarnIfNoModels(t *testing.T) {
	// No providers -> warn, naming the consequence (Unschedulable), cause, and remedy.
	var buf bytes.Buffer
	warnIfNoModels(&buf, scaffold.Options{Name: "demo", Members: 2, ModelFamily: "qwen", OutputDir: "."})
	out := buf.String()
	if !strings.Contains(out, "no Models were generated") || !strings.Contains(out, "Unschedulable") {
		t.Fatalf("want a no-Models warning naming Unschedulable, got %q", out)
	}
	if !strings.Contains(out, "no model providers") {
		t.Fatalf("want the no-providers cause named, got %q", out)
	}

	// Providers + a known family -> Models generated -> no warning.
	buf.Reset()
	warnIfNoModels(&buf, scaffold.Options{Name: "demo", Members: 2, ModelFamily: "qwen", Providers: []string{"ollama-gpu"}, OutputDir: "."})
	if buf.String() != "" {
		t.Fatalf("expected no warning when Models are generated, got %q", buf.String())
	}

	// Providers present but no family (the --no-input-without--model-family case) ->
	// still no Models -> warn, naming the family cause.
	buf.Reset()
	warnIfNoModels(&buf, scaffold.Options{Name: "demo", Members: 2, Providers: []string{"ollama-gpu"}, OutputDir: "."})
	if !strings.Contains(buf.String(), "no model family") {
		t.Fatalf("want the no-family cause named, got %q", buf.String())
	}

	// Providers and a family both set, but the family has no built-in sizes -> still
	// no Models -> warn, naming that specific (default-branch) cause.
	buf.Reset()
	warnIfNoModels(&buf, scaffold.Options{Name: "demo", Members: 2, ModelFamily: "no-such-family", Providers: []string{"ollama-gpu"}, OutputDir: "."})
	if !strings.Contains(buf.String(), "no built-in sizes") {
		t.Fatalf("want the no-built-in-sizes cause named, got %q", buf.String())
	}
}

// fakePrompter records calls and returns canned answers.
type fakePrompter struct {
	intVal   int
	multiVal []string
	selVal   string
	called   bool
}

func (f *fakePrompter) Int(string, int) (int, error) { f.called = true; return f.intVal, nil }
func (f *fakePrompter) MultiSelect(string, []string) ([]string, error) {
	f.called = true
	return f.multiVal, nil
}
func (f *fakePrompter) SelectOrOther(string, []string) (string, error) {
	f.called = true
	return f.selVal, nil
}

func changedSet(names ...string) func(string) bool {
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	return func(s string) bool { return set[s] }
}

func TestGather_NoInputRequiresMembers(t *testing.T) {
	opts := scaffold.Options{Name: "demo"}
	err := gather(&opts, changedSet(), true, nil, &fakePrompter{})
	if err == nil {
		t.Fatal("expected error: --members required with --no-input")
	}
}

func TestGather_FlagsProvided_NoPrompting(t *testing.T) {
	opts := scaffold.Options{Name: "demo", Members: 4, ModelFamily: "qwen", Providers: []string{"p"}}
	fp := &fakePrompter{}
	// All three inputs marked as set via flags -> prompter must not be called.
	if err := gather(&opts, changedSet("members", "providers", "model-family"), false, nil, fp); err != nil {
		t.Fatalf("gather: %v", err)
	}
	if fp.called {
		t.Error("prompter was called despite all flags being provided")
	}
	if opts.Members != 4 {
		t.Errorf("members changed unexpectedly: %d", opts.Members)
	}
}

func TestGather_PromptsFillMissing(t *testing.T) {
	opts := scaffold.Options{Name: "demo"}
	fp := &fakePrompter{intVal: 5, multiVal: []string{"ollama-gpu"}, selVal: "gemma"}
	if err := gather(&opts, changedSet(), false, []string{"ollama-gpu", "ollama-rig1"}, fp); err != nil {
		t.Fatalf("gather: %v", err)
	}
	if opts.Members != 5 || opts.ModelFamily != "gemma" || len(opts.Providers) != 1 {
		t.Errorf("prompted values not applied: %+v", opts)
	}
}

func TestCreateCommand_RequiresName(t *testing.T) {
	cmd := newCreateCommand(client.NewFactory())
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error when NAME is missing")
	}
}

// TestCreateCommand_ScaffoldsAndHints exercises the create RunE success path:
// scaffold files are written and the apply next-step hint is printed. Kept
// hermetic by pointing KUBECONFIG at an absent file so provider discovery fails
// closed (nil) and the test never reaches a real cluster.
func TestCreateCommand_ScaffoldsAndHints(t *testing.T) {
	t.Setenv("KUBECONFIG", t.TempDir()+"/absent-kubeconfig")
	dir := t.TempDir()
	cmd := newCreateCommand(client.NewFactory())
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"demo", "--no-input", "--members", "2", "--model-family", "qwen", "-o", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains(out.String(), "Next: kmctl apply -f") {
		t.Fatalf("want the apply next-step hint, got %q", out.String())
	}
}
