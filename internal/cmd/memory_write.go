package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/clierr"
	"github.com/kubemoot/kmctl/internal/memorywrite"
	"github.com/kubemoot/kubemoot/operator/pkg/crewmemory"
	"github.com/kubemoot/kubemoot/operator/pkg/crewmemory/natskv"
	"github.com/kubemoot/kubemoot/operator/pkg/crewscope"
	"github.com/kubemoot/kubemoot/operator/pkg/secretscreen"
	"github.com/spf13/cobra"
)

// memoryConn is an open connection to the crew-memory bucket: the write side and a
// read-only reader of the same bucket.
type memoryConn struct {
	store  memorywrite.Store
	reader crewmemory.EntryReader
}

// memoryStoreOpener opens the bucket for writing and returns what closes it. Tests
// replace it with a fake.
type memoryStoreOpener func(ctx context.Context, f *client.Factory, o memoryOptions) (memoryConn, func(), error)

// memoryWriteOptions are the flags the memory commands that change facts share.
type memoryWriteOptions struct {
	memoryOptions
	fromFile        string
	dryRun, yes     bool
	allowUnscreened bool
}

func addMemoryWriteCommands(parent *cobra.Command, f *client.Factory, open memoryStoreOpener) {
	parent.AddCommand(newMemoryPutCommand(f, open, memoryAdd), newMemoryPutCommand(f, open, memoryEdit),
		newMemoryDeleteCommand(f, open), newMemoryClearCommand(f, open))
}

// openMemoryStore connects to NATS, through a port-forward unless --nats-url is
// set, and binds the crew-memory bucket for writing and reading.
func openMemoryStore(ctx context.Context, f *client.Factory, o memoryOptions) (memoryConn, func(), error) {
	nc, stop, err := connectNATS(ctx, f, o)
	if err != nil {
		return memoryConn{}, nil, err
	}
	store, err := memorywrite.OpenNATS(ctx, nc)
	if err != nil {
		stop()
		return memoryConn{}, nil, bucketError(err)
	}
	reader, err := natskv.Open(nc)
	if err != nil {
		stop()
		return memoryConn{}, nil, bucketError(err)
	}
	return memoryConn{store: store, reader: reader}, stop, nil
}

// memoryPutMode is whether a fact is created or replaced.
type memoryPutMode int

const (
	memoryAdd memoryPutMode = iota
	memoryEdit
)

// putSpec is how one of add and edit differs from the other.
type putSpec struct {
	use, short, long, example, past string
	put                             func(context.Context, memorywrite.Store, memorywrite.Fact, time.Time, bool) (memorywrite.Result, error)
}

func (m memoryPutMode) spec() putSpec {
	if m == memoryAdd {
		return putSpec{"add CREW TOPIC KEY [VALUE]", "Add a fact to a crew's memory",
			`Add a fact the crew will remember under TOPIC and KEY. It fails when that fact
already exists; change an existing one with "kmctl memory edit". The value is the
VALUE argument, or the content of --from-file (- reads stdin). Topic and key keep
letters, digits, underscore, equals and hyphen; any other character becomes an
underscore, and the command says so.`,
			`  kmctl memory add homelab-pilot hardware gpu0 "RTX 5090, 32 GiB" -n crew-homelab-pilot
  kmctl memory add homelab-pilot notes rack --from-file rack.txt -n crew-homelab-pilot
  kmctl memory add homelab-pilot hardware gpu1 "RTX 4090" --dry-run -n crew-homelab-pilot`, "added", memorywrite.Add}
	}
	return putSpec{"edit CREW TOPIC KEY [VALUE]", "Change a fact in a crew's memory",
		`Replace the value of a fact the crew already holds. The time it was first learned
is kept. It fails when the fact does not exist; add one with "kmctl memory add". The
value is the VALUE argument, or the content of --from-file (- reads stdin).`,
		`  kmctl memory edit homelab-pilot hardware gpu0 "RTX 5090, 32 GiB, water cooled" -n crew-homelab-pilot
  echo "new text" | kmctl memory edit homelab-pilot notes rack --from-file - -n crew-homelab-pilot`, "changed", memorywrite.Edit}
}

func newMemoryPutCommand(f *client.Factory, open memoryStoreOpener, mode memoryPutMode) *cobra.Command {
	var o memoryWriteOptions
	sp := mode.spec()
	cmd := &cobra.Command{
		Use: sp.use, Short: sp.short, Example: sp.example,
		Annotations: changesState(),
		Long: sp.long + `

The facts are text agents read and trust, so a value that holds a credential (a bearer
token, an API key, a password in a URL, a private key header) is refused; pass
--allow-unscreened only when you accept that. Nothing is written with --dry-run.
Facts live in NATS: kmctl opens a port-forward to the NATS Service with your
kubeconfig credentials; --nats-url is refused because the command changes state.`,
		Args:              cobra.RangeArgs(3, 4),
		ValidArgsFunction: completeCrewArg(f),
		RunE: func(cmd *cobra.Command, args []string) error {
			value, err := factValue(cmd.InOrStdin(), args, o.fromFile)
			if err != nil {
				return err
			}
			scope, err := memoryScope(f, args[0])
			if err != nil {
				return err
			}
			fact := memorywrite.Fact{Scope: scope, Topic: args[1], Key: args[2], Value: value}
			if err := screenFact(fact, o.allowUnscreened); err != nil {
				return err
			}
			return withMemory(cmd, f, open, o.memoryOptions, func(conn memoryConn) error {
				res, err := sp.put(cmd.Context(), conn.store, fact, time.Now(), o.dryRun)
				if err != nil {
					return explainMemoryWrite(err, scope, fact)
				}
				return reportFact(cmd.OutOrStdout(), res, sp.past, fact)
			})
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&o.fromFile, "from-file", "", "Read the value from this file (- for stdin) instead of the VALUE argument")
	fl.BoolVar(&o.allowUnscreened, "allow-unscreened", false, "Store a value that holds a credential too (default: refuse it)")
	addDryRunFlag(cmd, &o.dryRun)
	o.addNATSFlags(fl, refusedNATSURL)
	return cmd
}

// withMemory opens the bucket, runs fn, and closes it.
func withMemory(cmd *cobra.Command, f *client.Factory, open memoryStoreOpener, o memoryOptions, fn func(memoryConn) error) error {
	conn, closeFn, err := open(cmd.Context(), f, o)
	if err != nil {
		return err
	}
	defer closeFn()
	return fn(conn)
}

// memoryScope names the crew in the namespace in effect.
func memoryScope(f *client.Factory, crew string) (crewscope.Scope, error) {
	scope, err := crewscope.New(f.Namespace(), crew)
	if err != nil {
		return crewscope.Scope{}, usageError{err}
	}
	return scope, nil
}

// factValue is the value to store: the fourth argument, or the content of the file.
func factValue(stdin io.Reader, args []string, fromFile string) (string, error) {
	if (len(args) == 4) == (fromFile != "") {
		return "", usageError{errors.New("give the value as the VALUE argument or with --from-file, not both and not neither")}
	}
	value := ""
	if len(args) == 4 {
		value = args[3]
	} else {
		raw, err := readValueFile(stdin, fromFile)
		if err != nil {
			return "", err
		}
		value = strings.TrimRight(raw, "\r\n")
	}
	if strings.TrimSpace(value) == "" {
		return "", usageError{errors.New("the value is empty")}
	}
	return value, nil
}

func readValueFile(stdin io.Reader, path string) (string, error) {
	var raw []byte
	var err error
	if path == "-" {
		raw, err = io.ReadAll(stdin)
	} else {
		raw, err = os.ReadFile(path)
	}
	return string(raw), err
}

// screenFact refuses a fact whose text holds a credential, naming the shape it
// matched and never the text.
func screenFact(f memorywrite.Fact, allow bool) error {
	if allow {
		return nil
	}
	if shape, found := secretscreen.Find(f.Topic + " " + f.Key + " " + f.Value); found {
		return clierr.New(fmt.Sprintf("the fact looks like it holds a credential (%s); nothing was stored", shape),
			"Crew memory is text agents read and repeat. Remove the credential, or pass --allow-unscreened if you accept that.", nil)
	}
	return nil
}

// explainMemoryWrite turns a store error into what to do about it.
func explainMemoryWrite(err error, scope crewscope.Scope, f memorywrite.Fact) error {
	where := fmt.Sprintf("topic %q, key %q of crew %q in namespace %q", f.Topic, f.Key, scope.Crew, scope.Namespace)
	switch {
	case errors.Is(err, memorywrite.ErrExists):
		return clierr.New("the fact already exists: "+where, `Change it with "kmctl memory edit".`, err)
	case errors.Is(err, memorywrite.ErrNotFound):
		return clierr.New("no such fact: "+where, `Add it with "kmctl memory add", or look at what the crew holds with "kmctl memory export".`, err)
	case errors.Is(err, memorywrite.ErrChanged):
		return clierr.New("the fact changed while you were editing it: "+where, "Run the command again.", err)
	}
	return clierr.New("could not change the crew's memory", "Check that NATS is reachable and the credentials in use may write the bucket.", err)
}

// reportFact says what happened to one fact, and whether the names were changed to
// keep them safe in a key.
func reportFact(w io.Writer, res memorywrite.Result, past string, f memorywrite.Fact) error {
	verb := past
	if res.DryRun {
		verb = "would be " + past
	}
	if _, err := fmt.Fprintf(w, "fact %s %s\n", res.Key, verb); err != nil {
		return err
	}
	if memorywrite.Sanitize(f.Topic) != f.Topic || memorywrite.Sanitize(f.Key) != f.Key {
		_, err := fmt.Fprintf(w, "note: topic and key keep only letters, digits, _ = and -; the stored key is %s\n", res.Key)
		return err
	}
	return nil
}

func newMemoryDeleteCommand(f *client.Factory, open memoryStoreOpener) *cobra.Command {
	var o memoryWriteOptions
	cmd := &cobra.Command{
		Use:         "delete CREW TOPIC KEY",
		Annotations: changesState(),
		Short:       "Delete one fact from a crew's memory",
		Long: `Delete the fact a crew holds under TOPIC and KEY. It fails when there is no such
fact. Facts live in NATS: kmctl opens a port-forward to the NATS Service with your
kubeconfig credentials; --nats-url is refused because the command changes state. It asks first unless --yes is
given, and removes nothing with --dry-run.`,
		Example: `  kmctl memory delete homelab-pilot hardware gpu0 -n crew-homelab-pilot
  kmctl memory delete homelab-pilot hardware gpu0 --dry-run -n crew-homelab-pilot
  kmctl memory delete homelab-pilot hardware gpu0 --yes -n crew-homelab-pilot`,
		Args:              cobra.ExactArgs(3),
		ValidArgsFunction: completeCrewArg(f),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, err := memoryScope(f, args[0])
			if err != nil {
				return err
			}
			fact := memorywrite.Fact{Scope: scope, Topic: args[1], Key: args[2]}
			return withMemory(cmd, f, open, o.memoryOptions, func(conn memoryConn) error {
				if !o.dryRun {
					if _, err := conn.store.Get(cmd.Context(), fact.StoreKey()); err != nil {
						return explainMemoryWrite(err, scope, fact)
					}
					if err := confirmChange(cmd, o.yes, "Delete fact "+fact.StoreKey()+"?"); err != nil {
						return err
					}
				}
				res, err := memorywrite.Delete(cmd.Context(), conn.store, fact, o.dryRun)
				if err != nil {
					return explainMemoryWrite(err, scope, fact)
				}
				return reportFact(cmd.OutOrStdout(), res, "deleted", fact)
			})
		},
	}
	addDryRunFlag(cmd, &o.dryRun)
	addYesFlag(cmd, &o.yes)
	o.addNATSFlags(cmd.Flags(), refusedNATSURL)
	return cmd
}

func newMemoryClearCommand(f *client.Factory, open memoryStoreOpener) *cobra.Command {
	var o memoryWriteOptions
	cmd := &cobra.Command{
		Use:         "clear CREW",
		Annotations: changesState(),
		Short:       "Delete every fact in a crew's memory",
		Long: `Delete every fact one crew holds in the namespace in effect. A crew of the same
name in another namespace, and a crew whose name only starts with this one, keep their
facts. This cannot be undone: export the memory first with "kmctl memory export" to
keep a copy. It asks first unless --yes is given, and removes nothing with --dry-run.
Facts live in NATS: kmctl opens a port-forward to the NATS Service with your
kubeconfig credentials; --nats-url is refused because the command changes state.`,
		Example: `  kmctl memory export homelab-pilot -n crew-homelab-pilot && kmctl memory clear homelab-pilot -n crew-homelab-pilot
  kmctl memory clear homelab-pilot --dry-run -n crew-homelab-pilot`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeCrewArg(f),
		RunE: func(cmd *cobra.Command, args []string) error {
			scope, err := memoryScope(f, args[0])
			if err != nil {
				return err
			}
			return withMemory(cmd, f, open, o.memoryOptions, func(conn memoryConn) error {
				return runMemoryClear(cmd, conn, scope, o)
			})
		},
	}
	addDryRunFlag(cmd, &o.dryRun)
	addYesFlag(cmd, &o.yes)
	o.addNATSFlags(cmd.Flags(), refusedNATSURL)
	return cmd
}

func runMemoryClear(cmd *cobra.Command, conn memoryConn, scope crewscope.Scope, o memoryWriteOptions) error {
	facts, err := memorywrite.Facts(conn.reader, scope)
	if err != nil {
		return explainMemoryWrite(err, scope, memorywrite.Fact{})
	}
	w := cmd.OutOrStdout()
	if len(facts) == 0 {
		_, err := fmt.Fprintf(w, "crew %s in namespace %s holds no facts\n", scope.Crew, scope.Namespace)
		return err
	}
	if !o.dryRun {
		q := fmt.Sprintf("Delete all %d fact(s) of crew %s in namespace %s? This cannot be undone.", len(facts), scope.Crew, scope.Namespace)
		if err := confirmChange(cmd, o.yes, q); err != nil {
			return err
		}
	}
	res, err := memorywrite.Clear(cmd.Context(), conn.store, scope, len(facts), o.dryRun)
	if err != nil {
		return explainMemoryWrite(err, scope, memorywrite.Fact{})
	}
	verb := "cleared"
	if res.DryRun {
		verb = "would be cleared"
	}
	_, err = fmt.Fprintf(w, "%d fact(s) of crew %s in namespace %s %s\n", res.Count, scope.Crew, scope.Namespace, verb)
	return err
}
