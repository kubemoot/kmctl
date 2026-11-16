// Package svcforward reaches a Service in the cluster from the machine kmctl runs
// on, with the credentials of the active kubeconfig context. It opens a
// port-forward to a ready pod behind the Service, as kubectl port-forward does,
// and gives back a local address. The traffic goes through the kubelet, so a
// default-deny NetworkPolicy does not block it the way it blocks the API server's
// service proxy, and it carries any protocol, not only HTTP. Nothing is installed
// or changed in the cluster.
package svcforward

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
	"k8s.io/streaming/pkg/httpstream"
)

// ErrNoPodsSelected means the Service has no selector, so no pod backs it.
var ErrNoPodsSelected = errors.New("selects no pods")

// ErrNoReadyPod means no pod behind the Service is running and ready.
var ErrNoReadyPod = errors.New("no ready pod")

// PickPod returns the Service and the name of a running, ready pod that backs it.
// It is an error when the Service is missing, has no selector, or has no ready
// pod, and the error names the Service so the person can pass the right one.
func PickPod(ctx context.Context, cs kubernetes.Interface, namespace, service string) (*corev1.Service, *corev1.Pod, error) {
	svc, err := cs.CoreV1().Services(namespace).Get(ctx, service, metav1.GetOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("find the service %s/%s: %w", namespace, service, err)
	}
	if len(svc.Spec.Selector) == 0 {
		return nil, nil, fmt.Errorf("the service %s/%s %w", namespace, service, ErrNoPodsSelected)
	}
	pods, err := cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(svc.Spec.Selector).String(),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("list the pods of %s/%s: %w", namespace, service, err)
	}
	for i := range pods.Items {
		if ready(&pods.Items[i]) {
			return svc, &pods.Items[i], nil
		}
	}
	return nil, nil, fmt.Errorf("the service %s/%s has %w", namespace, service, ErrNoReadyPod)
}

// ContainerPort turns a Service port, given by name or number, into the port the
// pod listens on: the port's targetPort, which is a number or the name of a
// container port. A number the Service does not declare is taken as the container
// port itself, as kubectl port-forward does for a pod; a name it does not declare
// is an error.
func ContainerPort(svc *corev1.Service, pod *corev1.Pod, port string) (int, error) {
	number, isNumber := parsePort(port)
	for _, p := range svc.Spec.Ports {
		if p.Name != port && (!isNumber || int(p.Port) != number) {
			continue
		}
		return targetPort(p, pod)
	}
	if isNumber {
		return number, nil
	}
	return 0, fmt.Errorf("the service %s/%s has no port named %q", svc.Namespace, svc.Name, port)
}

func parsePort(port string) (int, bool) {
	n, err := strconv.Atoi(port)
	return n, err == nil && n > 0 && n < 65536
}

// targetPort is the pod's port behind a Service port.
func targetPort(p corev1.ServicePort, pod *corev1.Pod) (int, error) {
	switch {
	case p.TargetPort.Type == intstr.String && p.TargetPort.StrVal != "":
		for _, c := range pod.Spec.Containers {
			for _, cp := range c.Ports {
				if cp.Name == p.TargetPort.StrVal {
					return int(cp.ContainerPort), nil
				}
			}
		}
		return 0, fmt.Errorf("pod %s has no container port named %q", pod.Name, p.TargetPort.StrVal)
	case p.TargetPort.IntVal > 0:
		return int(p.TargetPort.IntVal), nil
	}
	return int(p.Port), nil
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

// Open forwards a free local port to a Service port (a name or a number) on a
// ready pod behind the Service. It returns the local address, host:port, and a
// function that closes the forward. The forward also ends when ctx is done.
func Open(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, namespace, service, port string) (string, func(), error) {
	svc, pod, err := PickPod(ctx, cs, namespace, service)
	if err != nil {
		return "", nil, err
	}
	remote, err := ContainerPort(svc, pod, port)
	if err != nil {
		return "", nil, err
	}
	dialer, err := dialerFor(cfg, cs, namespace, pod.Name)
	if err != nil {
		return "", nil, err
	}
	return forward(ctx, dialer, fmt.Sprintf("0:%d", remote))
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
		return "", nil, errors.Join(errors.New("open the port-forward"), classify(err))
	case <-ctx.Done():
		cancel()
		return "", nil, ctx.Err()
	}
	ports, err := fw.GetPorts()
	if err != nil || len(ports) == 0 {
		cancel()
		return "", nil, errors.Join(errors.New("read the forwarded port"), err)
	}
	return fmt.Sprintf("127.0.0.1:%d", ports[0].Local), cancel, nil
}

// classify makes a refused upgrade answer apierrors.IsForbidden when the API
// server said 403. client-go reports it three ways: the WebSocket dialer wraps the
// server's Status in an UpgradeFailureError that does not unwrap, and the SPDY
// dialer (which the fallback tries next, so its error is the one that reaches
// us) keeps only the Status message, as "unable to upgrade connection: <message>"
// inside port-forward's own "error upgrading connection" wrapper.
// Any other error is returned as it is.
func classify(err error) error {
	var upgrade *httpstream.UpgradeFailureError
	if errors.As(err, &upgrade) {
		err = upgrade.Cause
	}
	if apierrors.IsForbidden(err) {
		return err
	}
	if _, msg, ok := strings.Cut(err.Error(), spdyUpgradePrefix); ok && strings.Contains(msg, forbiddenMarker) {
		return &apierrors.StatusError{ErrStatus: metav1.Status{
			Status: metav1.StatusFailure, Code: http.StatusForbidden, Reason: metav1.StatusReasonForbidden, Message: msg,
		}}
	}
	return err
}

// spdyUpgradePrefix and forbiddenMarker are the words of the SPDY dialer's error
// and of the API server's 403 message, which the SPDY dialer passes on as text.
const (
	spdyUpgradePrefix = "unable to upgrade connection: "
	forbiddenMarker   = " is forbidden: "
)
