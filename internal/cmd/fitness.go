package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/kubemoot/kmctl/internal/client"
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
	cmd.AddCommand(newListCommand(f, resource.CrewFitnessSuite))
	cmd.AddCommand(newGetCommand(f, resource.CrewFitnessSuite))
	cmd.AddCommand(newScenariosCommand(f))
	cmd.AddCommand(newRunCommand(f))
	cmd.AddCommand(newDownloadCommand(f))
	return cmd
}

// newDownloadCommand fetches a suite's XLSX artifact from the dashboard's
// artifact endpoint through the API server service proxy (kubeconfig creds, no
// direct NATS object-store access).
func newDownloadCommand(f *client.Factory) *cobra.Command {
	var outFile, dashNS, dashSvc string
	cmd := &cobra.Command{
		Use:     "download SUITE",
		Short:   "Download a fitness suite's XLSX artifact",
		Example: "  kmctl fitness download demo-starter\n  kmctl fitness download demo-starter -o results.xlsx",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			suite := args[0]
			rc, err := f.CoreRESTClient()
			if err != nil {
				return err
			}
			data, err := rc.Get().
				Namespace(dashNS).Resource("services").
				Name(dashSvc+":80").SubResource("proxy").
				Suffix("dashboard", "api", "kubemoot", "crewfitnesssuites", f.Namespace(), suite, "artifact").
				DoRaw(cmd.Context())
			if err != nil {
				return fmt.Errorf("download artifact for suite %q: %w", suite, err)
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
	cmd.Flags().StringVar(&dashNS, "dashboard-namespace", "kubemoot", "Namespace of the kubemoot-dashboard service")
	cmd.Flags().StringVar(&dashSvc, "dashboard-service", "kubemoot-dashboard", "Name of the dashboard service")
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
		Long: `Run waits for a fitness suite to finish - phase Completed (the operator sets
that only after the deferred judge has scored, so it is the completion gate) -
printing progress. Name an existing suite, or apply one from a manifest with -f.
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
				return pollPhase(cmd.Context(), dc, resource.CrewFitnessSuite, ns, suiteName, w, timeout)
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
			return pollPhase(cmd.Context(), dc, resource.CrewFitness, ns, created.GetName(), w, timeout)
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
	case "Completed", "Failed", "Error":
		return true
	default:
		return false
	}
}

// pollPhase polls an object's status.phase until terminal or timeout, printing
// progress lines (phase + iteration progress where present).
func pollPhase(ctx context.Context, dc dynamic.Interface, kind resource.Kind, ns, name string, w io.Writer, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	last := ""
	for {
		obj, err := resource.Get(ctx, dc, kind, ns, name)
		if err != nil {
			return err
		}
		phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
		if line := progressLine(obj, phase); line != last {
			if _, werr := fmt.Fprintln(w, line); werr != nil {
				return werr
			}
			last = line
		}
		if isTerminalPhase(phase) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for %s/%s (phase=%q)", timeout, kind.Singular, name, phase)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
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
