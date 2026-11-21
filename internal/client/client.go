// Package client wires the standard kubeconfig flags and builds Kubernetes
// clients for kmctl. kmctl talks to Kubemoot's CRDs through the dynamic client
// (by GroupVersionResource), so it needs no compile-time dependency on the
// operator's Go types for the CRDs and stays buildable standalone. (The memory export
// imports only the operator's public crew-memory packages.)
package client

import (
	"context"

	"github.com/kubemoot/kmctl/internal/svcforward"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// KubemootGroup is the Kubemoot CRD API group.
const KubemootGroup = "kubemoot.ai"

// Factory builds clients from the resolved kubeconfig/context/namespace flags.
type Factory struct {
	ConfigFlags *genericclioptions.ConfigFlags
	// OpenService, when set, replaces the port-forward ServiceClient opens; tests
	// point it at a local server.
	OpenService BaseOpener
	// Caller is who the commands that talk to a crew's gateway are there; the
	// conversation command's flags set it.
	Caller Caller
}

// NewFactory returns a Factory backed by the standard kubectl config flags
// (--kubeconfig, --context, --namespace, and the rest of the auth/connection set).
func NewFactory() *Factory {
	return &Factory{ConfigFlags: genericclioptions.NewConfigFlags(true)}
}

// RESTConfig resolves the *rest.Config for the active context.
//
// With no current context, no --context/--server/--cluster and no in-cluster
// config it fails with clientcmd.ErrEmptyConfig before any network call, instead
// of using client-go's http://localhost:8080 fallback.
func (f *Factory) RESTConfig() (*rest.Config, error) {
	if err := f.requireCluster(); err != nil {
		return nil, err
	}
	return f.ConfigFlags.ToRESTConfig()
}

// inCluster reports whether the process runs in a pod with a service account;
// tests replace it.
var inCluster = func() bool {
	_, err := rest.InClusterConfig()
	return err == nil
}

func flagSet(v *string) bool { return v != nil && *v != "" }

// requireCluster returns clientcmd.ErrEmptyConfig when nothing names a cluster.
// A kubeconfig that cannot be read is left to the normal path to report.
func (f *Factory) requireCluster() error {
	cf := f.ConfigFlags
	if flagSet(cf.APIServer) || flagSet(cf.Context) || flagSet(cf.ClusterName) {
		return nil
	}
	raw, err := cf.ToRawKubeConfigLoader().RawConfig()
	if err != nil || raw.CurrentContext != "" || inCluster() {
		return nil
	}
	return clientcmd.ErrEmptyConfig
}

// Dynamic builds a dynamic client for working with Kubemoot CRDs by GVR.
func (f *Factory) Dynamic() (dynamic.Interface, error) {
	cfg, err := f.RESTConfig()
	if err != nil {
		return nil, err
	}
	return dynamic.NewForConfig(cfg)
}

// Discovery builds a discovery client (server version, API groups).
func (f *Factory) Discovery() (discovery.DiscoveryInterface, error) {
	if err := f.requireCluster(); err != nil {
		return nil, err
	}
	return f.ConfigFlags.ToDiscoveryClient()
}

// RESTMapper builds a discovery-backed REST mapper for GVK<->GVR resolution.
func (f *Factory) RESTMapper() (meta.RESTMapper, error) {
	if err := f.requireCluster(); err != nil {
		return nil, err
	}
	return f.ConfigFlags.ToRESTMapper()
}

// Clientset builds a typed Kubernetes clientset for core resources (Services,
// the Service lookup) authenticated by the active kubeconfig context.
func (f *Factory) Clientset() (kubernetes.Interface, error) {
	cfg, err := f.RESTConfig()
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(cfg)
}

// ServiceOpener opens an HTTP client to a Service's port (a name or a number) and
// returns a function that closes it.
type ServiceOpener func(ctx context.Context, namespace, service, port string) (rest.Interface, func(), error)

// BaseOpener opens a connection to a Service's port and returns the base URL its
// HTTP server answers on and a function that closes the connection.
type BaseOpener func(ctx context.Context, namespace, service, port string) (string, func(), error)

// ServiceClient opens an HTTP client to a Service in the cluster (the per-crew
// discussion gateway, the dashboard) through a port-forward to one of its pods, as
// kubectl port-forward does, with the active kubeconfig context's credentials. The
// traffic goes through the kubelet, so it works under a default-deny NetworkPolicy,
// which blocks the API server's service proxy. The client sends no Kubernetes
// credentials to the Service; request paths are relative to the Service's root.
// The returned function closes the forward; the forward also ends with ctx.
func (f *Factory) ServiceClient(ctx context.Context, namespace, service, port string) (rest.Interface, func(), error) {
	return f.serviceClient(ctx, namespace, service, port, nil)
}

// serviceClient is ServiceClient with credentials: a non-nil auth sets them on the
// client's configuration once the forward is open.
func (f *Factory) serviceClient(ctx context.Context, namespace, service, port string, auth func(context.Context, *rest.Config) error) (rest.Interface, func(), error) {
	open := f.OpenService
	if open == nil {
		open = f.forwardService
	}
	base, stop, err := open(ctx, namespace, service, port)
	if err != nil {
		return nil, nil, err
	}
	cfg := &rest.Config{
		Host:          base,
		ContentConfig: rest.ContentConfig{NegotiatedSerializer: scheme.Codecs.WithoutConversion()},
	}
	if auth != nil {
		if err = auth(ctx, cfg); err != nil {
			stop()
			return nil, nil, err
		}
	}
	rc, err := rest.UnversionedRESTClientFor(cfg)
	if err != nil {
		stop()
		return nil, nil, err
	}
	return rc, stop, nil
}

// forwardService is the port-forward behind ServiceClient.
func (f *Factory) forwardService(ctx context.Context, namespace, service, port string) (string, func(), error) {
	cfg, err := f.RESTConfig()
	if err != nil {
		return "", nil, err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return "", nil, err
	}
	addr, stop, err := svcforward.Open(ctx, cfg, cs, namespace, service, port)
	if err != nil {
		return "", nil, err
	}
	return "http://" + addr, stop, nil
}

// Namespace resolves the effective namespace from the --namespace flag, the
// current context, or "default".
func (f *Factory) Namespace() string {
	if f.ConfigFlags.Namespace != nil && *f.ConfigFlags.Namespace != "" {
		return *f.ConfigFlags.Namespace
	}
	ns, _, err := f.ConfigFlags.ToRawKubeConfigLoader().Namespace()
	if err != nil || ns == "" {
		return "default"
	}
	return ns
}

// CurrentContext returns the context in effect: the --context override if set,
// otherwise the kubeconfig's current-context.
func (f *Factory) CurrentContext() string {
	if f.ConfigFlags.Context != nil && *f.ConfigFlags.Context != "" {
		return *f.ConfigFlags.Context
	}
	raw, err := f.ConfigFlags.ToRawKubeConfigLoader().RawConfig()
	if err != nil {
		return ""
	}
	return raw.CurrentContext
}

// GroupLister is the slice of the discovery API used to detect Kubemoot. A
// narrow interface keeps KubemootInstalled unit-testable without a cluster.
type GroupLister interface {
	ServerGroups() (*metav1.APIGroupList, error)
}

// KubemootInstalled reports whether the cluster serves the Kubemoot API group.
func KubemootInstalled(d GroupLister) (bool, error) {
	groups, err := d.ServerGroups()
	if err != nil {
		return false, err
	}
	for _, g := range groups.Groups {
		if g.Name == KubemootGroup {
			return true, nil
		}
	}
	return false, nil
}
