package resource

import (
	"bytes"
	"strings"
	"testing"
)

func tierStatusObj() map[string]any {
	return map[string]any{"status": map[string]any{
		"tier": map[string]any{"name": "smoke", "source": "demo-fitness", "available": int64(77),
			"scenarios": []any{"a", "b", "c"}},
		"pathCoverage": map[string]any{
			"calls": int64(87), "transcripts": int64(8),
			"kinds":     []any{map[string]any{"name": "tool-turn", "calls": int64(52)}, map[string]any{"name": "triage", "calls": int64(21)}},
			"providers": []any{map[string]any{"name": "vllm", "calls": int64(70)}},
			"thinking":  []any{map[string]any{"name": "off", "calls": int64(66)}},
			"gaps":      []any{"no call on provider ollama", "no simple call"},
		},
	}}
}

func render(t *testing.T, obj map[string]any) string {
	t.Helper()
	var b bytes.Buffer
	if err := RenderSuiteResults(&b, obj); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestRenderSuiteResultsShowsTierAndPathCoverage(t *testing.T) {
	out := render(t, tierStatusObj())
	for _, want := range []string{
		"Tier: smoke, 3 of 77 scenarios from demo-fitness",
		"Path coverage: 87 calls in 8 transcripts",
		"kinds:     tool-turn 52, triage 21",
		"providers: vllm 70",
		"thinking:  off 66",
		"gaps:     no call on provider ollama",
		"           no simple call",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "phases:") || strings.Contains(out, "models:") {
		t.Errorf("an empty table is not printed:\n%s", out)
	}
}

func TestRenderSuiteResultsSaysWhenThereAreNoGaps(t *testing.T) {
	obj := tierStatusObj()
	obj["status"].(map[string]any)["pathCoverage"] = map[string]any{"calls": int64(5), "transcripts": int64(1)}
	if out := render(t, obj); !strings.Contains(out, "gaps:      none") {
		t.Errorf("no gaps must say none:\n%s", out)
	}
}

func TestRenderSuiteResultsLeavesOutWhatTheOperatorDidNotWrite(t *testing.T) {
	if out := render(t, map[string]any{"status": map[string]any{"phase": "Running"}}); out != "" {
		t.Errorf("a suite with no results prints nothing, got %q", out)
	}
	obj := tierStatusObj()
	delete(obj["status"].(map[string]any), "pathCoverage")
	out := render(t, obj)
	if !strings.Contains(out, "Tier:") || strings.Contains(out, "Path coverage") {
		t.Errorf("tier without coverage:\n%s", out)
	}
}

func TestTierResultPathsAreRead(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range SuiteResultPaths {
		seen[p] = true
	}
	for _, p := range tierResultPaths {
		if !seen[p] {
			t.Errorf("%s is read but not in SuiteResultPaths (the contract test would not check it)", p)
		}
	}
}
