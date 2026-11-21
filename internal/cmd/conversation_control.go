package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/clierr"
	"github.com/kubemoot/kubemoot/operator/pkg/bus"
	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// controlRole is the ClusterRole an operator needs to act on another caller's
// conversations; the owner of a conversation needs no role.
const controlRole = "kubemoot-conversation-controller"

// controlVerb is one of stop, pause and resume: the gateway route and how the result reads.
type controlVerb struct {
	name, past, short, about string
}

var controlVerbs = []controlVerb{
	{"stop", "stopped", "Stop a conversation's running discussion", "Stop the discussion that is running in a conversation. The crew ends its work on the question without an answer."},
	{"pause", "paused", "Pause a conversation's running discussion", "Pause the discussion that is running in a conversation. Resume it with kmctl conversation resume."},
	{"resume", "resumed", "Resume a paused discussion", "Resume the discussion of a conversation that was paused."},
}

// newConversationControlCommands gives stop, pause and resume.
func newConversationControlCommands(f *client.Factory) []*cobra.Command {
	cmds := make([]*cobra.Command, 0, len(controlVerbs))
	for _, v := range controlVerbs {
		cmds = append(cmds, newControlCommand(f, v))
	}
	return cmds
}

func newControlCommand(f *client.Factory, v controlVerb) *cobra.Command {
	return &cobra.Command{
		Use:   v.name + " CREW ID",
		Short: v.short,
		Long: v.about + `

The request goes to the crew's discussion gateway, which checks who is asking: the
conversation's owner may do it, and so may an operator with update on the crew's
conversations (the ClusterRole ` + controlRole + `) who gives a reason with --reason;
the gateway audits it. A conversation with no running discussion is refused. ID can be
a unique prefix.`,
		Example: `  kmctl conversation ` + v.name + ` homelab-pilot 6733c361 -n crew-homelab-pilot
  kmctl conv ` + v.name + ` homelab-pilot 6733c361-aa0c-4bde-85c6-e143af23b251 --reason "ticket 1234: runaway question"`,
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: completeCrewThenConversation(f),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runControl(cmd, f, v, args[0], args[1])
		},
	}
}

func runControl(cmd *cobra.Command, f *client.Factory, v controlVerb, crew, id string) error {
	ns := f.Namespace()
	ctx := cmd.Context()
	rc, stop, err := openGateway(ctx, f, crew, ns)
	if err != nil {
		return err
	}
	defer stop()
	if id, err = resolveConversation(ctx, rc, crew, id); err != nil {
		return explainControl(ctx, f, err, crew, ns, v.name)
	}
	raw, err := rc.Post().Suffix("api", "v1", "discussions", crew, id, v.name).Do(ctx).Raw()
	if err != nil {
		return explainControl(ctx, f, err, crew, ns, v.name)
	}
	var res bus.ConversationControl
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("the gateway's answer is not a control result: %w", err)
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s conversation %s of crew %s: %d thread(s)%s\n",
		v.past, res.ConversationID, crew, len(res.Threads), threadList(res.Threads))
	return err
}

func threadList(threads []string) string {
	if len(threads) == 0 {
		return ""
	}
	return " (" + strings.Join(threads, ", ") + ")"
}

// explainControl explains a failed stop, pause, resume or delete. The gateway's 404, 400
// and 409 mean something particular for a control; everything else is the gateway's usual.
func explainControl(ctx context.Context, f *client.Factory, err error, crew, namespace, action string) error {
	var se *apierrors.StatusError
	if !errors.As(err, &se) {
		return err
	}
	text := statusText(se)
	switch code := int(se.Status().Code); {
	case code == http.StatusNotFound && strings.Contains(text, conversationNotFound):
		return clierr.New(fmt.Sprintf("conversation not found on crew %q, or you may not %s it", crew, action),
			"It does not exist, has expired, or belongs to another caller. An operator needs the ClusterRole "+controlRole+" and --reason \"<why>\"", err)
	case code == http.StatusBadRequest && strings.Contains(text, client.HeaderAccessReason):
		return clierr.New(fmt.Sprintf("the gateway wants a reason to %s another caller's conversation", action),
			`Say why with --reason "<text>" (1 to 200 characters); the gateway audits it`, err)
	case code == http.StatusConflict:
		return explainConflict(err, text, crew, namespace, action)
	}
	return explainGateway(ctx, f, err, crew, namespace)
}

func explainConflict(err error, text, crew, namespace, action string) error {
	if action == "delete" {
		return clierr.New(fmt.Sprintf("conversation on crew %q has a running discussion", crew),
			"Stop it first with: kmctl conversation stop "+crew+" <id> -n "+namespace+", or delete it anyway with --force (it stops the discussion, then erases)", err)
	}
	return clierr.New(fmt.Sprintf("nothing to %s: %s", action, text),
		"Only a conversation with a running discussion can be stopped, paused or resumed. See the crew's conversations with: kmctl conversation list "+crew+" -n "+namespace, err)
}

type deleteOptions struct {
	force, yes, dryRun bool
}

func newConversationDeleteCommand(f *client.Factory) *cobra.Command {
	var o deleteOptions
	cmd := &cobra.Command{
		Use:   "delete CREW ID",
		Short: "Erase a conversation from every store that holds it",
		Long: `Erase a conversation: its history, owner record, discussion messages, coordinator log,
workspace objects, scheduled follow-ups and the crew memory learned in it. The crew's
discussion gateway does the erasing and answers with a report of what it removed per store,
which kmctl prints, and of what it cannot remove (logs and backups, for example). It cannot
be undone, so kmctl asks first unless --yes is given.

A conversation with a running discussion is refused: stop it first, or pass --force to stop
it and erase. If a store could not be cleared the gateway says which; repeat the delete.

The gateway offers no dry run, so kmctl has no --dry-run for this command: it would have to
guess what the gateway removes. To see what a conversation holds, export it with
"kmctl conversation export --report".

The conversation's owner may delete it, and so may an operator with delete on the crew's
conversations (the ClusterRole ` + controlRole + `) who gives a reason with --reason; the
gateway audits it. ID can be a unique prefix.`,
		Example: `  kmctl conversation delete homelab-pilot 6733c361 -n crew-homelab-pilot
  kmctl conv delete homelab-pilot 6733c361 --yes
  kmctl conv delete homelab-pilot 6733c361 --force --reason "erasure request 88"`,
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: completeCrewThenConversation(f),
		RunE: func(cmd *cobra.Command, args []string) error {
			if o.dryRun {
				return usageError{errors.New("the discussion gateway offers no dry run for a delete, so nothing was deleted; see kmctl conversation delete --help")}
			}
			return runConversationDelete(cmd, f, o, args[0], args[1])
		},
	}
	cmd.Flags().BoolVar(&o.force, "force", false, "Delete a conversation whose discussion is running: stop it, then erase")
	addYesFlag(cmd, &o.yes)
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "Not available: the gateway has no dry run")
	_ = cmd.Flags().MarkHidden("dry-run")
	return cmd
}

func runConversationDelete(cmd *cobra.Command, f *client.Factory, o deleteOptions, crew, id string) error {
	ns := f.Namespace()
	ctx := cmd.Context()
	rc, stop, err := openGateway(ctx, f, crew, ns)
	if err != nil {
		return err
	}
	defer stop()
	if id, err = resolveConversation(ctx, rc, crew, id); err != nil {
		return explainControl(ctx, f, err, crew, ns, "delete")
	}
	question := fmt.Sprintf("Erase conversation %s of crew %s from every store? This cannot be undone.", id, crew)
	if o.force {
		question = fmt.Sprintf("Stop any running discussion, then erase conversation %s of crew %s from every store? This cannot be undone.", id, crew)
	}
	if err := confirmChange(cmd, o.yes, question); err != nil {
		return err
	}
	req := rc.Delete().Suffix("api", "v1", "discussions", crew, id)
	if o.force {
		req = req.Param("force", "true")
	}
	var code int
	raw, err := req.Do(ctx).StatusCode(&code).Raw()
	return finishDelete(ctx, cmd.OutOrStdout(), f, raw, err, code, crew, ns)
}

// finishDelete prints the erasure report of a delete, and explains a refusal. A 500 carries
// the report too: it says which stores could not be cleared.
func finishDelete(ctx context.Context, w io.Writer, f *client.Factory, raw []byte, err error, code int, crew, ns string) error {
	if err == nil {
		return printErasure(w, raw)
	}
	if code == http.StatusInternalServerError {
		if report, ok := parseErasure(raw); ok && len(report.Failed) > 0 {
			if perr := writeErasure(w, report); perr != nil {
				return perr
			}
			return clierr.New(fmt.Sprintf("the gateway could not clear every store: %s", strings.Join(report.Failed, ", ")),
				"What was removed is above. Repeat the delete; it removes what is left.", err)
		}
	}
	return explainControl(ctx, f, err, crew, ns, "delete")
}

func parseErasure(raw []byte) (bus.ConversationErasure, bool) {
	var report bus.ConversationErasure
	if json.Unmarshal(raw, &report) != nil || report.ConversationID == "" {
		return report, false
	}
	return report, true
}

func printErasure(w io.Writer, raw []byte) error {
	report, ok := parseErasure(raw)
	if !ok {
		return errors.New("the gateway's answer is not an erasure report")
	}
	return writeErasure(w, report)
}

// writeErasure prints what a delete removed per store and what it cannot remove.
func writeErasure(w io.Writer, r bus.ConversationErasure) error {
	d := r.Deleted
	rows := []struct {
		store string
		n     int64
	}{
		{"history", d.History}, {"owner record", d.Owner}, {"discussion messages", d.DiscussionMessages},
		{"coordinator events", d.CoordinatorEvents}, {"workspace objects", d.WorkspaceObjects},
		{"scheduled follow-ups", d.Scheduled}, {"memory facts", d.MemoryFacts},
	}
	var b strings.Builder
	fmt.Fprintf(&b, "erased conversation %s (%d thread(s))\n", r.ConversationID, len(r.Threads))
	for _, row := range rows {
		fmt.Fprintf(&b, "  %-22s %d\n", row.store, row.n)
	}
	for _, k := range r.Kept {
		fmt.Fprintf(&b, "not removed: %s\n", k)
	}
	for _, s := range r.Failed {
		fmt.Fprintf(&b, "failed: %s\n", s)
	}
	_, err := io.WriteString(w, b.String())
	return err
}
