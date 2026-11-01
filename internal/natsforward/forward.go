// Package natsforward reaches a cluster's NATS server from the machine kmctl runs
// on, with the credentials of the active kubeconfig context. The Kubernetes API
// server's service proxy carries HTTP only and NATS is not HTTP, so the package
// opens a port-forward to a ready pod behind the NATS Service, as kubectl
// port-forward does, and gives back a local URL. Nothing is installed or changed
// in the cluster.
package natsforward

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
	"k8s.io/streaming/pkg/httpstream"
)

// ClientPort is the port a NATS server listens on for clients.
const ClientPort = 4222

// PickPod returns the name of a running, ready pod that backs the Service. It
// is an error when the Service is missing, has no selector, or has no ready pod,
// and the error names the Service so the person can pass the right one.
func PickPod(ctx context.Context, cs kubernetes.Interface, namespace, service string) (string, error) {
	svc, err := cs.CoreV1().Services(namespace).Get(ctx, service, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("find the NATS service %s/%s: %w", namespace, service, err)
	}
	if len(svc.Spec.Selector) == 0 {
		return "", fmt.Errorf("the NATS service %s/%s selects no pods", namespace, service)
	}
	pods, err := cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(svc.Spec.Selector).String(),
	})
	if err != nil {
		return "", fmt.Errorf("list the pods of %s/%s: %w", namespace, service, err)
	}
	for i := range pods.Items {
		if ready(&pods.Items[i]) {
			return pods.Items[i].Name, nil
		}
	}
	return "", fmt.Errorf("the NATS service %s/%s has no ready pod", namespace, service)
}

func ready(p *corev1.Pod) bool {
	if p.DeletionTimestamp != nil || p.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// Open forwards a free local port to the NATS client port of a ready pod behind
// the Service and returns the URL to connect to and a function that closes the
// forward. The forward also ends when ctx is done.
func Open(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, namespace, service string) (string, func(), error) {
	pod, err := PickPod(ctx, cs, namespace, service)
	if err != nil {
		return "", nil, err
	}
	dialer, err := dialerFor(cfg, cs, namespace, pod)
	if err != nil {
		return "", nil, err
	}
	return forward(ctx, dialer, fmt.Sprintf("0:%d", ClientPort))
}

// dialerFor is the dialer kubectl port-forward uses: WebSocket tunneling first,
// and SPDY when the API server does not offer an upgrade.
func dialerFor(cfg *rest.Config, cs kubernetes.Interface, namespace, pod string) (httpstream.Dialer, error) {
	u := cs.CoreV1().RESTClient().Post().Resource("pods").Namespace(namespace).Name(pod).SubResource("portforward").URL()
	transport, upgrader, err := spdy.RoundTripperFor(cfg)
	if err != nil {
		return nil, err
	}
	spdyDialer := spdy.NewDialerForStreaming(upgrader, &http.Client{Transport: transport}, http.MethodPost, u)
	tunnel, err := portforward.NewSPDYOverWebsocketDialerForStreaming(u, cfg)
	if err != nil {
		return nil, err
	}
	return portforward.NewFallbackDialerForStreaming(tunnel, spdyDialer, func(err error) bool {
		return httpstream.IsUpgradeFailure(err) || httpstream.IsHTTPSProxyError(err)
	}), nil
}

// forward starts the forward and waits until it is ready or has failed.
func forward(ctx context.Context, dialer httpstream.Dialer, port string) (string, func(), error) {
	ctx, cancel := context.WithCancel(ctx)
	ready := make(chan struct{})
	fw, err := portforward.NewOnAddressesForStreamingWithContext(ctx, dialer, []string{"127.0.0.1"}, []string{port}, ready, io.Discard, io.Discard)
	if err != nil {
		cancel()
		return "", nil, err
	}
	failed := make(chan error, 1)
	go func() { failed <- fw.ForwardPorts() }()
	select {
	case <-ready:
	case err := <-failed:
		cancel()
		return "", nil, errors.Join(errors.New("open the port-forward to NATS"), err)
	case <-ctx.Done():
		cancel()
		return "", nil, ctx.Err()
	}
	ports, err := fw.GetPorts()
	if err != nil || len(ports) == 0 {
		cancel()
		return "", nil, errors.Join(errors.New("read the forwarded port"), err)
	}
	u := url.URL{Scheme: "nats", Host: fmt.Sprintf("127.0.0.1:%d", ports[0].Local)}
	return u.String(), cancel, nil
}
