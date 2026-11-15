package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/clierr"
	"github.com/kubemoot/kmctl/internal/output"
	"github.com/kubemoot/kmctl/internal/version"
	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// versionTimeout bounds the cluster lookup so an unreachable cluster never holds
// up the client line.
const versionTimeout = 5 * time.Second

// kubemootConfigName is the cluster-scoped KubemootConfig singleton the operator
// writes its release to.
const kubemootConfigName = "default"

var kubemootConfigGVR = schema.GroupVersionResource{
	Group: client.KubemootGroup, Version: "v1alpha1", Resource: "kubemootconfigs",
}

// clientVersion is kmctl's own build.
type clientVersion struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

// serverVersion is what the cluster's operator reports.
type serverVersion struct {
	Release string `json:"release"`
}

// versionReport is the whole `kmctl version` answer. ServerVersion is set only
// when the cluster reported a release; ServerMessage says why it did not, and
// ServerCause carries the underlying error, only with -v or KMCTL_DEBUG.
type versionReport struct {
	ClientVersion clientVersion  `json:"clientVersion"`
	ServerVersion *serverVersion `json:"serverVersion,omitempty"`
	ServerMessage string         `json:"serverMessage,omitempty"`
	ServerCause   string         `json:"serverCause,omitempty"`
}

// devBuildVersions are the versions an unstamped build reports: the placeholder
// a build without link flags carries, and the 0.0.0 files in git hold.
var devBuildVersions = map[string]bool{"dev": true, "0.0.0": true}

// devBuildLabel is what a version that no build stamped is shown as.
const devBuildLabel = "development build"

// shownVersion is v as people read it: a build that was never stamped with a
// release says so instead of printing a placeholder.
func shownVersion(v string) string {
	if devBuildVersions[v] {
		return devBuildLabel
	}
	return v
}

// newVersionCommand prints kmctl's own version and the Kubemoot release of the
// cluster it is connected to, the way `kubectl version` prints a client and a
// server line. `--short` prints just kmctl's version number.
func newVersionCommand(f *client.Factory) *cobra.Command {
	var short, clientOnly bool
	var outFmt string
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the kmctl version and the Kubemoot release of the cluster",
		Long: `Print kmctl's own version and the Kubemoot release the connected cluster runs,
read from the KubemootConfig status the operator writes.

When no cluster can be reached, or the operator reports no release, the server
line says why and the client line is still printed. Unlike kubectl version, the
command then still exits 0: the client version is a complete answer on its own (a
script that asks for it on a machine with no cluster should get it), and the
server line, or serverMessage with -o, tells the reason. A build that was never
stamped with a release is shown as "development build". Use --client to skip the
cluster, or --short for the bare kmctl version number; --short cannot be combined
with -o.`,
		Example: `  kmctl version
  kmctl version --client
  kmctl version -o json
  kmctl version --short`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if outFmt != "" {
				if err := output.Validate(output.Format(outFmt)); err != nil {
					return err
				}
			}
			info := version.Get()
			if short {
				_, err := fmt.Fprintln(cmd.OutOrStdout(), info.Version)
				return err
			}
			report := versionReport{ClientVersion: clientVersion{Version: info.Version, Commit: info.Commit, Date: info.Date}}
			if !clientOnly {
				ctx, cancel := context.WithTimeout(cmd.Context(), versionTimeout)
				defer cancel()
				report.ServerVersion, report.ServerMessage, report.ServerCause = lookupServerVersion(ctx, f, verboseOn(cmd))
			}
			return printVersion(cmd, report, outFmt, clientOnly)
		},
	}
	cmd.Flags().BoolVar(&short, "short", false, "Print just the kmctl version number")
	cmd.Flags().BoolVar(&clientOnly, "client", false, "Print only the client version; do not contact the cluster")
	cmd.Flags().StringVarP(&outFmt, "output", "o", "", output.FormatHelp(""))
	cmd.MarkFlagsMutuallyExclusive("output", "short")
	return cmd
}

// verboseOn reports whether the underlying error should be shown: --verbose or KMCTL_DEBUG.
func verboseOn(cmd *cobra.Command) bool {
	verbose, _ := cmd.Flags().GetBool(verboseFlag)
	return verbose || clierr.Debug(os.Getenv)
}

// printVersion writes the report as the two kubectl-style lines, or structured
// when a format was asked for.
func printVersion(cmd *cobra.Command, r versionReport, outFmt string, clientOnly bool) error {
	if outFmt != "" {
		return output.Print(cmd.OutOrStdout(), output.Format(outFmt), r)
	}
	out := output.NewWriter(cmd.OutOrStdout())
	out.Printf("Client Version: kmctl %s (commit %s, built %s)\n",
		shownVersion(r.ClientVersion.Version), r.ClientVersion.Commit, r.ClientVersion.Date)
	if !clientOnly {
		out.Printf("Server Version: %s\n", serverLine(r))
	}
	return out.Err()
}

// serverLine is the text after "Server Version:".
func serverLine(r versionReport) string {
	if r.ServerVersion != nil {
		return "Kubemoot " + shownVersion(r.ServerVersion.Release)
	}
	line := "unavailable (" + r.ServerMessage
	if r.ServerCause != "" {
		line += "; cause: " + r.ServerCause
	}
	return line + ")"
}

// lookupServerVersion reads the Kubemoot release from the cluster the factory
// resolves to. It returns the release, or the explained reason there is none,
// with the raw cause when verbose.
func lookupServerVersion(ctx context.Context, f *client.Factory, verbose bool) (*serverVersion, string, string) {
	env := clierr.Env{Kubeconfig: kubeconfigFlag(f), Context: f.CurrentContext(), Namespace: f.Namespace()}
	dyn, err := f.Dynamic()
	if err != nil {
		return failedLookup(err, env, verbose)
	}
	return serverReleaseFrom(ctx, dyn, env, verbose)
}

// serverReleaseFrom reads status.release from the KubemootConfig singleton.
func serverReleaseFrom(ctx context.Context, dyn dynamic.Interface, env clierr.Env, verbose bool) (*serverVersion, string, string) {
	cfg, err := dyn.Resource(kubemootConfigGVR).Get(ctx, kubemootConfigName, metav1.GetOptions{})
	if err != nil {
		return failedLookup(err, env, verbose)
	}
	release, note := releaseOf(cfg)
	return release, note, ""
}

// failedLookup explains a read that failed the way every other command does, and
// keeps the raw error for -v. A missing KubemootConfig means Kubemoot is not installed.
func failedLookup(err error, env clierr.Env, verbose bool) (*serverVersion, string, string) {
	msg := "Kubemoot is not installed: no KubemootConfig named " + kubemootConfigName
	if !apierrors.IsNotFound(err) {
		msg = clierr.Explain(err, env).Message
	}
	if verbose && msg != err.Error() {
		return nil, msg, err.Error()
	}
	return nil, msg, ""
}

// releaseOf reads status.release from a KubemootConfig. An empty status is an
// operator that has not written it yet; a status without a release is an operator
// that predates the field.
func releaseOf(cfg *unstructured.Unstructured) (*serverVersion, string) {
	status, found, _ := unstructured.NestedMap(cfg.Object, "status")
	if !found || len(status) == 0 {
		return nil, "the operator has not written its status yet"
	}
	release, _, _ := unstructured.NestedString(status, "release")
	if release == "" {
		return nil, "this operator does not report a release; upgrade Kubemoot to see it"
	}
	return &serverVersion{Release: release}, ""
}
