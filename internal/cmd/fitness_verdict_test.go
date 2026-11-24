package cmd

import (
	"strings"
	"testing"

	"github.com/kubemoot/kmctl/internal/clierr"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func suiteAt(name string, status map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "kubemoot.ai/v1alpha1", "kind": "CrewFitnessSuite",
		"metadata": map[string]any{"name": name}, "status": status,
	}}
}

func TestSuiteVerdict(t *testing.T) {
	cases := []struct {
		name   string
		status map[string]any
		want   []string // empty: the suite passed
	}{
		{"all gates passed", map[string]any{"phase": "Completed", "iterationsTotal": int64(4), "passed": int64(4)}, nil},
		{"a low judge score is not a failure", map[string]any{"phase": "Completed", "iterationsTotal": int64(2), "passed": int64(2),
			"judge": map[string]any{"phase": "Complete", "mean": int64(20)}}, nil},
		{"still running", map[string]any{"phase": "Running"}, nil},
		{"failed gate", map[string]any{"phase": "Completed", "iterationsTotal": int64(32), "passed": int64(30), "failed": int64(2)},
			[]string{"fitness suite s failed", "2 of 32 iterations failed their gates and 0 could not run"}},
		{"errored iteration", map[string]any{"phase": "Completed", "iterationsTotal": int64(3), "passed": int64(2), "errored": int64(1)},
			[]string{"0 of 3 iterations failed their gates and 1 could not run"}},
		{"reasons per scenario", map[string]any{"phase": "Completed", "iterationsTotal": int64(4), "passed": int64(2), "failed": int64(1), "errored": int64(1),
			"scenarios": []any{
				map[string]any{"name": "fine", "iterations": int64(2), "passed": int64(2)},
				map[string]any{"name": "gated", "iterations": int64(1), "failed": int64(1), "failureReason": "forbidden term found"},
				map[string]any{"name": "broken", "iterations": int64(1), "errored": float64(1)},
				"not-a-map",
			}},
			[]string{"gated: forbidden term found", "broken: 0 failed, 1 errored"}},
		{"tool smoke", map[string]any{"phase": "ToolSmokeFailed", "conditions": []any{
			map[string]any{"type": "ToolSmoke", "message": "9 tool(s) failed the smoke, no model ran"}}},
			[]string{"the tool smoke failed, so no model ran: 9 tool(s) failed the smoke, no model ran"}},
		{"tool smoke without a message", map[string]any{"phase": "ToolSmokeFailed"}, []string{"status.toolSmoke"}},
		{"error", map[string]any{"phase": "Error", "error": "invalid spec"}, []string{"invalid spec"}},
		{"failed without a reason", map[string]any{"phase": "Failed"}, []string{"status.error"}},
		{"cancelled", map[string]any{"phase": "Cancelled", "iterationsCompleted": int64(5), "iterationsTotal": int64(32)},
			[]string{"stopped after 5 of 32 iterations"}},
	}
	for _, c := range cases {
		checkVerdict(t, c.name, c.want, suiteVerdict(suiteAt("s", c.status)))
	}
}

// checkVerdict asserts err is nil when want is empty, else a failure carrying every want and the hint.
func checkVerdict(t *testing.T, name string, want []string, err error) {
	t.Helper()
	if len(want) == 0 {
		if err != nil {
			t.Errorf("%s: want exit 0, got %v", name, err)
		}
		return
	}
	if err == nil {
		t.Errorf("%s: want a failure", name)
		return
	}
	for _, w := range want {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("%s: %q lacks %q", name, err.Error(), w)
		}
	}
	if ce, ok := clierr.As(err); !ok || !strings.Contains(ce.Hint, "kmctl fitness get s") {
		t.Errorf("%s: the hint names kmctl fitness get: %v", name, err)
	}
}

func TestScenarioVerdict(t *testing.T) {
	cf := func(status map[string]any) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"metadata": map[string]any{"name": "starter-pods-kmctl-x"}, "status": status}}
	}
	for _, phase := range []string{"Passed", "Running", ""} {
		if err := scenarioVerdict(cf(map[string]any{"phase": phase}), "pods"); err != nil {
			t.Errorf("phase %q is not a failure: %v", phase, err)
		}
	}
	failed := cf(map[string]any{"phase": "Failed", "assertions": []any{
		map[string]any{"passed": true, "message": "POST returned 200"},
		map[string]any{"passed": false, "message": "agree floor not met"},
		"not-a-map",
	}})
	err := scenarioVerdict(failed, "pods")
	if err == nil || !strings.Contains(err.Error(), "scenario pods failed its gates: agree floor not met") {
		t.Errorf("failed scenario = %v", err)
	}
	err = scenarioVerdict(cf(map[string]any{"phase": "Error", "error": "Job failed"}), "pods")
	if err == nil || !strings.Contains(err.Error(), "scenario pods could not run: Job failed") {
		t.Errorf("errored scenario = %v", err)
	}
	err = scenarioVerdict(cf(map[string]any{"phase": "Failed"}), "pods")
	if err == nil || !strings.Contains(err.Error(), "no reason recorded") {
		t.Errorf("a failure without a recorded reason says so: %v", err)
	}
}

func TestIntOf(t *testing.T) {
	if intOf(int64(3)) != 3 || intOf(float64(2)) != 2 || intOf("x") != 0 || intOf(nil) != 0 {
		t.Error("intOf reads int64 and float64 and nothing else")
	}
}

func TestOrUnknown(t *testing.T) {
	if orUnknown("  ", "fb") != "fb" || orUnknown("why", "fb") != "why" {
		t.Error("orUnknown falls back on blank text")
	}
}
