package cmd

import (
	"strings"
	"testing"
)

const tierSuiteName = "demo-smoke-x7k2p"

func TestFitnessRun_TierCreatesASuiteFromTheTierAndWaits(t *testing.T) {
	s := newFakeAPIServer(t)
	s.react = func(obj map[string]any) {
		obj["status"] = map[string]any{"phase": "Completed", "iterationsCompleted": 8, "iterationsTotal": 8, "passed": 8}
	}
	out, _, err := s.kmctl(t, "fitness", "run", "--tier", "smoke", "--crew", "demo")
	if err != nil {
		t.Fatalf("a tier whose gates passed exits 0, got %v", err)
	}
	assertContains(t, "tier run", out, "created crewfitnesssuite/"+tierSuiteName, "tier smoke", "Completed  8/8 done")

	spec, _ := s.get("crewfitnesssuites", tierSuiteName)["spec"].(map[string]any)
	if spec["crewRef"] != "demo" || spec["tier"] != "smoke" || spec["iterations"] != float64(1) {
		t.Errorf("the suite names the crew and the tier and one iteration, got %v", spec)
	}
	if _, has := spec["scripts"]; has {
		t.Errorf("the operator picks the scripts; the CLI must not send any: %v", spec)
	}
}

func TestFitnessRun_TierNameAndIterations(t *testing.T) {
	s := newFakeAPIServer(t)
	s.react = func(obj map[string]any) { obj["status"] = map[string]any{"phase": "Completed"} }
	if _, _, err := s.kmctl(t, "fitness", "run", "--tier", "full", "--crew", "demo", "--name", "my-run", "--iterations", "3"); err != nil {
		t.Fatal(err)
	}
	spec, _ := s.get("crewfitnesssuites", "my-run")["spec"].(map[string]any)
	if spec["tier"] != "full" || spec["iterations"] != float64(3) {
		t.Errorf("spec = %v", spec)
	}
}

func TestFitnessRun_TierSuiteThatFailsItsGatesExitsNonZero(t *testing.T) {
	s := newFakeAPIServer(t)
	s.react = func(obj map[string]any) {
		obj["status"] = map[string]any{"phase": "Error", "error": "spec.tier smoke from ConfigMap demo-fitness: no scenario carries tier \"smoke\""}
	}
	_, _, err := s.kmctl(t, "fitness", "run", "--tier", "smoke", "--crew", "demo")
	if err == nil || !strings.Contains(err.Error(), "no scenario carries tier") {
		t.Errorf("the operator's reason reaches the terminal, got %v", err)
	}
}

func TestFitnessRun_TierRefusesWhatCannotStartBeforeCreatingAnything(t *testing.T) {
	cases := map[string]struct {
		args []string
		want string
	}{
		"unknown tier":    {[]string{"--tier", "weekly", "--crew", "demo"}, `unknown tier "weekly"`},
		"upper case tier": {[]string{"--tier", "Smoke", "--crew", "demo"}, `unknown tier "Smoke"`},
		"no crew":         {[]string{"--tier", "smoke"}, "--tier needs --crew"},
		"a suite name":    {[]string{"starter", "--tier", "smoke", "--crew", "demo"}, "do not combine it"},
		"a manifest":      {[]string{"-f", "x.yaml", "--tier", "smoke", "--crew", "demo"}, "do not combine it"},
		"one scenario":    {[]string{"--scenario", "pods", "--tier", "smoke", "--crew", "demo"}, "do not combine it"},
		"zero iterations": {[]string{"--tier", "smoke", "--crew", "demo", "--iterations", "0"}, "at least 1"},
	}
	for name, c := range cases {
		s := newFakeAPIServer(t)
		_, _, err := s.kmctl(t, append([]string{"fitness", "run"}, c.args...)...)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want an error containing %q, got %v", name, c.want, err)
		}
		if got := s.get("crewfitnesssuites", tierSuiteName); got != nil {
			t.Errorf("%s: nothing may be created for a refused run, found %v", name, got)
		}
	}
}

func TestTierRunSuiteGeneratesANameFromCrewAndTier(t *testing.T) {
	obj := tierRun{tier: "standard", crew: "pilot", iterations: 2}.suite().Object
	meta, _ := obj["metadata"].(map[string]any)
	if meta["generateName"] != "pilot-standard-" || meta["name"] != nil {
		t.Errorf("metadata = %v", meta)
	}
	named, _ := tierRun{tier: "standard", crew: "pilot", name: "n", iterations: 2}.suite().Object["metadata"].(map[string]any)
	if named["name"] != "n" || named["generateName"] != nil {
		t.Errorf("a given name is used as is: %v", named)
	}
}

func TestFitnessRun_TierDryRunCreatesNothingAndDoesNotWait(t *testing.T) {
	s := newFakeAPIServer(t)
	out, _, err := s.kmctl(t, "fitness", "run", "--tier", "smoke", "--crew", "demo", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "dry run", out, "would be created (tier smoke, crew demo; dry run, nothing changed)")
	if strings.Contains(out, "Completed") || strings.Contains(out, "Pending") {
		t.Errorf("a dry run does not wait for a run that was never started: %q", out)
	}
	if _, _, err = s.kmctl(t, "fitness", "run", "starter", "--dry-run"); err == nil || !strings.Contains(err.Error(), "--tier only") {
		t.Errorf("--dry-run without --tier is refused, got %v", err)
	}
}
