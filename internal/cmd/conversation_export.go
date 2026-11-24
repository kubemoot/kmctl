package cmd

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/clierr"
	"github.com/kubemoot/kmctl/internal/discussion"
	"github.com/kubemoot/kubemoot/operator/pkg/conversationexport"
	"github.com/kubemoot/kubemoot/operator/pkg/crewscope"
	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/rest"
)

// The operator's report Service, which builds conversation exports. Its chart names
// them; they can be changed for an installation that renames them.
const (
	defaultOperatorNamespace = "kubemoot"
	defaultReportService     = "kubemoot-operator-report"
	reportPort               = "8082"
)

// conversationExportOptions are the flags of kmctl conversation export.
type conversationExportOptions struct {
	crew, namespace string
	ids             []string
	out             string
	report, asJSON  bool
	timeZone        string
	opNamespace     string
	opService       string
	// viaService is set when the flags name the report Service, so the aggregated API is not tried.
	viaService bool
}

// reportRoutes names the operator's report endpoints for the way they are reached: the
// aggregated API through the API server, or the report Service.
type reportRoutes struct{ aggregated bool }

// conversationExport is the path segments of the conversation export of a crew.
func (r reportRoutes) conversationExport(namespace, crew string) []string {
	if r.aggregated {
		return []string{"namespaces", namespace, "crews", crew, "conversation-export"}
	}
	return []string{"conversation-export", namespace, crew}
}

// openReport opens a client to the operator's reports as the caller. It prefers the
// aggregated report API through the API server, which takes any kubeconfig; it uses the
// report Service when that API is not installed, or when the flags name the Service or
// ask for a minted ServiceAccount token. Tests reach the Service through the factory's
// OpenService.
func openReport(ctx context.Context, f *client.Factory, o conversationExportOptions) (rest.Interface, reportRoutes, func(), error) {
	if !o.viaService && f.Caller.ServiceAccount == "" {
		rc, ok, err := f.ReportsClient(ctx)
		switch {
		case err != nil:
			return nil, reportRoutes{}, nil, explainReportsAPI(f, err)
		case ok:
			return rc, reportRoutes{aggregated: true}, func() {}, nil
		}
	}
	rc, stop, err := f.GatewayClient(ctx, o.opNamespace, o.opService, reportPort)
	if err != nil {
		return nil, reportRoutes{}, nil, explainReportOpen(f, err, o)
	}
	return rc, reportRoutes{}, stop, nil
}

func newConversationExportCommand(f *client.Factory) *cobra.Command {
	var o conversationExportOptions
	cmd := &cobra.Command{
		Use:   "export CREW ID...",
		Short: "Take conversations away as a zip",
		Long: fmt.Sprintf(`Download everything Kubemoot still holds about one or more conversations of a crew as
a zip: the turns as Markdown and JSON, the receipts and workspace files still stored, the
crew memory learned in the conversation (screened), and a manifest. The operator builds the
zip on request from its stores and keeps no copy.

One ID gives that conversation's zip, named <crew>-conversation-<id>.zip. Several IDs
(at most %d) give a zip of zips, named <crew>-conversations.zip, with a manifest that says
which conversations are included and which are refused (you may not read them), expired
(Kubemoot no longer holds them) or failed (a store could not be read); kmctl prints each
of those after the download. The file is written whole or not at all, and an existing
file is never overwritten.

--report asks for the manifest only, with no zip: what would be included and what left out.

When the operator serves its aggregated report API (reports.kubemoot.ai), kmctl reaches it
through the API server with your own kubeconfig, whatever it signs in with. Otherwise, or
with --operator-namespace, --operator-service or --token-service-account, kmctl port-forwards
to the operator's report Service with your kubeconfig token, as the dashboard does for you.
You need get on the Crew, and the operator checks it. Pass the full
conversation IDs that "kmctl conversation list" prints, or a unique prefix of each.`, conversationexport.MaxBundle),
		Example: `  kmctl conversation export homelab-pilot 6733c361-aa0c-4bde-85c6-e143af23b251 -n crew-homelab-pilot
  kmctl conv export homelab-pilot 6733c361 9f1e22a0 --output-file review.zip
  kmctl conv export homelab-pilot 6733c361 9f1e22a0 --report
  kmctl conv export homelab-pilot 6733c361 --report --json`,
		Args:              cobra.MinimumNArgs(2),
		ValidArgsFunction: completeCrewThenConversation(f),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.crew, o.ids, o.namespace = args[0], args[1:], f.Namespace()
			o.viaService = cmd.Flags().Changed("operator-namespace") || cmd.Flags().Changed("operator-service")
			if err := checkPath(cmd, outputFileFlag, o.out); err != nil {
				return err
			}
			if o.asJSON && !o.report {
				return usageError{errors.New("--json is for --report; the zip carries its own manifest.json")}
			}
			return runConversationExport(cmd, f, o)
		},
	}
	fl := cmd.Flags()
	outputFileFlag.add(fl, &o.out, "", "File to write (default ./<crew>-conversation-<id>.zip, or ./<crew>-conversations.zip for several)")
	fl.BoolVar(&o.report, "report", false, "Print what the export would include and leave out, and write no zip")
	fl.BoolVar(&o.asJSON, "json", false, "With --report, print the manifest as JSON")
	fl.StringVar(&o.timeZone, "time-zone", discussion.LocalZoneName(), "IANA time zone the times in the export are written in (default: this machine's)")
	fl.StringVar(&o.opNamespace, "operator-namespace", defaultOperatorNamespace, "Namespace of the Kubemoot operator's report Service")
	fl.StringVar(&o.opService, "operator-service", defaultReportService, "Name of the Kubemoot operator's report Service")
	return cmd
}

func runConversationExport(cmd *cobra.Command, f *client.Factory, o conversationExportOptions) error {
	scope, err := validateExport(o)
	if err != nil {
		return err
	}
	if o.out == "" {
		o.out = defaultExportName(scope, o.ids)
	}
	if !o.report {
		if err := checkDownloadTarget(o.out, false); err != nil {
			return exportTargetError(o.out, err)
		}
	}
	ctx := cmd.Context()
	if o.ids, err = resolveExportIDs(ctx, f, o); err != nil {
		return err
	}
	rc, routes, stop, err := openReport(ctx, f, o)
	if err != nil {
		return err
	}
	defer stop()
	if o.report {
		return runExportReport(ctx, cmd.OutOrStdout(), rc, routes, o)
	}
	return runExportZip(ctx, cmd.OutOrStdout(), rc, routes, o, scope)
}

// validateExport checks what the flags and arguments say before anything is asked of the cluster.
func validateExport(o conversationExportOptions) (crewscope.Scope, error) {
	scope, err := crewscope.New(o.namespace, o.crew)
	if err != nil {
		return scope, usageError{err}
	}
	if len(o.ids) > conversationexport.MaxBundle {
		return scope, usageError{fmt.Errorf("ask for at most %d conversations at once", conversationexport.MaxBundle)}
	}
	if _, err := time.LoadLocation(o.timeZone); o.timeZone != "" && err != nil {
		return scope, usageError{fmt.Errorf("--time-zone %q is not an IANA time zone name", o.timeZone)}
	}
	return scope, nil
}

// exportTargetError explains a file the export cannot be written to.
func exportTargetError(path string, err error) error {
	if errors.Is(err, fs.ErrExist) {
		return clierr.New(fmt.Sprintf("%s already exists", path), "Nothing was written. Choose another file with -o, or remove it.", err)
	}
	return clierr.New(fmt.Sprintf("cannot write %s", path), "Nothing was written. Choose a file in a directory that exists with -o.", err)
}

func defaultExportName(scope crewscope.Scope, ids []string) string {
	if len(ids) == 1 {
		return conversationexport.FileName(scope, ids[0])
	}
	return conversationexport.BundleFileName(scope)
}

// resolveExportIDs turns each prefix into a full conversation ID through the crew's
// gateway. The gateway is opened only when an ID is not already a full one.
func resolveExportIDs(ctx context.Context, f *client.Factory, o conversationExportOptions) ([]string, error) {
	out := make([]string, len(o.ids))
	var rc rest.Interface
	for i, id := range o.ids {
		if discussion.IsFullID(id) {
			out[i] = id
			continue
		}
		if rc == nil {
			var stop func()
			var err error
			if rc, stop, err = openGateway(ctx, f, o.crew, o.namespace); err != nil {
				return nil, err
			}
			defer stop()
		}
		full, err := resolveConversation(ctx, rc, o.crew, id)
		if err != nil {
			return nil, explainGateway(ctx, f, err, o.crew, o.namespace)
		}
		out[i] = full
	}
	return out, nil
}

// exportRequest builds the report Service request for the export.
func exportRequest(rc rest.Interface, routes reportRoutes, o conversationExportOptions) *rest.Request {
	req := rc.Get().Suffix(routes.conversationExport(o.namespace, o.crew)...)
	for _, id := range o.ids {
		req = req.Param("id", id)
	}
	if o.timeZone != "" {
		req = req.Param("timeZone", o.timeZone)
	}
	return req
}

func runExportZip(
	ctx context.Context, stdout io.Writer, rc rest.Interface, routes reportRoutes, o conversationExportOptions, scope crewscope.Scope,
) error {
	body, err := exportRequest(rc, routes, o).Stream(ctx)
	if err != nil {
		return explainReport(err, routes, o)
	}
	defer func() { _ = body.Close() }()
	if err := writeDownload(o.out, body, downloadOptions{mode: 0o600}); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return exportTargetError(o.out, err)
		}
		return clierr.New(fmt.Sprintf("could not write %s", o.out), "Nothing was kept; the file is written whole or not at all.", err)
	}
	if err := printZipManifest(stdout, o.out); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "wrote %d conversation(s) of %s/%s to %s\n", len(o.ids), scope.Namespace, scope.Crew, o.out)
	return err
}

// printZipManifest prints what the zip's own manifest says was left out, before the
// summary line. A zip with no readable manifest is noted, not an error: the file is whole.
func printZipManifest(w io.Writer, path string) error {
	raw, err := readZipManifest(path)
	if err == nil {
		var m any
		if m, err = decodeManifest(raw); err == nil {
			return printLeftOut(w, m)
		}
	}
	_, werr := fmt.Fprintf(w, "note: could not read the manifest of %s: %v\n", path, err)
	return werr
}

// manifestLimit bounds how much of a manifest kmctl reads.
const manifestLimit = 4 << 20

func readZipManifest(path string) ([]byte, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = zr.Close() }()
	for _, zf := range zr.File {
		if zf.Name != conversationexport.FileManifest {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, err
		}
		defer func() { _ = rc.Close() }()
		raw, err := io.ReadAll(io.LimitReader(rc, manifestLimit+1))
		if err == nil && len(raw) > manifestLimit {
			err = fmt.Errorf("%s is larger than %d bytes", conversationexport.FileManifest, manifestLimit)
		}
		return raw, err
	}
	return nil, errors.New("the zip has no " + conversationexport.FileManifest)
}

// decodeManifest reads the manifest the operator wrote, a bundle's or one conversation's,
// into the operator's own type.
func decodeManifest(raw []byte) (any, error) {
	var kind struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &kind); err != nil {
		return nil, fmt.Errorf("the manifest is not JSON: %w", err)
	}
	switch kind.Kind {
	case conversationexport.KindBundle:
		var m conversationexport.BundleManifest
		return m, json.Unmarshal(raw, &m)
	case conversationexport.KindConversation:
		var m conversationexport.Manifest
		return m, json.Unmarshal(raw, &m)
	}
	return nil, fmt.Errorf("the manifest is of kind %q, which kmctl does not know", clean(kind.Kind))
}

// printLeftOut prints what a manifest says was not included: the refused, expired and
// failed conversations of a bundle, or the facts one conversation's export left out.
func printLeftOut(w io.Writer, m any) error {
	switch m := m.(type) {
	case conversationexport.BundleManifest:
		return printEntries(w, m)
	case conversationexport.Manifest:
		for _, l := range m.LeftOut {
			if _, err := fmt.Fprintf(w, "left out %q: %s\n", clean(l.Key), clean(l.Reason)); err != nil {
				return err
			}
		}
	}
	return nil
}

func printEntries(w io.Writer, m conversationexport.BundleManifest) error {
	for _, g := range []struct {
		word    string
		entries []conversationexport.Entry
	}{{"refused", m.Refused}, {"expired", m.Expired}, {"failed", m.Failed}} {
		for _, e := range g.entries {
			if _, err := fmt.Fprintf(w, "%s %s: %s\n", g.word, clean(e.ConversationID), orDash(clean(e.Reason))); err != nil {
				return err
			}
		}
	}
	return nil
}

// runExportReport asks for the manifest only and prints it.
func runExportReport(
	ctx context.Context, stdout io.Writer, rc rest.Interface, routes reportRoutes, o conversationExportOptions,
) error {
	raw, err := exportRequest(rc, routes, o).Param("report", "true").Do(ctx).Raw()
	if err != nil {
		return explainReport(err, routes, o)
	}
	m, err := decodeManifest(raw)
	if err != nil {
		return fmt.Errorf("the operator's report is not a manifest: %w", err)
	}
	if o.asJSON {
		var b bytes.Buffer
		if err := json.Indent(&b, raw, "", "  "); err != nil {
			return fmt.Errorf("the operator's report is not JSON: %w", err)
		}
		b.WriteByte('\n')
		_, err = stdout.Write(b.Bytes())
		return err
	}
	return printReport(stdout, m)
}

// printReport prints a report: what is included, then what is left out.
func printReport(w io.Writer, m any) error {
	switch m := m.(type) {
	case conversationexport.BundleManifest:
		for _, e := range m.Included {
			if _, err := fmt.Fprintf(w, "included %s\n", clean(e.ConversationID)); err != nil {
				return err
			}
		}
	case conversationexport.Manifest:
		if _, err := fmt.Fprintf(w, "conversation %s: %d turn(s)\n", clean(m.ConversationID), m.Turns); err != nil {
			return err
		}
		for _, p := range m.Parts {
			if _, err := fmt.Fprintf(w, "  %-12s %s (%d)%s\n", clean(p.Path), clean(p.Status), p.Count, reasonSuffix(clean(p.Reason))); err != nil {
				return err
			}
		}
	}
	return printLeftOut(w, m)
}

func reasonSuffix(reason string) string {
	if reason == "" {
		return ""
	}
	return ": " + reason
}

// explainReportOpen explains why the port-forward to the report Service could not open.
func explainReportOpen(f *client.Factory, err error, o conversationExportOptions) error {
	var caller *client.CallerError
	if errors.As(err, &caller) {
		return explainCaller(f, caller)
	}
	where := o.opNamespace + "/" + o.opService
	if apierrors.IsForbidden(err) {
		return clierr.New(fmt.Sprintf("you are not allowed to reach the operator's report service %s", where),
			"kmctl port-forwards to it, which takes get on services, list on pods, and create on pods/portforward in "+o.opNamespace+"; check yours with: kubectl auth can-i create pods/portforward -n "+o.opNamespace, err)
	}
	return clierr.New(fmt.Sprintf("could not reach the operator's report service %s", where),
		"Check that Kubemoot is installed there, or name its report Service with --operator-namespace and --operator-service.", err)
}

// unauthorized explains a 401 of the way the reports were reached.
func (r reportRoutes) unauthorized(err error) error {
	if r.aggregated {
		return clierr.New("the API server did not accept your credentials for the operator's report API",
			"Check that your kubeconfig works with: kubectl auth whoami", err)
	}
	return clierr.New("the operator did not accept your Kubernetes token",
		"The export needs a bearer token: a kubeconfig that signs in with a client certificate has none; use --token-service-account NAMESPACE/NAME", err)
}

// forbiddenHint says what a refused export needs, of the way the reports were reached.
func (r reportRoutes) forbiddenHint(o conversationExportOptions) string {
	hint := "An export needs get on the Crew; check yours with: kubectl auth can-i get crews.kubemoot.ai/" + o.crew + " -n " + o.namespace
	if r.aggregated {
		hint += ". Through the report API it also needs get on crews/conversation-export in the reports.kubemoot.ai group, " +
			"which the operator chart's reports-api ClusterRole grants; check with: kubectl auth can-i get crews.reports.kubemoot.ai/" + o.crew + "/conversation-export -n " + o.namespace
	}
	return hint
}

// explainReportsAPI explains why the aggregated report API could not be asked about.
func explainReportsAPI(f *client.Factory, err error) error {
	var caller *client.CallerError
	if errors.As(err, &caller) {
		return explainCaller(f, caller)
	}
	return clierr.New("could not ask the API server whether the operator's report API is installed",
		"Check that your kubeconfig works with: kubectl auth whoami. To skip this check and use the operator's report Service, name it with --operator-service.", err)
}

// explainReport explains a failed answer of the operator's reports by its HTTP status.
func explainReport(err error, routes reportRoutes, o conversationExportOptions) error {
	var se *apierrors.StatusError
	if !errors.As(err, &se) {
		return err
	}
	text := clean(statusText(se))
	switch int(se.Status().Code) {
	case http.StatusUnauthorized:
		return routes.unauthorized(err)
	case http.StatusForbidden:
		return clierr.New(fmt.Sprintf("the operator refused the export of crew %q in namespace %q: %s", o.crew, o.namespace, text), routes.forbiddenHint(o), err)
	case http.StatusNotFound:
		return clierr.New(fmt.Sprintf("Kubemoot holds no such conversation of crew %q: %s", o.crew, text),
			"It does not exist or has expired. List the crew's conversations with: kmctl conversation list "+o.crew+" -n "+o.namespace, err)
	case http.StatusBadRequest:
		return usageError{errors.New(text)}
	}
	return clierr.New(fmt.Sprintf("the operator could not export the conversation: %s", strings.TrimSpace(text)),
		"The operator's conversation stores may be unreachable; see its logs with: kubectl logs -n "+o.opNamespace+" deploy/kubemoot-operator", err)
}
