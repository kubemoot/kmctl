package resource

import (
	"bytes"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestKind_GVR(t *testing.T) {
	gvr := Crew.GVR()
	if gvr.Group != "kubemoot.ai" || gvr.Version != "v1alpha1" || gvr.Resource != "crews" {
		t.Errorf("Crew GVR = %v, want kubemoot.ai/v1alpha1/crews", gvr)
	}
	if Agent.GVR().Resource != "agents" {
		t.Errorf("Agent GVR resource = %q, want agents", Agent.GVR().Resource)
	}
	wantResource := map[*Kind]string{
		&PromptModule:     "promptmodules",
		&ModelProvider:    "modelproviders",
		&Model:            "models",
		&CrewFitnessSuite: "crewfitnesssuites",
		&CrewFitness:      "crewfitnesses",
	}
	for k, want := range wantResource {
		if got := k.GVR().Resource; got != want {
			t.Errorf("%s GVR resource = %q, want %q", k.Singular, got, want)
		}
	}
}

func TestCell(t *testing.T) {
	obj := map[string]any{
		"metadata": map[string]any{"name": "homelab"},
		"spec":     map[string]any{"type": "specialist"},
		"status": map[string]any{
			"ready":      true,
			"agentCount": int64(3),
		},
	}
	cases := []struct {
		col  Column
		want string
	}{
		{Column{"NAME", "metadata.name"}, "homelab"},
		{Column{"TYPE", "spec.type"}, "specialist"},
		{Column{"READY", "status.ready"}, "true"},
		{Column{"AGENTS", "status.agentCount"}, "3"},
		{Column{"PHASE", "status.phase"}, none},
		{Column{"MISSING", "status.deep.missing"}, none},
	}
	for _, tc := range cases {
		if got := Cell(obj, tc.col); got != tc.want {
			t.Errorf("Cell(%s) = %q, want %q", tc.col.Path, got, tc.want)
		}
	}
}

func TestRenderTable(t *testing.T) {
	items := []unstructured.Unstructured{
		{Object: map[string]any{
			"metadata": map[string]any{"name": "homelab"},
			"status":   map[string]any{"ready": true, "phase": "Running", "agentCount": int64(2)},
		}},
	}
	buf := new(bytes.Buffer)
	if err := RenderTable(buf, Crew, items, false); err != nil {
		t.Fatalf("RenderTable: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"NAME", "READY", "PHASE", "AGENTS", "homelab", "Running", "true"} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q\n%s", want, out)
		}
	}
}

func TestRenderTable_AllNamespaces(t *testing.T) {
	items := []unstructured.Unstructured{
		{Object: map[string]any{
			"metadata": map[string]any{"name": "a", "namespace": "team-x"},
		}},
	}
	buf := new(bytes.Buffer)
	if err := RenderTable(buf, Agent, items, true); err != nil {
		t.Fatalf("RenderTable: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "NAMESPACE") || !strings.Contains(out, "team-x") {
		t.Errorf("expected NAMESPACE column with value, got:\n%s", out)
	}
}
