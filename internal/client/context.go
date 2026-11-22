package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/kubemoot/kmctl/internal/clierr"
)

// contextCheckTimeout bounds the one request RequireContext makes.
const contextCheckTimeout = 10 * time.Second

const contextHint = "Pick one with: kubectl config use-context NAME, or pass --context NAME; --kubeconfig PATH names the kubeconfig"

// RequireContext fails unless the kubeconfig names a usable Kubernetes context:
// the --kubeconfig file (or KUBECONFIG, or ~/.kube/config) can be read, the
// context in effect (--context, else the current context) exists in it, the
// context's user carries credentials, and the API server is reachable and accepts
// them. The check makes one read-only request (GET /version); it never writes.
//
// A process running in a pod with a service account and no kubeconfig context
// counts as having a context: the service account is its authenticated identity.
// Every error it returns is a *clierr.Error naming what is missing and the fix.
func (f *Factory) RequireContext(ctx context.Context) error {
	if err := f.requireNamedContext(); err != nil {
		return err
	}
	cfg, err := f.ConfigFlags.ToRESTConfig()
	if err != nil {
		return clierr.New("the Kubernetes context cannot be used: "+err.Error(), contextHint, err)
	}
	if !inClusterIdentity(f) && !hasCredentials(cfg) {
		return clierr.New(fmt.Sprintf("context %q has no credentials, so the request would be anonymous", f.CurrentContext()),
			"Add a user with a token, client certificate or exec plugin to the context, or pick another context with --context", nil)
	}
	return f.probeAPIServer(ctx, cfg)
}

// requireNamedContext checks the kubeconfig names the context in effect.
func (f *Factory) requireNamedContext() error {
	raw, err := f.ConfigFlags.ToRawKubeConfigLoader().RawConfig()
	if err != nil {
		return clierr.New("the kubeconfig cannot be read: "+err.Error(),
			"Set KUBECONFIG or pass --kubeconfig PATH to a kubeconfig that exists", err)
	}
	name := f.CurrentContext()
	if name == "" {
		if inCluster() {
			return nil
		}
		return clierr.New("no Kubernetes context is selected: the kubeconfig has no current context, or there is no kubeconfig", contextHint, clientcmd.ErrEmptyConfig)
	}
	if _, ok := raw.Contexts[name]; !ok {
		return clierr.New(fmt.Sprintf("context %q is not in the kubeconfig", name),
			"List the contexts with: kubectl config get-contexts. "+contextHint, nil)
	}
	return nil
}

// inClusterIdentity reports whether no kubeconfig context is in effect and the
// process uses its pod's service account.
func inClusterIdentity(f *Factory) bool { return f.CurrentContext() == "" && inCluster() }

// hasCredentials reports whether the config signs the request in as someone.
func hasCredentials(cfg *rest.Config) bool {
	return HasToken(cfg) || cfg.Username != "" ||
		len(cfg.CertData) > 0 || cfg.CertFile != ""
}

// probeAPIServer asks the API server for its version. An invalid credential is
// answered 401 there, so the same request tells an unreachable server from one
// that refuses the context's user.
func (f *Factory) probeAPIServer(ctx context.Context, cfg *rest.Config) error {
	c := rest.CopyConfig(cfg)
	c.Timeout = contextCheckTimeout
	dc, err := discovery.NewDiscoveryClientForConfig(c)
	if err != nil {
		return clierr.New("the Kubernetes context cannot be used: "+err.Error(), contextHint, err)
	}
	name := f.CurrentContext()
	if name == "" {
		name = "in-cluster service account"
	}
	_, probeErr := dc.RESTClient().Get().AbsPath("/version").Do(ctx).Raw()
	return explainProbe(probeErr, name)
}

func explainProbe(err error, name string) error {
	switch {
	case err == nil:
		return nil
	case apierrors.IsUnauthorized(err):
		return clierr.New(fmt.Sprintf("the Kubernetes API server did not accept the credentials of context %q", name),
			"Log in again or refresh the credentials in your kubeconfig, or pass --context for another context", err)
	case apierrors.IsForbidden(err):
		return nil // the credentials were accepted; what they may do is the command's own request
	}
	var status apierrors.APIStatus
	if errors.As(err, &status) {
		return nil // the server answered; only a transport failure means unreachable
	}
	return clierr.New(fmt.Sprintf("cannot reach the Kubernetes API server (context %q)", name),
		"Check that the cluster is running and the context points at it: kmctl status", err)
}
