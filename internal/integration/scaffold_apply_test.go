//go:build integration

package integration

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kubemoot/kmctl/internal/manifest"
	"github.com/kubemoot/kmctl/internal/scaffold"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/restmapper"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/yaml"
)

// requireEnv makes a missing prerequisite (envtest assets, CRDs, helm) a failure
// instead of a skip. The release gates set it, so a gate that cannot run never
// passes silently.
const requireEnv = "KMCTL_REQUIRE_SCAFFOLD_APPLIES"

const fieldManager = "kmctl-scaffold-applies"

// notRunnable skips the test, or fails it where the gate is required.
func notRunnable(t *testing.T, format string, args ...any) {
	t.Helper()
	msg := fmt.Sprintf(format, args...)
	if os.Getenv(requireEnv) != "" {
		t.Fatalf("%s (%s is set, so this is a failure)", msg, requireEnv)
	}
	t.Skip(msg)
}

// applier applies objects to the envtest API server the way kmctl apply does:
// server-side apply, here with strict field validation so a field the CRD does
// not declare is an error instead of being dropped.
type applier struct {
	dc     dynamic.Interface
	mapper meta.RESTMapper
}

func startAPIServer(t *testing.T) *applier {
	t.Helper()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		notRunnable(t, "KUBEBUILDER_ASSETS not set; run: make setup-envtest")
	}
	dir := os.Getenv("KMCTL_CRD_DIR")
	if dir == "" {
		notRunnable(t, "KMCTL_CRD_DIR not set; point it at the Kubemoot CRD bases dir")
	}
	env := &envtest.Environment{CRDDirectoryPaths: []string{dir}, ErrorIfCRDPathMissing: true}
	cfg, err := env.Start()
	if err != nil {
		t.Fatalf("envtest start: %v", err)
	}
	t.Cleanup(func() { _ = env.Stop() })
	dc, err := dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("dynamic client: %v", err)
	}
	disc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		t.Fatalf("discovery client: %v", err)
	}
	return &applier{dc: dc, mapper: restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(disc))}
}

func (a *applier) namespace(ctx context.Context, t *testing.T, name string) {
	t.Helper()
	ns := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": name},
	}}
	gvr := schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
	if _, err := a.dc.Resource(gvr).Create(ctx, ns, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create namespace %s: %v", name, err)
	}
}

// resourceFor is the dynamic client for the object's kind, in its namespace when
// the kind is namespaced.
func (a *applier) resourceFor(obj *unstructured.Unstructured, ns string) (dynamic.ResourceInterface, error) {
	gvk := obj.GroupVersionKind()
	m, err := a.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, fmt.Errorf("the API server does not serve %s: %w", gvk, err)
	}
	if m.Scope.Name() == meta.RESTScopeNameNamespace {
		return a.dc.Resource(m.Resource).Namespace(ns), nil
	}
	return a.dc.Resource(m.Resource), nil
}

// apply applies obj with strict validation and reads it back.
func (a *applier) apply(ctx context.Context, obj *unstructured.Unstructured, ns string) (*unstructured.Unstructured, error) {
	ri, err := a.resourceFor(obj, ns)
	if err != nil {
		return nil, err
	}
	body, err := obj.MarshalJSON()
	if err != nil {
		return nil, err
	}
	force := true
	if _, err := ri.Patch(ctx, obj.GetName(), "application/apply-patch+yaml", body,
		metav1.PatchOptions{FieldManager: fieldManager, FieldValidation: "Strict", Force: &force}); err != nil {
		return nil, err
	}
	return ri.Get(ctx, obj.GetName(), metav1.GetOptions{})
}

// kept reports where the applied value is not in the stored one. Every field the
// object sets must come back: a map by each of its keys, a list element by element.
// The server may add fields (defaults), never drop or change one.
func kept(path string, applied, stored any) []string {
	switch a := applied.(type) {
	case map[string]any:
		return keptMap(path, a, stored)
	case []any:
		return keptList(path, a, stored)
	}
	if reflect.DeepEqual(applied, stored) || fmt.Sprint(applied) == fmt.Sprint(stored) {
		return nil
	}
	return []string{fmt.Sprintf("%s: applied %v, stored %v", path, applied, stored)}
}

func keptMap(path string, applied map[string]any, stored any) []string {
	s, ok := stored.(map[string]any)
	if !ok {
		return []string{path + ": stored as " + fmt.Sprintf("%T", stored)}
	}
	var lost []string
	for k, v := range applied {
		sv, present := s[k]
		if !present {
			lost = append(lost, path+"."+k+": pruned")
			continue
		}
		lost = append(lost, kept(path+"."+k, v, sv)...)
	}
	return lost
}

func keptList(path string, applied []any, stored any) []string {
	s, ok := stored.([]any)
	if !ok || len(s) != len(applied) {
		return []string{fmt.Sprintf("%s: list of %d came back as %v", path, len(applied), stored)}
	}
	var lost []string
	for i := range applied {
		lost = append(lost, kept(fmt.Sprintf("%s[%d]", path, i), applied[i], s[i])...)
	}
	return lost
}

// lostFields compares everything but metadata and status.
func lostFields(applied, stored *unstructured.Unstructured) []string {
	var lost []string
	for k, v := range applied.Object {
		if k == "metadata" || k == "status" || k == "apiVersion" || k == "kind" {
			continue
		}
		sv, present := stored.Object[k]
		if !present {
			lost = append(lost, k+": pruned")
			continue
		}
		lost = append(lost, kept(k, v, sv)...)
	}
	return lost
}

// scaffoldCase is one thing `kmctl create` makes: a bundle or a chart, of some size.
type scaffoldCase struct {
	name    string
	members int
	chart   bool
	values  []string // helm --set, chart only
}

func (c scaffoldCase) options(dir, ns string) scaffold.Options {
	return scaffold.Options{Name: "starter-guide", Members: c.members, OutputDir: dir, Chart: c.chart, Namespace: ns}
}

// objects is what kmctl create writes for the case, as the objects a user's
// next step applies: the bundle's files read as written, the chart rendered by
// helm template, and the fitness suite the scaffold keeps beside either.
func (c scaffoldCase) objects(t *testing.T, ns string) []unstructured.Unstructured {
	t.Helper()
	dir := t.TempDir()
	if _, err := scaffold.Write(c.options(dir, ns)); err != nil {
		t.Fatalf("scaffold.Write: %v", err)
	}
	root := filepath.Join(dir, "starter-guide")
	if c.chart {
		out := helmTemplate(t, root, ns, c.values)
		objs, err := manifest.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatalf("helm template output is not manifests: %v", err)
		}
		suite, err := manifest.FromPath(filepath.Join(root, "fitness", "fitness.yaml"), nil)
		if err != nil {
			t.Fatalf("read the fitness suite: %v", err)
		}
		return append(objs, suite...)
	}
	return bundleObjects(t, root)
}

func bundleObjects(t *testing.T, root string) []unstructured.Unstructured {
	t.Helper()
	var objs []unstructured.Unstructured
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(p) != ".yaml" {
			return err
		}
		got, err := manifest.FromPath(p, nil)
		objs = append(objs, got...)
		return err
	})
	if err != nil {
		t.Fatalf("read the bundle: %v", err)
	}
	return objs
}

func helmTemplate(t *testing.T, chartDir, ns string, set []string) []byte {
	t.Helper()
	helm, err := exec.LookPath("helm")
	if err != nil {
		notRunnable(t, "helm not on PATH; the chart half of the test renders with helm template")
	}
	args := []string{"template", "starter-guide", chartDir, "--namespace", ns}
	for _, s := range set {
		args = append(args, "--set", s)
	}
	var stderr bytes.Buffer
	cmd := exec.Command(helm, args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helm %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return out
}

func scaffoldCases() []scaffoldCase {
	var cases []scaffoldCase
	for m := 1; m <= scaffold.MaxMembers; m++ {
		cases = append(cases,
			scaffoldCase{name: fmt.Sprintf("bundle-%d", m), members: m},
			scaffoldCase{name: fmt.Sprintf("chart-%d", m), members: m, chart: true},
		)
	}
	// The chart's other RBAC branch: read-only access across the cluster.
	return append(cases, scaffoldCase{name: "chart-1-cluster-wide", members: 1, chart: true, values: []string{"access.clusterWide=true"}})
}

// TestScaffoldApplies proves that what `kmctl create` writes is accepted by the
// Kubemoot API it targets: every object of every size, bundle and chart, applies
// with strict field validation (an unknown or renamed field fails) and reads back
// with every field it set. It runs against the CRDs in KMCTL_CRD_DIR, with no
// operator and no cluster.
func TestScaffoldApplies(t *testing.T) {
	a := startAPIServer(t)
	ctx := context.Background()
	t.Run("rejects an undeclared field", func(t *testing.T) { rejectsUndeclared(ctx, t, a) })
	for _, c := range scaffoldCases() {
		t.Run(c.name, func(t *testing.T) {
			ns := "t-" + c.name
			a.namespace(ctx, t, ns)
			objs := c.objects(t, ns)
			if len(objs) == 0 {
				t.Fatal("the scaffold wrote no objects")
			}
			for i := range objs {
				applyAndCheck(ctx, t, a, &objs[i], ns)
			}
		})
	}
}

func applyAndCheck(ctx context.Context, t *testing.T, a *applier, obj *unstructured.Unstructured, ns string) {
	t.Helper()
	id := obj.GetKind() + "/" + obj.GetName()
	stored, err := a.apply(ctx, obj, ns)
	if err != nil {
		t.Errorf("%s was refused by the API server: %v", id, err)
		return
	}
	if lost := lostFields(obj, stored); len(lost) > 0 {
		t.Errorf("%s came back without what the scaffold wrote:\n  %s", id, strings.Join(lost, "\n  "))
	}
}

// rejectsUndeclared is the control: the strict apply used above does refuse a
// field a CRD does not declare, so a pass means the scaffold names none.
func rejectsUndeclared(ctx context.Context, t *testing.T, a *applier) {
	a.namespace(ctx, t, "t-control")
	var crew unstructured.Unstructured
	if err := yaml.Unmarshal([]byte(`
apiVersion: kubemoot.ai/v1alpha1
kind: Crew
metadata:
  name: control
spec:
  description: a field the Crew CRD does not declare follows
  aFieldNoCRDDeclares: true
`), &crew.Object); err != nil {
		t.Fatal(err)
	}
	if _, err := a.apply(ctx, &crew, "t-control"); err == nil {
		t.Fatal("a Crew with an undeclared field was accepted: strict validation is not in effect, so this test proves nothing")
	}
}

// TestKeptFindsWhatTheServerDropped checks the read-back comparison itself: a
// pruned key, a changed value and a shortened list are found; a field the server
// added is not.
func TestKeptFindsWhatTheServerDropped(t *testing.T) {
	applied := map[string]any{"a": "x", "list": []any{"1", "2"}, "m": map[string]any{"k": int64(3)}}
	same := map[string]any{"a": "x", "list": []any{"1", "2"}, "m": map[string]any{"k": float64(3), "defaulted": true}}
	if lost := kept("spec", applied, same); len(lost) != 0 {
		t.Errorf("an unchanged object (plus a default) reported lost: %v", lost)
	}
	for name, stored := range map[string]map[string]any{
		"pruned key":      {"list": []any{"1", "2"}, "m": map[string]any{"k": int64(3)}},
		"changed value":   {"a": "y", "list": []any{"1", "2"}, "m": map[string]any{"k": int64(3)}},
		"shorter list":    {"a": "x", "list": []any{"1"}, "m": map[string]any{"k": int64(3)}},
		"wrong nesting":   {"a": "x", "list": []any{"1", "2"}, "m": "flat"},
		"list not a list": {"a": "x", "list": "1,2", "m": map[string]any{"k": int64(3)}},
	} {
		if lost := kept("spec", applied, stored); len(lost) == 0 {
			t.Errorf("%s: nothing reported lost", name)
		}
	}
}
