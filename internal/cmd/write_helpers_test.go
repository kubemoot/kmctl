package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/kubemoot/kmctl/internal/client"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/spf13/cobra"
)

const writeNS = "crew-demo"

var (
	suiteGVR  = schema.GroupVersionResource{Group: "kubemoot.ai", Version: "v1alpha1", Resource: "crewfitnesssuites"}
	reportGVR = schema.GroupVersionResource{Group: "kubemoot.ai", Version: "v1alpha1", Resource: "mcpserverreports"}
)

// writeDyn is a fake dynamic client for the commands that patch a resource. Every
// patch it receives is recorded, and one that carries a dry-run option is answered
// without being stored, as the API server answers it.
type writeDyn struct {
	*dynamicfake.FakeDynamicClient
	patches []k8stesting.PatchActionImpl
}

func newWriteDyn(objs ...runtime.Object) *writeDyn {
	listKinds := map[schema.GroupVersionResource]string{suiteGVR: "CrewFitnessSuiteList", reportGVR: "MCPServerReportList"}
	w := &writeDyn{FakeDynamicClient: dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), listKinds, objs...)}
	w.PrependReactor("patch", "*", func(a k8stesting.Action) (bool, runtime.Object, error) {
		p := a.(k8stesting.PatchActionImpl)
		w.patches = append(w.patches, p)
		if len(p.PatchOptions.DryRun) == 0 {
			return false, nil, nil
		}
		obj, err := w.Tracker().Get(p.Resource, p.Namespace, p.Name)
		return true, obj, err
	})
	return w
}

// deny makes every patch fail as an API server does for a caller without the right.
func (w *writeDyn) deny(resource string) {
	w.PrependReactor("patch", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		msg := `User "jo" cannot patch resource "` + resource + `" in API group "kubemoot.ai" in the namespace "` + writeNS + `"`
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "kubemoot.ai", Resource: resource}, "x", errors.New(msg))
	})
}

func (w *writeDyn) specOf(t *testing.T, gvr schema.GroupVersionResource, name string) map[string]any {
	t.Helper()
	u, err := w.Resource(gvr).Namespace(writeNS).Get(t.Context(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := u.Object["spec"].(map[string]any)
	return spec
}

func suiteObj(phase string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kubemoot.ai/v1alpha1", "kind": "CrewFitnessSuite",
		"metadata": map[string]any{"name": "starter", "namespace": writeNS},
		"spec":     map[string]any{"crewRef": "demo"},
		"status":   map[string]any{"phase": phase},
	}}
}

func reportObj() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kubemoot.ai/v1alpha1", "kind": "MCPServerReport",
		"metadata": map[string]any{"name": "k8s", "namespace": writeNS},
		"spec":     map[string]any{"adminVerdict": "use", "adminNotes": "old", "adminAuthor": "ann"},
	}}
}

// runWrite runs one kmctl command line with kubeconfig-free fakes: build wires the
// command under test into root, and stdin is what a person would type.
func runWrite(t *testing.T, stdin string, build func(root *cobra.Command, f *client.Factory), args ...string) (stdout, stderr string, err error) {
	t.Helper()
	f := client.NewFactory()
	ns := writeNS
	f.ConfigFlags.Namespace = &ns
	root := &cobra.Command{Use: "kmctl", SilenceUsage: true, SilenceErrors: true}
	build(root, f)
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(args)
	err = root.Execute()
	return out.String(), errOut.String(), err
}

// explained is what kmctl prints for err.
func explained(t *testing.T, err error) string {
	t.Helper()
	f := client.NewFactory()
	e := explain(err, &cobra.Command{Use: "x"}, f)
	return e.Message + " | " + e.Hint
}
