package svcforward

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	"k8s.io/streaming/pkg/httpstream"
)

// fakeConn is a streaming connection that accepts nothing and stays open until
// closed.
type fakeConn struct {
	closed chan bool
	once   sync.Once
}

func newFakeConn() *fakeConn { return &fakeConn{closed: make(chan bool)} }

func (c *fakeConn) CreateStream(http.Header) (httpstream.Stream, error) {
	return nil, errors.New("no streams in this fake")
}
func (c *fakeConn) Close() error                       { c.once.Do(func() { close(c.closed) }); return nil }
func (c *fakeConn) CloseChan() <-chan bool             { return c.closed }
func (c *fakeConn) SetIdleTimeout(time.Duration)       {}
func (c *fakeConn) RemoveStreams(...httpstream.Stream) {}

type fakeDialer struct {
	err  error
	conn *fakeConn
}

func (d fakeDialer) Dial(...string) (httpstream.Connection, string, error) {
	if d.err != nil {
		return nil, "", d.err
	}
	return d.conn, "portforward.k8s.io", nil
}

func TestForwardReturnsALocalAddressAndStopsOnClose(t *testing.T) {
	conn := newFakeConn()
	addr, stop, err := forward(context.Background(), fakeDialer{conn: conn}, "0:4222")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(addr, "127.0.0.1:") || strings.HasSuffix(addr, ":0") {
		t.Errorf("addr = %q, want 127.0.0.1:<free port>", addr)
	}
	stop()
	select {
	case <-conn.closed:
	case <-time.After(5 * time.Second):
		t.Error("the connection was not closed after stop()")
	}
}

func TestForwardReportsADialerFailure(t *testing.T) {
	boom := errors.New("pods/portforward is forbidden")
	_, _, err := forward(context.Background(), fakeDialer{err: boom}, "0:4222")
	if err == nil || !strings.Contains(err.Error(), boom.Error()) {
		t.Errorf("forward() error = %v, want the dialer error", err)
	}
}

func TestForwardGivesUpWhenTheContextIsAlreadyDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := forward(ctx, fakeDialer{conn: newFakeConn()}, "0:4222"); err == nil {
		t.Error("forward() = nil error on a cancelled context")
	}
}

func TestForwardRejectsAMalformedPort(t *testing.T) {
	if _, _, err := forward(context.Background(), fakeDialer{conn: newFakeConn()}, "nope"); err == nil {
		t.Error("forward() = nil error for a malformed port")
	}
}

func TestDialerForBuildsWithoutAServer(t *testing.T) {
	cfg := &rest.Config{Host: "https://127.0.0.1:1"}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dialerFor(cfg, cs, "nats", "nats-0"); err != nil {
		t.Errorf("dialerFor() = %v", err)
	}
}

func service(selector map[string]string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "nats", Name: "nats"},
		Spec:       corev1.ServiceSpec{Selector: selector},
	}
}

func pod(name string, phase corev1.PodPhase, ready corev1.ConditionStatus) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "nats", Name: name, Labels: map[string]string{"app": "nats"}},
		Status: corev1.PodStatus{
			Phase:      phase,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: ready}},
		},
	}
}

func TestPickPodReturnsAReadyPod(t *testing.T) {
	cs := fake.NewSimpleClientset([]runtime.Object{
		service(map[string]string{"app": "nats"}),
		pod("nats-0", corev1.PodRunning, corev1.ConditionFalse),
		pod("nats-1", corev1.PodRunning, corev1.ConditionTrue),
	}...)
	svc, got, err := PickPod(context.Background(), cs, "nats", "nats")
	if err != nil || got.Name != "nats-1" || svc.Name != "nats" {
		t.Errorf("PickPod() = %v, %v, %v, want service nats and pod nats-1", svc, got, err)
	}
}

func TestPickPodSkipsPodsThatAreNotRunning(t *testing.T) {
	cs := fake.NewSimpleClientset([]runtime.Object{
		service(map[string]string{"app": "nats"}),
		pod("nats-0", corev1.PodPending, corev1.ConditionTrue),
	}...)
	if _, _, err := PickPod(context.Background(), cs, "nats", "nats"); err == nil || !strings.Contains(err.Error(), "no ready pod") {
		t.Errorf("PickPod() error = %v, want no ready pod", err)
	}
}

func TestPickPodSkipsAPodBeingDeleted(t *testing.T) {
	p := pod("nats-0", corev1.PodRunning, corev1.ConditionTrue)
	now := metav1.NewTime(time.Now())
	p.DeletionTimestamp = &now
	cs := fake.NewSimpleClientset(service(map[string]string{"app": "nats"}), p)
	if _, _, err := PickPod(context.Background(), cs, "nats", "nats"); err == nil {
		t.Error("PickPod() chose a pod that is being deleted")
	}
}

func TestPickPodNamesAMissingService(t *testing.T) {
	_, _, err := PickPod(context.Background(), fake.NewSimpleClientset(), "nats", "nats")
	if err == nil || !strings.Contains(err.Error(), "nats/nats") {
		t.Errorf("PickPod() error = %v, want the service named", err)
	}
}

func TestPickPodRefusesAServiceWithoutASelector(t *testing.T) {
	cs := fake.NewSimpleClientset(service(nil))
	if _, _, err := PickPod(context.Background(), cs, "nats", "nats"); err == nil || !strings.Contains(err.Error(), "selects no pods") {
		t.Errorf("PickPod() error = %v, want selects no pods", err)
	}
}

func portService(ports ...corev1.ServicePort) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: "kubemoot", Name: "dash"},
		Spec:       corev1.ServiceSpec{Ports: ports},
	}
}

func podWithPort(name string, port int32) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "dash-0"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Ports: []corev1.ContainerPort{{Name: name, ContainerPort: port}},
		}}},
	}
}

func TestContainerPort(t *testing.T) {
	pod := podWithPort("web", 3000)
	svc := portService(
		corev1.ServicePort{Name: "http", Port: 80, TargetPort: intstr.FromString("web")},
		corev1.ServicePort{Name: "api", Port: 8080, TargetPort: intstr.FromInt32(9090)},
		corev1.ServicePort{Name: "plain", Port: 7000},
	)
	cases := map[string]struct {
		port string
		want int
		fail bool
	}{
		"name to a named target":     {"http", 3000, false},
		"number to a named target":   {"80", 3000, false},
		"name to a numeric target":   {"api", 9090, false},
		"no target uses the port":    {"plain", 7000, false},
		"undeclared number is as is": {"4222", 4222, false},
		"undeclared name":            {"nope", 0, true},
		"zero is a name, not a port": {"0", 0, true},
		"out of range is a name":     {"70000", 0, true},
	}
	for name, c := range cases {
		got, err := ContainerPort(svc, pod, c.port)
		if (err != nil) != c.fail || got != c.want {
			t.Errorf("%s: ContainerPort(%q) = %d, %v; want %d, fail=%v", name, c.port, got, err, c.want, c.fail)
		}
	}
}

func TestContainerPortNamedTargetMissingOnThePod(t *testing.T) {
	svc := portService(corev1.ServicePort{Name: "http", Port: 80, TargetPort: intstr.FromString("web")})
	_, err := ContainerPort(svc, podWithPort("other", 1), "http")
	if err == nil || !strings.Contains(err.Error(), `"web"`) {
		t.Errorf("ContainerPort() error = %v, want the missing container port named", err)
	}
}

func TestOpenNamesAMissingService(t *testing.T) {
	_, _, err := Open(context.Background(), &rest.Config{}, fake.NewSimpleClientset(), "kubemoot", "dash", "http")
	if err == nil || !strings.Contains(err.Error(), "kubemoot/dash") {
		t.Errorf("Open() error = %v, want the service named", err)
	}
}

func podNoReadyCondition(name string) *corev1.Pod {
	p := pod(name, corev1.PodRunning, corev1.ConditionTrue)
	p.Status.Conditions = nil
	return p
}

func TestPickPodNoneReady(t *testing.T) {
	cases := map[string][]runtime.Object{
		"ready condition false":   {pod("nats-0", corev1.PodRunning, corev1.ConditionFalse), pod("nats-1", corev1.PodRunning, corev1.ConditionFalse)},
		"no ready condition":      {podNoReadyCondition("nats-0")},
		"ready condition unknown": {pod("nats-0", corev1.PodRunning, corev1.ConditionUnknown)},
		"no pods at all":          {},
		"ready but not running":   {pod("nats-0", corev1.PodSucceeded, corev1.ConditionTrue)},
	}
	for name, pods := range cases {
		cs := fake.NewSimpleClientset(append([]runtime.Object{service(map[string]string{"app": "nats"})}, pods...)...)
		_, _, err := PickPod(context.Background(), cs, "nats", "nats")
		if !errors.Is(err, ErrNoReadyPod) {
			t.Errorf("%s: PickPod() error = %v, want ErrNoReadyPod", name, err)
		}
	}
}

func TestPickPodTakesTheLaterReadyPod(t *testing.T) {
	cs := fake.NewSimpleClientset(
		service(map[string]string{"app": "nats"}),
		pod("nats-0", corev1.PodRunning, corev1.ConditionFalse),
		podNoReadyCondition("nats-1"),
		pod("nats-2", corev1.PodPending, corev1.ConditionTrue),
		pod("nats-3", corev1.PodRunning, corev1.ConditionTrue),
	)
	_, got, err := PickPod(context.Background(), cs, "nats", "nats")
	if err != nil || got.Name != "nats-3" {
		t.Errorf("PickPod() = %v, %v, want nats-3", got, err)
	}
}

func TestPickPodSentinelForAServiceWithoutASelector(t *testing.T) {
	_, _, err := PickPod(context.Background(), fake.NewSimpleClientset(service(nil)), "nats", "nats")
	if !errors.Is(err, ErrNoPodsSelected) || errors.Is(err, ErrNoReadyPod) {
		t.Errorf("PickPod() error = %v, want ErrNoPodsSelected only", err)
	}
}

// forbiddenServer answers every request, the WebSocket and the SPDY upgrade
// alike, with the 403 Status the API server sends when pods/portforward is refused.
func forbiddenServer(t *testing.T) *rest.Config {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","message":"pods \"nats-0\" is forbidden: User \"dev\" cannot create resource \"pods/portforward\" in API group \"\" in the namespace \"nats\"","reason":"Forbidden","details":{"name":"nats-0","kind":"pods"},"code":403}`)
	}))
	t.Cleanup(srv.Close)
	return &rest.Config{Host: srv.URL}
}

func TestForwardForbiddenByTheAPIServerIsForbidden(t *testing.T) {
	cfg := forbiddenServer(t)
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	dialer, err := dialerFor(cfg, cs, "nats", "nats-0")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = forward(context.Background(), dialer, "0:4222")
	if !apierrors.IsForbidden(err) {
		t.Fatalf("forward() error = %v, want apierrors.IsForbidden", err)
	}
	if !strings.Contains(err.Error(), "pods/portforward") {
		t.Errorf("the error lost the server's message: %v", err)
	}
}

func TestClassify(t *testing.T) {
	status := &apierrors.StatusError{ErrStatus: metav1.Status{Code: 403, Reason: metav1.StatusReasonForbidden, Message: "no"}}
	cases := map[string]struct {
		err           error
		wantForbidden bool
	}{
		"websocket upgrade failure carrying a 403 Status": {&httpstream.UpgradeFailureError{Cause: status}, true},
		"spdy upgrade text with a forbidden message":      {errors.New(`unable to upgrade connection: pods "p" is forbidden: User "u" cannot create`), true},
		"spdy upgrade text with another message":          {errors.New("unable to upgrade connection: pod does not exist"), false},
		"a forbidden word outside an upgrade":             {errors.New(`pods "p" is forbidden: x`), false},
		"a dial failure":                                  {errors.New("connection refused"), false},
		"a not found Status":                              {&apierrors.StatusError{ErrStatus: metav1.Status{Code: 404, Reason: metav1.StatusReasonNotFound}}, false},
	}
	for name, c := range cases {
		if got := apierrors.IsForbidden(classify(c.err)); got != c.wantForbidden {
			t.Errorf("%s: IsForbidden(classify(err)) = %v, want %v", name, got, c.wantForbidden)
		}
	}
}
