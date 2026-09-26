// Package cmd builds the kmctl command tree.
package cmd

import (
	"fmt"
	"os"

	"github.com/javajon-homelab/kmctl/internal/client"
	"github.com/spf13/cobra"
)

// NewRootCommand builds the root kmctl command. Subcommands are attached here as
// the tool grows; keeping construction in one function makes the tree testable.
func NewRootCommand() *cobra.Command {
	f := client.NewFactory()

	root := &cobra.Command{
		Use:   "kmctl",
		Short: "Work with crews and Kubemoot from the command line",
		Long: `kmctl is the command-line tool for working with crews and Kubemoot.

It is the scriptable, terminal-native counterpart to the CrewForge IDE: a
convenience layer over the Kubernetes resources Kubemoot manages (crews, agents,
prompt modules, models) plus its live discussions and fitness suites. The
operator remains the single source of lifecycle truth; kmctl is a client.

kmctl is early. Commands are added as the tool grows.`,
		Example: `  kmctl status                          # is the cluster reachable + Kubemoot installed?
  kmctl create demo                     # scaffold a crew
  kmctl apply -f demo/                  # create its resources
  kmctl crew get demo                   # inspect it
  kmctl conversation ask demo "hello"   # ask it something
  kmctl fitness run demo-starter        # run its fitness suite`,
		// Errors are surfaced once by Execute; usage noise on error is unhelpful.
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Standard kubectl-style connection flags: --kubeconfig, --context,
	// --namespace, and the auth set.
	f.ConfigFlags.AddFlags(root.PersistentFlags())

	root.AddCommand(newVersionCommand())
	root.AddCommand(newInfoCommand(f))
	root.AddCommand(newStatusCommand(f))
	root.AddCommand(newCreateCommand(f))
	root.AddCommand(newCrewCommand(f))
	root.AddCommand(newAgentCommand(f))
	root.AddCommand(newPromptCommand(f))
	root.AddCommand(newModelCommand(f))
	root.AddCommand(newConversationCommand(f))
	root.AddCommand(newFitnessCommand(f))
	root.AddCommand(newApplyCommand(f))
	root.AddCommand(newDeleteCommand(f))

	return root
}

// Execute runs the root command and returns a process exit code.
func Execute() int {
	if err := NewRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	return 0
}
