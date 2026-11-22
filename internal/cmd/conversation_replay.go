package cmd

import (
	"context"
	"fmt"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/discussion"
	"github.com/spf13/cobra"
	"k8s.io/client-go/rest"
)

// addConversationReplay adds "conversation replay" to the conversation command. The
// command goes through the crew's discussion gateway like "conversation ask": the
// gateway starts the new conversation, so kmctl publishes nothing to the bus itself.
func addConversationReplay(conversation *cobra.Command, f *client.Factory) {
	conversation.AddCommand(newReplayCommand(f))
}

func newReplayCommand(f *client.Factory) *cobra.Command {
	var sf streamFlags
	cmd := &cobra.Command{
		Use:         "replay CREW CONVERSATION_ID",
		Annotations: changesState(),
		Short:       "Ask a conversation's first question again, in a new conversation",
		Long: `Replay reads the first question of a recent conversation from the crew's gateway and
asks it again in a new conversation, streaming the discussion as "kmctl conversation
ask" does. The original conversation is left as it is, and the new one is not marked as
a replay. Use it to see whether a crew answers the same question the same way after a
change. The ID can be shortened to any unique prefix. The gateway keeps about a day of
conversations, so an older one cannot be replayed.

The output modes (-o full|plain|json) are the same as for "kmctl conversation ask".
` + waitingHelp,
		Example: `  kmctl conversation replay homelab-pilot 6733c361 -n crew-homelab-pilot
  kmctl conv replay homelab-pilot 6733c -o plain`,
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: completeCrewThenConversation(f),
		RunE: func(cmd *cobra.Command, args []string) error {
			crew := args[0]
			opts, err := sf.options(cmd, f, crew)
			if err != nil {
				return err
			}
			ctx, cancel := sf.overall(cmd.Context())
			defer cancel()
			rc, stop, err := openGateway(ctx, f, crew, opts.Namespace)
			if err != nil {
				return err
			}
			defer stop()
			question, err := firstQuestion(ctx, rc, crew, args[1])
			if err != nil {
				return explainGateway(ctx, f, err, crew, opts.Namespace)
			}
			opts.ConversationID, err = discussion.Ask(ctx, rc, crew, question, "")
			if err != nil {
				return explainGateway(ctx, f, err, crew, opts.Namespace)
			}
			opts.Announce = true
			return followStream(ctx, cmd, rc, opts, sf, f)
		},
	}
	sf.register(cmd)
	return cmd
}

// firstQuestion is the first question of the conversation given by ID or unique prefix.
func firstQuestion(ctx context.Context, rc rest.Interface, crew, given string) (string, error) {
	conversations, err := listConversations(ctx, rc, crew)
	if err != nil {
		return "", err
	}
	id, err := discussion.ResolveConversationID(given, func() ([]discussion.ConversationInfo, error) { return conversations, nil })
	if err != nil {
		return "", err
	}
	for _, c := range conversations {
		if c.ConversationID != id {
			continue
		}
		if c.Question == "" {
			return "", fmt.Errorf("conversation %s has no recorded question to ask again", id)
		}
		return c.Question, nil
	}
	return "", fmt.Errorf("%w: %s (kmctl conversation list shows the recent ones)", discussion.ErrUnknownConversation, id)
}
