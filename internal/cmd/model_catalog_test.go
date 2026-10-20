package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// catalogStatus is a ModelServer status offering the given status.models items.
func catalogStatus(models ...any) map[string]any {
	return map[string]any{"status": map[string]any{"ready": true, "models": models}}
}

func toolCallingModel(name, size string) map[string]any {
	return map[string]any{
		"name": name, "present": true, "size": size, "contextLength": 40960,
		"capabilities": []any{"tool-calling"},
		"labels":       map[string]any{"capability/tool-calling": "true", "kubemoot.ai/size": size},
	}
}

// storedObject returns a stored kubemoot.ai object in any namespace, or nil.
func (s *fakeAPIServer) storedObject(plural, ns, name string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.objs[objKey(plural, ns, name)]
}

const noToolCallingWarning = "warning: no model in the cluster's model catalog can call tools"

func TestModelList_ReadsTheCatalogAcrossNamespaces(t *testing.T) {
	s := newFakeAPIServer(t)
	s.add("modelservers", "kubemoot", "ollama-gpu", catalogStatus(toolCallingModel("qwen3:8b", "small"), map[string]any{"name": "nomic-chat"}))
	s.add("modelservers", "other", "vllm-gpu", catalogStatus(toolCallingModel("qwen3:14b", "medium")))
	out, errOut, err := s.kmctl(t, "model", "list")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "catalog", out, "MODEL", "SERVER", "SIZE", "ABILITIES", "CONTEXT", "PRESENT",
		"qwen3:8b", "ollama-gpu", "small", "tool-calling", "40960", "qwen3:14b", "vllm-gpu", "medium", "nomic-chat", "unknown")
	if strings.Contains(errOut, "warning") {
		t.Errorf("a catalog with a tool-calling model needs no warning: %q", errOut)
	}
}

func TestModelList_WarnsWithoutToolCalling(t *testing.T) {
	s := newFakeAPIServer(t)
	s.add("modelservers", "kubemoot", "ollama-cpu", catalogStatus(map[string]any{"name": "tiny", "capabilities": []any{"reasoning"}}))
	out, errOut, err := s.kmctl(t, "model", "list")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "row", out, "tiny", "reasoning")
	assertContains(t, "warning", errOut, noToolCallingWarning, "Unschedulable", "kmctl model add")
}

func TestModelList_EmptyCatalogs(t *testing.T) {
	s := newFakeAPIServer(t)
	_, errOut, err := s.kmctl(t, "model", "list")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "no servers", errOut, "No model servers found.", noToolCallingWarning)

	s.add("modelservers", "kubemoot", "ollama-cpu", nil)
	_, errOut, err = s.kmctl(t, "model", "list")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "no models", errOut, "1 model server(s) found, offering none yet", noToolCallingWarning)
}

// -n reads only that namespace, and the warning names it.
func TestModelList_NamespaceNarrows(t *testing.T) {
	s := newFakeAPIServer(t)
	s.add("modelservers", "kubemoot", "ollama-gpu", catalogStatus(toolCallingModel("qwen3:8b", "small")))
	s.add("modelservers", "team-b", "cpu", catalogStatus(map[string]any{"name": "tiny"}))
	out, errOut, err := s.kmctl(t, "model", "list", "-n", "team-b")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "qwen3:8b") {
		t.Errorf("-n team-b must not list kubemoot's models:\n%s", out)
	}
	assertContains(t, "namespaced warning", errOut, `the model catalog in namespace "team-b"`)
}

func TestModelList_StructuredOutput(t *testing.T) {
	s := newFakeAPIServer(t)
	s.add("modelservers", "kubemoot", "ollama-gpu", catalogStatus(toolCallingModel("qwen3:8b", "small")))
	out, _, err := s.kmctl(t, "model", "list", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var entries []map[string]any
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("-o json is not a JSON list: %v\n%s", err, out)
	}
	if len(entries) != 1 || entries[0]["model"] != "qwen3:8b" || entries[0]["server"] != "ollama-gpu" || entries[0]["namespace"] != "kubemoot" {
		t.Errorf("entries = %v", entries)
	}
}

func TestModelList_StructuredOutputEdges(t *testing.T) {
	s := newFakeAPIServer(t)
	out, _, err := s.kmctl(t, "model", "list", "-o", "json")
	if err != nil || strings.TrimSpace(out) != "[]" {
		t.Errorf("an empty catalog is an empty list, got %q, %v", out, err)
	}
	if _, _, err := s.kmctl(t, "model", "list", "-o", "wide"); err == nil || !strings.Contains(err.Error(), "unsupported output format") {
		t.Errorf("want -o wide rejected, got %v", err)
	}
}

func TestModelServerList_ListsServers(t *testing.T) {
	s := newFakeAPIServer(t)
	s.add("modelservers", contextNS, "gpu-a", map[string]any{"spec": map[string]any{"type": "ollama", "endpoint": "http://ollama:11434"}})
	out, _, err := s.kmctl(t, "model", "server", "list")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "servers", out, "CALIBRATION", "gpu-a", "ollama")
}

// create warns when the current cluster's catalog has no tool-calling model, and
// is quiet when it has one.
func TestCreate_WarnsWhenTheCatalogCannotCallTools(t *testing.T) {
	s := newFakeAPIServer(t)
	s.add("modelservers", "kubemoot", "ollama-cpu", catalogStatus(map[string]any{"name": "tiny"}))
	_, errOut, err := s.kmctl(t, "create", "demo", "--no-input", "-o", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "create warning", errOut, noToolCallingWarning)

	s.add("modelservers", "kubemoot", "ollama-gpu", catalogStatus(toolCallingModel("qwen3:8b", "small")))
	_, errOut, err = s.kmctl(t, "create", "demo", "--no-input", "-o", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errOut, "warning") {
		t.Errorf("no warning when a model can call tools: %q", errOut)
	}
}
