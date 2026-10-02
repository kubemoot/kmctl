package cmd

import (
	"strings"
	"testing"

	"github.com/kubemoot/kmctl/internal/client"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func suiteWithScripts() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kubemoot.ai/v1alpha1",
		"kind":       "CrewFitnessSuite",
		"metadata":   map[string]any{"name": "starter"},
		"spec": map[string]any{
			"crewRef": "demo",
			"scripts": []any{
				map[string]any{"testRef": "smoke", "testContent": "AAA"},
				map[string]any{"testRef": "stand-aside", "testContent": "BBB"},
			},
		},
	}}
}

func TestScriptTestRefs(t *testing.T) {
	got := scriptTestRefs(suiteWithScripts())
	if len(got) != 2 || got[0] != "smoke" || got[1] != "stand-aside" {
		t.Errorf("scriptTestRefs = %v, want [smoke stand-aside]", got)
	}
}

func TestFindScript(t *testing.T) {
	s := suiteWithScripts()
	if c, ok := findScript(s, "stand-aside"); !ok || c != "BBB" {
		t.Errorf("findScript(stand-aside) = %q,%v want BBB,true", c, ok)
	}
	if _, ok := findScript(s, "missing"); ok {
		t.Error("findScript(missing) should be false")
	}
}

func TestBuildCrewFitness(t *testing.T) {
	cf := buildCrewFitness("starter", "smoke", "demo", "AAA")
	if cf.GetKind() != "CrewFitness" {
		t.Errorf("kind = %q", cf.GetKind())
	}
	gen, _, _ := unstructured.NestedString(cf.Object, "metadata", "generateName")
	if !strings.HasPrefix(gen, "starter-smoke-kmctl-") {
		t.Errorf("generateName = %q", gen)
	}
	crewRef, _, _ := unstructured.NestedString(cf.Object, "spec", "crewRef")
	content, _, _ := unstructured.NestedString(cf.Object, "spec", "testContent")
	if crewRef != "demo" || content != "AAA" {
		t.Errorf("spec wrong: crewRef=%q content=%q", crewRef, content)
	}
}

func TestIsTerminalPhase(t *testing.T) {
	for _, p := range []string{"Completed", "Failed", "Error"} {
		if !isTerminalPhase(p) {
			t.Errorf("%q should be terminal", p)
		}
	}
	for _, p := range []string{"", "Pending", "Running"} {
		if isTerminalPhase(p) {
			t.Errorf("%q should not be terminal", p)
		}
	}
}

func TestProgressLine(t *testing.T) {
	withProgress := &unstructured.Unstructured{Object: map[string]any{
		"status": map[string]any{
			"iterationsCompleted": int64(3),
			"iterationsTotal":     int64(5),
			"passed":              int64(2),
			"failed":              int64(1),
		},
	}}
	line := progressLine(withProgress, "Running")
	for _, want := range []string{"Running", "3/5", "passed 2", "failed 1"} {
		if !strings.Contains(line, want) {
			t.Errorf("progressLine %q missing %q", line, want)
		}
	}
	if got := progressLine(&unstructured.Unstructured{Object: map[string]any{}}, ""); got != "Pending" {
		t.Errorf("empty phase = %q, want Pending", got)
	}
}

func TestFitnessCommand_Subcommands(t *testing.T) {
	cmd := newFitnessCommand(client.NewFactory())
	assertSubcommands(t, cmd, "list", "get", "scenarios", "run", "download")
}

func TestDownloadCommand_Flags(t *testing.T) {
	cmd := newDownloadCommand(client.NewFactory())
	if cmd.Use != "download SUITE" {
		t.Errorf("Use = %q", cmd.Use)
	}
	for _, flag := range []string{"output", "dashboard-namespace", "dashboard-service"} {
		if cmd.Flags().Lookup(flag) == nil {
			t.Errorf("missing --%s flag", flag)
		}
	}
}

// The dashboard Service is found by label unless --dashboard-service names it,
// so the flag has no fixed default name to go stale.
func TestDownloadCommand_DashboardFlagDefaults(t *testing.T) {
	cmd := newDownloadCommand(client.NewFactory())
	if got := cmd.Flags().Lookup("dashboard-service").DefValue; got != "" {
		t.Errorf("--dashboard-service default = %q, want empty (found by label)", got)
	}
	if got := cmd.Flags().Lookup("dashboard-namespace").DefValue; got != "kubemoot" {
		t.Errorf("--dashboard-namespace default = %q, want kubemoot", got)
	}
}
