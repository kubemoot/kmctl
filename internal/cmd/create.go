package cmd

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/resource"
	"github.com/kubemoot/kmctl/internal/scaffold"
	"github.com/spf13/cobra"
)

// prompter asks the user for inputs not supplied as flags. Abstracted so the
// gather logic is testable without a TTY.
type prompter interface {
	Int(label string, def int) (int, error)
	MultiSelect(label string, options []string) ([]string, error)
	SelectOrOther(label string, options []string) (string, error)
}

func newCreateCommand(f *client.Factory) *cobra.Command {
	var (
		members     int
		providers   []string
		modelFamily string
		noInput     bool
		outputDir   string
		chart       bool
	)
	cmd := &cobra.Command{
		Use:   "create NAME",
		Short: "Scaffold a working crew with starter fitness tests",
		Long: `Create scaffolds a minimal, valid, ready-to-apply crew (the helm-create
analog): a Crew + scheduling policy, a coordinator and tooler agents (loose
model coupling), prompt modules, and a starter fitness suite.

Required inputs not given as flags are prompted for interactively. Use --no-input
in scripts/CI; then the inputs must come from flags.`,
		Example: `  kmctl create demo
  kmctl create demo --members 3 --model-family qwen --no-input
  kmctl create demo -o ./crews
  kmctl create demo --chart --members 2 --model-family qwen --no-input`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := scaffold.Options{
				Name:        args[0],
				Members:     members,
				Providers:   providers,
				ModelFamily: modelFamily,
				OutputDir:   outputDir,
				Chart:       chart,
			}
			discovered := discoverProviders(cmd.Context(), f)
			if err := gather(&opts, cmd.Flags().Changed, noInput, discovered, &surveyPrompter{}); err != nil {
				return err
			}
			// Default to all discovered providers so the scaffold can generate Models
			// (a crew needs Models to run); the generated models.yaml is editable.
			if len(opts.Providers) == 0 {
				opts.Providers = discovered
			}
			written, err := scaffold.Write(opts)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if _, err := fmt.Fprintf(out, "Scaffolded crew %q (%d tooler(s)) in %s/%s:\n", opts.Name, opts.Members, opts.OutputDir, opts.Name); err != nil {
				return err
			}
			for _, p := range written {
				if _, err := fmt.Fprintf(out, "  %s\n", p); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintf(out, "\nNext: %s\n", nextStep(opts)); err != nil {
				return err
			}
			warnIfNoModels(cmd.ErrOrStderr(), opts)
			return nil
		},
	}
	cmd.Flags().IntVar(&members, "members", 0, "Number of tooler agents (prompted if unset)")
	cmd.Flags().StringSliceVar(&providers, "providers", nil, "Model providers the crew may use (prompted if unset)")
	cmd.Flags().StringVar(&modelFamily, "model-family", "", "Preferred model family, e.g. qwen (prompted if unset)")
	cmd.Flags().BoolVar(&noInput, "no-input", false, "Never prompt; required inputs must come from flags")
	cmd.Flags().StringVarP(&outputDir, "output", "o", ".", "Directory to write the scaffold into")
	cmd.Flags().BoolVar(&chart, "chart", false, "Lay the crew out as a Helm chart (Chart.yaml, templates/, fitness/)")
	return cmd
}

// nextStep is the command that deploys what was just scaffolded.
func nextStep(opts scaffold.Options) string {
	dir := opts.OutputDir + "/" + opts.Name
	if opts.Chart {
		return fmt.Sprintf("helm upgrade --install %s %s --namespace %s --create-namespace", opts.Name, dir, opts.Name)
	}
	return "kmctl apply -f " + dir
}

// gather fills any input not provided as a flag, prompting unless noInput.
func gather(opts *scaffold.Options, changed func(string) bool, noInput bool, discovered []string, p prompter) error {
	if !changed("members") {
		if noInput {
			return fmt.Errorf("--members is required with --no-input")
		}
		n, err := p.Int("Starting number of members in this crew?", 3)
		if err != nil {
			return err
		}
		opts.Members = n
	}
	if !changed("providers") && !noInput && len(discovered) > 0 {
		label := fmt.Sprintf("%d LLM providers (ollama) discovered - select those this crew may use:", len(discovered))
		sel, err := p.MultiSelect(label, discovered)
		if err != nil {
			return err
		}
		opts.Providers = sel
	}
	if !changed("model-family") && !noInput {
		fam, err := p.SelectOrOther("Which LLM model family to start with?", scaffold.KnownModelFamilies)
		if err != nil {
			return err
		}
		opts.ModelFamily = fam
	}
	return nil
}

// warnIfNoModels prints a warning when the scaffold generated no Model CRs, so the
// empty (commented-only) models.yaml is never silent: such a crew applies Ready but
// is Unschedulable until Models are added. Names the cause + the remedy.
func warnIfNoModels(w io.Writer, opts scaffold.Options) {
	if opts.ModelCount() > 0 {
		return
	}
	var cause string
	switch {
	case len(opts.Providers) == 0:
		cause = "no model providers were discovered or selected"
	case opts.ModelFamily == "":
		cause = "no model family was set (--model-family)"
	default:
		cause = fmt.Sprintf("model family %q has no built-in sizes", opts.ModelFamily)
	}
	// Best-effort warning to stderr; a write failure here is not worth failing on.
	_, _ = fmt.Fprintf(w,
		"\nwarning: no Models were generated (%s); models.yaml has only commented placeholders.\n"+
			"The crew will apply Ready but be Unschedulable until you add Models - run `kmctl model list`\n"+
			"to see available providers, then add Model CRs to %s/%s/models.yaml.\n",
		cause, opts.OutputDir, opts.Name)
}

func discoverProviders(ctx context.Context, f *client.Factory) []string {
	dc, err := f.Dynamic()
	if err != nil {
		return nil
	}
	list, err := resource.List(ctx, dc, resource.ModelProvider, "", true)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(list.Items))
	for i := range list.Items {
		names = append(names, list.Items[i].GetName())
	}
	return names
}

// surveyPrompter is the interactive TTY implementation.
type surveyPrompter struct{}

func (surveyPrompter) Int(label string, def int) (int, error) {
	answer := strconv.Itoa(def)
	if err := survey.AskOne(&survey.Input{Message: label, Default: answer}, &answer); err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(answer))
	if err != nil {
		return 0, fmt.Errorf("expected a number, got %q", answer)
	}
	return n, nil
}

func (surveyPrompter) MultiSelect(label string, options []string) ([]string, error) {
	var selected []string
	err := survey.AskOne(&survey.MultiSelect{Message: label, Options: options}, &selected)
	return selected, err
}

func (surveyPrompter) SelectOrOther(label string, options []string) (string, error) {
	const other, skip = "other (type a name)", "skip - define later"
	choice := ""
	if err := survey.AskOne(&survey.Select{Message: label, Options: append(append([]string{}, options...), other, skip)}, &choice); err != nil {
		return "", err
	}
	switch choice {
	case skip:
		return "", nil
	case other:
		name := ""
		if err := survey.AskOne(&survey.Input{Message: "Model name:"}, &name); err != nil {
			return "", err
		}
		return strings.TrimSpace(name), nil
	default:
		return choice, nil
	}
}
