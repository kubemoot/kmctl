package cmd

import (
	"fmt"

	"github.com/javajon-homelab/kmctl/internal/version"
	"github.com/spf13/cobra"
)

// newVersionCommand prints build metadata. `--short` prints just the version,
// matching the convention of kubectl/helm `version` output.
func newVersionCommand() *cobra.Command {
	var short bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the kmctl version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			if short {
				_, err := fmt.Fprintln(out, version.Version)
				return err
			}
			_, err := fmt.Fprintf(out, "kmctl version %s (commit %s, built %s)\n",
				version.Version, version.Commit, version.Date)
			return err
		},
	}
	cmd.Flags().BoolVar(&short, "short", false, "Print just the version number")
	return cmd
}
