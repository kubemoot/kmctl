package client

import (
	"errors"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
