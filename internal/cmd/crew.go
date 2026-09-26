package cmd

import (
	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/resource"
	"github.com/spf13/cobra"
)

// newCrewCommand groups the crew subcommands.
func newCrewCommand(f *client.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "crew",
		Short:   "List and inspect crews",
		Aliases: []string{"crews"},
		Args:    cobra.NoArgs,
	}
	cmd.AddCommand(newListCommand(f, resource.Crew))
	cmd.AddCommand(newGetCommand(f, resource.Crew))
	return cmd
}
