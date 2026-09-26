package cmd

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/kubemoot/kmctl/internal/client"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestApplyCommand_RequiresFile(t *testing.T) {
	cmd := newApplyCommand(client.NewFactory())
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error when -f is missing, got nil")
	}
}

func TestApplyObject_RefusesNonKubemoot(t *testing.T) {
	// The group check happens before any client use, so nil dyn/mapper is fine.
	obj := unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]any{"name": "x"},
	}}
	err := applyObject(context.Background(), nil, nil, obj, applyOpts{defaultNS: "default"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "only manages") {
		t.Fatalf("expected refusal of non-kubemoot kind, got %v", err)
	}
}

// TestApplyObject_DefaultsNamespace covers the namespaced-apply path: an object
// with no namespace is server-side applied into the factory's default namespace.
func TestApplyObject_DefaultsNamespace(t *testing.T) {
	dc := fakeDynApplyOK()
	obj := *newCrew("demo")
	obj.SetNamespace("") // force the defaultNS branch
	var buf bytes.Buffer
	err := applyObject(context.Background(), dc, staticMapper(), obj, applyOpts{defaultNS: "team-a"}, &buf)
	if err != nil {
		t.Fatalf("applyObject: %v", err)
	}
	if !strings.Contains(buf.String(), "crew/demo applied") || strings.Contains(buf.String(), "dry run") {
		t.Fatalf("want a plain applied message, got %q", buf.String())
	}
	// applyObject defaulted the empty namespace in place before applying.
	if obj.GetNamespace() != "team-a" {
		t.Fatalf("expected namespace defaulted to team-a, got %q", obj.GetNamespace())
	}
}

// TestApplyObject_DryRun covers the dry-run branch: ApplyOptions carry DryRunAll
// and the output is suffixed, without persisting beyond the fake's apply.
func TestApplyObject_DryRun(t *testing.T) {
	dc := fakeDynApplyOK()
	obj := *newCrew("dry")
	var buf bytes.Buffer
	err := applyObject(context.Background(), dc, staticMapper(), obj, applyOpts{defaultNS: "default", dryRun: true}, &buf)
	if err != nil {
		t.Fatalf("applyObject dry-run: %v", err)
	}
	if !strings.Contains(buf.String(), "crew/dry applied (dry run)") {
		t.Fatalf("want a dry-run-suffixed message, got %q", buf.String())
	}
}
