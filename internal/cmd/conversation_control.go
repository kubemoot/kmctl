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
	"github.com/kubemoot/kmctl/internal/discussion"
	"github.com/kubemoot/kubemoot/operator/pkg/bus"
	"github.com/kubemoot/kubemoot/operator/pkg/crewscope"
	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/rest"
)

// controlRole is the ClusterRole an operator needs to act on another caller's
// conversations; the owner of a conversation needs no role.
var controlRole = controlAction("stop").role

// controlVerb is one of stop, pause and resume: the gateway route and how the result reads.
type controlVerb struct {
	name, past, short, about string
}

var controlVerbs = []controlVerb{
	{"stop", "stopped", "Stop a conversation's running discussion", "Stop the turn that is running in a conversation, or remove a question that still waits in the crew's queue. A stopped turn closes with a text that says you stopped it; it has no answer."},
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
		Use:         v.name + " CREW ID",
		Annotations: changesState(),
		Short:       v.short,
		Long: v.about + `

The request goes to the crew's discussion gateway, which checks who is asking: the
conversation's owner may do it, and so may an operator with update on the crew's
conversations (the ClusterRole ` + controlRole + `) who gives a reason with --reason;
the gateway audits it. ID can be a unique prefix, or the full ID of a question still
waiting in the queue.

stop says what happened and its exit code follows: stopped, cancelled (a question that
was still queued, removed before the crew saw it) and nothing_to_stop (every turn had
already finished) succeed; unconfirmed (the crew did not confirm in time, the work may
still run) and a question the crew has just taken (stop again in a moment) fail.`,
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
	act := controlAction(v.name)
	return withResolvedConversation(cmd, f, crew, id, act, func(ctx context.Context, rc rest.Interface, id string) error {
		var code int
		raw, err := rc.Post().Suffix("api", "v1", "discussions", crew, id, v.name).Do(ctx).StatusCode(&code).Raw()
		if err != nil {
			return explainControl(ctx, f, err, crew, f.Namespace(), act)
		}
		var res bus.ConversationControl
		if err := json.Unmarshal(raw, &res); err != nil || res.ConversationID == "" {
			return errors.New("the gateway's answer is not a control result; the request may still have been carried out")
		}
		if v.name == "stop" && res.Outcome != "" {
			return reportStop(cmd.OutOrStdout(), res, crew, code)
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s conversation %s of crew %s: %d thread(s)%s\n",
			v.past, clean(res.ConversationID), crew, len(res.Threads), threadList(res.Threads))
		return err
	})
}

// stopHeadlines is the first line of each outcome of a stop.
var stopHeadlines = map[bus.ConversationControlOutcome]string{
	bus.ConversationControlOutcomeStopped:       "stopped conversation %s of crew %s",
	bus.ConversationControlOutcomeCancelled:     "cancelled the queued question of conversation %s of crew %s",
	bus.ConversationControlOutcomeNothingToStop: "nothing to stop in conversation %s of crew %s",
	bus.ConversationControlOutcomeUnconfirmed:   "asked conversation %s of crew %s to stop; not confirmed",
}

// reportStop prints what a stop did, in the gateway's words, and fails for an outcome that is not
// a stop: unconfirmed means the work may still be running.
func reportStop(w io.Writer, res bus.ConversationControl, crew string, code int) error {
	headline, ok := stopHeadlines[res.Outcome]
	if !ok {
		headline = "stop of conversation %s of crew %s: " + clean(string(res.Outcome))
	}
	text := fmt.Sprintf(headline, clean(res.ConversationID), crew) + threadList(res.Threads) + "\n"
	if res.Message != "" {
		text += clean(res.Message) + "\n"
	}
	if _, err := io.WriteString(w, text); err != nil {
		return err
	}
	if res.Outcome == bus.ConversationControlOutcomeUnconfirmed || code == http.StatusAccepted {
		return clierr.New("the crew did not confirm the stop; the work may still be running",
			"See where the turn stands with: kmctl conversation get "+crew+" "+clean(res.ConversationID)+", and stop again if it still runs", nil)
	}
	return nil
}

// withResolvedConversation validates the crew, opens its gateway as the caller, turns the
// conversation ID or prefix into the full one, and runs fn with it. A failure to look the
// conversation up is explained for the action.
func withResolvedConversation(cmd *cobra.Command, f *client.Factory, crew, id string, act gatewayAction,
	fn func(ctx context.Context, rc rest.Interface, id string) error) error {
	ns := f.Namespace()
	if _, err := crewscope.New(ns, crew); err != nil {
		return usageError{err}
	}
	ctx := cmd.Context()
	rc, stop, err := openGateway(ctx, f, crew, ns)
	if err != nil {
		return err
	}
	defer stop()
	if id, err = resolveConversation(ctx, rc, crew, id); err != nil {
		return explainControl(ctx, f, err, crew, ns, act)
	}
	return fn(ctx, rc, id)
}

func threadList(threads []string) string {
	if len(threads) == 0 {
		return ""
	}
	return " (" + strings.Join(cleanAll(threads), ", ") + ")"
}

// explainControl explains a failed stop, pause, resume or delete: the gateway's usual
// refusals, named for the action, and the 409 a control alone can get.
func explainControl(ctx context.Context, f *client.Factory, err error, crew, namespace string, act gatewayAction) error {
	var se *apierrors.StatusError
	if errors.As(err, &se) && int(se.Status().Code) == http.StatusConflict {
		return explainConflict(err, clean(statusText(se)), crew, namespace, act)
	}
	return explainGatewayFor(ctx, f, err, crew, namespace, act)
}

func explainConflict(err error, text, crew, namespace string, act gatewayAction) error {
	switch {
	case act.verb == "delete" && act.force:
		return clierr.New(fmt.Sprintf("the gateway refused to delete a conversation of crew %q even with --force: %s", crew, text),
			"The gateway may be older than this kmctl; see its logs with: kubectl logs -n "+namespace+" svc/"+discussion.ServiceName(crew), err)
	case act.verb == "delete":
		return clierr.New(fmt.Sprintf("conversation on crew %q has a running discussion", crew),
			"Stop it first with: kmctl conversation stop "+crew+" <id> -n "+namespace+", or delete it anyway with --force (it stops the discussion, then erases)", err)
	}
	if act.verb == "stop" {
		return clierr.New("the crew has just taken the question and there is no turn to stop yet: "+text,
			"Stop again in a moment; the turn begins as soon as the crew starts on it", err)
	}
	return clierr.New(fmt.Sprintf("nothing to %s: %s", act.verb, text),
		"Only a conversation with a running discussion can be stopped, paused or resumed. See the crew's conversations with: kmctl conversation list "+crew+" -n "+namespace, err)
}

type deleteOptions struct {
	force, yes, dryRun bool
}

func newConversationDeleteCommand(f *client.Factory) *cobra.Command {
	var o deleteOptions
	cmd := &cobra.Command{
		Use:         "delete CREW ID",
		Annotations: changesState(),
		Short:       "Erase a conversation from every store that holds it",
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
	act := controlAction("delete")
	act.force = o.force
	return withResolvedConversation(cmd, f, crew, id, act, func(ctx context.Context, rc rest.Interface, id string) error {
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
		return finishDelete(ctx, cmd.OutOrStdout(), f, deleteReply{raw: raw, err: err, code: code}, erasureTarget{crew: crew, ns: f.Namespace()}, act)
	})
}

// deleteReply is the gateway's answer to a delete request.
type deleteReply struct {
	raw  []byte
	err  error
	code int
}

// erasureTarget names the conversation's crew and namespace for an explanation.
type erasureTarget struct {
	crew string
	ns   string
}

// finishDelete prints the erasure report of a delete, and explains a refusal. A 500 carries
// the report too: it says which stores could not be cleared.
func finishDelete(ctx context.Context, w io.Writer, f *client.Factory, r deleteReply, t erasureTarget, act gatewayAction) error {
	if r.err == nil {
		return printErasure(w, r.raw)
	}
	if r.code == http.StatusInternalServerError {
		if report, ok := parseErasure(r.raw); ok && len(report.Failed) > 0 {
			if perr := writeErasure(w, report); perr != nil {
				return perr
			}
			return clierr.New(fmt.Sprintf("the gateway could not clear every store: %s", strings.Join(cleanAll(report.Failed), ", ")),
				"What was removed is above. Repeat the delete; it removes what is left.", r.err)
		}
	}
	return explainControl(ctx, f, r.err, t.crew, t.ns, act)
}

func cleanAll(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = clean(v)
	}
	return out
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
		return errors.New("the gateway's answer is not an erasure report; the conversation may still have been erased")
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
	fmt.Fprintf(&b, "erased conversation %s (%d thread(s))\n", clean(r.ConversationID), len(r.Threads))
	for _, row := range rows {
		fmt.Fprintf(&b, "  %-22s %d\n", row.store, row.n)
	}
	for _, k := range r.Kept {
		fmt.Fprintf(&b, "not removed: %s\n", clean(k))
	}
	for _, s := range r.Failed {
		fmt.Fprintf(&b, "failed: %s\n", clean(s))
	}
	_, err := io.WriteString(w, b.String())
	return err
}
