package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/conversation"
	"github.com/kubemoot/kmctl/internal/discussion"
	"github.com/spf13/cobra"
	"k8s.io/client-go/rest"
)

// newConversationCommand groups the conversation subcommands. The crew's
// discussion gateway is reached through the API server's service proxy, so it
// uses the active kubeconfig credentials (no direct NATS).
func newConversationCommand(f *client.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "conversation",
		Aliases: []string{"conv"},
		Short:   "Ask a crew a question and watch the discussion",
		Long: `Start a turn with a crew and stream the moot's deliberation, or watch an
in-flight conversation. The crew's discussion gateway is reached through the
Kubernetes API server's service proxy using your kubeconfig credentials.

Pass the crew's namespace with -n (the crew and its <crew>-discussion service
live there, e.g. -n crew-homelab-pilot).`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(newAskCommand(f), newWatchCommand(f))
	return cmd
}

func newAskCommand(f *client.Factory) *cobra.Command {
	var conversationID string
	var sf streamFlags
	cmd := &cobra.Command{
		Use:   "ask CREW MESSAGE",
		Short: "Start a turn with a crew and stream the discussion",
		Long: `Start a turn with a crew and stream the discussion until the crew answers.

Output modes (-o):
  full   live view: one row per agent with a spinner, its step, the accelerator it
         runs on, and its step durations; then the answer rendered as markdown
  plain  one line per event with the time since the turn began; the answer as raw
         markdown
  json   newline-delimited JSON: a conversation line, one line per event, and a
         final result line with the answer and the summary

full is the default and falls back to plain when stdout is not a terminal or
NO_COLOR is set. Every mode ends with a summary of how the answer was made and,
as the last line, the command that asks a follow-up in the same conversation.
--quiet prints only the answer.`,
		Example: `  kmctl conversation ask homelab-pilot "what is failing?" -n crew-homelab-pilot
  kmctl conv ask homelab-pilot "summarize gpu usage" --quiet
  kmctl conv ask homelab-pilot "list the nodes" -o json | jq -c 'select(.type == "result")'
  kmctl conv ask homelab-pilot "and their memory?" --conversation-id 6733c361-aa0c-4bde-85c6-e143af23b251`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			crew, message := args[0], args[1]
			opts, err := sf.options(cmd, f, crew)
			if err != nil {
				return err
			}
			rc, err := f.CoreRESTClient()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), sf.timeout)
			defer cancel()
			opts.ConversationID, err = discussion.Ask(ctx, rc, opts.Namespace, crew, message, conversationID)
			if err != nil {
				return err
			}
			opts.Announce = true
			return followStream(ctx, cmd, rc, opts, sf.timeout)
		},
	}
	cmd.Flags().StringVar(&conversationID, "conversation-id", "", "Continue an existing conversation (its full ID)")
	sf.register(cmd)
	return cmd
}

func newWatchCommand(f *client.Factory) *cobra.Command {
	var sf streamFlags
	cmd := &cobra.Command{
		Use:   "watch CREW CONVERSATION_ID",
		Short: "Live-tail an in-flight conversation",
		Long: `Follow the discussion of a conversation's current turn until the crew answers.
The output modes (-o full|plain|json) are the same as for "kmctl conversation ask".`,
		Example: `  kmctl conversation watch homelab-pilot 6733c361-aa0c-4bde-85c6-e143af23b251 -n crew-homelab-pilot
  kmctl conv watch homelab-pilot 6733c361-aa0c-4bde-85c6-e143af23b251 -o plain`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			crew, id := args[0], args[1]
			opts, err := sf.options(cmd, f, crew)
			if err != nil {
				return err
			}
			rc, err := f.CoreRESTClient()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), sf.timeout)
			defer cancel()
			opts.ConversationID = id
			return followStream(ctx, cmd, rc, opts, sf.timeout)
		},
	}
	sf.register(cmd)
	return cmd
}

// streamFlags are the flags shared by the commands that stream a discussion.
type streamFlags struct {
	output  string
	quiet   bool
	timeout time.Duration
}

func (sf *streamFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&sf.output, "output", "o", string(conversation.ModeFull), conversation.ModeHelp())
	cmd.Flags().BoolVarP(&sf.quiet, "quiet", "q", false, "Print only the final answer (in any output mode, including json)")
	cmd.Flags().DurationVar(&sf.timeout, "timeout", 5*time.Minute, "Max time to wait for the discussion to complete")
	_ = cmd.RegisterFlagCompletionFunc("output", func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return conversation.ModeNames(), cobra.ShellCompDirectiveNoFileComp
	})
}

// options resolves the output mode for this command's stdout and starts the
// turn's clock.
func (sf *streamFlags) options(cmd *cobra.Command, f *client.Factory, crew string) (conversation.Options, error) {
	mode, err := conversation.Resolve(sf.output, conversation.IsTerminal(cmd.OutOrStdout()), os.Getenv)
	if err != nil {
		return conversation.Options{}, err
	}
	return conversation.Options{
		Mode: mode, Quiet: sf.quiet, Crew: crew, Namespace: f.Namespace(), Start: time.Now(),
	}, nil
}

// followStream opens the conversation's discussion stream and renders it.
func followStream(ctx context.Context, cmd *cobra.Command, rc rest.Interface, opts conversation.Options, timeout time.Duration) error {
	stream, err := discussion.Stream(ctx, rc, opts.Namespace, opts.Crew, opts.ConversationID)
	if err != nil {
		return err
	}
	defer func() { _ = stream.Close() }()
	return streamTimeoutErr(ctx, conversation.Render(cmd.OutOrStdout(), stream, opts), timeout)
}

// streamTimeoutErr turns a context-deadline failure into a clear message: the
// discussion never emitted a terminal event within the timeout.
func streamTimeoutErr(ctx context.Context, err error, timeout time.Duration) error {
	if err != nil && (errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded) {
		return fmt.Errorf("timed out after %s waiting for the discussion to finish (no 'done' event)", timeout)
	}
	return err
}
