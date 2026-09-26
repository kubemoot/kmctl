package cmd

import (
	"fmt"
	"strings"

	"github.com/javajon-homelab/kmctl/internal/client"
	"github.com/javajon-homelab/kmctl/internal/output"
	"github.com/javajon-homelab/kmctl/internal/resource"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// newListCommand builds a `list` subcommand for a kind.
func newListCommand(f *client.Factory, k resource.Kind) *cobra.Command {
	var allNS bool
	var outFmt string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   fmt.Sprintf("List %s", k.Plural),
		Example: fmt.Sprintf("  kmctl %s list\n  kmctl %s list -A -o yaml", k.Singular, k.Singular),
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
func newGetCommand(f *client.Factory, k resource.Kind) *cobra.Command {
	var outFmt string
	cmd := &cobra.Command{
		Use:               "get NAME",
		Short:             fmt.Sprintf("Get a %s by name", k.Singular),
		Example:           fmt.Sprintf("  kmctl %s get NAME\n  kmctl %s get NAME -o yaml", k.Singular, k.Singular),
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
			if outFmt != "" {
				return output.Print(cmd.OutOrStdout(), output.Format(outFmt), obj)
			}
			return resource.RenderTable(cmd.OutOrStdout(), k, []unstructured.Unstructured{*obj}, false)
		},
	}
	cmd.Flags().StringVarP(&outFmt, "output", "o", "", "Output format: yaml or json (default: table)")
	return cmd
}
