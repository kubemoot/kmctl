package dashboard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kubemoot/kmctl/internal/client"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
)

const ns = "kubemoot"

// svc builds a Service with the given app.kubernetes.io/name label (empty for
// none) and ports.
func svc(name, appName string, ports ...corev1.ServicePort) *corev1.Service {
	labels := map[string]string{"app.kubernetes.io/part-of": "kubemoot"}
	if appName != "" {
		labels["app.kubernetes.io/name"] = appName
	}
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels},
		Spec:       corev1.ServiceSpec{Ports: ports},
	}
}

var httpPort = corev1.ServicePort{Name: "http", Port: 80}

func find(t *testing.T, override string, objs ...runtime.Object) (Target, error) {
	t.Helper()
	cs := fake.NewClientset(objs...)
	return Find(context.Background(), cs.CoreV1().Services(ns), ns, override)
}

func TestFind_SubchartServiceByLabel(t *testing.T) {
	// The operator chart's dashboard subchart: Service kubemoot-operator-dashboard,
	// app.kubernetes.io/name=dashboard (the subchart alias).
	got, err := find(t, "",
		svc("kubemoot-operator-dashboard", "dashboard", httpPort),
		svc("kubemoot-operator", "kubemoot-operator", httpPort),
	)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if got != (Target{Name: "kubemoot-operator-dashboard", Port: "http"}) {
		t.Errorf("Find = %+v, want kubemoot-operator-dashboard on port http", got)
	}
}

func TestFind_StandaloneChartServiceByLabel(t *testing.T) {
	got, err := find(t, "", svc("kubemoot-dashboard", "kubemoot-dashboard", httpPort))
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if got.Name != "kubemoot-dashboard" {
		t.Errorf("Name = %q", got.Name)
	}
}

func TestFind_IgnoresAnotherProductsDashboard(t *testing.T) {
	other := svc("grafana-dashboard", "dashboard", httpPort)
	other.Labels["app.kubernetes.io/part-of"] = "grafana"
	_, err := find(t, "", other)
	if err == nil || !strings.Contains(err.Error(), "no Kubemoot dashboard service") {
		t.Fatalf("want a not-found error naming the fix, got %v", err)
	}
	if !strings.Contains(err.Error(), "--dashboard-service") {
		t.Errorf("the error should point at --dashboard-service: %v", err)
	}
}

func TestFind_AmbiguousLabelMatch(t *testing.T) {
	_, err := find(t, "",
		svc("a-dashboard", "dashboard", httpPort),
		svc("b-dashboard", "kubemoot-dashboard", httpPort),
	)
	if err == nil || !strings.Contains(err.Error(), "a-dashboard, b-dashboard") {
		t.Fatalf("want an ambiguity error naming both services, got %v", err)
	}
}

func TestFind_OverrideNamesTheService(t *testing.T) {
	// The override wins even when the named Service carries no dashboard labels.
	got, err := find(t, "custom",
		svc("custom", "", corev1.ServicePort{Name: "web", Port: 8080}),
		svc("kubemoot-operator-dashboard", "dashboard", httpPort),
	)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if got != (Target{Name: "custom", Port: "8080"}) {
		t.Errorf("Find = %+v, want custom on port 8080 (first port when none is named http)", got)
	}
}

func TestFind_OverrideMissing(t *testing.T) {
	_, err := find(t, "absent")
	if err == nil || !strings.Contains(err.Error(), "kubemoot/absent") {
		t.Fatalf("want an error naming the missing service, got %v", err)
	}
}

func TestFind_ServiceWithoutPorts(t *testing.T) {
	_, err := find(t, "", svc("kubemoot-operator-dashboard", "dashboard"))
	if err == nil || !strings.Contains(err.Error(), "no ports") {
		t.Fatalf("want a no-ports error, got %v", err)
	}
}

func TestFind_ListError(t *testing.T) {
	cs := fake.NewClientset()
	cs.PrependReactor("list", "services", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden")
	})
	_, err := Find(context.Background(), cs.CoreV1().Services(ns), ns, "")
	if err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("want the list error surfaced, got %v", err)
	}
}

func TestProxyPort(t *testing.T) {
	cases := map[string]struct {
		ports []corev1.ServicePort
		want  string
	}{
		"named http wins":     {[]corev1.ServicePort{{Name: "metrics", Port: 9090}, httpPort}, "http"},
		"single unnamed port": {[]corev1.ServicePort{{Port: 3000}}, "3000"},
		"first when no http":  {[]corev1.ServicePort{{Name: "web", Port: 8080}, {Name: "metrics", Port: 9090}}, "8080"},
	}
	for name, c := range cases {
		got, err := proxyPort(svc("d", "dashboard", c.ports...))
		if err != nil || got != c.want {
			t.Errorf("%s: proxyPort = %q, %v; want %q", name, got, err, c.want)
		}
	}
}

// openAt is a ServiceOpener that reaches a local server, as a port-forward would,
// and records what it was asked to open.
type opened struct {
	namespace, service, port string
	stopped                  bool
}

func openAt(t *testing.T, handler http.HandlerFunc, o *opened) client.ServiceOpener {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return func(_ context.Context, namespace, service, port string) (rest.Interface, func(), error) {
		o.namespace, o.service, o.port = namespace, service, port
		rc, err := rest.UnversionedRESTClientFor(&rest.Config{
			Host:          srv.URL,
			ContentConfig: rest.ContentConfig{NegotiatedSerializer: scheme.Codecs.WithoutConversion()},
		})
		return rc, func() { o.stopped = true }, err
	}
}

func artifactHandler(artifact string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dashboard/api/kubemoot/crewfitnesssuites/crew-ns/suite1/artifact" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(artifact))
	}
}

func dashboardServices() corev1client.ServiceInterface {
	return fake.NewClientset(svc("kubemoot-operator-dashboard", "dashboard", httpPort)).CoreV1().Services(ns)
}

func TestDownloadArtifact_ThroughDiscoveredService(t *testing.T) {
	var o opened
	data, err := DownloadArtifact(context.Background(), dashboardServices(), openAt(t, artifactHandler("XLSX-BYTES"), &o), ns, "", "crew-ns", "suite1")
	if err != nil {
		t.Fatalf("DownloadArtifact: %v", err)
	}
	if string(data) != "XLSX-BYTES" {
		t.Errorf("data = %q", data)
	}
	if o.namespace != ns || o.service != "kubemoot-operator-dashboard" || o.port != "http" {
		t.Errorf("opened %+v, want kubemoot/kubemoot-operator-dashboard:http", o)
	}
	if !o.stopped {
		t.Error("the connection to the dashboard was left open")
	}
}

func TestDownloadArtifact_FindErrorOpensNothing(t *testing.T) {
	var o opened
	_, err := DownloadArtifact(context.Background(), dashboardServices(), openAt(t, artifactHandler("unused"), &o), ns, "absent", "crew-ns", "suite1")
	if err == nil {
		t.Fatal("want an error for a missing override service")
	}
	if o.service != "" {
		t.Errorf("nothing may be opened after a failed lookup, opened %+v", o)
	}
}

func TestDownloadArtifact_OpenErrorNamesTheService(t *testing.T) {
	open := func(context.Context, string, string, string) (rest.Interface, func(), error) {
		return nil, nil, errors.New("pods/portforward is forbidden")
	}
	_, err := DownloadArtifact(context.Background(), dashboardServices(), open, ns, "", "crew-ns", "suite1")
	if err == nil || !strings.Contains(err.Error(), "kubemoot/kubemoot-operator-dashboard") || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("want the service and the cause named, got %v", err)
	}
}

func TestDownloadArtifact_DownloadErrorNamesTheSuite(t *testing.T) {
	var o opened
	_, err := DownloadArtifact(context.Background(), dashboardServices(), openAt(t, artifactHandler("unused"), &o), ns, "", "crew-ns", "no-such-suite")
	if err == nil || !strings.Contains(err.Error(), `suite "no-such-suite"`) {
		t.Fatalf("want a download error naming the suite, got %v", err)
	}
	if !o.stopped {
		t.Error("the connection to the dashboard was left open after a failed download")
	}
}
