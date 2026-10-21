package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/conversation"
	"github.com/kubemoot/kmctl/internal/discussion"
	"github.com/kubemoot/kmctl/internal/output"
	"github.com/kubemoot/kmctl/internal/placement"
	"github.com/spf13/cobra"
)

// placementsOutput is the structured form of the command: the calls exactly as the
// receipt carried them, and the totals kmctl computed from them.
type placementsOutput struct {
	ConversationID string            `json:"conversationId"`
	Crew           string            `json:"crew"`
	Calls          []json.RawMessage `json:"calls"`
	Summary        placement.Stats   `json:"summary"`
}

func newConversationPlacementsCommand(f *client.Factory) *cobra.Command {
	var outFmt string
	var sf streamFlags
	cmd := &cobra.Command{
		Use:   "placements CREW CONVERSATION_ID",
		Short: "Show where the scheduler placed each model call of a turn",
		Long: `Show the scheduler's placement decisions for a conversation's latest turn: one
row per model call with the agent, the phase, the model and model server it ran on,
the policy that placed it (learned, algorithm, or shadow), the predicted and the
measured time, and the signed error (positive when the call took longer than
predicted). Below the table, each call lists the reason for its placement, its
predicted and measured time to the first token, and the other servers the scheduler
weighed, with their predicted times. The last line counts the calls per policy and
gives the mean absolute error.

The calls come from the receipt on the turn's closing message. When the stream holds
more than one closed thread for the turn, the latest thread's receipt is used. A
turn whose receipt has no placement calls (older agents do not report them) prints a
one-line note. -o json, yaml, or kyaml print the calls as the receipt carried them,
with the same totals under "summary".

A turn that has closed replays from the discussion stream at once. For a turn still
running, kmctl waits for it to close, on the same terms as "kmctl conversation
watch".
` + waitingHelp,
		Example: `  kmctl conversation placements homelab-pilot 6733c -n crew-homelab-pilot
  kmctl conv placements homelab-pilot 6733c361-aa0c-4bde-85c6-e143af23b251 -o json | jq '.calls[] | select(.policy == "shadow")'`,
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: completeCrewThenConversation(f),
		RunE: func(cmd *cobra.Command, args []string) error {
			if outFmt != "" {
				if err := output.Validate(output.Format(outFmt)); err != nil {
					return err
				}
			}
			ctx, cancel := sf.overall(cmd.Context())
			defer cancel()
			crew := args[0]
			id, closed, err := fetchReceipt(ctx, f, sf, crew, args[1])
			if err != nil {
				return err
			}
			return printPlacements(cmd.OutOrStdout(), outFmt, crew, id, closed.Receipt)
		},
	}
	cmd.Flags().StringVarP(&outFmt, "output", "o", "", output.FormatHelp("table"))
	sf.registerWaiting(cmd)
	return cmd
}

// fetchReceipt resolves the conversation ID and follows its stream to the receipt of
// the latest closed thread. A stream that ends or stalls before a thread closes is
// explained the way watch explains it, with the IDs and the command that reattaches.
func fetchReceipt(ctx context.Context, f *client.Factory, sf streamFlags, crew, given string) (string, discussion.Closed, error) {
	rc, err := f.CoreRESTClient()
	if err != nil {
		return "", discussion.Closed{}, err
	}
	ns := f.Namespace()
	id, err := resolveConversation(ctx, rc, ns, crew, given)
	if err != nil {
		return "", discussion.Closed{}, explainGateway(ctx, f, err, crew, ns)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	w := &streamWatch{idle: conversation.NewIdleClock(sf.idleTimeout, sf.after, cancel)}
	defer w.idle.Stop()
	closed, err := discussion.FetchReceipt(ctx, rc, ns, crew, id, w.seen)
	if err == nil {
		return id, closed, nil
	}
	if ctx.Err() != nil || errors.Is(err, discussion.ErrNoClose) {
		o := conversation.Options{Crew: crew, Namespace: ns, ConversationID: id}
		return id, closed, w.endErr(ctx, err, sf.timeout, o)
	}
	return id, closed, explainGateway(ctx, f, err, crew, ns)
}

// printPlacements writes the placements of a receipt as a table, or in a structured
// format when outFmt names one.
func printPlacements(w io.Writer, outFmt, crew, id string, receipt json.RawMessage) error {
	decoded, err := placement.Decode(receipt)
	if err != nil {
		return err
	}
	if outFmt != "" {
		raw := decoded.Raw
		if raw == nil {
			raw = []json.RawMessage{}
		}
		return output.Print(w, output.Format(outFmt), placementsOutput{
			ConversationID: id, Crew: crew, Calls: raw, Summary: placement.Summarize(decoded.Calls),
		})
	}
	if len(receipt) == 0 {
		_, err := fmt.Fprintln(w, placement.NoReceiptNote)
		return err
	}
	return placement.Render(w, decoded.Calls)
}
