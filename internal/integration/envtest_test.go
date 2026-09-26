//go:build integration

package integration

import (
	"context"
	"os"
	"testing"

	"github.com/javajon-homelab/kmctl/internal/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// TestEnvtest_CrewApplyListGet starts an ephemeral apiserver with the CURRENT
// Kubemoot CRDs (no operator, no homelab cluster) and exercises kmctl's real
// client paths against them - so create/list/get drift surfaces here.
func TestEnvtest_CrewApplyListGet(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS not set; run: make setup-envtest")
	}
	dir := crdDir(t)

	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{dir},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("envtest start: %v", err)
	}
	t.Cleanup(func() { _ = env.Stop() })

	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("dynamic client: %v", err)
	}
	ctx := context.Background()

	crew := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kubemoot.ai/v1alpha1",
		"kind":       "Crew",
		"metadata":   map[string]any{"name": "drift-demo", "namespace": "default"},
		"spec":       map[string]any{"description": "drift test"},
	}}
	if _, err := dc.Resource(resource.Crew.GVR()).Namespace("default").Create(ctx, crew, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create Crew against the real CRD (schema/GVR drift?): %v", err)
	}

	list, err := resource.List(ctx, dc, resource.Crew, "default", false)
	if err != nil {
		t.Fatalf("list crews: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("list returned %d crews, want 1", len(list.Items))
	}

	got, err := resource.Get(ctx, dc, resource.Crew, "default", "drift-demo")
	if err != nil {
		t.Fatalf("get crew: %v", err)
	}
	if got.GetName() != "drift-demo" {
		t.Errorf("get name = %q, want drift-demo", got.GetName())
	}

	if err := dc.Resource(resource.Crew.GVR()).Namespace("default").Delete(ctx, "drift-demo", metav1.DeleteOptions{}); err != nil {
		t.Errorf("delete crew: %v", err)
	}
}
