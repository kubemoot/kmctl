package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/javajon-homelab/kmctl/internal/manifest"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func generateAll(t *testing.T) map[string][]unstructured.Unstructured {
	t.Helper()
	files, err := Generate(Options{Name: "demo", Members: 2, ModelFamily: "qwen", Providers: []string{"ollama-gpu"}})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	parsed := map[string][]unstructured.Unstructured{}
	for name, content := range files {
		if name == "README.md" {
			continue
		}
		objs, err := manifest.Decode(strings.NewReader(content))
		if err != nil {
			t.Fatalf("generated %s is not valid YAML: %v\n%s", name, err, content)
		}
		parsed[name] = objs
	}
	return parsed
}

func TestGenerate_KindsAndCounts(t *testing.T) {
	p := generateAll(t)

	kinds := map[string]int{}
	for _, objs := range p {
		for _, o := range objs {
			kinds[o.GetKind()]++
		}
	}
	// 1 coordinator + 2 toolers = 3 Agents.
	if kinds["Agent"] != 3 {
		t.Errorf("Agent count = %d, want 3", kinds["Agent"])
	}
	if kinds["Crew"] != 1 || kinds["CrewSchedulingPolicy"] != 1 {
		t.Errorf("want 1 Crew + 1 CrewSchedulingPolicy, got %d / %d", kinds["Crew"], kinds["CrewSchedulingPolicy"])
	}
	if kinds["CrewFitnessSuite"] != 1 {
		t.Errorf("CrewFitnessSuite count = %d, want 1", kinds["CrewFitnessSuite"])
	}
	// qwen ladder (3 sizes) x 1 provider = 3 Models, so the crew is inference-ready.
	if kinds["Model"] != 3 {
		t.Errorf("Model count = %d, want 3 (qwen sizes x 1 provider)", kinds["Model"])
	}
	if kinds["PromptModule"] < 5+2 {
		t.Errorf("PromptModule count = %d, want >= 7 (shared+coordinator+2 tooler)", kinds["PromptModule"])
	}
}

// Generated Models must carry the capability label the CrewSchedulingPolicy
// requires (capability/tool-calling), or the scheduler binds nothing.
func TestGenerate_ModelsMatchPolicyLabel(t *testing.T) {
	p := generateAll(t)
	for _, m := range p["models.yaml"] {
		labels := m.GetLabels()
		if labels["capability/tool-calling"] != "true" {
			t.Errorf("model %q missing capability/tool-calling=true (label: %v)", m.GetName(), labels)
		}
		ref, _, _ := unstructured.NestedString(m.Object, "spec", "providerRef")
		if ref == "" {
			t.Errorf("model %q has no spec.providerRef", m.GetName())
		}
	}
}

func TestGenerate_PromptRefIntegrity(t *testing.T) {
	p := generateAll(t)

	modules := map[string]bool{}
	for _, o := range p["promptmodules.yaml"] {
		modules[o.GetName()] = true
	}
	for _, o := range p["agents.yaml"] {
		refs, _, _ := unstructured.NestedStringSlice(o.Object, "spec", "promptRefs")
		for _, r := range refs {
			if !modules[r] {
				t.Errorf("agent %q references promptModule %q which is not generated", o.GetName(), r)
			}
		}
	}
}

// The starter suite must DEMONSTRATE a working crew, not reward incompetence: a
// scaffold crew must ANSWER general knowledge directly (capital of France ->
// Paris), and must NOT ship a scenario that treats refusing a basic answerable
// question as correct. A crew that can't say "Paris" is a bad kmctl create demo.
func TestGenerate_StarterSuiteAnswersGeneralKnowledge(t *testing.T) {
	p := generateAll(t)

	allScripts := collectFitnessScripts(p["fitness.yaml"])
	if allScripts == "" {
		t.Fatal("no fitness scripts found in the scaffolded suite")
	}
	// The general-knowledge scenario must require the real answer, not a refusal.
	if !strings.Contains(allScripts, `synthesis CONTAINS "Paris"`) {
		t.Errorf("starter suite must assert the crew answers the general-knowledge question (synthesis CONTAINS \"Paris\"); content:\n%s", allScripts)
	}
	// The old bogus test rewarded refusing a basic answerable question - must be gone.
	if strings.Contains(allScripts, "outside its domain and does not fabricate") {
		t.Error("starter suite still rewards refusing a general-knowledge question (the bogus out-of-domain scenario); a basic answerable question must be answered, not refused")
	}
	// The coordinator/synthesis prompts must permit answering general knowledge.
	synth := promptModuleContent(p["promptmodules.yaml"], "synthesis-prompt")
	if !strings.Contains(synth, "general knowledge") {
		t.Errorf("synthesis-prompt must allow answering general knowledge directly; content:\n%s", synth)
	}
}

// collectFitnessScripts concatenates every script's testContent across the
// given fitness objects, one per line.
func collectFitnessScripts(objs []unstructured.Unstructured) string {
	var b strings.Builder
	for _, o := range objs {
		scripts, _, _ := unstructured.NestedSlice(o.Object, "spec", "scripts")
		for _, s := range scripts {
			m, ok := s.(map[string]any)
			if !ok {
				continue
			}
			if c, ok := m["testContent"].(string); ok {
				b.WriteString(c)
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}

// promptModuleContent returns spec.content of the named PromptModule, or "" if
// it is not present among the given objects.
func promptModuleContent(objs []unstructured.Unstructured, name string) string {
	for _, o := range objs {
		if o.GetName() == name {
			c, _, _ := unstructured.NestedString(o.Object, "spec", "content")
			return c
		}
	}
	return ""
}

// The synthesis prompt must forbid leaking internal mechanics into the user
// answer (a scaffold crew was observed ending its answer with "Contributed by:
// <agent> (agreement signal)"). Without this rule the final answer exposes agent
// names and signal terms.
func TestGenerate_SynthesisForbidsInternalLeakage(t *testing.T) {
	p := generateAll(t)

	content := promptModuleContent(p["promptmodules.yaml"], "synthesis-prompt")
	if content == "" {
		t.Fatal("synthesis-prompt module not found or has no content")
	}
	if !strings.Contains(content, "NEVER mention agent names") {
		t.Errorf("synthesis-prompt must forbid mentioning agent names; content:\n%s", content)
	}
	if !strings.Contains(content, "internal signals") {
		t.Errorf("synthesis-prompt must forbid exposing internal signals; content:\n%s", content)
	}
}

func TestValidate(t *testing.T) {
	bad := []Options{
		{Name: "", Members: 1},
		{Name: "Bad_Name", Members: 1},
		{Name: "ok", Members: 0},
	}
	for _, o := range bad {
		if err := o.Validate(); err == nil {
			t.Errorf("expected validation error for %+v", o)
		}
	}
	if err := (Options{Name: "ok", Members: 1}).Validate(); err != nil {
		t.Errorf("valid options rejected: %v", err)
	}
}

func TestWrite_RefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	o := Options{Name: "demo", Members: 1, OutputDir: dir}
	if _, err := Write(o); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "demo", "crew.yaml")); err != nil {
		t.Fatalf("expected crew.yaml written: %v", err)
	}
	if _, err := Write(o); err == nil {
		t.Error("expected Write to refuse an existing directory")
	}
}

func TestModelCount(t *testing.T) {
	// qwen has 3 sizes; x 2 providers = 6 Models.
	if n := (Options{ModelFamily: "qwen", Providers: []string{"a", "b"}}).ModelCount(); n != 6 {
		t.Fatalf("qwen x 2 providers: want 6 Models, got %d", n)
	}
	// No providers -> no Models (the silent-empty case the warning guards).
	if n := (Options{ModelFamily: "qwen"}).ModelCount(); n != 0 {
		t.Fatalf("no providers: want 0 Models, got %d", n)
	}
	// No family -> no Models even with providers.
	if n := (Options{Providers: []string{"a"}}).ModelCount(); n != 0 {
		t.Fatalf("no family: want 0 Models, got %d", n)
	}
	// Unknown family -> no built-in sizes -> 0 Models.
	if n := (Options{ModelFamily: "nope", Providers: []string{"a"}}).ModelCount(); n != 0 {
		t.Fatalf("unknown family: want 0 Models, got %d", n)
	}
}
