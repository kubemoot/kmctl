package cmd

import (
	"fmt"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/clierr"
	"github.com/spf13/cobra"
)

// stateAnnotation marks a command that changes Kubemoot state in a cluster.
const stateAnnotation = "kmctl.kubemoot.org/changes-state"

// changesState declares that a command writes Kubemoot state. The root command
// runs requireContext before any such command, so a new command opts in by
// putting this in its Annotations.
func changesState() map[string]string { return map[string]string{stateAnnotation: "true"} }

// changesClusterState reports whether cmd declared it changes Kubemoot state.
func changesClusterState(cmd *cobra.Command) bool { return cmd.Annotations[stateAnnotation] == "true" }

// directURLFlags are flags that point a command at a server other than through the
// kubeconfig context. A state-changing command refuses them: they would write
// without being authenticated as the context's user.
var directURLFlags = []string{"nats-url"}

// requireContext is the root's PersistentPreRunE: a state-changing command fails
// here, before it does anything, unless the kubeconfig gives a usable context.
func requireContext(f *client.Factory) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		if !changesClusterState(cmd) {
			return nil
		}
		for _, name := range directURLFlags {
			if fl := cmd.Flags().Lookup(name); fl != nil && fl.Changed {
				return clierr.New(fmt.Sprintf("--%s is not accepted by %q: it changes Kubemoot state and must go through your Kubernetes context", name, cmd.CommandPath()),
					"Drop --"+name+"; kmctl reaches the service through a port-forward with the context's credentials", nil)
			}
		}
		return f.RequireContext(cmd.Context())
	}
}

// refusedNATSURL is the --nats-url help of a command that changes state.
const refusedNATSURL = "Not accepted here: this command writes through your Kubernetes context only"
