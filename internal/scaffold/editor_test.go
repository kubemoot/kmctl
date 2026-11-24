package scaffold

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func settingsOf(t *testing.T, o Options) map[string]any {
	t.Helper()
	files, err := Generate(o)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal([]byte(files[settingsPath]), &settings); err != nil {
		t.Fatalf("%s is not JSON: %v\n%s", settingsPath, err, files[settingsPath])
	}
	return settings
}

func TestEditorFiles_MapEveryManifestToTheSchema(t *testing.T) {
	for _, chart := range []bool{false, true} {
		schemas := settingsOf(t, Options{Name: "demo", Members: 1, Chart: chart})["yaml.schemas"].(map[string]any)
		globs, ok := schemas[DefaultSchemaURL].([]any)
		if len(schemas) != 1 || !ok || len(globs) != 1 || globs[0] != "**/*.yaml" {
			t.Errorf("chart=%v: yaml.schemas = %v, want the default schema for **/*.yaml", chart, schemas)
		}
	}
}

func TestEditorFiles_OnlyAChartAssociatesTemplatesWithHelm(t *testing.T) {
	chart := settingsOf(t, Options{Name: "demo", Members: 1, Chart: true})
	assoc, _ := chart["files.associations"].(map[string]any)
	if assoc["**/templates/**/*.yaml"] != "helm" {
		t.Errorf("a chart's templates are not associated with helm: %v", chart)
	}
	if _, ok := settingsOf(t, Options{Name: "demo", Members: 1})["files.associations"]; ok {
		t.Error("a bundle has no templates and needs no Helm association")
	}
}

func TestEditorFiles_RecommendTheExtensions(t *testing.T) {
	files, err := Generate(Options{Name: "demo", Members: 1})
	if err != nil {
		t.Fatal(err)
	}
	var ext struct {
		Recommendations []string `json:"recommendations"`
	}
	if err := json.Unmarshal([]byte(files[extensionsPath]), &ext); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"redhat.vscode-yaml", "ms-kubernetes-tools.vscode-kubernetes-tools"} {
		if !slices.Contains(ext.Recommendations, want) {
			t.Errorf("%s is not recommended: %v", want, ext.Recommendations)
		}
	}
}

func TestEditorFiles_SchemaURLOverridesTheDefault(t *testing.T) {
	url := "https://mirror.example.com/kubemoot_v1alpha1.json"
	schemas := settingsOf(t, Options{Name: "demo", Members: 1, SchemaURL: url})["yaml.schemas"].(map[string]any)
	if _, ok := schemas[url]; !ok || len(schemas) != 1 {
		t.Errorf("yaml.schemas = %v, want only %s", schemas, url)
	}
}

func TestValidateSchemaURL(t *testing.T) {
	good := []string{"", DefaultSchemaURL, "http://127.0.0.1:8080/kubemoot_v1alpha1.json", "file:///srv/schemas/kubemoot_v1alpha1.json"}
	bad := []string{"kubemoot.json", "/srv/kubemoot.json", "ftp://example.com/x.json", "https://", "file://", "https://a b/x.json", "https://x/\"y", "https://x/\ny", "%zz", "https:///x.json"}
	for _, u := range good {
		if err := validateSchemaURL(u); err != nil {
			t.Errorf("%q rejected: %v", u, err)
		}
	}
	for _, u := range bad {
		if err := validateSchemaURL(u); err == nil {
			t.Errorf("%q accepted", u)
		}
	}
	if _, err := Generate(Options{Name: "demo", Members: 1, SchemaURL: "nope"}); err == nil || !strings.Contains(err.Error(), "--schema-url") {
		t.Errorf("Generate accepts a bad schema URL: %v", err)
	}
}

func TestEditorFiles_ChartKeepsWorkspaceFilesOutOfThePackage(t *testing.T) {
	chart, err := Generate(Options{Name: "demo", Members: 1, Chart: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(chart[".helmignore"], ".vscode/") {
		t.Errorf("the chart would package .vscode/: %q", chart[".helmignore"])
	}
	bundle, err := Generate(Options{Name: "demo", Members: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := bundle[".helmignore"]; ok {
		t.Error("a bundle is not a chart and needs no .helmignore")
	}
}
