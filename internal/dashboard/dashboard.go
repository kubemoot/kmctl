// Package dashboard finds the Kubemoot dashboard Service in the cluster and reads
// from it through the API server's service proxy, using the active kubeconfig
// credentials.
package dashboard

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/kubemoot/kmctl/internal/client"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
)

// Selector matches the dashboard Service by its labels, not its name. Installed as
// the operator chart's subchart the dashboard's app.kubernetes.io/name is
// "dashboard" (the subchart alias) and its Service is <release>-dashboard;
// installed on its own the name is "kubemoot-dashboard". part-of=kubemoot keeps
// the generic "dashboard" name from matching another product's Service.
const Selector = "app.kubernetes.io/part-of=kubemoot,app.kubernetes.io/name in (dashboard,kubemoot-dashboard)"

// httpPortName is the dashboard chart's Service port name.
const httpPortName = "http"

// Target is a dashboard Service and the port the service proxy reaches it on.
type Target struct {
	Name string
	Port string
}

// Find returns the dashboard Service in the namespace: the named Service when
// override is set, otherwise the one Service that matches Selector.
func Find(ctx context.Context, svcs corev1client.ServiceInterface, namespace, override string) (Target, error) {
	svc, err := findService(ctx, svcs, namespace, override)
	if err != nil {
		return Target{}, err
	}
	port, err := proxyPort(svc)
	if err != nil {
		return Target{}, err
	}
	return Target{Name: svc.Name, Port: port}, nil
}

func findService(ctx context.Context, svcs corev1client.ServiceInterface, namespace, override string) (*corev1.Service, error) {
	if override != "" {
		svc, err := svcs.Get(ctx, override, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("dashboard service %s/%s: %w", namespace, override, err)
		}
		return svc, nil
	}
	list, err := svcs.List(ctx, metav1.ListOptions{LabelSelector: Selector})
	if err != nil {
		return nil, fmt.Errorf("find the dashboard service in namespace %q: %w", namespace, err)
	}
	switch len(list.Items) {
	case 0:
		return nil, fmt.Errorf("no Kubemoot dashboard service in namespace %q (selector %q); "+
			"enable the dashboard (operator chart value dashboard.enabled=true), "+
			"or point at it with --dashboard-namespace (where it runs) and --dashboard-service (its name)", namespace, Selector)
	case 1:
		return &list.Items[0], nil
	default:
		names := make([]string, 0, len(list.Items))
		for i := range list.Items {
			names = append(names, list.Items[i].Name)
		}
		return nil, fmt.Errorf("more than one Kubemoot dashboard service in namespace %q (%s); choose one with --dashboard-service",
			namespace, strings.Join(names, ", "))
	}
}

// proxyPort picks the Service port named "http", else the first port.
func proxyPort(svc *corev1.Service) (string, error) {
	if len(svc.Spec.Ports) == 0 {
		return "", fmt.Errorf("dashboard service %s/%s exposes no ports", svc.Namespace, svc.Name)
	}
	for _, p := range svc.Spec.Ports {
		if p.Name == httpPortName {
			return p.Name, nil
		}
	}
	return strconv.Itoa(int(svc.Spec.Ports[0].Port)), nil
}

// DownloadArtifact fetches a fitness suite's XLSX artifact from the dashboard's
// artifact endpoint.
func DownloadArtifact(ctx context.Context, cs kubernetes.Interface, dashNS, dashSvc, suiteNS, suite string) ([]byte, error) {
	t, err := Find(ctx, cs.CoreV1().Services(dashNS), dashNS, dashSvc)
	if err != nil {
		return nil, err
	}
	data, err := client.ServiceProxy(cs.CoreV1().RESTClient().Get(), dashNS, t.Name, t.Port).
		Suffix("dashboard", "api", "kubemoot", "crewfitnesssuites", suiteNS, suite, "artifact").
		DoRaw(ctx)
	if err != nil {
		return nil, fmt.Errorf("download artifact for suite %q from %s/%s: %w", suite, dashNS, t.Name, err)
	}
	return data, nil
}
