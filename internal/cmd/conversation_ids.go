package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/discussion"
	"github.com/kubemoot/kmctl/internal/output"
	"github.com/kubemoot/kmctl/internal/resource"
	"github.com/spf13/cobra"
	"k8s.io/client-go/rest"
)

// resolveConversation turns a conversation ID or a unique prefix of one into the full
// ID, listing the crew's recent conversations through its gateway when needed.
func resolveConversation(ctx context.Context, rc rest.Interface, namespace, crew, given string) (string, error) {
	return discussion.ResolveConversationID(given, func() ([]discussion.ConversationInfo, error) {
		return listConversations(ctx, rc, namespace, crew)
	})
}

// listConversations lists as many of the crew's recent conversations as the gateway
// returns.
func listConversations(ctx context.Context, rc rest.Interface, namespace, crew string) ([]discussion.ConversationInfo, error) {
	return discussion.List(ctx, rc, namespace, crew, discussion.ListLimit)
}

// completeConversationIDs completes a conversation ID of the crew named by the
// command's first argument, each with its first question as the description. It
// offers nothing when the crew is not named yet or the gateway cannot list.
func completeConversationIDs(f *client.Factory, cmd *cobra.Command, crew, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if crew == "" {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	rc, err := f.CoreRESTClient()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	conversations, err := listConversations(cmd.Context(), rc, f.Namespace(), crew)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return discussion.Completions(conversations, toComplete), cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveKeepOrder
}

// completeCrewThenConversation completes a crew name as the first argument and one of
// its conversation IDs as the second.
func completeCrewThenConversation(f *client.Factory) cobra.CompletionFunc {
	return completeCrewThen(f, func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return completeConversationIDs(f, cmd, args[0], toComplete)
	})
}

// completeCrewArg completes a crew name as the first argument only.
func completeCrewArg(f *client.Factory) cobra.CompletionFunc { return completeCrewThen(f, nil) }

// completeCrewThen completes a crew name as the first argument and, with second, the
// second argument; it offers nothing for later arguments.
func completeCrewThen(f *client.Factory, second cobra.CompletionFunc) cobra.CompletionFunc {
	crews := completeNames(f, resource.Crew)
	return func(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		switch {
		case len(args) == 0:
			return crews(cmd, args, toComplete)
		case len(args) == 1 && second != nil:
			return second(cmd, args, toComplete)
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

func newConversationListCommand(f *client.Factory) *cobra.Command {
	var outFmt string
	cmd := &cobra.Command{
		Use:     "list CREW",
		Aliases: []string{"ls"},
		Short:   "List a crew's recent conversations",
		Long: `List a crew's recent conversations, newest first, with each one's first question.
The discussion gateway reads them from the discussion stream, which keeps about a
day of discussions. Any unique prefix of a listed ID works wherever kmctl takes a
conversation ID.`,
		Example: `  kmctl conversation list homelab-pilot -n crew-homelab-pilot
  kmctl conv ls homelab-pilot -o json`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeCrewArg(f),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := f.CoreRESTClient()
			if err != nil {
				return err
			}
			conversations, err := listConversations(cmd.Context(), rc, f.Namespace(), args[0])
			if err != nil {
				return explainGateway(cmd.Context(), f, err, args[0], f.Namespace())
			}
			if outFmt != "" {
				return output.Print(cmd.OutOrStdout(), output.Format(outFmt), conversations)
			}
			return printConversations(cmd.OutOrStdout(), conversations)
		},
	}
	cmd.Flags().StringVarP(&outFmt, "output", "o", "", output.FormatHelp("table"))
	return cmd
}

// printConversations writes the conversations as a table.
func printConversations(w io.Writer, conversations []discussion.ConversationInfo) error {
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	_, _ = fmt.Fprintln(tw, "CONVERSATION ID\tLAST TURN\tTURNS\tFIRST QUESTION")
	for _, c := range conversations {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", c.ConversationID, c.LastTurnAt, c.Turns, discussion.Shorten(c.Question, 60))
	}
	return tw.Flush()
}

func newConversationArtifactCommand(f *client.Factory) *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "artifact CREW KEY",
		Short: "Fetch an artifact an agent attached to a discussion",
		Long: `Fetch an artifact an agent attached to a discussion: a contribution too large
for the discussion stream, kept in the crew's artifact store under KEY. The
conversation output prints this command for each artifact. A thread's artifacts
are deleted soon after the thread closes, so fetch them while the crew is working on
the question.`,
		Example: `  kmctl conversation artifact homelab-pilot crew-homelab-pilot/homelab-pilot/<thread>/pilot-metrics/agree-1b2c -n crew-homelab-pilot
  kmctl conv artifact homelab-pilot <key> -f metrics.txt`,
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: completeCrewArg(f),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc, err := f.CoreRESTClient()
			if err != nil {
				return err
			}
			body, err := discussion.FetchArtifact(cmd.Context(), rc, f.Namespace(), args[0], args[1])
			if err != nil {
				return explainGateway(cmd.Context(), f, err, args[0], f.Namespace())
			}
			defer func() { _ = body.Close() }()
			return writeArtifact(cmd.OutOrStdout(), file, body)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Write the artifact to this file instead of stdout")
	return cmd
}

// writeArtifact copies the artifact to file, or to stdout when file is empty. A file
// that could not be written whole is removed.
func writeArtifact(stdout io.Writer, file string, body io.Reader) error {
	if file == "" {
		_, err := io.Copy(stdout, body)
		return err
	}
	out, err := os.Create(file)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, body)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		// A partial file would look like the whole artifact.
		_ = os.Remove(file)
	}
	return err
}
