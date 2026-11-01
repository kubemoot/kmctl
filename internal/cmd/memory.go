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
)

const (
	defaultNATSNamespace = "nats"
	defaultNATSService   = "nats"
	natsConnectTimeout   = 15 * time.Second
)

// memoryOptions are the flags of kmctl memory export.
type memoryOptions struct {
	namespace     string
	crew          string
	out           string
	origin        string
	markdown      bool
	natsURL       string
	creds         string
	natsNamespace string
	natsService   string
}

// memoryOpener gives the read-only reader of the crew-memory bucket and a
// function that closes whatever it opened. Tests replace it with a fake.
type memoryOpener func(ctx context.Context, f *client.Factory, o memoryOptions) (crewmemory.EntryReader, func(), error)

// newMemoryCommand groups the commands on a crew's long-term memory.
func newMemoryCommand(f *client.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Work with a crew's long-term memory",
		Long: `Commands on the facts a crew learned and keeps in its memory. Memory is read
only here: kmctl never writes, changes or deletes a fact.`,
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(newMemoryExportCommand(f, openMemory))
	return cmd
}

func newMemoryExportCommand(f *client.Factory, open memoryOpener) *cobra.Command {
	var o memoryOptions
	cmd := &cobra.Command{
		Use:   "export CREW",
		Short: "Write a crew's memory in the OMPI interchange format",
		Long: `Write a crew's long-term memory in the interchange format of the Open Memory
Protocol Initiative (OMPI) exchange-and-runtime working spec v0.2: a manifest.json
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

The facts are text the agents wrote, so treat the files as data.`,
		Example: `  kmctl memory export homelab-pilot -n crew-homelab-pilot
  kmctl memory export homelab-pilot -n crew-homelab-pilot -o ./pilot-memory --markdown
  kmctl memory export demo --nats-url nats://localhost:4222`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: completeCrewArg(f),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.crew = args[0]
			o.namespace = f.Namespace()
			if o.out == "" {
				o.out = "." + string(filepath.Separator) + o.crew + "-memory"
			}
			return runMemoryExport(cmd.Context(), cmd.OutOrStdout(), f, o, open, time.Now())
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&o.out, "output", "o", "", "Directory to write; it must not exist or be empty (default ./<crew>-memory)")
	fl.BoolVar(&o.markdown, "markdown", false, "Also write the runtime projection as Markdown files")
	fl.StringVar(&o.origin, "origin", "", "Origin id for the manifest (default: derived from the namespace and crew)")
	fl.StringVar(&o.natsNamespace, "nats-namespace", defaultNATSNamespace, "Namespace of the NATS Service")
	fl.StringVar(&o.natsService, "nats-service", defaultNATSService, "Name of the NATS Service")
	fl.StringVar(&o.natsURL, "nats-url", "", "Connect to this NATS URL instead of port-forwarding to the NATS Service")
	fl.StringVar(&o.creds, "creds", "", "NATS credentials file, for a server that needs them")
	return cmd
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
	exp, err := ompi.ExportCrew(reader, scope, o.out, o.markdown, ompi.Options{ExportedAt: now, Origin: o.origin})
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
	url, stop, err := natsEndpoint(ctx, f, o)
	if err != nil {
		return nil, nil, err
	}
	opts := []nats.Option{nats.Timeout(natsConnectTimeout), nats.MaxReconnects(0)}
	if o.creds != "" {
		opts = append(opts, nats.UserCredentials(o.creds))
	}
	nc, err := nats.Connect(url, opts...)
	if err != nil {
		stop()
		return nil, nil, clierr.New(fmt.Sprintf("could not connect to NATS at %s", url),
			"Check the NATS Service with --nats-namespace and --nats-service, or pass --nats-url (and --creds if the server needs them).", err)
	}
	reader, err := natskv.Open(nc)
	if err != nil {
		nc.Close()
		stop()
		return nil, nil, clierr.New("could not open the crew-memory bucket",
			"The bucket kubemoot_crew_memory is created by the Kubemoot operator; check that Kubemoot is installed and its streams job has run.", err)
	}
	return reader, func() { nc.Close(); stop() }, nil
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
