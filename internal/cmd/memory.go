package cmd

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/clierr"
	"github.com/kubemoot/kmctl/internal/natsforward"
	"github.com/kubemoot/kubemoot/operator/pkg/crewmemory"
	"github.com/kubemoot/kubemoot/operator/pkg/crewmemory/natskv"
	"github.com/kubemoot/kubemoot/operator/pkg/crewmemory/ompi"
	"github.com/kubemoot/kubemoot/operator/pkg/crewscope"
	"github.com/nats-io/nats.go"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	defaultNATSNamespace = "nats"
	defaultNATSService   = "nats"
	natsConnectTimeout   = 15 * time.Second
)

// memoryOptions are the flags of kmctl memory export.
type memoryOptions struct {
	namespace       string
	crew            string
	out             string
	origin          string
	markdown        bool
	allowUnscreened bool
	natsURL         string
	creds           string
	natsNamespace   string
	natsService     string
}

// memoryOpener gives the read-only reader of the crew-memory bucket and a
// function that closes whatever it opened. Tests replace it with a fake.
type memoryOpener func(ctx context.Context, f *client.Factory, o memoryOptions) (crewmemory.EntryReader, func(), error)

// newMemoryCommand groups the commands on a crew's long-term memory.
func newMemoryCommand(f *client.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Work with a crew's long-term memory",
		Long: `Commands on the facts a crew learned and keeps in its memory: export them, or
add, edit, delete and clear them. Memory lives in a NATS key-value bucket, so every
command opens a port-forward to the NATS Service with your kubeconfig credentials (it
needs create on pods/portforward in the NATS namespace), or connects to the server
you name with --nats-url.`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(newMemoryExportCommand(f, openMemory))
	addMemoryWriteCommands(cmd, f, openMemoryStore)
	return cmd
}

func newMemoryExportCommand(f *client.Factory, open memoryOpener) *cobra.Command {
	var o memoryOptions
	cmd := &cobra.Command{
		Use:   "export CREW",
		Short: "Write a crew's memory in the OMPI interchange format",
		Long: fmt.Sprintf(`Write a crew's long-term memory in the interchange format of the Open Memory
Protocol Initiative (OMPI) exchange-and-runtime working spec v%s: a manifest.json
and one JSON memory object per fact under memories/. With --markdown it also writes
each fact as a Markdown file under markdown/, the spec's runtime form. Every document
is checked against the schema before anything is written, and the directory is
written atomically; it must not exist or be empty.

The export only reads. Crew memory lives in a NATS key-value bucket and NATS is not
behind the Kubernetes API server's service proxy, so kmctl opens a port-forward to a
ready pod of the NATS Service with your kubeconfig credentials (it needs create on
pods/portforward in the NATS namespace), reads the bucket, and closes the forward.
Pass --nats-url to connect to a server you already reach instead. An unreachable
server is an error, never an empty export.

A fact whose value, topic or key holds a credential (a bearer or basic token, an API
key, a token or password assignment, a JSON Web Token, a cloud access key, a private key
header, a password in a URL, a long hexadecimal string) is left out and never printed as
text: the spec says credentials must not be exported as memory. A credential in the value
is listed with the shape it matched. A credential in the topic or key is found with the
strict redaction, and the fact is listed by an opaque id (<namespace>.<crew>.#<id>, a
truncated one-way hash of the store key) so the key does not print it. Ordinary words
such as "basic authentication", content digests and bare commit hashes are not flagged
in a value, but the screen matches shapes, so it can leave out a fact that only looks
like a credential. --allow-unscreened exports every fact as stored; use it only when you
accept that the files may hold credentials.

The facts are text the agents wrote, so treat the files as data.`, ompi.SpecVersion),
		Example: `  kmctl memory export homelab-pilot -n crew-homelab-pilot
  kmctl memory export homelab-pilot -n crew-homelab-pilot --dir ./pilot-memory --markdown
  kmctl memory export demo --nats-url nats://localhost:4222`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeCrewArg(f),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.crew = args[0]
			o.namespace = f.Namespace()
			if err := checkPath(cmd, outputDirFlag, o.out); err != nil {
				return err
			}
			if o.out == "" {
				o.out = "." + string(filepath.Separator) + o.crew + "-memory"
			}
			return runMemoryExport(cmd.Context(), cmd.OutOrStdout(), f, o, open, time.Now())
		},
	}
	fl := cmd.Flags()
	outputDirFlag.add(fl, &o.out, "", "Directory to write; it must not exist or be empty (default ./<crew>-memory)")
	fl.BoolVar(&o.markdown, "markdown", false, "Also write the runtime projection as Markdown files")
	fl.BoolVar(&o.allowUnscreened, "allow-unscreened", false,
		"Export facts that hold a credential too (default: leave them out and list them)")
	fl.StringVar(&o.origin, "origin", "", "Origin id for the manifest (default: derived from the namespace and crew)")
	o.addNATSFlags(fl, "Connect to this NATS URL instead of port-forwarding to the NATS Service")
	return cmd
}

// addNATSFlags registers the flags that say where NATS is.
func (o *memoryOptions) addNATSFlags(fl *pflag.FlagSet, urlUsage string) {
	fl.StringVar(&o.natsNamespace, "nats-namespace", defaultNATSNamespace, "Namespace of the NATS Service")
	fl.StringVar(&o.natsService, "nats-service", defaultNATSService, "Name of the NATS Service")
	fl.StringVar(&o.natsURL, "nats-url", "", urlUsage)
	fl.StringVar(&o.creds, "creds", "", "NATS credentials file, for a server that needs them")
}

// runMemoryExport reads the crew's facts and writes the export. The mapping, the
// schema check and the atomic write are the Kubemoot operator's own
// (ompi.ExportCrew); this function only connects and reports.
func runMemoryExport(ctx context.Context, stdout io.Writer, f *client.Factory, o memoryOptions, open memoryOpener, now time.Time) error {
	scope, err := crewscope.New(o.namespace, o.crew)
	if err != nil {
		return usageError{err}
	}
	reader, closeFn, err := open(ctx, f, o)
	if err != nil {
		return err
	}
	defer closeFn()
	exp, err := ompi.ExportCrew(reader, scope, o.out, o.markdown, ompi.Options{ExportedAt: now, Origin: o.origin, AllowUnscreened: o.allowUnscreened})
	if err != nil {
		return clierr.New(fmt.Sprintf("could not export the memory of crew %q in namespace %q", o.crew, o.namespace),
			"Nothing was written. Check that NATS is reachable and the directory is new or empty.", err)
	}
	return reportExport(stdout, exp, scope, o.out)
}

func reportExport(stdout io.Writer, exp ompi.Export, scope crewscope.Scope, out string) error {
	for _, s := range exp.Skipped {
		if _, err := fmt.Fprintf(stdout, "left out %q: %s\n", s.Key, s.Reason); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(stdout, "wrote %d memory object(s) of %s/%s to %s\n", len(exp.Memories), scope.Namespace, scope.Crew, out)
	return err
}

// openMemory connects to NATS, through a port-forward unless --nats-url is set,
// and binds the crew-memory bucket for reading.
func openMemory(ctx context.Context, f *client.Factory, o memoryOptions) (crewmemory.EntryReader, func(), error) {
	nc, stop, err := connectNATS(ctx, f, o)
	if err != nil {
		return nil, nil, err
	}
	reader, err := natskv.Open(nc)
	if err != nil {
		stop()
		return nil, nil, bucketError(err)
	}
	return reader, stop, nil
}

// connectNATS connects to NATS, through a port-forward unless --nats-url is set. The
// function it returns closes the connection and the forward.
func connectNATS(ctx context.Context, f *client.Factory, o memoryOptions) (*nats.Conn, func(), error) {
	url, stopForward, err := natsEndpoint(ctx, f, o)
	if err != nil {
		return nil, nil, err
	}
	opts := []nats.Option{nats.Timeout(natsConnectTimeout), nats.MaxReconnects(0)}
	if o.creds != "" {
		opts = append(opts, nats.UserCredentials(o.creds))
	}
	nc, err := nats.Connect(url, opts...)
	if err != nil {
		stopForward()
		return nil, nil, clierr.New(fmt.Sprintf("could not connect to NATS at %s", url),
			"Check the NATS Service with --nats-namespace and --nats-service, or pass --nats-url (and --creds if the server needs them).", err)
	}
	return nc, func() { nc.Close(); stopForward() }, nil
}

// bucketError explains a crew-memory bucket that could not be opened.
func bucketError(err error) error {
	return clierr.New("could not open the crew-memory bucket",
		"The bucket kubemoot_crew_memory is created by the Kubemoot operator; check that Kubemoot is installed and its streams job has run.", err)
}

func natsEndpoint(ctx context.Context, f *client.Factory, o memoryOptions) (string, func(), error) {
	if o.natsURL != "" {
		return o.natsURL, func() {}, nil
	}
	cfg, err := f.RESTConfig()
	if err != nil {
		return "", nil, err
	}
	cs, err := f.Clientset()
	if err != nil {
		return "", nil, err
	}
	url, stop, err := natsforward.Open(ctx, cfg, cs, o.natsNamespace, o.natsService)
	if err != nil {
		return "", nil, clierr.New(fmt.Sprintf("could not reach NATS through service %s/%s", o.natsNamespace, o.natsService),
			"Port-forwarding needs get on services and list on pods, and create on pods/portforward, in that namespace. Or pass --nats-url.", err)
	}
	return url, stop, nil
}
