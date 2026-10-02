package resource

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// completedSuite is a CrewFitnessSuite status as the dynamic client decodes it
// (JSON integers as int64): two scenarios, both judged.
func completedSuite() map[string]any {
	return map[string]any{
		"status": map[string]any{
			"phase": "Completed",
			"scenarios": []any{
				map[string]any{"name": "gpu-live", "iterations": int64(15), "passed": int64(14), "failed": int64(1), "meanDurationMs": int64(84210)},
				map[string]any{"name": "node-count", "iterations": int64(15), "passed": int64(15)},
			},
			"judge": map[string]any{
				"phase": "Complete", "judged": int64(2), "total": int64(2), "mean": int64(44), "zeros": int64(1),
				"completedAt": "2026-10-02T14:31:07Z",
				"scores": []any{
					map[string]any{"scenario": "gpu-live", "reason": "all iterations gated"},
					map[string]any{"scenario": "node-count", "score": int64(87), "reason": "correct count"},
				},
			},
		},
	}
}

func renderResults(t *testing.T, obj map[string]any) string {
	t.Helper()
	var buf bytes.Buffer
	if err := RenderSuiteResults(&buf, obj); err != nil {
		t.Fatalf("RenderSuiteResults: %v", err)
	}
	return buf.String()
}

func TestRenderSuiteResultsJudgeAndScenarios(t *testing.T) {
	out := renderResults(t, completedSuite())
	for _, want := range []string{
		"Judge: Complete, 2 of 2 scenarios judged, mean 44, 1 scored 0, completed 2026-10-02T14:31:07Z",
		"SCENARIO", "MEAN DURATION", "REASON",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	gpu, node := strings.Fields(lines[len(lines)-2]), strings.Fields(lines[len(lines)-1])
	// gpu-live: 15 iterations, 14 passed, 1 failed, 0 errored, 1m24s, score 0 (omitted by the operator).
	if strings.Join(gpu[:7], " ") != "gpu-live 15 14 1 0 1m24s 0" {
		t.Errorf("gpu-live row = %v", gpu)
	}
	if strings.Join(node[:7], " ") != "node-count 15 15 0 0 <none> 87" || !strings.Contains(lines[len(lines)-1], "correct count") {
		t.Errorf("node-count row = %v", node)
	}
}

func TestRenderSuiteResultsWhileJudging(t *testing.T) {
	obj := map[string]any{"status": map[string]any{
		"judge":     map[string]any{"phase": "Judging", "total": int64(3)},
		"scenarios": []any{map[string]any{"name": "a"}},
	}}
	out := renderResults(t, obj)
	if !strings.Contains(out, "Judge: Judging, 0 of 3 scenarios judged\n") {
		t.Errorf("an in-progress judge reads 0 of N with no mean:\n%s", out)
	}
	row := strings.Fields(strings.Split(strings.TrimSpace(out), "\n")[3])
	if strings.Join(row, " ") != "a 0 0 0 0 <none> <none>" {
		t.Errorf("an unjudged scenario row = %v", row)
	}
}

func TestRenderSuiteResultsPendingOrSkippedPhaseOnly(t *testing.T) {
	for _, phase := range []string{"Pending", "Skipped"} {
		out := renderResults(t, map[string]any{"status": map[string]any{"judge": map[string]any{"phase": phase}}})
		if out != "\nJudge: "+phase+"\n" {
			t.Errorf("%s judge output = %q", phase, out)
		}
	}
}

func TestRenderSuiteResultsScoresWithoutScenarios(t *testing.T) {
	// A suite judged before status.scenarios existed: score rows only.
	obj := map[string]any{"status": map[string]any{"judge": map[string]any{
		"phase": "Complete", "judged": int64(1), "total": int64(1), "mean": int64(90),
		"scores": []any{map[string]any{"scenario": "old", "score": int64(90)}, "not-a-map"},
	}}}
	out := renderResults(t, obj)
	last := strings.Fields(strings.TrimSpace(out[strings.LastIndex(out, "\nold"):]))
	if strings.Join(last, " ") != "old <none> <none> <none> <none> <none> 90" {
		t.Errorf("score-only row = %v\n%s", last, out)
	}
}

func TestRenderSuiteResultsScoreForAnUnlistedScenario(t *testing.T) {
	obj := completedSuite()
	judge := obj["status"].(map[string]any)["judge"].(map[string]any)
	judge["scores"] = append(judge["scores"].([]any), map[string]any{"scenario": "renamed", "score": int64(50)})
	judge["phase"] = ""
	out := renderResults(t, obj)
	last := strings.Fields(strings.Split(strings.TrimSpace(out), "\n")[len(strings.Split(strings.TrimSpace(out), "\n"))-1])
	if strings.Join(last, " ") != "renamed <none> <none> <none> <none> <none> 50" {
		t.Errorf("a score with no scenario row gets its own row last, got %v", last)
	}
	if !strings.Contains(out, "Judge: <none>, 2 of 2") {
		t.Errorf("an empty judge phase reads <none>:\n%s", out)
	}
}

func TestRenderSuiteResultsNothingRecorded(t *testing.T) {
	for _, obj := range []map[string]any{
		{},
		{"status": map[string]any{"phase": "Running"}},
		{"status": map[string]any{"scenarios": []any{"not-a-map"}}},
	} {
		if out := renderResults(t, obj); out != "" {
			t.Errorf("no judge and no scenarios should print nothing, got %q", out)
		}
	}
}

// failingWriter fails every write.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestRenderSuiteResultsWriteError(t *testing.T) {
	if err := RenderSuiteResults(failingWriter{}, completedSuite()); err == nil {
		t.Error("a write error must be returned")
	}
	scenariosOnly := map[string]any{"status": map[string]any{"scenarios": []any{map[string]any{"name": "a"}}}}
	if err := RenderSuiteResults(failingWriter{}, scenariosOnly); err == nil {
		t.Error("a write error on the table must be returned")
	}
}

func TestSuiteKindHasJudgeColumnsAndDetails(t *testing.T) {
	headers := map[string]string{}
	for _, c := range CrewFitnessSuite.Columns {
		headers[c.Header] = c.Path
	}
	if headers["JUDGE"] != "status.judge.phase" || headers["QUALITY"] != "status.judge.mean" {
		t.Errorf("suite columns = %v", headers)
	}
	if CrewFitnessSuite.Details == nil {
		t.Error("a suite's get prints its results")
	}
	if Crew.Details != nil {
		t.Error("other kinds have no details")
	}
}
