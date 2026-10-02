package client

import (
	"errors"
	"os"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
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
	rc, err := f.CoreRESTClient()
	if err != nil || rc == nil {
		t.Fatalf("CoreRESTClient = %v, %v", rc, err)
	}
}

func TestFactory_ClientsetConfigError(t *testing.T) {
	f := NewFactory()
	missing := t.TempDir() + "/no-such-kubeconfig"
	f.ConfigFlags.KubeConfig = &missing
	if _, err := f.Clientset(); err == nil {
		t.Error("a missing kubeconfig must surface as an error")
	}
	if _, err := f.CoreRESTClient(); err == nil {
		t.Error("a missing kubeconfig must surface as an error")
	}
}

func TestServiceProxy_Path(t *testing.T) {
	cs, err := kubernetes.NewForConfig(&rest.Config{Host: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	got := ServiceProxy(cs.CoreV1().RESTClient().Get(), "ns", "svc", "http").Suffix("a", "b").URL().Path
	if want := "/api/v1/namespaces/ns/services/svc:http/proxy/a/b"; got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
}
