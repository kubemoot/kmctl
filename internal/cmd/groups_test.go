package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestGroup_MistypedSubcommandIsAnErrorWithASuggestion(t *testing.T) {
	got, code := runKmctl(t, runOpts{}, "crew", "lst")
	if code != 1 {
		t.Errorf("exit %d, want 1", code)
	}
	for _, want := range []string{`Error: unknown command "lst" for "kmctl crew"`, "kmctl crew list"} {
		if !strings.Contains(got, want) {
			t.Errorf("stderr %q lacks %q", got, want)
		}
	}
}

func TestGroup_NoSuggestionListsTheCommands(t *testing.T) {
	got, code := runKmctl(t, runOpts{}, "memory", "zzzzzz")
	if code != 1 || !strings.Contains(got, "Available commands: ") || strings.Contains(got, "Did you mean") {
		t.Errorf("exit %d, stderr %q", code, got)
	}
}

func TestGroup_BareGroupStillPrintsHelp(t *testing.T) {
	root := NewRootCommand()
	var out strings.Builder
	root.SetOut(&out)
	root.SetArgs([]string{"crew"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Available Commands:") {
		t.Errorf("help not printed: %q", out.String())
	}
}

// Every group in the tree rejects an unknown subcommand, including nested ones.
func TestGroup_EveryGroupRejectsAnUnknownSubcommand(t *testing.T) {
	root := NewRootCommand()
	var groups [][]string
	var collect func(path []string, c *cobra.Command)
	collect = func(path []string, c *cobra.Command) {
		if isGroup(c) {
			groups = append(groups, path)
		}
		for _, s := range c.Commands() {
			collect(append(append([]string{}, path...), s.Name()), s)
		}
	}
	collect(nil, root)
	if len(groups) < 6 {
		t.Fatalf("found only groups %v", groups)
	}
	for _, g := range groups {
		_, code := runKmctl(t, runOpts{}, append(g, "nonesuch")...)
		if code != 1 {
			t.Errorf("kmctl %s nonesuch: exit %d, want 1", strings.Join(g, " "), code)
		}
	}
}
