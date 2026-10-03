package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/dashboard"
	"github.com/kubemoot/kmctl/internal/manifest"
	"github.com/kubemoot/kmctl/internal/output"
	"github.com/kubemoot/kmctl/internal/resource"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
)

// newFitnessCommand groups the fitness subcommands.
func newFitnessCommand(f *client.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "fitness",
		Short:   "Run and inspect crew fitness suites",
		Aliases: []string{"fit"},
		Args:    cobra.NoArgs,
	}
	addResourceCommands(cmd, f, resource.CrewFitnessSuite)
	cmd.AddCommand(newScenariosCommand(f))
	cmd.AddCommand(newRunCommand(f))
	cmd.AddCommand(newDownloadCommand(f))
	return cmd
}

// newDownloadCommand fetches a suite's XLSX artifact from the dashboard's
// artifact endpoint through the API server service proxy (kubeconfig creds, no
// direct NATS object-store access). The dashboard Service is found by label.
func newDownloadCommand(f *client.Factory) *cobra.Command {
	var outFile, dashNS, dashSvc string
	cmd := &cobra.Command{
		Use:   "download SUITE",
		Short: "Download a fitness suite's XLSX artifact",
		Long: `Download fetches a fitness suite's XLSX artifact from the Kubemoot dashboard
through the API server's service proxy. The dashboard Service is found in
--dashboard-namespace by its labels (` + dashboard.Selector + `);
--dashboard-service names it instead.`,
		Example: `  kmctl fitness download demo-starter
  kmctl fitness download demo-starter -o results.xlsx
  kmctl fitness download demo-starter --dashboard-namespace ops --dashboard-service my-dashboard`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			suite := args[0]
			cs, err := f.Clientset()
			if err != nil {
				return err
			}
			data, err := dashboard.DownloadArtifact(cmd.Context(), cs, dashNS, dashSvc, f.Namespace(), suite)
			if err != nil {
				return err
			}
			if outFile == "" {
				outFile = suite + ".xlsx"
			}
			if err := os.WriteFile(outFile, data, 0o644); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "wrote %s (%d bytes)\n", outFile, len(data))
			return err
		},
	}
	cmd.Flags().StringVarP(&outFile, "output", "o", "", "Output file (default <suite>.xlsx)")
	cmd.Flags().StringVar(&dashNS, "dashboard-namespace", "kubemoot", "Namespace of the Kubemoot dashboard Service")
	cmd.Flags().StringVar(&dashSvc, "dashboard-service", "", "Name of the dashboard Service (default: found by label)")
	return cmd
}

func newScenariosCommand(f *client.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "scenarios SUITE",
		Short: "List the scenarios in a fitness suite",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dc, err := f.Dynamic()
			if err != nil {
				return err
			}
			suite, err := resource.Get(cmd.Context(), dc, resource.CrewFitnessSuite, f.Namespace(), args[0])
			if err != nil {
				return err
			}
			refs := scriptTestRefs(suite)
			if len(refs) == 0 {
				_, err = fmt.Fprintln(cmd.ErrOrStderr(), "no scenarios found in suite")
				return err
			}
			out := output.NewWriter(cmd.OutOrStdout())
			for _, r := range refs {
				out.Printf("%s\n", r)
			}
			return out.Err()
		},
	}
}

func newRunCommand(f *client.Factory) *cobra.Command {
	var scenario string
	var filename string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "run [SUITE]",
		Short: "Run a fitness suite (or one scenario) and wait for completion",
		Long: `Run waits for a fitness suite's iterations to finish (phase Completed, or
Cancelled when the suite is stopped), printing progress. The deferred judge scores
the run after that: when run returns while the judge is still scoring, it says so,
and kmctl fitness get shows the judge's progress and scores from the suite's status.
The exit code reflects timeouts and API errors only, not the suite's result. Name an
existing suite, or apply one from a manifest with -f.
With --scenario NAME, only that one scenario runs: a single CrewFitness is created
from the suite's matching script, useful for iterating on one scenario.`,
		Example: `  kmctl fitness run demo-starter
  kmctl fitness run -f suite.yaml
  kmctl fitness run demo-starter --scenario smoke-hello --timeout 10m`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dc, err := f.Dynamic()
			if err != nil {
				return err
			}
			ns := f.Namespace()
			suiteName, err := resolveRunSuite(cmd, f, dc, args, filename)
			if err != nil {
				return err
			}
			suite, err := resource.Get(cmd.Context(), dc, resource.CrewFitnessSuite, ns, suiteName)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()

			if scenario == "" {
				done, err := pollPhase(cmd.Context(), dc, resource.CrewFitnessSuite, ns, suiteName, w, timeout)
				if err != nil {
					return err
				}
				return printJudgeHint(w, done)
			}

			content, ok := findScript(suite, scenario)
			if !ok {
				return fmt.Errorf("scenario %q not found in suite %q (see: kmctl fitness scenarios %s)", scenario, suiteName, suiteName)
			}
			crewRef, _, _ := unstructured.NestedString(suite.Object, "spec", "crewRef")
			cf := buildCrewFitness(suiteName, scenario, crewRef, content)
			created, err := dc.Resource(resource.CrewFitness.GVR()).Namespace(ns).Create(cmd.Context(), cf, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("create single-scenario run: %w", err)
			}
			if _, err := fmt.Fprintf(w, "running scenario %q as crewfitness/%s\n", scenario, created.GetName()); err != nil {
				return err
			}
			_, err = pollPhase(cmd.Context(), dc, resource.CrewFitness, ns, created.GetName(), w, timeout)
			return err
		},
	}
	cmd.Flags().StringVar(&scenario, "scenario", "", "Run only this scenario (by testRef) as a single CrewFitness")
	cmd.Flags().StringVarP(&filename, "filename", "f", "", "Apply a CrewFitnessSuite manifest, then run it")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Minute, "Max time to wait for completion")
	return cmd
}

// resolveRunSuite returns the suite name to run: from -f (apply the manifest and
// use the CrewFitnessSuite it contains) or from the SUITE argument.
func resolveRunSuite(cmd *cobra.Command, f *client.Factory, dc dynamic.Interface, args []string, filename string) (string, error) {
	if filename == "" {
		if len(args) != 1 {
			return "", fmt.Errorf("provide a SUITE name or -f FILE")
		}
		return args[0], nil
	}
	if len(args) > 0 {
		return "", fmt.Errorf("provide either a SUITE name or -f, not both")
	}
	mapper, err := f.RESTMapper()
	if err != nil {
		return "", err
	}
	objs, err := manifest.FromPath(filename, cmd.InOrStdin())
	if err != nil {
		return "", err
	}
	name := ""
	for i := range objs {
		if err := applyObject(cmd.Context(), dc, mapper, objs[i], applyOpts{defaultNS: f.Namespace()}, cmd.OutOrStdout()); err != nil {
			return "", err
		}
		if objs[i].GetKind() == "CrewFitnessSuite" {
			name = objs[i].GetName()
		}
	}
	if name == "" {
		return "", fmt.Errorf("no CrewFitnessSuite found in %q", filename)
	}
	return name, nil
}

// scriptTestRefs returns the testRef of each script in a suite.
func scriptTestRefs(suite *unstructured.Unstructured) []string {
	scripts, _, _ := unstructured.NestedSlice(suite.Object, "spec", "scripts")
	refs := make([]string, 0, len(scripts))
	for _, s := range scripts {
		if m, ok := s.(map[string]any); ok {
			if ref, ok := m["testRef"].(string); ok {
				refs = append(refs, ref)
			}
		}
	}
	return refs
}

// findScript returns the testContent of the script whose testRef matches name.
func findScript(suite *unstructured.Unstructured, name string) (string, bool) {
	scripts, _, _ := unstructured.NestedSlice(suite.Object, "spec", "scripts")
	for _, s := range scripts {
		m, ok := s.(map[string]any)
		if !ok || m["testRef"] != name {
			continue
		}
		content, _ := m["testContent"].(string)
		return content, true
	}
	return "", false
}

func buildCrewFitness(suite, scenario, crewRef, testContent string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kubemoot.ai/v1alpha1",
		"kind":       "CrewFitness",
		"metadata":   map[string]any{"generateName": fmt.Sprintf("%s-%s-kmctl-", suite, scenario)},
		"spec": map[string]any{
			"crewRef":     crewRef,
			"testRef":     scenario,
			"testContent": testContent,
		},
	}}
}

func isTerminalPhase(phase string) bool {
	switch phase {
	case "Completed", "Cancelled", "Passed", "Failed", "Error":
		return true
	default:
		return false
	}
}

// pollPhase polls an object's status.phase until terminal or timeout, printing
// progress lines (phase + iteration progress where present), and returns the
// object as last read.
func pollPhase(ctx context.Context, dc dynamic.Interface, kind resource.Kind, ns, name string, w io.Writer, timeout time.Duration) (*unstructured.Unstructured, error) {
	deadline := time.Now().Add(timeout)
	last := ""
	for {
		obj, err := resource.Get(ctx, dc, kind, ns, name)
		if err != nil {
			return nil, err
		}
		phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
		if line := progressLine(obj, phase); line != last {
			if _, werr := fmt.Fprintln(w, line); werr != nil {
				return nil, werr
			}
			last = line
		}
		if isTerminalPhase(phase) {
			return obj, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out after %s waiting for %s/%s (phase=%q)", timeout, kind.Singular, name, phase)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// printJudgeHint tells the reader when a finished suite's judge is still to
// score it (status.judge.phase Pending or Judging), and where the scores show.
func printJudgeHint(w io.Writer, suite *unstructured.Unstructured) error {
	phase, _, _ := unstructured.NestedString(suite.Object, "status", "judge", "phase")
	if phase != "Pending" && phase != "Judging" {
		return nil
	}
	_, err := fmt.Fprintf(w, "The judge is still scoring this run (judge %s); kmctl fitness get %s shows its progress and scores.\n",
		phase, suite.GetName())
	return err
}

func progressLine(obj *unstructured.Unstructured, phase string) string {
	if phase == "" {
		phase = "Pending"
	}
	done, ok1, _ := unstructured.NestedInt64(obj.Object, "status", "iterationsCompleted")
	total, ok2, _ := unstructured.NestedInt64(obj.Object, "status", "iterationsTotal")
	if ok1 && ok2 && total > 0 {
		passed, _, _ := unstructured.NestedInt64(obj.Object, "status", "passed")
		failed, _, _ := unstructured.NestedInt64(obj.Object, "status", "failed")
		return fmt.Sprintf("%-10s %d/%d done  (passed %d, failed %d)", phase, done, total, passed, failed)
	}
	return phase
}
