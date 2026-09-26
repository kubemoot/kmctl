package cmd

import (
	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/resource"
	"github.com/spf13/cobra"
)

// newPromptCommand groups the PromptModule subcommands. Authoring stays in
// CrewForge / kmctl apply; these are read-focused (use -o yaml to read the ADL).
func newPromptCommand(f *client.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "prompt",
		Short:   "List and inspect prompt modules",
		Aliases: []string{"prompts", "promptmodule", "promptmodules"},
		Args:    cobra.NoArgs,
	}
	cmd.AddCommand(newListCommand(f, resource.PromptModule))
	cmd.AddCommand(newGetCommand(f, resource.PromptModule))
	return cmd
}
