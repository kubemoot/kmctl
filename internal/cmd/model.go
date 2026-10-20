package cmd

import (
	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/resource"
	"github.com/spf13/cobra"
)

// newModelCommand groups model-server subcommands. `list`/`get` cover the
// servers; `footprint` lists the scheduled Model resources (which models are
// resident on which server) - the terminal view of the live GPU footprint.
func newModelCommand(f *client.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "model",
		Short:   "List model servers and the live model footprint",
		Aliases: []string{"models", "modelserver", "modelservers"},
		Args:    cobra.NoArgs,
	}
	addResourceCommands(cmd, f, resource.ModelServer)

	footprint := newListCommand(f, cmd.Name(), resource.Model)
	footprint.Use = "footprint"
	footprint.Aliases = []string{"resident"}
	footprint.Short = "List models resident on servers (the live GPU footprint)"
	footprint.Example = "  kmctl model footprint\n  kmctl model footprint -A -o yaml"
	cmd.AddCommand(footprint)
	return cmd
}
