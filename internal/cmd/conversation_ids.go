package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
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
func resolveConversation(ctx context.Context, rc rest.Interface, crew, given string) (string, error) {
	return discussion.ResolveConversationID(given, func() ([]discussion.ConversationInfo, error) {
		return listConversations(ctx, rc, crew)
	})
}

// listConversations lists as many of the crew's recent conversations as the gateway
// returns.
func listConversations(ctx context.Context, rc rest.Interface, crew string) ([]discussion.ConversationInfo, error) {
	return discussion.List(ctx, rc, crew, discussion.ListLimit)
}

// completeConversationIDs completes a conversation ID of the crew named by the
// command's first argument, each with its first question as the description. It
// offers nothing when the crew is not named yet or the gateway cannot list.
func completeConversationIDs(f *client.Factory, cmd *cobra.Command, crew, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if crew == "" {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	rc, stop, err := openGateway(cmd.Context(), f, crew, f.Namespace())
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer stop()
	conversations, err := listConversations(cmd.Context(), rc, crew)
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
	var all bool
	cmd := &cobra.Command{
		Use:     "list CREW",
		Aliases: []string{"ls"},
		Short:   "List a crew's recent conversations",
		Long: `List a crew's recent conversations, newest first, with each one's first question.
The discussion gateway reads them from the discussion stream, which keeps about a
day of discussions. Any unique prefix of a listed ID works wherever kmctl takes a
conversation ID.

On a crew that verifies who is asking you see the conversations you started. --all
lists every caller's conversations, with each owner's id; it needs the list
permission on crews/conversations and a --reason, which the gateway audits.`,
		Example: `  kmctl conversation list homelab-pilot -n crew-homelab-pilot
  kmctl conv ls homelab-pilot -o json
  kmctl conv ls homelab-pilot --all --reason "weekly review of unanswered questions"`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeCrewArg(f),
		RunE: func(cmd *cobra.Command, args []string) error {
			if all && strings.TrimSpace(f.Caller.Reason) == "" {
				return errors.New(`--all lists other callers' conversations; say why with --reason "<text>"`)
			}
			rc, stop, err := openGateway(cmd.Context(), f, args[0], f.Namespace())
			if err != nil {
				return err
			}
			defer stop()
			conversations, err := discussion.ListAs(cmd.Context(), rc, args[0], discussion.ListLimit, all)
			if err != nil {
				return explainGateway(cmd.Context(), f, err, args[0], f.Namespace())
			}
			if outFmt != "" {
				return output.Print(cmd.OutOrStdout(), output.Format(outFmt), conversations)
			}
			return printConversations(cmd.OutOrStdout(), conversations, all)
		},
	}
	cmd.Flags().StringVarP(&outFmt, "output", "o", "", output.FormatHelp("table"))
	cmd.Flags().BoolVar(&all, "all", false, "List every caller's conversations (an operator; needs --reason)")
	return cmd
}

// printConversations writes the conversations as a table; withOwner adds the owner's
// id, which only an operator's --all listing carries.
func printConversations(w io.Writer, conversations []discussion.ConversationInfo, withOwner bool) error {
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	header, row := "CONVERSATION ID\tLAST TURN\tTURNS\tFIRST QUESTION", "%s\t%s\t%d\t%s\n"
	if withOwner {
		header, row = "CONVERSATION ID\tOWNER\tLAST TURN\tTURNS\tFIRST QUESTION", "%s\t%s\t%s\t%d\t%s\n"
	}
	_, _ = fmt.Fprintln(tw, header)
	for _, c := range conversations {
		q := discussion.Shorten(c.Question, 60)
		if withOwner {
			_, _ = fmt.Fprintf(tw, row, c.ConversationID, orDash(c.Owner), c.LastTurnAt, c.Turns, q)
			continue
		}
		_, _ = fmt.Fprintf(tw, row, c.ConversationID, c.LastTurnAt, c.Turns, q)
	}
	return tw.Flush()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func newConversationWorkspaceCommand(f *client.Factory) *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "workspace CREW KEY",
		Short: "Fetch an object from a discussion's workspace",
		Long: `Fetch an object from a discussion's workspace: a result too large for the
discussion stream, kept in the workspace under KEY. The conversation output prints
this command for each object. A thread's workspace is deleted soon after the
thread closes, so fetch objects while the crew is working on the question.`,
		Example: `  kmctl conversation workspace homelab-pilot crew-homelab-pilot/homelab-pilot/<thread>/pilot-metrics/agree-1b2c -n crew-homelab-pilot
  kmctl conv workspace homelab-pilot <key> -f metrics.txt`,
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: completeCrewArg(f),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runFetchWorkspace(cmd, f, file, args[0], args[1])
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Write the object to this file instead of stdout")
	return cmd
}

// newConversationArtifactAliasCommand is the old name of the workspace command. It is
// hidden, warns on stderr, and does the same work.
func newConversationArtifactAliasCommand(f *client.Factory) *cobra.Command {
	cmd := newConversationWorkspaceCommand(f)
	cmd.Use = "artifact CREW KEY"
	cmd.Hidden = true
	run := cmd.RunE
	cmd.RunE = func(c *cobra.Command, args []string) error {
		_, _ = fmt.Fprintln(c.ErrOrStderr(), "kmctl conversation artifact is deprecated and is removed in the next release; use kmctl conversation workspace")
		return run(c, args)
	}
	return cmd
}

func runFetchWorkspace(cmd *cobra.Command, f *client.Factory, file, crew, key string) error {
	rc, stop, err := openGateway(cmd.Context(), f, crew, f.Namespace())
	if err != nil {
		return err
	}
	defer stop()
	body, err := discussion.FetchWorkspace(cmd.Context(), rc, crew, key)
	if err != nil {
		return explainGateway(cmd.Context(), f, err, crew, f.Namespace())
	}
	defer func() { _ = body.Close() }()
	return writeWorkspaceObject(cmd.OutOrStdout(), file, body)
}

// writeWorkspaceObject copies the workspace object to file, or to stdout when file is empty. A file
// that could not be written whole is never left, and the file it would replace stays as it was.
func writeWorkspaceObject(stdout io.Writer, file string, body io.Reader) error {
	if file == "" {
		_, err := io.Copy(stdout, body)
		return err
	}
	return writeDownload(file, body, downloadOptions{overwrite: true, mode: 0o644})
}
