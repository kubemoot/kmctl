package cmd

import (
	"fmt"
	"strings"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/resource"
	"github.com/spf13/cobra"
	"k8s.io/client-go/dynamic"
)

// mcpReportNoun is how the examples of the generic list and get commands name the group.
const mcpReportNoun = "mcp report"

// mcpVerdicts are the verdicts an administrator can pin, as the MCPServerReport
// CRD allows them.
var mcpVerdicts = []string{"use", "caution", "avoid"}

// newMCPCommand groups the commands on what the MCP gateway learned about MCP servers.
func newMCPCommand(f *client.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Inspect and curate what Kubemoot learned about MCP servers",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newMCPReportCommand(f, f.Dynamic))
	return cmd
}

func newMCPReportCommand(f *client.Factory, dyn func() (dynamic.Interface, error)) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "report",
		Short:   "MCP server reports: reliability verdicts, computed or pinned",
		Aliases: []string{"reports"},
		Args:    cobra.NoArgs,
	}
	cmd.AddCommand(newListCommand(f, mcpReportNoun, resource.MCPServerReport), newGetCommand(f, mcpReportNoun, resource.MCPServerReport),
		newMCPPinCommand(f, dyn), newMCPUnpinCommand(f, dyn))
	return cmd
}

func newMCPPinCommand(f *client.Factory, dyn func() (dynamic.Interface, error)) *cobra.Command {
	var verdict, notes, author string
	var dryRun bool
	cmd := &cobra.Command{
		Use:         "pin REPORT --verdict use|caution|avoid",
		Annotations: changesState(),
		Short:       "Pin an administrator verdict on an MCP server report",
		Long: `Pin sets spec.adminVerdict (and the notes and author that explain it) on an MCP
server report. The controller keeps a pinned verdict whatever the trials find. The
author defaults to the user of your kubeconfig context. Remove the pin with
"kmctl mcp report unpin".

It changes the report with your kubeconfig credentials, so you need patch on
mcpserverreports in the report's namespace. With --dry-run the API server checks the
change and your access and stores nothing.`,
		Example: `  kmctl mcp report pin kubernetes-mcp-server --verdict avoid --notes "crashes under load" -n kubemoot
  kmctl mcp report pin kubernetes-mcp-server --verdict use --dry-run -n kubemoot`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeNames(f, resource.MCPServerReport),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !validVerdict(verdict) {
				return usageError{fmt.Errorf("--verdict must be one of %s, not %q", strings.Join(mcpVerdicts, ", "), verdict)}
			}
			dc, err := dyn()
			if err != nil {
				return err
			}
			if author == "" {
				author = kubeconfigUser(f)
			}
			spec := map[string]any{"adminVerdict": verdict, "adminNotes": notes, "adminAuthor": author}
			return changeReport(cmd, dc, f.Namespace(), args[0], spec, dryRun, "pinned to "+verdict)
		},
	}
	cmd.Flags().StringVar(&verdict, "verdict", "", "The verdict to pin: "+strings.Join(mcpVerdicts, ", "))
	cmd.Flags().StringVar(&notes, "notes", "", "Why the verdict is pinned")
	cmd.Flags().StringVar(&author, "author", "", "Who pins it (default: the user of your kubeconfig context)")
	_ = cmd.RegisterFlagCompletionFunc("verdict", cobra.FixedCompletions(mcpVerdicts, cobra.ShellCompDirectiveNoFileComp))
	addDryRunFlag(cmd, &dryRun)
	return cmd
}

func newMCPUnpinCommand(f *client.Factory, dyn func() (dynamic.Interface, error)) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:         "unpin REPORT",
		Annotations: changesState(),
		Short:       "Remove the pinned verdict from an MCP server report",
		Long: `Unpin removes spec.adminVerdict, spec.adminNotes and spec.adminAuthor, so the
controller computes the verdict from the trials again. It needs patch on
mcpserverreports in the report's namespace. With --dry-run the API server checks the
change and your access and stores nothing.`,
		Example:           `  kmctl mcp report unpin kubernetes-mcp-server -n kubemoot`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeNames(f, resource.MCPServerReport),
		RunE: func(cmd *cobra.Command, args []string) error {
			dc, err := dyn()
			if err != nil {
				return err
			}
			spec := map[string]any{"adminVerdict": nil, "adminNotes": nil, "adminAuthor": nil}
			return changeReport(cmd, dc, f.Namespace(), args[0], spec, dryRun, "unpinned")
		},
	}
	addDryRunFlag(cmd, &dryRun)
	return cmd
}

func validVerdict(v string) bool {
	for _, ok := range mcpVerdicts {
		if v == ok {
			return true
		}
	}
	return false
}

func changeReport(cmd *cobra.Command, dc dynamic.Interface, ns, name string, spec map[string]any, dryRun bool, did string) error {
	if err := patchSpec(cmd.Context(), dc, resource.MCPServerReport, ns, name, spec, dryRun); err != nil {
		return err
	}
	if dryRun {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "mcpserverreport/%s would be %s (dry run, nothing changed)\n", name, did)
		return err
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "mcpserverreport/%s %s\n", name, did)
	return err
}

// kubeconfigUser is the user of the active kubeconfig context, or empty when it
// cannot be read.
func kubeconfigUser(f *client.Factory) string {
	raw, err := f.ConfigFlags.ToRawKubeConfigLoader().RawConfig()
	if err != nil {
		return ""
	}
	ctx := raw.Contexts[f.CurrentContext()]
	if ctx == nil {
		return ""
	}
	return ctx.AuthInfo
}
