package cmd

import (
	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/resource"
	"github.com/spf13/cobra"
)

// newModelCommand groups model-provider subcommands. `list`/`get` cover the
// providers; `footprint` lists the scheduled Model resources (which models are
// resident on which provider) - the terminal view of the live GPU footprint.
func newModelCommand(f *client.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "model",
		Short:   "List model providers and the live model footprint",
		Aliases: []string{"models", "modelprovider", "modelproviders"},
		Args:    cobra.NoArgs,
	}
	addResourceCommands(cmd, f, resource.ModelProvider)

	footprint := newListCommand(f, cmd.Name(), resource.Model)
	footprint.Use = "footprint"
	footprint.Aliases = []string{"resident"}
	footprint.Short = "List models resident on providers (the live GPU footprint)"
	footprint.Example = "  kmctl model footprint\n  kmctl model footprint -A -o yaml"
	cmd.AddCommand(footprint)
	return cmd
}
