package client

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
)

type fakeGroups struct {
	names []string
	err   error
}

func (f fakeGroups) ServerGroups() (*metav1.APIGroupList, error) {
	if f.err != nil {
		return nil, f.err
	}
	gl := &metav1.APIGroupList{}
	for _, n := range f.names {
		gl.Groups = append(gl.Groups, metav1.APIGroup{Name: n})
	}
	return gl, nil
}

func TestKubemootInstalled(t *testing.T) {
	cases := []struct {
		name   string
		lister GroupLister
		want   bool
		isErr  bool
	}{
		{"present", fakeGroups{names: []string{"apps", "kubemoot.ai"}}, true, false},
		{"absent", fakeGroups{names: []string{"apps", "batch"}}, false, false},
		{"empty", fakeGroups{names: nil}, false, false},
		{"error", fakeGroups{err: errors.New("boom")}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := KubemootInstalled(tc.lister)
			if tc.isErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.isErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("KubemootInstalled = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNewFactory_HasConfigFlags(t *testing.T) {
	f := NewFactory()
	if f.ConfigFlags == nil {
		t.Fatal("expected non-nil ConfigFlags")
	}
}

func TestFactory_NamespaceFromFlag(t *testing.T) {
	// The --namespace override takes precedence over context/in-cluster
	// resolution, so this is deterministic across environments (unlike the
	// "default" fallback, which becomes the SA namespace on in-cluster runners).
	f := NewFactory()
	ns := "team-a"
	f.ConfigFlags.Namespace = &ns

	if got := f.Namespace(); got != ns {
		t.Errorf("Namespace() = %q, want %q", got, ns)
	}
}

func TestFactory_CurrentContextOverride(t *testing.T) {
	f := NewFactory()
	override := "staging"
	f.ConfigFlags.Context = &override
	if got := f.CurrentContext(); got != override {
		t.Errorf("CurrentContext() = %q, want %q", got, override)
	}
}

func TestFactory_ClientsetAndCoreRESTClient(t *testing.T) {
	f := NewFactory()
	server := "https://127.0.0.1:6443"
	empty := t.TempDir() + "/kubeconfig"
	if err := os.WriteFile(empty, []byte("apiVersion: v1\nkind: Config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.ConfigFlags.KubeConfig = &empty
	f.ConfigFlags.APIServer = &server
	cs, err := f.Clientset()
	if err != nil || cs == nil {
		t.Fatalf("Clientset = %v, %v", cs, err)
	}
}

func TestFactory_ClientsetConfigError(t *testing.T) {
	f := NewFactory()
	missing := t.TempDir() + "/no-such-kubeconfig"
	f.ConfigFlags.KubeConfig = &missing
	if _, err := f.Clientset(); err == nil {
		t.Error("a missing kubeconfig must surface as an error")
	}
	if _, _, err := f.ServiceClient(context.Background(), "ns", "svc", "http"); err == nil {
		t.Error("a missing kubeconfig must surface as an error")
	}
}

func TestServiceClient_RequestsAreRelativeToTheServiceRoot(t *testing.T) {
	var path, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()
	var gotNS, gotSvc, gotPort string
	f := NewFactory()
	f.OpenService = func(_ context.Context, ns, svc, port string) (string, func(), error) {
		gotNS, gotSvc, gotPort = ns, svc, port
		return srv.URL, func() {}, nil
	}
	rc, stop, err := f.ServiceClient(context.Background(), "team-a", "demo-discussion", "80")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	raw, err := rc.Get().Suffix("api", "v1", "discussions", "demo").DoRaw(context.Background())
	if err != nil || string(raw) != "ok" {
		t.Fatalf("DoRaw = %q, %v", raw, err)
	}
	if path != "/api/v1/discussions/demo" {
		t.Errorf("path = %q, want the Service-relative path", path)
	}
	if auth != "" {
		t.Errorf("the Kubernetes credentials reached the Service: %q", auth)
	}
	if gotNS != "team-a" || gotSvc != "demo-discussion" || gotPort != "80" {
		t.Errorf("opened %s/%s:%s", gotNS, gotSvc, gotPort)
	}
}

func TestServiceClient_OpenErrorSurfaces(t *testing.T) {
	f := NewFactory()
	boom := errors.New("pods/portforward is forbidden")
	f.OpenService = func(context.Context, string, string, string) (string, func(), error) { return "", nil, boom }
	if _, _, err := f.ServiceClient(context.Background(), "ns", "svc", "http"); !errors.Is(err, boom) {
		t.Errorf("error = %v, want the open error", err)
	}
}

func TestServiceClient_StopsTheForwardWhenTheClientCannotBeBuilt(t *testing.T) {
	f := NewFactory()
	stopped := false
	f.OpenService = func(context.Context, string, string, string) (string, func(), error) {
		return "http://[::1", func() { stopped = true }, nil
	}
	if _, _, err := f.ServiceClient(context.Background(), "ns", "svc", "http"); err == nil || !stopped {
		t.Errorf("err = %v, stopped = %v; want an error and a closed forward", err, stopped)
	}
}

// -n wins even when the kubeconfig the other flags name cannot be read: the
// namespace a command reports and acts in is the one the user asked for.
func TestFactory_NamespaceFlagHoldsWithAMissingKubeconfig(t *testing.T) {
	f := NewFactory()
	ns, missing := "team-b", filepath.Join(t.TempDir(), "no-such-kubeconfig")
	f.ConfigFlags.Namespace = &ns
	f.ConfigFlags.KubeConfig = &missing
	if got := f.Namespace(); got != ns {
		t.Errorf("Namespace() = %q, want %q", got, ns)
	}
}

// Without -n and without a readable kubeconfig the namespace is "default".
func TestFactory_NamespaceDefaultsWithAMissingKubeconfig(t *testing.T) {
	f := NewFactory()
	missing := filepath.Join(t.TempDir(), "no-such-kubeconfig")
	f.ConfigFlags.KubeConfig = &missing
	if got := f.Namespace(); got != "default" {
		t.Errorf("Namespace() = %q, want default", got)
	}
}

// noContextFactory returns a Factory whose kubeconfig has no current context.
func noContextFactory(t *testing.T) *Factory {
	t.Helper()
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	f := NewFactory()
	f.ConfigFlags.KubeConfig = &empty
	return f
}

func TestFactory_NoCurrentContextFailsWithoutANetworkCall(t *testing.T) {
	// Something may listen on client-go's localhost:8080 fallback; nothing may reach it.
	var hits atomic.Int32
	if l, err := net.Listen("tcp", "127.0.0.1:8080"); err == nil {
		srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
		_ = srv.Listener.Close()
		srv.Listener = l
		srv.Start()
		defer srv.Close()
	} else {
		t.Log("127.0.0.1:8080 is taken; asserting on the error alone")
	}
	f := noContextFactory(t)
	calls := map[string]func() error{
		"RESTConfig": func() error { _, err := f.RESTConfig(); return err },
		"Dynamic":    func() error { _, err := f.Dynamic(); return err },
		"Clientset":  func() error { _, err := f.Clientset(); return err },
		"Discovery":  func() error { _, err := f.Discovery(); return err },
		"RESTMapper": func() error { _, err := f.RESTMapper(); return err },
	}
	for name, call := range calls {
		if err := call(); !clientcmd.IsEmptyConfig(err) {
			t.Errorf("%s error = %v, want an empty-config error", name, err)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("%d requests reached localhost:8080", n)
	}
}

func TestFactory_ExplicitServerStillWorksWithoutAContext(t *testing.T) {
	f := noContextFactory(t)
	server := "https://127.0.0.1:6443"
	f.ConfigFlags.APIServer = &server
	cfg, err := f.RESTConfig()
	if err != nil || cfg.Host != server {
		t.Fatalf("RESTConfig = %v, %v; want host %s", cfg, err, server)
	}
}

func TestFactory_InClusterConfigStillWorksWithoutAContext(t *testing.T) {
	f := noContextFactory(t)
	old := inCluster
	inCluster = func() bool { return true }
	defer func() { inCluster = old }()
	if err := f.requireCluster(); err != nil {
		t.Errorf("requireCluster in a pod = %v, want nil", err)
	}
}

func TestFactory_ContextFlagSkipsTheGuard(t *testing.T) {
	f := noContextFactory(t)
	ctx := "nope"
	f.ConfigFlags.Context = &ctx
	if err := f.requireCluster(); err != nil {
		t.Errorf("requireCluster with --context = %v, want nil (the loader reports the unknown context)", err)
	}
}
