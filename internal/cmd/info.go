package cmd

import (
	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/output"
	"github.com/spf13/cobra"
)

// newInfoCommand reports the resolved connection: context, namespace, server,
// and server version. The analog of `kubectl cluster-info` / `helm env`.
func newInfoCommand(f *client.Factory) *cobra.Command {
	var outFmt string
	cmd := &cobra.Command{
		Use:   "info",
		Short: "Show the resolved context, namespace, and cluster connection",
		Long: `Show the connection kmctl will use: the active context and namespace
(after applying --context / --namespace), the API server address, and the
server version if the cluster is reachable.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			info := map[string]string{
				"context":   f.CurrentContext(),
				"namespace": f.Namespace(),
				"server":    "(unresolved)",
				"version":   "(unreachable)",
			}
			if cfg, err := f.RESTConfig(); err == nil {
				info["server"] = cfg.Host
			}
			if disc, err := f.Discovery(); err == nil {
				if v, verr := disc.ServerVersion(); verr == nil {
					info["version"] = v.GitVersion
				}
			}

			if outFmt != "" {
				return output.Print(cmd.OutOrStdout(), output.Format(outFmt), info)
			}
			out := output.NewWriter(cmd.OutOrStdout())
			out.Printf("Context:        %s\n", info["context"])
			out.Printf("Namespace:      %s\n", info["namespace"])
			out.Printf("Server:         %s\n", info["server"])
			out.Printf("Server version: %s\n", info["version"])
			return out.Err()
		},
	}
	cmd.Flags().StringVarP(&outFmt, "output", "o", "", output.FormatHelp(""))
	return cmd
}
