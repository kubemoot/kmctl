package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
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
  quiet  only the answer (-q is the same as -o quiet)

full is the default and falls back to plain when stdout is not a terminal or
NO_COLOR is set. Every mode ends with a summary of how the answer was made and,
as the last line, the command that asks a follow-up in the same conversation.
` + waitingHelp,
		Example: `  kmctl conversation ask homelab-pilot "what is failing?" -n crew-homelab-pilot
  kmctl conv ask homelab-pilot "summarize gpu usage" --quiet
  kmctl conv ask homelab-pilot "list the nodes" -o json --timeout 15m
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
			ctx, cancel := sf.overall(cmd.Context())
			defer cancel()
			opts.ConversationID, err = discussion.Ask(ctx, rc, opts.Namespace, crew, message, conversationID)
			if err != nil {
				return err
			}
			opts.Announce = true
			return followStream(ctx, cmd, rc, opts, sf)
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
The output modes (-o full|plain|json) are the same as for "kmctl conversation ask".
` + waitingHelp,
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
			ctx, cancel := sf.overall(cmd.Context())
			defer cancel()
			opts.ConversationID = id
			return followStream(ctx, cmd, rc, opts, sf)
		},
	}
	sf.register(cmd)
	return cmd
}

// waitingHelp says how long the streaming commands wait.
const waitingHelp = `
kmctl waits as long as the crew is working. While anyone on the crew is busy (an
agent triaging or evaluating, waiting for an accelerator, a model loading, the
coordinator synthesizing) the gateway sends activity events; plain shows them as
"busy:" lines, json as type "activity", and full in the header. kmctl stops only
when the crew shows no activity for --idle-timeout (default 5m), or when --timeout
passes, if you set one. It then prints the conversation and thread IDs and the
"kmctl conversation watch" command that reattaches; the crew may still answer.`

// defaultIdleTimeout is how long the crew may show no activity before kmctl stops
// waiting. A gateway that reports activity sends it every 15 seconds while anyone
// works, so the limit is reached only when the crew has stopped. An older gateway
// sends nothing between an agent's signals; the limit covers a cold model load
// followed by a long evaluation there.
const defaultIdleTimeout = 5 * time.Minute

// streamFlags are the flags shared by the commands that stream a discussion.
type streamFlags struct {
	output      string
	quiet       bool
	timeout     time.Duration
	idleTimeout time.Duration
	// after is the idle clock's timer; nil means the wall clock.
	after conversation.AfterFunc
}

func (sf *streamFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&sf.output, "output", "o", string(conversation.ModeFull), conversation.ModeHelp())
	cmd.Flags().BoolVarP(&sf.quiet, "quiet", "q", false, "Print only the final answer (the same as -o quiet)")
	cmd.Flags().DurationVar(&sf.idleTimeout, "idle-timeout", defaultIdleTimeout,
		"Stop waiting when the crew shows no activity for this long (0 waits while the stream is open)")
	cmd.Flags().DurationVar(&sf.timeout, "timeout", 0,
		"Overall cap on the wait, for scripts (0, the default, means no cap)")
	_ = cmd.RegisterFlagCompletionFunc("output", func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return conversation.ModeNames(), cobra.ShellCompDirectiveNoFileComp
	})
}

// options resolves the output mode for this command's stdout and starts the
// turn's clock.
func (sf *streamFlags) options(cmd *cobra.Command, f *client.Factory, crew string) (conversation.Options, error) {
	requested, err := sf.requestedOutput(cmd)
	if err != nil {
		return conversation.Options{}, err
	}
	mode, err := conversation.Resolve(requested, conversation.IsTerminal(cmd.OutOrStdout()), os.Getenv)
	if err != nil {
		return conversation.Options{}, err
	}
	return conversation.Options{
		Mode: mode, Crew: crew, Namespace: f.Namespace(), Start: time.Now(),
	}, nil
}

// requestedOutput is the output mode asked for: -q means -o quiet, and -q with
// a different -o is an error rather than a silent choice.
func (sf *streamFlags) requestedOutput(cmd *cobra.Command) (string, error) {
	if !sf.quiet {
		return sf.output, nil
	}
	if cmd.Flags().Changed("output") && !strings.EqualFold(strings.TrimSpace(sf.output), string(conversation.ModeQuiet)) {
		return "", fmt.Errorf("-q is the same as -o quiet and cannot be combined with -o %s", sf.output)
	}
	return string(conversation.ModeQuiet), nil
}

// overall is the command's context, capped by --timeout when it is set.
func (sf *streamFlags) overall(parent context.Context) (context.Context, context.CancelFunc) {
	if sf.timeout > 0 {
		return context.WithTimeout(parent, sf.timeout)
	}
	return context.WithCancel(parent)
}

// followStream opens the conversation's discussion stream and renders it. The idle
// clock ends the stream when the crew shows no activity for --idle-timeout.
func followStream(ctx context.Context, cmd *cobra.Command, rc rest.Interface, opts conversation.Options, sf streamFlags) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := discussion.Stream(ctx, rc, opts.Namespace, opts.Crew, opts.ConversationID)
	if err != nil {
		return err
	}
	defer func() { _ = stream.Close() }()
	w := &streamWatch{idle: conversation.NewIdleClock(sf.idleTimeout, sf.after, cancel)}
	defer w.idle.Stop()
	opts.OnEvent = w.seen
	return w.endErr(ctx, conversation.Render(cmd.OutOrStdout(), stream, opts), sf.timeout, opts)
}

// streamWatch follows a stream's events: it restarts the idle clock and keeps the
// thread ID for the message kmctl prints when it stops waiting.
type streamWatch struct {
	idle *conversation.IdleClock

	mu       sync.Mutex
	threadID string
}

func (w *streamWatch) seen(e discussion.Event) {
	w.idle.Seen(e)
	if e.Type == "thread_found" {
		w.mu.Lock()
		w.threadID = e.ThreadID
		w.mu.Unlock()
	}
}

// endErr explains why kmctl stopped waiting: the crew showed no activity for the
// idle limit, or --timeout passed. Either way the discussion may still finish, so
// the message names the conversation and thread and how to reattach.
func (w *streamWatch) endErr(ctx context.Context, err error, timeout time.Duration, o conversation.Options) error {
	if err == nil {
		return nil
	}
	var why string
	switch {
	case w.idle.Fired() && errors.Is(ctx.Err(), context.Canceled):
		why = fmt.Sprintf("the crew showed no activity for %s", w.idle.Limit())
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		why = fmt.Sprintf("stopped waiting after --timeout %s", timeout)
	default:
		return err
	}
	w.mu.Lock()
	thread := w.threadID
	w.mu.Unlock()
	return fmt.Errorf("%s; the discussion may still finish.\n%s\nReattach: %s",
		why, idsText(o.ConversationID, thread), conversation.Reattach(o.Crew, o.Namespace, o.ConversationID))
}

// idsText names the conversation and, once known, its thread.
func idsText(conversationID, threadID string) string {
	if threadID == "" {
		return "Conversation " + conversationID
	}
	return "Conversation " + conversationID + ", thread " + threadID
}
