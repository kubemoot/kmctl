package cmd

import (
	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/resource"
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
	addResourceCommands(cmd, f, resource.Agent)
	return cmd
}
