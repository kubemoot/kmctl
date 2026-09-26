package cmd

import (
	"github.com/javajon-homelab/kmctl/internal/client"
	"github.com/javajon-homelab/kmctl/internal/resource"
	"github.com/spf13/cobra"
)

// newAgentCommand groups the agent subcommands.
func newAgentCommand(f *client.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "agent",
		Short:   "List and inspect agents",
		Aliases: []string{"agents"},
		Args:    cobra.NoArgs,
	}
	cmd.AddCommand(newListCommand(f, resource.Agent))
	cmd.AddCommand(newGetCommand(f, resource.Agent))
	return cmd
}
