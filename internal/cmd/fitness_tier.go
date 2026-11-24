package cmd

import (
	"fmt"
	"strings"

	"github.com/kubemoot/kmctl/internal/clierr"
	"github.com/kubemoot/kmctl/internal/resource"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
)

// fitnessTiers are the values of spec.tier the operator accepts, smallest first.
var fitnessTiers = []string{"smoke", "standard", "full"}

// tierRun is what "fitness run --tier" needs to start a suite: the crew, the tier,
// and how many times to run each scenario.
type tierRun struct {
	tier       string
	crew       string
	name       string
	iterations int
	dryRun     bool
}

// validate refuses a tier run that cannot start, before anything is created, and
// refuses the flags that name a suite or a scenario another way.
func (t tierRun) validate(args []string, filename, scenario string) error {
	if !isFitnessTier(t.tier) {
		return clierr.New(fmt.Sprintf("unknown tier %q", t.tier),
			"Use --tier "+strings.Join(fitnessTiers, ", --tier ")+".", nil)
	}
	if t.crew == "" {
		return clierr.New("--tier needs --crew, the crew whose scenarios the tier selects",
			`List crews with "kmctl crew list".`, nil)
	}
	if len(args) > 0 || filename != "" || scenario != "" {
		return fmt.Errorf("--tier creates its own suite: do not combine it with a SUITE name, -f or --scenario")
	}
	if t.iterations < 1 {
		return fmt.Errorf("--iterations must be at least 1")
	}
	return nil
}

func isFitnessTier(s string) bool {
	for _, t := range fitnessTiers {
		if s == t {
			return true
		}
	}
	return false
}

// suite builds the CrewFitnessSuite for the tier. It lists no scripts: the operator
// reads the crew's scenarios when the suite starts and records what it chose.
func (t tierRun) suite() *unstructured.Unstructured {
	meta := map[string]any{}
	if t.name != "" {
		meta["name"] = t.name
	} else {
		meta["generateName"] = fmt.Sprintf("%s-%s-", t.crew, t.tier)
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kubemoot.ai/v1alpha1",
		"kind":       "CrewFitnessSuite",
		"metadata":   meta,
		"spec": map[string]any{
			"crewRef":    t.crew,
			"tier":       t.tier,
			"iterations": int64(t.iterations),
		},
	}}
}

// start creates the suite and returns its name. With --dry-run the API server checks
// the suite and your access and stores nothing; the name is then empty.
func (t tierRun) start(cmd *cobra.Command, dc dynamic.Interface, ns string) (string, error) {
	opts := metav1.CreateOptions{}
	if t.dryRun {
		opts.DryRun = []string{metav1.DryRunAll}
	}
	created, err := dc.Resource(resource.CrewFitnessSuite.GVR()).Namespace(ns).Create(cmd.Context(), t.suite(), opts)
	if err != nil {
		return "", fmt.Errorf("create the %s suite: %w", t.tier, err)
	}
	if t.dryRun {
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "crewfitnesssuite would be created (tier %s, crew %s; dry run, nothing changed)\n", t.tier, t.crew)
		return "", err
	}
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "created crewfitnesssuite/%s (tier %s, crew %s, %d iteration(s) of each scenario)\n",
		created.GetName(), t.tier, t.crew, t.iterations); err != nil {
		return "", err
	}
	return created.GetName(), nil
}
