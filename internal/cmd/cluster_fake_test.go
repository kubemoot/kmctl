package cmd

import (
	"context"
	"io"
	"testing"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

var crewGVR = schema.GroupVersionResource{Group: "kubemoot.ai", Version: "v1alpha1", Resource: "crews"}

func fakeDyn(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{crewGVR: "CrewList"}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, objs...)
}

// fakeDynApplyOK returns a fake dynamic client whose server-side apply (a PATCH
// with ApplyPatchType) succeeds by echoing back an object carrying the patched
// name and namespace. The stock fake client cannot strategic-merge unstructured
// SSA patches, so a reactor stands in for the apiserver's apply.
func fakeDynApplyOK() *dynamicfake.FakeDynamicClient {
	dc := fakeDyn()
	dc.PrependReactor("patch", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		pa := action.(k8stesting.PatchAction)
		if pa.GetPatchType() != types.ApplyPatchType {
			return false, nil, nil // only stand in for server-side apply
		}
		return true, &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "kubemoot.ai/v1alpha1",
			"kind":       "Crew",
			"metadata":   map[string]any{"name": pa.GetName(), "namespace": pa.GetNamespace()},
		}}, nil
	})
	return dc
}

func staticMapper() meta.RESTMapper {
	m := meta.NewDefaultRESTMapper([]schema.GroupVersion{{Group: "kubemoot.ai", Version: "v1alpha1"}})
	m.Add(schema.GroupVersionKind{Group: "kubemoot.ai", Version: "v1alpha1", Kind: "Crew"}, meta.RESTScopeNamespace)
	return m
}

func newCrew(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kubemoot.ai/v1alpha1",
		"kind":       "Crew",
		"metadata":   map[string]any{"name": name, "namespace": "default"},
	}}
}

func TestDeleteByKindName_FakeClient(t *testing.T) {
	dc := fakeDyn(newCrew("demo"))
	ctx := context.Background()

	if err := deleteByKindName(ctx, dc, staticMapper(), "crew", "demo", "default", io.Discard); err != nil {
		t.Fatalf("deleteByKindName: %v", err)
	}
	_, err := dc.Resource(crewGVR).Namespace("default").Get(ctx, "demo", metav1.GetOptions{})
	if !errors.IsNotFound(err) {
		t.Errorf("expected NotFound after delete, got %v", err)
	}
}

func TestDeleteByKindName_UnknownKind(t *testing.T) {
	err := deleteByKindName(context.Background(), fakeDyn(), staticMapper(), "nonsense", "x", "default", io.Discard)
	if err == nil {
		t.Fatal("expected error for unknown kind, got nil")
	}
}
