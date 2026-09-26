package resource

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// fakeDynamic builds an in-memory dynamic client seeded with the given objects,
// so List/Get are exercised end to end without a cluster.
func fakeDynamic(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		Crew.GVR():          "CrewList",
		Agent.GVR():         "AgentList",
		PromptModule.GVR():  "PromptModuleList",
		ModelProvider.GVR(): "ModelProviderList",
		Model.GVR():         "ModelList",
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, objs...)
}

func crewObj(name, namespace string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kubemoot.ai/v1alpha1",
		"kind":       "Crew",
		"metadata":   map[string]any{"name": name, "namespace": namespace},
		"status":     map[string]any{"ready": true, "phase": "Running"},
	}}
}

func TestList_FakeClient(t *testing.T) {
	dc := fakeDynamic(crewObj("a", "default"), crewObj("b", "default"), crewObj("c", "other"))
	ctx := context.Background()

	inNS, err := List(ctx, dc, Crew, "default", false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(inNS.Items) != 2 {
		t.Errorf("namespaced list = %d items, want 2", len(inNS.Items))
	}

	all, err := List(ctx, dc, Crew, "", true)
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(all.Items) != 3 {
		t.Errorf("all-namespaces list = %d items, want 3", len(all.Items))
	}
}

func TestGet_FakeClient(t *testing.T) {
	dc := fakeDynamic(crewObj("demo", "default"))
	got, err := Get(context.Background(), dc, Crew, "default", "demo")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.GetName() != "demo" {
		t.Errorf("Get name = %q, want demo", got.GetName())
	}

	if _, err := Get(context.Background(), dc, Crew, "default", "missing"); err == nil {
		t.Error("expected NotFound error for missing object, got nil")
	}
}
