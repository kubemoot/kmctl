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
	Text(label, def string) (string, error)
	Int(label string, def int) (int, error)
	MultiSelect(label string, options []string) ([]string, error)
	SelectOrOther(label string, options []string) (string, error)
}

func newCreateCommand(f *client.Factory) *cobra.Command {
	var (
		members     int
		displayName string
		providers   []string
		modelFamily string
		noInput     bool
		outputDir   string
		chart       bool
	)
	cmd := &cobra.Command{
		Use:   "create NAME",
		Short: "Scaffold the starter crew: small, complete, and working",
		Long: `Create scaffolds the starter crew (the helm-create analog): a read-only guide
to its own Kubernetes namespace that works on any cluster with no configuration.
It has a coordinator, one to five specialists, one Kubernetes MCP server in
read-only mode with a read-only Role, ADL prompt modules, Models for the chosen
family, and a fitness suite that uses the crew's own pods as ground truth.

Specialists, in the order --members adds them:
` + specialistHelp() + `
Agents declare capabilities, never a model; the scheduling policy binds Models.

NAME is the crew's technical name: lowercase letters, digits and hyphens, at
most ` + strconv.Itoa(scaffold.MaxNameLength) + ` characters; it becomes the Kubernetes name of the crew's objects.
--display-name is the name people read, any one line of text, such as
"Homelab Health Guide"; it is stored as the kubemoot.ai/display-name annotation on
the Crew (and on a chart's Chart.yaml) and defaults to NAME.

Inputs not given as flags are prompted for interactively. With --no-input nothing
is prompted, and --members defaults to 1. A bundle (no --chart) is applied to the
namespace given by -n, or crew-NAME.`,
		Example: `  kmctl create demo
  kmctl create demo --chart --members 5 --model-family qwen --no-input
  kmctl create homelab-health-guide --display-name "Homelab Health Guide"
  kmctl create demo -o ./crews
  kmctl create demo --members 2 --model-family qwen --no-input -n team-a`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := scaffold.Options{
				Name:        args[0],
				DisplayName: displayName,
				Members:     members,
				Providers:   providers,
				ModelFamily: modelFamily,
				OutputDir:   outputDir,
				Chart:       chart,
				Namespace:   explicitNamespace(f),
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
			if _, err := fmt.Fprintf(out, "Scaffolded crew %s (%d specialist(s) beside the coordinator) in %s/%s:\n", crewNamed(opts), opts.Members, opts.OutputDir, opts.Name); err != nil {
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
	cmd.Flags().StringVar(&displayName, "display-name", "", "The name people read, any one line of text (default NAME; prompted if unset)")
	cmd.Flags().IntVar(&members, "members", 1, fmt.Sprintf("Specialists beside the coordinator, 1 to %d (prompted if unset)", scaffold.MaxMembers))
	cmd.Flags().StringSliceVar(&providers, "providers", nil, "Model providers the crew may use (prompted if unset)")
	cmd.Flags().StringVar(&modelFamily, "model-family", "", "Preferred model family, e.g. qwen (prompted if unset)")
	cmd.Flags().BoolVar(&noInput, "no-input", false, "Never prompt; unset inputs take their defaults")
	cmd.Flags().StringVarP(&outputDir, "output", "o", ".", "Directory to write the scaffold into")
	cmd.Flags().BoolVar(&chart, "chart", false, "Lay the crew out as a Helm chart (Chart.yaml, templates/, fitness/)")
	return cmd
}

// crewNamed is the crew as the summary names it: "demo", or "Demo Crew" (demo).
func crewNamed(opts scaffold.Options) string {
	named := strconv.Quote(opts.Display())
	if opts.Display() != opts.Name {
		named += " (" + opts.Name + ")"
	}
	return named
}

// nextStep is the command that deploys what was just scaffolded.
func nextStep(opts scaffold.Options) string {
	dir := opts.OutputDir + "/" + opts.Name
	ns := opts.TargetNamespace()
	if opts.Chart {
		return fmt.Sprintf("helm upgrade --install %s %s --namespace %s --create-namespace", opts.Name, dir, ns)
	}
	return fmt.Sprintf("kubectl create namespace %[1]s && kubectl apply -n %[1]s -f %[2]s/access && kmctl apply -n %[1]s -f %[2]s", ns, dir)
}

// explicitNamespace is the -n flag when given, else "": the bundle then targets crew-NAME.
func explicitNamespace(f *client.Factory) string {
	if f.ConfigFlags.Namespace == nil {
		return ""
	}
	return *f.ConfigFlags.Namespace
}

// specialistHelp lists the specialists for the command's long help.
func specialistHelp() string {
	var b strings.Builder
	for i, line := range scaffold.SizeSummary(scaffold.MaxMembers) {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, line)
	}
	return b.String()
}

// gather fills any input not provided as a flag, prompting unless noInput.
func gather(opts *scaffold.Options, changed func(string) bool, noInput bool, discovered []string, p prompter) error {
	if noInput {
		return nil
	}
	steps := []struct {
		flag string
		ask  func(*scaffold.Options, []string, prompter) error
	}{
		{"display-name", askDisplayName},
		{"members", askMembers},
		{"providers", askProviders},
		{"model-family", askFamily},
	}
	for _, s := range steps {
		if changed(s.flag) {
			continue
		}
		if err := s.ask(opts, discovered, p); err != nil {
			return err
		}
	}
	return nil
}

func askDisplayName(opts *scaffold.Options, _ []string, p prompter) error {
	name, err := p.Text("Display name, the name people read (any text):", opts.Name)
	if err == nil {
		opts.DisplayName = strings.TrimSpace(name)
	}
	return err
}

func askMembers(opts *scaffold.Options, _ []string, p prompter) error {
	n, err := p.Int(fmt.Sprintf("How many specialists beside the coordinator (1-%d)?", scaffold.MaxMembers), 1)
	if err == nil {
		opts.Members = n
	}
	return err
}

// askProviders offers the discovered providers; with none discovered there is nothing to pick.
func askProviders(opts *scaffold.Options, discovered []string, p prompter) error {
	if len(discovered) == 0 {
		return nil
	}
	label := fmt.Sprintf("%d LLM providers (ollama) discovered - select those this crew may use:", len(discovered))
	sel, err := p.MultiSelect(label, discovered)
	if err == nil {
		opts.Providers = sel
	}
	return err
}

func askFamily(opts *scaffold.Options, _ []string, p prompter) error {
	fam, err := p.SelectOrOther("Which LLM model family to start with?", scaffold.KnownModelFamilies)
	if err == nil {
		opts.ModelFamily = fam
	}
	return err
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

func (surveyPrompter) Text(label, def string) (string, error) {
	answer := def
	err := survey.AskOne(&survey.Input{Message: label, Default: def}, &answer)
	return answer, err
}

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
