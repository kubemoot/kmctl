package resource

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/kubemoot/kmctl/internal/output"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// SuiteResultPaths are the CrewFitnessSuite status fields RenderSuiteResults
// reads (judgeSummary, judgeScores, scenarioRow); add a path here with every
// field a reader starts using. A path through a list names the list, then the
// field of its items. The Tier 2 contract test checks each one against the CRD
// schema.
var SuiteResultPaths = []string{
	"status.judge.phase",
	"status.judge.judged",
	"status.judge.total",
	"status.judge.mean",
	"status.judge.zeros",
	"status.judge.completedAt",
	"status.judge.scores.scenario",
	"status.judge.scores.score",
	"status.judge.scores.reason",
	"status.scenarios.name",
	"status.scenarios.iterations",
	"status.scenarios.passed",
	"status.scenarios.failed",
	"status.scenarios.errored",
	"status.scenarios.meanDurationMs",
}

// judgeScore is one entry of status.judge.scores.
type judgeScore struct {
	score  string
	reason string
}

// suiteRow is one scenario line of the results table.
type suiteRow struct {
	name                             string
	iterations, passed, failed, errs string
	meanDuration                     string
	judgeScore
}

// RenderSuiteResults writes what the operator records on a CrewFitnessSuite's
// status after the summary row: a judge summary line, then one row per
// scenario joining status.scenarios and status.judge.scores. It writes nothing
// when the status carries neither.
func RenderSuiteResults(w io.Writer, obj map[string]any) error {
	judge, hasJudge, _ := unstructured.NestedMap(obj, "status", "judge")
	rows := suiteRows(obj)
	if !hasJudge && len(rows) == 0 {
		return nil
	}
	out := output.NewWriter(w)
	if hasJudge {
		out.Printf("\nJudge: %s\n", judgeSummary(judge))
	}
	if out.Err() != nil || len(rows) == 0 {
		return out.Err()
	}
	out.Printf("\n")
	if out.Err() != nil {
		return out.Err()
	}
	return renderSuiteRows(w, rows)
}

// judgeSummary is the one-line judge state: phase, progress, mean, zeros and
// completion time, each part only when status has it.
func judgeSummary(judge map[string]any) string {
	parts := []string{orNone(stringField(judge, "phase"))}
	if total := intField(judge, "total"); total != "" {
		parts = append(parts, fmt.Sprintf("%s of %s scenarios judged", orZero(intField(judge, "judged")), total))
	}
	if mean := intField(judge, "mean"); mean != "" {
		parts = append(parts, "mean "+mean)
	}
	if zeros := intField(judge, "zeros"); zeros != "" {
		parts = append(parts, zeros+" scored 0")
	}
	if done := stringField(judge, "completedAt"); done != "" {
		parts = append(parts, "completed "+done)
	}
	return strings.Join(parts, ", ")
}

// suiteRows joins status.scenarios with status.judge.scores by scenario name,
// in the order of status.scenarios; scored scenarios missing from it follow.
func suiteRows(obj map[string]any) []suiteRow {
	scores, order := judgeScores(obj)
	scenarios, _, _ := unstructured.NestedSlice(obj, "status", "scenarios")
	rows := make([]suiteRow, 0, len(scenarios)+len(order))
	listed := map[string]bool{}
	for _, item := range scenarios {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		row := scenarioRow(m)
		row.judgeScore = scores[row.name]
		listed[row.name] = true
		rows = append(rows, row)
	}
	for _, name := range order {
		if !listed[name] {
			rows = append(rows, suiteRow{name: name, judgeScore: scores[name]})
		}
	}
	return rows
}

// judgeScores indexes status.judge.scores by scenario and keeps their order.
func judgeScores(obj map[string]any) (map[string]judgeScore, []string) {
	items, _, _ := unstructured.NestedSlice(obj, "status", "judge", "scores")
	scores := make(map[string]judgeScore, len(items))
	order := make([]string, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := stringField(m, "scenario")
		scores[name] = judgeScore{score: orZero(intField(m, "score")), reason: stringField(m, "reason")}
		order = append(order, name)
	}
	return scores, order
}

func scenarioRow(m map[string]any) suiteRow {
	row := suiteRow{
		name:       stringField(m, "name"),
		iterations: orZero(intField(m, "iterations")),
		passed:     orZero(intField(m, "passed")),
		failed:     orZero(intField(m, "failed")),
		errs:       orZero(intField(m, "errored")),
	}
	if ms, ok := m["meanDurationMs"].(int64); ok && ms > 0 {
		row.meanDuration = (time.Duration(ms) * time.Millisecond).Round(time.Second).String()
	}
	return row
}

func renderSuiteRows(w io.Writer, rows []suiteRow) error {
	cells := make([][]string, 0, len(rows))
	for _, r := range rows {
		cells = append(cells, []string{r.name, orNone(r.iterations), orNone(r.passed), orNone(r.failed),
			orNone(r.errs), orNone(r.meanDuration), orNone(r.score), r.reason})
	}
	return writeTable(w, []string{"SCENARIO", "ITERATIONS", "PASSED", "FAILED", "ERRORED", "MEAN DURATION", "SCORE", "REASON"}, cells)
}

// stringField reads a string field, "" when absent or not a string.
func stringField(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// intField reads an integer field as text, "" when absent. The dynamic client
// decodes JSON integers as int64.
func intField(m map[string]any, key string) string {
	if v, ok := m[key].(int64); ok {
		return fmt.Sprintf("%d", v)
	}
	return ""
}

// orZero fills an absent count: the operator omits zero counts.
func orZero(s string) string {
	if s == "" {
		return "0"
	}
	return s
}

func orNone(s string) string {
	if s == "" {
		return none
	}
	return s
}
