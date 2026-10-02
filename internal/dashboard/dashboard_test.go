package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
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

// fakeAPIServer serves the two calls DownloadArtifact makes: the labelled
// Service list and the service-proxy GET of the artifact.
func fakeAPIServer(t *testing.T, artifact string) (kubernetes.Interface, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	seen := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), paths...)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/api/v1/namespaces/kubemoot/services":
			if r.URL.Query().Get("labelSelector") != Selector {
				http.Error(w, "unexpected selector", http.StatusBadRequest)
				return
			}
			list := corev1.ServiceList{Items: []corev1.Service{*svc("kubemoot-operator-dashboard", "dashboard", httpPort)}}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(list)
		case "/api/v1/namespaces/kubemoot/services/kubemoot-operator-dashboard:http/proxy/dashboard/api/kubemoot/crewfitnesssuites/crew-ns/suite1/artifact":
			_, _ = w.Write([]byte(artifact))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	cs, err := kubernetes.NewForConfig(&rest.Config{Host: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return cs, seen
}

func TestDownloadArtifact_ThroughDiscoveredService(t *testing.T) {
	cs, _ := fakeAPIServer(t, "XLSX-BYTES")
	data, err := DownloadArtifact(context.Background(), cs, ns, "", "crew-ns", "suite1")
	if err != nil {
		t.Fatalf("DownloadArtifact: %v", err)
	}
	if string(data) != "XLSX-BYTES" {
		t.Errorf("data = %q", data)
	}
}

func TestDownloadArtifact_FindErrorStopsBeforeProxy(t *testing.T) {
	cs, seen := fakeAPIServer(t, "unused")
	_, err := DownloadArtifact(context.Background(), cs, ns, "absent", "crew-ns", "suite1")
	if err == nil {
		t.Fatal("want an error for a missing override service")
	}
	for _, p := range seen() {
		if strings.Contains(p, "/proxy/") {
			t.Errorf("no proxy call may follow a failed lookup: %s", p)
		}
	}
}

func TestDownloadArtifact_ProxyError(t *testing.T) {
	cs, _ := fakeAPIServer(t, "unused")
	_, err := DownloadArtifact(context.Background(), cs, ns, "", "crew-ns", "no-such-suite")
	if err == nil || !strings.Contains(err.Error(), `suite "no-such-suite"`) {
		t.Fatalf("want a download error naming the suite, got %v", err)
	}
}
