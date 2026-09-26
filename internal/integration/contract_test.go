//go:build integration

// Package integration holds drift-detection tests (Tier 2). They run against the
// CURRENT Kubemoot CRDs, not a live cluster, so they catch CRD/GVR/schema drift
// without the operator. Build-tagged `integration` so per-push CI never runs them;
// a scheduled workflow does. Point KMCTL_CRD_DIR at the kubemoot CRD bases dir.
package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/javajon-homelab/kmctl/internal/resource"
	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

func crdDir(t *testing.T) string {
	t.Helper()
	d := os.Getenv("KMCTL_CRD_DIR")
	if d == "" {
		t.Skip("KMCTL_CRD_DIR not set; point it at the kubemoot operator CRD bases dir")
	}
	return d
}

func loadCRD(t *testing.T, dir, file string) apiext.CustomResourceDefinition {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		t.Fatalf("read CRD %s: %v", file, err)
	}
	var crd apiext.CustomResourceDefinition
	if err := yaml.Unmarshal(b, &crd); err != nil {
		t.Fatalf("parse CRD %s: %v", file, err)
	}
	return crd
}

// kindCRD pairs a kmctl Kind with its CRD file.
type kindCRD struct {
	kind resource.Kind
	file string
}

func allKinds() []kindCRD {
	return []kindCRD{
		{resource.Crew, "kubemoot.ai_crews.yaml"},
		{resource.Agent, "kubemoot.ai_agents.yaml"},
		{resource.PromptModule, "kubemoot.ai_promptmodules.yaml"},
		{resource.ModelProvider, "kubemoot.ai_modelproviders.yaml"},
		{resource.Model, "kubemoot.ai_models.yaml"},
		{resource.CrewFitnessSuite, "kubemoot.ai_crewfitnesssuites.yaml"},
		{resource.CrewFitness, "kubemoot.ai_crewfitnesses.yaml"},
	}
}

// TestContract_GVR asserts kmctl's GroupVersionResource for each kind still
// matches the CRD (catches plural/group/version renames that silently break the
// dynamic client).
func TestContract_GVR(t *testing.T) {
	dir := crdDir(t)
	for _, kc := range allKinds() {
		crd := loadCRD(t, dir, kc.file)
		gvr := kc.kind.GVR()
		if crd.Spec.Group != gvr.Group {
			t.Errorf("%s: group drift - kmctl %q, CRD %q", kc.kind.Singular, gvr.Group, crd.Spec.Group)
		}
		if crd.Spec.Names.Plural != gvr.Resource {
			t.Errorf("%s: plural drift - kmctl %q, CRD %q", kc.kind.Singular, gvr.Resource, crd.Spec.Names.Plural)
		}
		if !servesVersion(crd, gvr.Version) {
			t.Errorf("%s: CRD no longer serves version %q", kc.kind.Singular, gvr.Version)
		}
	}
}

// TestContract_Columns asserts every kmctl table-column field-path still exists
// in the CRD's openAPI schema (catches status-field renames like status.ready ->
// status.isReady that would render every cell as <none>).
func TestContract_Columns(t *testing.T) {
	dir := crdDir(t)
	for _, kc := range allKinds() {
		crd := loadCRD(t, dir, kc.file)
		schema := versionSchema(crd, kc.kind.GVR().Version)
		if schema == nil {
			t.Errorf("%s: no openAPI schema for version", kc.kind.Singular)
			continue
		}
		for _, col := range kc.kind.Columns {
			if strings.HasPrefix(col.Path, "metadata.") {
				continue // metadata fields are standard, not in the CRD's own properties
			}
			if !schemaHasPath(schema, strings.Split(col.Path, ".")) {
				t.Errorf("%s: column %q path %q not found in CRD schema - drift?", kc.kind.Singular, col.Header, col.Path)
			}
		}
	}
}

func servesVersion(crd apiext.CustomResourceDefinition, version string) bool {
	for _, v := range crd.Spec.Versions {
		if v.Name == version && v.Served {
			return true
		}
	}
	return false
}

func versionSchema(crd apiext.CustomResourceDefinition, version string) *apiext.JSONSchemaProps {
	for _, v := range crd.Spec.Versions {
		if v.Name == version && v.Schema != nil {
			return v.Schema.OpenAPIV3Schema
		}
	}
	return nil
}

func schemaHasPath(schema *apiext.JSONSchemaProps, parts []string) bool {
	cur := schema
	for _, p := range parts {
		if cur == nil {
			return false
		}
		next, ok := cur.Properties[p]
		if !ok {
			return false
		}
		cur = &next
	}
	return true
}
