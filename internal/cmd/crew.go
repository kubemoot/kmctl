package cmd

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/clierr"
	"github.com/kubemoot/kmctl/internal/crewstate"
	"github.com/kubemoot/kmctl/internal/resource"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
)

// defaultCrewWait is the safety net on `crew wait`; the wait ends on the crew's state.
const defaultCrewWait = 10 * time.Minute

const (
	waitForAnswer     = "answer"
	waitForReconciled = "reconciled"
)

// newCrewCommand groups the crew subcommands.
func newCrewCommand(f *client.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "crew",
		Short:   "List, inspect, and wait for crews",
		Aliases: []string{"crews"},
		Args:    cobra.NoArgs,
	}
	addResourceCommands(cmd, f, resource.Crew)
	cmd.AddCommand(newCrewWaitCommand(f))
	return cmd
}

func newCrewWaitCommand(f *client.Factory) *cobra.Command {
	var forWhat string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "wait NAME",
		Short: "Wait until a crew can answer",
		Long: `Wait until a crew can answer a question: it is reconciled as declared, its phase
is Ready, and its CapacityAvailable condition is True. While no model in the
cluster catalog can run the crew's agents, kmctl says "waiting for an accelerator"
with the scheduler's reason, and keeps waiting: the crew answers once a model
returns, and nothing needs to be applied again.

--for reconciled waits only until the crew is reconciled as declared (status.ready
and the latest generation), whether or not a model is available now. Use it to
check that a change reached the crew, not that the crew can answer.

A Kubemoot that predates the CapacityAvailable condition does not report it; then
the crew can answer when it is reconciled and its phase is Ready.

--timeout is a safety net, not the pace of the wait; the error names what the crew
was still waiting for.`,
		Example: "  kmctl crew wait demo -n crew-demo\n  kmctl crew wait demo -n crew-demo --for reconciled --timeout 2m",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			q, err := waitQuestion(forWhat)
			if err != nil {
				return err
			}
			dc, err := f.Dynamic()
			if err != nil {
				return fmt.Errorf("build client: %w", err)
			}
			return runCrewWait(cmd.Context(), dc, f.Namespace(), args[0], q, timeout, cmd)
		},
		ValidArgsFunction: completeNames(f, resource.Crew),
	}
	cmd.Flags().StringVar(&forWhat, "for", waitForAnswer, "What to wait for: answer (the crew can answer) or reconciled (the crew is reconciled as declared)")
	cmd.Flags().DurationVar(&timeout, "timeout", defaultCrewWait, "Stop waiting after this long")
	return cmd
}

// waitQuestion maps --for to what the wait asks of the crew.
func waitQuestion(forWhat string) (crewstate.Question, error) {
	switch forWhat {
	case waitForAnswer:
		return crewstate.ForAnswer, nil
	case waitForReconciled:
		return crewstate.ForReconciled, nil
	}
	return 0, fmt.Errorf("--for %q is not one of %s, %s", forWhat, waitForAnswer, waitForReconciled)
}

// runCrewWait waits for the crew, reporting each change of what it waits for on
// stderr, and says on stdout when the wait is over.
func runCrewWait(ctx context.Context, dc dynamic.Interface, ns, name string, q crewstate.Question, timeout time.Duration, cmd *cobra.Command) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	get := func(ctx context.Context) (*unstructured.Unstructured, error) {
		return resource.Get(ctx, dc, resource.Crew, ns, name)
	}
	opts := crewstate.Options{Progress: func(waiting string) { writeLine(cmd.ErrOrStderr(), waiting) }}
	if _, err := crewstate.Wait(ctx, get, q, opts); err != nil {
		return explainCrewWait(err, name, ns, timeout)
	}
	if q == crewstate.ForReconciled {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "crew/%s is reconciled\n", name)
		return err
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "crew/%s can answer\n", name)
	return err
}

func explainCrewWait(err error, name, ns string, timeout time.Duration) error {
	if !crewstate.IsNotReady(err) {
		return err
	}
	return clierr.New(fmt.Sprintf("crew %q in namespace %q was not ready after %s", name, ns, timeout), "See what it reports with: kmctl crew get "+name+" -n "+ns, err)
}

// noteIfCannotAnswer tells the person on w when the crew is waiting for an
// accelerator before kmctl asks it. It does not stop the ask: the crew answers when
// a model returns, and the stream shows the wait. It says nothing when the crew can
// answer, cannot be read, or runs on a Kubemoot that does not report capacity.
func noteIfCannotAnswer(ctx context.Context, f *client.Factory, w io.Writer, crew, ns string) {
	dc, err := f.Dynamic()
	if err != nil {
		return
	}
	obj, err := resource.Get(ctx, dc, resource.Crew, ns, crew)
	if err != nil {
		return
	}
	noteWith(w, obj)
}

// noteWith writes the note for a crew that reports it has no capacity.
func noteWith(w io.Writer, crew *unstructured.Unstructured) {
	st := crewstate.Read(crew.Object)
	if st.Capacity == nil {
		return
	}
	if ok, why := st.CanAnswer(); !ok {
		writeLine(w, why+" (asking anyway; the crew answers when a model is available)")
	}
}

func writeLine(w io.Writer, s string) {
	_, _ = fmt.Fprintln(w, s)
}
