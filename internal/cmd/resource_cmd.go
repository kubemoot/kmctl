package cmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/output"
	"github.com/kubemoot/kmctl/internal/resource"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// addResourceCommands adds `list` and `get` for a kind under a noun command; their
// examples name the noun the commands are reached by.
func addResourceCommands(parent *cobra.Command, f *client.Factory, k resource.Kind) {
	parent.AddCommand(newListCommand(f, parent.Name(), k), newGetCommand(f, parent.Name(), k))
}

// newListCommand builds a `list` subcommand for a kind, under the parent command
// named noun (its examples read `kmctl <noun> list`).
func newListCommand(f *client.Factory, noun string, k resource.Kind) *cobra.Command {
	var allNS bool
	var outFmt string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   fmt.Sprintf("List %s", k.Plural),
		Example: fmt.Sprintf("  kmctl %s list\n  kmctl %s list -A -o yaml", noun, noun),
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dc, err := f.Dynamic()
			if err != nil {
				return fmt.Errorf("build client: %w", err)
			}
			list, err := resource.List(cmd.Context(), dc, k, f.Namespace(), allNS)
			if err != nil {
				return err
			}
			if outFmt != "" {
				return output.Print(cmd.OutOrStdout(), output.Format(outFmt), list)
			}
			if len(list.Items) == 0 {
				if allNS {
					_, err = fmt.Fprintf(cmd.ErrOrStderr(), "No %s found.\n", k.Plural)
				} else {
					_, err = fmt.Fprintf(cmd.ErrOrStderr(), "No %s found in %s namespace.\n", k.Plural, f.Namespace())
				}
				return err
			}
			return resource.RenderTable(cmd.OutOrStdout(), k, list.Items, allNS)
		},
	}
	cmd.Flags().BoolVarP(&allNS, "all-namespaces", "A", false, "List across all namespaces")
	cmd.Flags().StringVarP(&outFmt, "output", "o", "", "Output format: yaml or json (default: table)")
	return cmd
}

// completeNames returns a shell-completion function that lists object names of a
// kind, so `kmctl crew get <TAB>` completes crew names. It degrades to no
// completion when the cluster is unreachable.
func completeNames(f *client.Factory, k resource.Kind) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		dc, err := f.Dynamic()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		list, err := resource.List(cmd.Context(), dc, k, f.Namespace(), false)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		var names []string
		for i := range list.Items {
			if n := list.Items[i].GetName(); strings.HasPrefix(n, toComplete) {
				names = append(names, n)
			}
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}

// newGetCommand builds a `get NAME` subcommand for a kind. Default output is a
// single-row table (kubectl-consistent); -o yaml|json prints the full object.
func newGetCommand(f *client.Factory, noun string, k resource.Kind) *cobra.Command {
	var outFmt string
	cmd := &cobra.Command{
		Use:               "get NAME",
		Short:             fmt.Sprintf("Get %s by name", withArticle(k.Singular)),
		Example:           fmt.Sprintf("  kmctl %s get NAME\n  kmctl %s get NAME -o yaml", noun, noun),
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeNames(f, k),
		RunE: func(cmd *cobra.Command, args []string) error {
			dc, err := f.Dynamic()
			if err != nil {
				return fmt.Errorf("build client: %w", err)
			}
			obj, err := resource.Get(cmd.Context(), dc, k, f.Namespace(), args[0])
			if err != nil {
				return err
			}
			return printObject(cmd.OutOrStdout(), k, obj, outFmt)
		},
	}
	cmd.Flags().StringVarP(&outFmt, "output", "o", "", "Output format: yaml or json (default: table)")
	return cmd
}

// printObject writes one object: the full object for -o yaml|json, otherwise
// its table row followed by the kind's details, if it has any.
func printObject(w io.Writer, k resource.Kind, obj *unstructured.Unstructured, outFmt string) error {
	if outFmt != "" {
		return output.Print(w, output.Format(outFmt), obj)
	}
	if err := resource.RenderTable(w, k, []unstructured.Unstructured{*obj}, false); err != nil {
		return err
	}
	if k.Details == nil {
		return nil
	}
	return k.Details(w, obj.Object)
}

// withArticle prefixes a singular noun with "a", or "an" before a vowel.
func withArticle(noun string) string {
	if noun != "" && strings.ContainsRune("aeiouAEIOU", rune(noun[0])) {
		return "an " + noun
	}
	return "a " + noun
}
