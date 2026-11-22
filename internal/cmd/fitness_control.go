package cmd

import (
	"fmt"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/clierr"
	"github.com/kubemoot/kmctl/internal/resource"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
)

// suiteControl is one way to steer a running fitness suite: the spec field the
// operator reads and what it does.
type suiteControl struct {
	verb  string
	past  string
	short string
	long  string
	spec  map[string]any
	// confirm is the question asked before an action that ends the run, or empty.
	confirm string
	// refuseWhenFinished makes the command fail on a suite that already finished.
	refuseWhenFinished bool
}

var suiteControls = []suiteControl{
	{
		verb: "pause", past: "paused", short: "Pause a fitness suite",
		long: `Pause sets spec.suspend on the suite. The iteration that is running finishes, no new
one starts, and the suite's phase becomes Paused. Resume it with "kmctl fitness resume".`,
		spec: map[string]any{"suspend": true}, refuseWhenFinished: true,
	},
	{
		verb: "resume", past: "resumed", short: "Resume a paused fitness suite",
		long: `Resume clears spec.suspend on the suite, so the operator starts the next iteration.`,
		spec: map[string]any{"suspend": false}, refuseWhenFinished: true,
	},
	{
		verb: "stop", past: "stopped", short: "Stop a fitness suite",
		long: `Stop sets spec.cancel on the suite. The operator deletes the iteration in flight,
the suite's phase becomes Cancelled, and a partial XLSX report is written. A stopped
suite cannot be resumed; run it again, or delete it with "kmctl delete crewfitnesssuite".
It asks first unless --yes is given.`,
		spec:    map[string]any{"cancel": true},
		confirm: "Stop suite %s? Its running iteration is deleted and the suite ends Cancelled.", refuseWhenFinished: true,
	},
}

// addSuiteControlCommands adds fitness pause, resume and stop. Each patches one
// spec field with the caller's own credentials; the operator does the work.
func addSuiteControlCommands(parent *cobra.Command, f *client.Factory, dyn func() (dynamic.Interface, error)) {
	for _, c := range suiteControls {
		parent.AddCommand(newSuiteControlCommand(f, dyn, c))
	}
}

func newSuiteControlCommand(f *client.Factory, dyn func() (dynamic.Interface, error), c suiteControl) *cobra.Command {
	var dryRun, yes bool
	cmd := &cobra.Command{
		Use:         c.verb + " SUITE",
		Annotations: changesState(),
		Short:       c.short,
		Long: c.long + `

It changes the suite with your kubeconfig credentials, so you need patch on
crewfitnesssuites in the suite's namespace. With --dry-run the API server checks the
change and your access and stores nothing.`,
		Example:           fmt.Sprintf("  kmctl fitness %s demo-starter\n  kmctl fitness %s demo-starter --dry-run", c.verb, c.verb),
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeNames(f, resource.CrewFitnessSuite),
		RunE: func(cmd *cobra.Command, args []string) error {
			dc, err := dyn()
			if err != nil {
				return err
			}
			return runSuiteControl(cmd, dc, f.Namespace(), args[0], c, dryRun, yes)
		},
	}
	addDryRunFlag(cmd, &dryRun)
	if c.confirm != "" {
		addYesFlag(cmd, &yes)
	}
	return cmd
}

func runSuiteControl(cmd *cobra.Command, dc dynamic.Interface, ns, name string, c suiteControl, dryRun, yes bool) error {
	suite, err := resource.Get(cmd.Context(), dc, resource.CrewFitnessSuite, ns, name)
	if err != nil {
		return err
	}
	if err := checkSuiteState(suite, c); err != nil {
		return err
	}
	if c.confirm != "" && !dryRun {
		if err := confirmChange(cmd, yes, fmt.Sprintf(c.confirm, name)); err != nil {
			return err
		}
	}
	if err := patchSpec(cmd.Context(), dc, resource.CrewFitnessSuite, ns, name, c.spec, dryRun); err != nil {
		return err
	}
	if dryRun {
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "crewfitnesssuite/%s would be %s (dry run, nothing changed)\n", name, c.past)
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "crewfitnesssuite/%s %s\n", name, c.past)
	return err
}

// checkSuiteState refuses to steer a suite that has already finished.
func checkSuiteState(suite *unstructured.Unstructured, c suiteControl) error {
	phase, _, _ := unstructured.NestedString(suite.Object, "status", "phase")
	if c.refuseWhenFinished && isTerminalPhase(phase) {
		return clierr.New(fmt.Sprintf("suite %s is already %s; there is nothing to %s", suite.GetName(), phase, c.verb),
			`Run it again with "kmctl fitness run", or remove it with "kmctl delete crewfitnesssuite".`, nil)
	}
	return nil
}
