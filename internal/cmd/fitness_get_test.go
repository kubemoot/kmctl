package cmd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const judgedSuiteJSON = `{"apiVersion":"kubemoot.ai/v1alpha1","kind":"CrewFitnessSuite",
"metadata":{"name":"baseline","namespace":"team-a","creationTimestamp":"2026-10-01T10:00:00Z"},
"spec":{"crewRef":"demo","iterations":1,"scripts":[{"testRef":"smoke","testContent":"x"}]},
"status":{"phase":"Completed","iterationsCompleted":1,"iterationsTotal":1,"passed":1,
"scenarios":[{"name":"smoke","iterations":1,"passed":1,"meanDurationMs":2000}],
"judge":{"phase":"Complete","judged":1,"total":1,"mean":87,"scores":[{"scenario":"smoke","score":87,"reason":"matches the reference"}]}}}`

// suiteAPIServer serves one CrewFitnessSuite at its API path and records the
// paths requested, so a test proves the command read the Kubernetes API only.
func suiteAPIServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path != "/apis/kubemoot.ai/v1alpha1/namespaces/team-a/crewfitnesssuites/baseline" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(judgedSuiteJSON))
	}))
	t.Cleanup(srv.Close)
	return srv, &paths
}

// factoryFor points a factory at the test API server with an empty kubeconfig.
func factoryFor(t *testing.T, server string) *client.Factory {
	t.Helper()
	kubeconfig := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(kubeconfig, []byte("apiVersion: v1\nkind: Config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := client.NewFactory()
	ns := "team-a"
	f.ConfigFlags.KubeConfig = &kubeconfig
	f.ConfigFlags.APIServer = &server
	f.ConfigFlags.Namespace = &ns
	return f
}

func TestFitnessGetPrintsJudgeResultsFromStatus(t *testing.T) {
	srv, paths := suiteAPIServer(t)
	cmd := newFitnessCommand(factoryFor(t, srv.URL))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"get", "baseline"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("fitness get: %v\n%s", err, out.String())
	}
	for _, want := range []string{"JUDGE", "QUALITY", "Complete", "Judge: Complete, 1 of 1 scenarios judged, mean 87", "matches the reference"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	for _, p := range *paths {
		if !strings.HasPrefix(p, "/apis/kubemoot.ai/") {
			t.Errorf("fitness get must read only the CRD API, requested %s", p)
		}
	}
}

func TestPrintObjectFormats(t *testing.T) {
	obj := &unstructured.Unstructured{}
	if err := obj.UnmarshalJSON([]byte(judgedSuiteJSON)); err != nil {
		t.Fatal(err)
	}
	var yamlOut bytes.Buffer
	if err := printObject(&yamlOut, resource.CrewFitnessSuite, obj, "yaml"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(yamlOut.String(), "Judge:") || !strings.Contains(yamlOut.String(), "judged: 1") {
		t.Errorf("-o yaml prints the object only:\n%s", yamlOut.String())
	}
	var table bytes.Buffer
	if err := printObject(&table, resource.Crew, obj, ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(table.String(), "Judge:") {
		t.Errorf("a kind without details prints its row only:\n%s", table.String())
	}
	if err := printObject(&table, resource.CrewFitnessSuite, obj, "toml"); err == nil {
		t.Error("an unknown output format is an error")
	}
}

func suiteWithJudge(phase string) *unstructured.Unstructured {
	s := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kubemoot.ai/v1alpha1", "kind": "CrewFitnessSuite",
		"metadata": map[string]any{"name": "baseline", "namespace": "default"},
		"status":   map[string]any{"phase": "Completed"},
	}}
	if phase != "" {
		_ = unstructured.SetNestedField(s.Object, phase, "status", "judge", "phase")
	}
	return s
}

func TestPrintJudgeHint(t *testing.T) {
	for phase, want := range map[string]bool{"Pending": true, "Judging": true, "Complete": false, "Skipped": false, "": false} {
		var out bytes.Buffer
		if err := printJudgeHint(&out, suiteWithJudge(phase)); err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(out.String(), "kmctl fitness get baseline"); got != want {
			t.Errorf("judge %q: hint printed = %v, want %v (%q)", phase, got, want, out.String())
		}
	}
}

func TestPollPhaseReturnsTheFinishedObject(t *testing.T) {
	dc := fakeDyn(suiteWithJudge("Judging"))
	var out bytes.Buffer
	obj, err := pollPhase(context.Background(), dc, resource.CrewFitnessSuite, "default", "baseline", &out, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if phase, _, _ := unstructured.NestedString(obj.Object, "status", "judge", "phase"); phase != "Judging" {
		t.Errorf("pollPhase returned judge phase %q", phase)
	}
	if strings.TrimSpace(out.String()) != "Completed" {
		t.Errorf("progress = %q", out.String())
	}
	if _, err := pollPhase(context.Background(), dc, resource.CrewFitnessSuite, "default", "absent", &out, time.Minute); err == nil {
		t.Error("a missing suite is an error")
	}
}
