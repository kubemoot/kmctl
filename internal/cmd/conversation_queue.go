package cmd

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/kubemoot/kmctl/internal/conversation"
	"github.com/kubemoot/kmctl/internal/discussion"
	"github.com/spf13/cobra"
	"k8s.io/client-go/rest"
)

// queuePollEvery is how often a waiting question's place in the crew's queue is read. It only
// paces the display: the stream, not this poll, says when the crew starts.
var queuePollEvery = 3 * time.Second

// queueNotes is where a change of queue position is written when the live view does not show it:
// the error stream, so the answer and the JSON on stdout stay clean.
func queueNotes(cmd *cobra.Command, opts conversation.Options) io.Writer {
	if opts.Mode == conversation.ModeFull {
		return nil
	}
	return cmd.ErrOrStderr()
}

// pollQueue reads the conversation's state until the crew shows activity, the question is not
// queued, or the gateway has no state read, and records the queue position in opts.Queue. It
// writes a line to notes (when not nil) each time the position changes, so a question that
// waits behind others is not silence.
func pollQueue(ctx context.Context, rc rest.Interface, opts conversation.Options, started <-chan struct{}, notes io.Writer) {
	tick := time.NewTicker(queuePollEvery)
	defer tick.Stop()
	last := 0
	for {
		state, err := discussion.State(ctx, rc, opts.Crew, opts.ConversationID)
		if err != nil {
			return
		}
		position := discussion.QueuePosition(state)
		opts.Queue.Set(position)
		if position == 0 {
			return
		}
		if position != last && notes != nil {
			_, _ = fmt.Fprintln(notes, conversation.QueueText(position))
		}
		last = position
		select {
		case <-ctx.Done():
			return
		case <-started:
			opts.Queue.Set(0)
			return
		case <-tick.C:
		}
	}
}
