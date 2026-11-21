package cmd

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

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
}

// openReport opens a client to the operator's report Service as the caller. Tests
// reach it through the factory's OpenService.
func openReport(ctx context.Context, f *client.Factory, o conversationExportOptions) (rest.Interface, func(), error) {
	rc, stop, err := f.GatewayClient(ctx, o.opNamespace, o.opService, reportPort)
	if err != nil {
		return nil, nil, explainReportOpen(f, err, o)
	}
	return rc, stop, nil
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

kmctl port-forwards to the operator's report Service with your kubeconfig token, as the
dashboard does for you; you need get on the Crew, and the operator checks it. Pass the full
conversation IDs that "kmctl conversation list" prints, or a unique prefix of each.`, conversationexport.MaxBundle),
		Example: `  kmctl conversation export homelab-pilot 6733c361-aa0c-4bde-85c6-e143af23b251 -n crew-homelab-pilot
  kmctl conv export homelab-pilot 6733c361 9f1e22a0 -o review.zip
  kmctl conv export homelab-pilot 6733c361 9f1e22a0 --report
  kmctl conv export homelab-pilot 6733c361 --report --json`,
		Args:              cobra.MinimumNArgs(2),
		ValidArgsFunction: completeCrewThenConversation(f),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.crew, o.ids, o.namespace = args[0], args[1:], f.Namespace()
			if o.asJSON && !o.report {
				return usageError{errors.New("--json is for --report; the zip carries its own manifest.json")}
			}
			return runConversationExport(cmd, f, o)
		},
	}
	fl := cmd.Flags()
	fl.StringVarP(&o.out, "output", "o", "", "File to write (default ./<crew>-conversation-<id>.zip, or ./<crew>-conversations.zip for several)")
	fl.BoolVar(&o.report, "report", false, "Print what the export would include and leave out, and write no zip")
	fl.BoolVar(&o.asJSON, "json", false, "With --report, print the manifest as JSON")
	fl.StringVar(&o.timeZone, "time-zone", discussion.LocalZoneName(), "IANA time zone the times in the export are written in (default: this machine's)")
	fl.StringVar(&o.opNamespace, "operator-namespace", defaultOperatorNamespace, "Namespace of the Kubemoot operator's report Service")
	fl.StringVar(&o.opService, "operator-service", defaultReportService, "Name of the Kubemoot operator's report Service")
	return cmd
}

func runConversationExport(cmd *cobra.Command, f *client.Factory, o conversationExportOptions) error {
	scope, err := crewscope.New(o.namespace, o.crew)
	if err != nil {
		return usageError{err}
	}
	if len(o.ids) > conversationexport.MaxBundle {
		return usageError{fmt.Errorf("ask for at most %d conversations at once", conversationexport.MaxBundle)}
	}
	if o.out == "" {
		o.out = defaultExportName(scope, o.ids)
	}
	if _, err := os.Stat(o.out); !o.report && err == nil {
		return clierr.New(fmt.Sprintf("%s already exists", o.out), "Nothing was written. Choose another file with -o, or remove it.", nil)
	}
	ctx := cmd.Context()
	ids, err := resolveExportIDs(ctx, f, o)
	if err != nil {
		return err
	}
	o.ids = ids
	rc, stop, err := openReport(ctx, f, o)
	if err != nil {
		return err
	}
	defer stop()
	if o.report {
		return runExportReport(ctx, cmd.OutOrStdout(), rc, o)
	}
	return runExportZip(ctx, cmd.OutOrStdout(), rc, o, scope)
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
func exportRequest(rc rest.Interface, o conversationExportOptions) *rest.Request {
	req := rc.Get().Suffix("conversation-export", o.namespace, o.crew)
	for _, id := range o.ids {
		req = req.Param("id", id)
	}
	if o.timeZone != "" {
		req = req.Param("timeZone", o.timeZone)
	}
	return req
}

func runExportZip(ctx context.Context, stdout io.Writer, rc rest.Interface, o conversationExportOptions, scope crewscope.Scope) error {
	body, err := exportRequest(rc, o).Stream(ctx)
	if err != nil {
		return explainReport(err, o)
	}
	defer func() { _ = body.Close() }()
	if err := writeFileWhole(o.out, body); err != nil {
		return clierr.New(fmt.Sprintf("could not write %s", o.out), "Nothing was kept; the file is written whole or not at all.", err)
	}
	if err := printZipManifest(stdout, o.out); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "wrote %d conversation(s) of %s/%s to %s\n", len(o.ids), scope.Namespace, scope.Crew, o.out)
	return err
}

// writeFileWhole writes the body to path through a temporary file in the same directory,
// so a transfer that is cut short leaves no file that looks whole.
func writeFileWhole(path string, body io.Reader) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".kmctl-export-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = io.Copy(tmp, body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// printZipManifest prints what the zip's own manifest says was left out, before the
// summary line. A zip with no readable manifest is noted, not an error: the file is whole.
func printZipManifest(w io.Writer, path string) error {
	raw, err := readZipManifest(path)
	if err != nil {
		_, werr := fmt.Fprintf(w, "note: could not read the manifest of %s: %v\n", path, err)
		return werr
	}
	return printManifest(w, raw)
}

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
		return io.ReadAll(io.LimitReader(rc, manifestLimit))
	}
	return nil, errors.New("no " + conversationexport.FileManifest)
}

// manifestLimit bounds how much of a manifest kmctl reads.
const manifestLimit = 4 << 20

// printManifest prints the left-out entries of a bundle's or one conversation's manifest.
func printManifest(w io.Writer, raw []byte) error {
	var kind struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &kind); err != nil {
		return err
	}
	if kind.Kind == conversationexport.KindBundle {
		var m conversationexport.BundleManifest
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
		return printBundleLeftOut(w, m)
	}
	var m conversationexport.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	return printConversationLeftOut(w, m)
}

func printBundleLeftOut(w io.Writer, m conversationexport.BundleManifest) error {
	for _, g := range []struct {
		word    string
		entries []conversationexport.Entry
	}{{"refused", m.Refused}, {"expired", m.Expired}, {"failed", m.Failed}} {
		for _, e := range g.entries {
			if _, err := fmt.Fprintf(w, "%s %s: %s\n", g.word, e.ConversationID, orDash(e.Reason)); err != nil {
				return err
			}
		}
	}
	return nil
}

func printConversationLeftOut(w io.Writer, m conversationexport.Manifest) error {
	for _, l := range m.LeftOut {
		if _, err := fmt.Fprintf(w, "left out %q: %s\n", l.Key, l.Reason); err != nil {
			return err
		}
	}
	return nil
}

// runExportReport asks for the manifest only and prints it.
func runExportReport(ctx context.Context, stdout io.Writer, rc rest.Interface, o conversationExportOptions) error {
	raw, err := exportRequest(rc, o).Param("report", "true").Do(ctx).Raw()
	if err != nil {
		return explainReport(err, o)
	}
	if o.asJSON {
		var pretty any
		if err := json.Unmarshal(raw, &pretty); err != nil {
			return fmt.Errorf("the operator's report is not JSON: %w", err)
		}
		b, _ := json.MarshalIndent(pretty, "", "  ")
		_, err = fmt.Fprintln(stdout, string(b))
		return err
	}
	return printReport(stdout, raw)
}

// printReport prints the manifest of a report: what is included, then what is left out.
func printReport(w io.Writer, raw []byte) error {
	var m struct {
		Kind     string                       `json:"kind"`
		Turns    int                          `json:"turns"`
		Parts    []conversationexport.Part    `json:"parts"`
		Included []conversationexport.Entry   `json:"included"`
		Refused  []conversationexport.Entry   `json:"refused"`
		Expired  []conversationexport.Entry   `json:"expired"`
		Failed   []conversationexport.Entry   `json:"failed"`
		LeftOut  []conversationexport.LeftOut `json:"leftOut"`
		ID       string                       `json:"conversationId"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("the operator's report is not JSON: %w", err)
	}
	if m.Kind == conversationexport.KindBundle {
		for _, e := range m.Included {
			if _, err := fmt.Fprintf(w, "included %s\n", e.ConversationID); err != nil {
				return err
			}
		}
		return printBundleLeftOut(w, conversationexport.BundleManifest{Refused: m.Refused, Expired: m.Expired, Failed: m.Failed})
	}
	if _, err := fmt.Fprintf(w, "conversation %s: %d turn(s)\n", m.ID, m.Turns); err != nil {
		return err
	}
	for _, p := range m.Parts {
		if _, err := fmt.Fprintf(w, "  %-12s %s (%d)%s\n", p.Path, p.Status, p.Count, reasonSuffix(p.Reason)); err != nil {
			return err
		}
	}
	return printConversationLeftOut(w, conversationexport.Manifest{LeftOut: m.LeftOut})
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

// explainReport explains a failed answer of the report Service by its HTTP status.
func explainReport(err error, o conversationExportOptions) error {
	var se *apierrors.StatusError
	if !errors.As(err, &se) {
		return err
	}
	text := statusText(se)
	switch int(se.Status().Code) {
	case http.StatusUnauthorized:
		return clierr.New("the operator did not accept your Kubernetes token",
			"The export needs a bearer token: a kubeconfig that signs in with a client certificate has none; use --token-service-account NAMESPACE/NAME", err)
	case http.StatusForbidden:
		return clierr.New(fmt.Sprintf("the operator refused the export of crew %q in namespace %q: %s", o.crew, o.namespace, text),
			"An export needs get on the Crew; check yours with: kubectl auth can-i get crews.kubemoot.ai/"+o.crew+" -n "+o.namespace, err)
	case http.StatusNotFound:
		return clierr.New(fmt.Sprintf("Kubemoot holds no such conversation of crew %q: %s", o.crew, text),
			"It does not exist or has expired. List the crew's conversations with: kmctl conversation list "+o.crew+" -n "+o.namespace, err)
	case http.StatusBadRequest:
		return usageError{errors.New(text)}
	}
	return clierr.New(fmt.Sprintf("the operator could not export the conversation: %s", strings.TrimSpace(text)),
		"The operator's conversation stores may be unreachable; see its logs with: kubectl logs -n "+o.opNamespace+" deploy/kubemoot-operator", err)
}
