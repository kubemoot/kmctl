// Package client wires the standard kubeconfig flags and builds Kubernetes
// clients for kmctl. kmctl talks to Kubemoot's CRDs through the dynamic client
// (by GroupVersionResource), so it needs no compile-time dependency on the
// operator's Go types and stays buildable standalone.
package client

import (
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// KubemootGroup is the Kubemoot CRD API group.
const KubemootGroup = "kubemoot.ai"

// Factory builds clients from the resolved kubeconfig/context/namespace flags.
type Factory struct {
	ConfigFlags *genericclioptions.ConfigFlags
}

// NewFactory returns a Factory backed by the standard kubectl config flags
// (--kubeconfig, --context, --namespace, and the rest of the auth/connection set).
func NewFactory() *Factory {
	return &Factory{ConfigFlags: genericclioptions.NewConfigFlags(true)}
}

// RESTConfig resolves the *rest.Config for the active context.
func (f *Factory) RESTConfig() (*rest.Config, error) {
	return f.ConfigFlags.ToRESTConfig()
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
	return f.ConfigFlags.ToDiscoveryClient()
}

// RESTMapper builds a discovery-backed REST mapper for GVK<->GVR resolution.
func (f *Factory) RESTMapper() (meta.RESTMapper, error) {
	return f.ConfigFlags.ToRESTMapper()
}

// CoreRESTClient builds a core/v1 REST client, used to reach in-cluster services
// (e.g. the per-crew discussion gateway) through the API server's service proxy,
// authenticated by the active kubeconfig context.
func (f *Factory) CoreRESTClient() (rest.Interface, error) {
	cfg, err := f.RESTConfig()
	if err != nil {
		return nil, err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return cs.CoreV1().RESTClient(), nil
}

// Namespace resolves the effective namespace from the --namespace flag, the
// current context, or "default".
func (f *Factory) Namespace() string {
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
