package resource

import (
	"bytes"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// modelServer is a ModelServer with the given status.models items.
func modelServer(ns, name string, models ...any) unstructured.Unstructured {
	obj := map[string]any{
		"apiVersion": "kubemoot.ai/v1alpha1",
		"kind":       "ModelServer",
		"metadata":   map[string]any{"name": name, "namespace": ns},
	}
	if models != nil {
		obj["status"] = map[string]any{"models": models}
	}
	return unstructured.Unstructured{Object: obj}
}

func toolCaller(name, size string) map[string]any {
	return map[string]any{
		"name": name, "present": true, "size": size, "parameterCount": int64(14_800_000_000), "contextLength": int64(40960),
		"capabilities": []any{"tool-calling", "reasoning"},
		"labels":       map[string]any{ToolCallingLabel: "true", "capability/reasoning": "true", "kubemoot.ai/size": size},
	}
}

func TestCatalogOf_ReadsEveryServersModelsSorted(t *testing.T) {
	cat := CatalogOf([]unstructured.Unstructured{
		modelServer("kubemoot", "vllm-gpu", toolCaller("qwen3:14b", "medium")),
		modelServer("kubemoot", "ollama-gpu", toolCaller("qwen3:8b", "small"), toolCaller("qwen3:14b", "medium")),
		modelServer("kubemoot", "ollama-cpu"), // no status yet
	})
	if cat.Servers != 3 {
		t.Errorf("servers = %d, want 3", cat.Servers)
	}
	var got []string
	for _, e := range cat.Entries {
		got = append(got, e.Model+"@"+e.Server)
	}
	want := "qwen3:14b@ollama-gpu qwen3:14b@vllm-gpu qwen3:8b@ollama-gpu"
	if strings.Join(got, " ") != want {
		t.Errorf("entries = %v, want %s", got, want)
	}
	e := cat.Entries[0]
	if !e.Present || e.Size != "medium" || e.ParameterCount != 14_800_000_000 || e.ContextLength != 40960 || e.Namespace != "kubemoot" || !e.CallsTools() {
		t.Errorf("entry not read in full: %+v", e)
	}
}

// Items the reader cannot use are skipped, and numbers decoded as float64 (an
// object built by encoding/json) read the same as int64.
func TestCatalogOf_UnexpectedItems(t *testing.T) {
	cat := CatalogOf([]unstructured.Unstructured{modelServer("ns", "s",
		"not a map",
		map[string]any{"present": true}, // no name
		map[string]any{"name": "bare"},
		map[string]any{"name": "floats", "contextLength": float64(8192), "parameterCount": float64(-1)},
	)})
	if len(cat.Entries) != 2 {
		t.Fatalf("want the 2 named entries, got %+v", cat.Entries)
	}
	bare, floats := cat.Entries[0], cat.Entries[1]
	if bare.Model != "bare" || bare.Present || bare.Size != "" || bare.CallsTools() {
		t.Errorf("bare entry: %+v", bare)
	}
	if floats.ContextLength != 8192 || floats.ParameterCount != 0 {
		t.Errorf("floats entry: %+v", floats)
	}
}

func TestCatalog_HasToolCalling(t *testing.T) {
	cases := map[string]struct {
		servers []unstructured.Unstructured
		want    bool
	}{
		"no servers":    {nil, false},
		"empty catalog": {[]unstructured.Unstructured{modelServer("ns", "s")}, false},
		"no label": {[]unstructured.Unstructured{modelServer("ns", "s",
			map[string]any{"name": "m", "capabilities": []any{"tool-calling"}})}, false},
		"label false": {[]unstructured.Unstructured{modelServer("ns", "s",
			map[string]any{"name": "m", "labels": map[string]any{ToolCallingLabel: "false"}})}, false},
		"one tool caller": {[]unstructured.Unstructured{modelServer("ns", "s",
			map[string]any{"name": "embed"}, toolCaller("m", "small"))}, true},
	}
	for name, c := range cases {
		if got := CatalogOf(c.servers).HasToolCalling(); got != c.want {
			t.Errorf("%s: HasToolCalling = %v, want %v", name, got, c.want)
		}
	}
}

func TestRenderCatalog(t *testing.T) {
	cat := CatalogOf([]unstructured.Unstructured{modelServer("kubemoot", "vllm-gpu",
		toolCaller("qwen3:14b", "medium"),
		map[string]any{"name": "mystery"},
	)})
	var buf bytes.Buffer
	if err := RenderCatalog(&buf, cat.Entries); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("want a header and 2 rows, got:\n%s", buf.String())
	}
	for i, want := range [][]string{
		{"MODEL", "SERVER", "SIZE", "ABILITIES", "CONTEXT", "PRESENT"},
		{"mystery", "vllm-gpu", "unknown", "none", "unknown", "no"},
		{"qwen3:14b", "vllm-gpu", "medium", "reasoning,tool-calling", "40960", "yes"},
	} {
		if got := strings.Fields(lines[i]); strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("line %d = %v, want %v", i, got, want)
		}
	}
}
