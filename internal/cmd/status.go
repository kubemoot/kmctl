package cmd

import (
	"fmt"

	"github.com/javajon-homelab/kmctl/internal/client"
	"github.com/javajon-homelab/kmctl/internal/output"
	"github.com/spf13/cobra"
)

// newStatusCommand checks connectivity to the cluster and whether Kubemoot is
// installed (its API group is served).
func newStatusCommand(f *client.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Check cluster connectivity and whether Kubemoot is installed",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			disc, err := f.Discovery()
			if err != nil {
				return fmt.Errorf("build discovery client: %w", err)
			}
			ver, err := disc.ServerVersion()
			if err != nil {
				return fmt.Errorf("cannot reach cluster (context %q): %w", f.CurrentContext(), err)
			}

			out := output.NewWriter(cmd.OutOrStdout())
			out.Printf("Cluster:   reachable (context %s, %s)\n", f.CurrentContext(), ver.GitVersion)

			installed, err := client.KubemootInstalled(disc)
			if err != nil {
				return fmt.Errorf("query API groups: %w", err)
			}
			if installed {
				out.Printf("Kubemoot:  installed (%s)\n", client.KubemootGroup)
			} else {
				out.Printf("Kubemoot:  not found (no %s API group)\n", client.KubemootGroup)
			}
			return out.Err()
		},
	}
}
