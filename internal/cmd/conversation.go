package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/discussion"
	"github.com/spf13/cobra"
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
	var quiet bool
	var conversationID string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "ask CREW MESSAGE",
		Short: "Start a turn with a crew and stream the discussion",
		Example: `  kmctl conversation ask homelab-pilot "what is failing?" -n crew-homelab-pilot
  kmctl conv ask homelab-pilot "summarize gpu usage" --quiet`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			crew, message := args[0], args[1]
			rc, err := f.CoreRESTClient()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			ns := f.Namespace()
			id, err := discussion.Ask(ctx, rc, ns, crew, message, conversationID)
			if err != nil {
				return err
			}
			if !quiet {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "conversation %s\n", id); err != nil {
					return err
				}
			}
			stream, err := discussion.Stream(ctx, rc, ns, crew, id)
			if err != nil {
				return err
			}
			defer func() { _ = stream.Close() }()
			return streamTimeoutErr(ctx, renderStream(cmd.OutOrStdout(), stream, quiet), timeout)
		},
	}
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Print only the final answer")
	cmd.Flags().StringVar(&conversationID, "conversation-id", "", "Continue an existing conversation")
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "Max time to wait for the discussion to complete")
	return cmd
}

func newWatchCommand(f *client.Factory) *cobra.Command {
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "watch CREW CONVERSATION_ID",
		Short: "Live-tail an in-flight conversation",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			crew, id := args[0], args[1]
			rc, err := f.CoreRESTClient()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			stream, err := discussion.Stream(ctx, rc, f.Namespace(), crew, id)
			if err != nil {
				return err
			}
			defer func() { _ = stream.Close() }()
			return streamTimeoutErr(ctx, renderStream(cmd.OutOrStdout(), stream, false), timeout)
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "Max time to wait for the discussion to complete")
	return cmd
}

// streamTimeoutErr turns a context-deadline failure into a clear message: the
// discussion never emitted a terminal event within the timeout.
func streamTimeoutErr(ctx context.Context, err error, timeout time.Duration) error {
	if err != nil && (errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded) {
		return fmt.Errorf("timed out after %s waiting for the discussion to finish (no 'done' event)", timeout)
	}
	return err
}

// renderStream consumes the SSE stream, printing each event (or only the final
// answer when quiet), and stops on a terminal event.
func renderStream(w io.Writer, stream io.Reader, quiet bool) error {
	var writeErr error
	parseErr := discussion.ParseSSE(stream, func(e discussion.Event) bool {
		if quiet {
			if e.Type == "synthesis" {
				_, writeErr = fmt.Fprintln(w, e.Content)
			}
		} else if line := discussion.Render(e); line != "" {
			_, writeErr = fmt.Fprintln(w, line)
		}
		return writeErr != nil || discussion.Terminal(e)
	})
	if writeErr != nil {
		return writeErr
	}
	return parseErr
}
