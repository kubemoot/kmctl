package cmd

import (
	"strings"
	"testing"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/spf13/cobra"
	"k8s.io/client-go/dynamic"
)

// runSuite runs "fitness <args>" against the fake dynamic client.
func runSuite(t *testing.T, dc *writeDyn, stdin string, args ...string) (string, string, error) {
	t.Helper()
	build := func(root *cobra.Command, f *client.Factory) {
		fitness := &cobra.Command{Use: "fitness"}
		addSuiteControlCommands(fitness, f, func() (dynamic.Interface, error) { return dc, nil })
		root.AddCommand(fitness)
	}
	return runWrite(t, stdin, build, append([]string{"fitness"}, args...)...)
}

func TestFitnessPauseAndResumeSetSpecSuspend(t *testing.T) {
	dc := newWriteDyn(suiteObj("Running"))
	out, _, err := runSuite(t, dc, "", "pause", "starter")
	if err != nil || !strings.Contains(out, "crewfitnesssuite/starter paused") {
		t.Fatalf("pause: %v\n%s", err, out)
	}
	if got := dc.specOf(t, suiteGVR, "starter")["suspend"]; got != true {
		t.Errorf("spec.suspend = %v, want true", got)
	}
	if string(dc.patches[0].Patch) != `{"spec":{"suspend":true}}` {
		t.Errorf("patch = %s", dc.patches[0].Patch)
	}
	if _, _, err := runSuite(t, dc, "", "resume", "starter"); err != nil {
		t.Fatal(err)
	}
	if got := dc.specOf(t, suiteGVR, "starter")["suspend"]; got != false {
		t.Errorf("spec.suspend = %v, want false", got)
	}
}

func TestFitnessStopAsksFirstAndSetsSpecCancel(t *testing.T) {
	dc := newWriteDyn(suiteObj("Running"))
	_, errOut, err := runSuite(t, dc, "n\n", "stop", "starter")
	if err == nil || !strings.Contains(err.Error(), "aborted") || !strings.Contains(errOut, "Stop suite starter?") {
		t.Fatalf("declined stop: %v\n%s", err, errOut)
	}
	if len(dc.patches) != 0 {
		t.Fatalf("a declined stop patched the suite: %v", dc.patches)
	}
	if _, _, err := runSuite(t, dc, "", "stop", "starter"); err == nil {
		t.Fatal("stop with no answer must abort")
	}
	out, _, err := runSuite(t, dc, "y\n", "stop", "starter")
	if err != nil || !strings.Contains(out, "crewfitnesssuite/starter stopped") {
		t.Fatalf("confirmed stop: %v\n%s", err, out)
	}
	if got := dc.specOf(t, suiteGVR, "starter")["cancel"]; got != true {
		t.Errorf("spec.cancel = %v, want true", got)
	}
}

func TestFitnessStopWithYesDoesNotAsk(t *testing.T) {
	dc := newWriteDyn(suiteObj("Running"))
	_, errOut, err := runSuite(t, dc, "", "stop", "starter", "--yes")
	if err != nil || errOut != "" {
		t.Fatalf("stop --yes: %v %q", err, errOut)
	}
}

func TestFitnessDryRunAsksTheServerAndStoresNothing(t *testing.T) {
	for _, verb := range []string{"pause", "resume", "stop"} {
		dc := newWriteDyn(suiteObj("Running"))
		out, errOut, err := runSuite(t, dc, "", verb, "starter", "--dry-run")
		if err != nil || !strings.Contains(out, "would be") || !strings.Contains(out, "dry run, nothing changed") || errOut != "" {
			t.Fatalf("%s --dry-run: %v\n%s\n%s", verb, err, out, errOut)
		}
		if len(dc.patches) != 1 || len(dc.patches[0].PatchOptions.DryRun) != 1 || dc.patches[0].PatchOptions.DryRun[0] != "All" {
			t.Fatalf("%s: patches = %+v, want one server-side dry run", verb, dc.patches)
		}
		if spec := dc.specOf(t, suiteGVR, "starter"); len(spec) != 1 {
			t.Errorf("%s: a dry run changed the spec: %v", verb, spec)
		}
	}
}

func TestFitnessRealRunSendsNoDryRunOption(t *testing.T) {
	dc := newWriteDyn(suiteObj("Running"))
	if _, _, err := runSuite(t, dc, "", "pause", "starter"); err != nil {
		t.Fatal(err)
	}
	if len(dc.patches[0].PatchOptions.DryRun) != 0 {
		t.Errorf("DryRun = %v", dc.patches[0].PatchOptions.DryRun)
	}
}

func TestFitnessControlRefusesAFinishedSuite(t *testing.T) {
	for _, phase := range []string{"Completed", "Cancelled", "Failed"} {
		dc := newWriteDyn(suiteObj(phase))
		_, _, err := runSuite(t, dc, "", "pause", "starter")
		if err == nil || !strings.Contains(err.Error(), "already "+phase) {
			t.Errorf("%s: err = %v", phase, err)
		}
		if len(dc.patches) != 0 {
			t.Errorf("%s: patched a finished suite", phase)
		}
	}
}

func TestFitnessControlOfAMissingSuite(t *testing.T) {
	dc := newWriteDyn()
	_, _, err := runSuite(t, dc, "", "pause", "nope")
	if err == nil || !strings.Contains(explained(t, err), "nope") {
		t.Fatalf("err = %v", err)
	}
}

func TestFitnessControlDeniedByRBAC(t *testing.T) {
	dc := newWriteDyn(suiteObj("Running"))
	dc.deny("crewfitnesssuites")
	_, _, err := runSuite(t, dc, "", "pause", "starter")
	if err == nil {
		t.Fatal("want a forbidden error")
	}
	msg := explained(t, err)
	if !strings.Contains(msg, `you are not allowed to patch crewfitnesssuites in namespace "crew-demo"`) {
		t.Errorf("message = %s", msg)
	}
}

func TestFitnessControlArgumentValidation(t *testing.T) {
	dc := newWriteDyn()
	for _, args := range [][]string{{"pause"}, {"stop", "a", "b"}} {
		if _, _, err := runSuite(t, dc, "", args...); err == nil {
			t.Errorf("%v: want an error", args)
		}
	}
}

func TestFitnessControlCommandsAreOnTheFitnessNoun(t *testing.T) {
	root := newRootCommand(client.NewFactory())
	fit, _, err := root.Find([]string{"fitness"})
	if err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"pause", "resume", "stop"} {
		c, _, err := fit.Find([]string{verb})
		if err != nil || c.Name() != verb || c.Example == "" || c.Flags().Lookup("dry-run") == nil {
			t.Errorf("fitness %s: %v", verb, err)
		}
	}
	stop, _, _ := fit.Find([]string{"stop"})
	pause, _, _ := fit.Find([]string{"pause"})
	if stop.Flags().Lookup("yes") == nil || pause.Flags().Lookup("yes") != nil {
		t.Error("only stop confirms, so only stop has --yes")
	}
}
