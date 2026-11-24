package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/clierr"
	"github.com/kubemoot/kmctl/internal/discussion"
	"github.com/kubemoot/kmctl/internal/output"
	"github.com/kubemoot/kubemoot/operator/pkg/bus"
	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/rest"
)

// newConversationGetCommand shows where a conversation stands: its state and each turn's.
func newConversationGetCommand(f *client.Factory) *cobra.Command {
	var outFmt string
	cmd := &cobra.Command{
		Use:   "get CREW ID",
		Short: "Show a conversation's state and the state of each turn",
		Long: `Show where a conversation stands, as the crew's discussion gateway reads it from the
bus: the conversation is running, paused, queued, stopped or finished, and so is each turn.
A question that waits in the crew's queue shows its place (1 is the next to be taken). It
answers for a conversation another client started, and for a question no agent has seen.
ID can be a unique prefix, or the full ID of a question still waiting in the queue.`,
		Example: `  kmctl conversation get homelab-pilot 6733c361 -n crew-homelab-pilot
  kmctl conv get homelab-pilot 6733c361 -o json | jq -r .state`,
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: completeCrewThenConversation(f),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConversationGet(cmd, f, outFmt, args[0], args[1])
		},
	}
	cmd.Flags().StringVarP(&outFmt, "output", "o", "", output.FormatHelp("table"))
	return cmd
}

func runConversationGet(cmd *cobra.Command, f *client.Factory, outFmt, crew, id string) error {
	act := readAction
	return withResolvedConversation(cmd, f, crew, id, act, func(ctx context.Context, rc rest.Interface, id string) error {
		state, err := discussion.State(ctx, rc, crew, id)
		if err != nil {
			return explainState(ctx, f, err, crew, act)
		}
		if outFmt != "" {
			return output.Print(cmd.OutOrStdout(), output.Format(outFmt), state)
		}
		return printConversationState(cmd.OutOrStdout(), state)
	})
}

// explainState explains a failed state read: a 404 that does not say the conversation is
// missing comes from a gateway older than the state read.
func explainState(ctx context.Context, f *client.Factory, err error, crew string, act gatewayAction) error {
	var se *apierrors.StatusError
	if errors.As(err, &se) && se.Status().Code == http.StatusNotFound && !strings.Contains(statusText(se), conversationNotFound) {
		return clierr.New(fmt.Sprintf("the discussion gateway of crew %q has no state read", crew),
			"It is older than this kmctl; update the crew's Kubemoot, or follow the conversation with: kmctl conversation watch "+crew+" <id>", err)
	}
	return explainControl(ctx, f, err, crew, f.Namespace(), act)
}

// printConversationState writes the conversation's state and a table of its turns.
func printConversationState(w io.Writer, s bus.ConversationState) error {
	if _, err := fmt.Fprintf(w, "conversation %s: %s\n", clean(s.ConversationID), s.State); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	_, _ = fmt.Fprintln(tw, "THREAD\tSTATE\tQUESTION")
	for _, t := range s.Turns {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", orDash(clean(t.ThreadID)), turnStateText(t), discussion.Shorten(clean(t.Question), 60))
	}
	return tw.Flush()
}

// turnStateText is a turn's state with what qualifies it: the place of a queued question, a
// stop that is not confirmed yet.
func turnStateText(t bus.TurnStatus) string {
	switch {
	case t.State == bus.TurnStateQueued && t.QueuePosition > 0:
		return "queued (position " + strconv.FormatInt(t.QueuePosition, 10) + ")"
	case t.StopRequested:
		return string(t.State) + " (stop requested)"
	}
	return string(t.State)
}
