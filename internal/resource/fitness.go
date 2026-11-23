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
	"status.scenarios.failureReason",
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
	failureReason                    string
	judgeScore
}

// RenderSuiteResults writes what the operator records on a CrewFitnessSuite's
// status after the summary row: why the suite failed when it did, a judge summary
// line, then one row per scenario joining status.scenarios (the gates) and
// status.judge.scores (the judge), the gate failure of each scenario that has
// one, and a line saying what the two kinds of column mean. It writes nothing
// when the status carries none of these.
func RenderSuiteResults(w io.Writer, obj map[string]any) error {
	judge, hasJudge, _ := unstructured.NestedMap(obj, "status", "judge")
	rows := suiteRows(obj)
	reason := suiteFailureReason(obj)
	if !hasJudge && len(rows) == 0 && reason == "" {
		return nil
	}
	out := output.NewWriter(w)
	if reason != "" {
		out.Printf("\nFailure: %s\n", reason)
	}
	if hasJudge {
		out.Printf("\nJudge: %s\n", judgeSummary(judge))
	}
	if out.Err() != nil || len(rows) == 0 {
		return out.Err()
	}
	if err := renderGateNotes(w, rows); err != nil {
		return err
	}
	return renderSuiteRows(w, rows)
}

// suiteFailureReason is the one-line reason a suite did not run to a result:
// status.error for an Error or Failed suite, the ToolSmoke condition message for
// a ToolSmokeFailed one. Empty for every other phase.
func suiteFailureReason(obj map[string]any) string {
	phase := stringField(mapAt(obj, "status"), "phase")
	switch phase {
	case "Error", "Failed":
		return stringField(mapAt(obj, "status"), "error")
	case "ToolSmokeFailed":
		conds, _, _ := unstructured.NestedSlice(obj, "status", "conditions")
		for _, c := range conds {
			if m, ok := c.(map[string]any); ok && m["type"] == "ToolSmoke" {
				return stringField(m, "message")
			}
		}
	}
	return ""
}

func mapAt(obj map[string]any, field string) map[string]any {
	m, _, _ := unstructured.NestedMap(obj, field)
	return m
}

// renderGateNotes lists why each scenario failed its gates, then says what the
// gate and judge columns of the table that follows mean, so the two cannot be
// read as one result.
func renderGateNotes(w io.Writer, rows []suiteRow) error {
	out := output.NewWriter(w)
	header := false
	for _, r := range rows {
		if r.failureReason == "" {
			continue
		}
		if !header {
			out.Printf("\nGate failures:\n")
			header = true
		}
		out.Printf("  %s: %s\n", r.name, r.failureReason)
	}
	out.Printf("\nGATES are the scenario's deterministic assertions, checked as each iteration ran.\n" +
		"JUDGE SCORE is the judge's 0-100 rating of the answer against the reference.\n" +
		"They are independent: an iteration can pass its gates and score low, or fail a gate and score high.\n\n")
	return out.Err()
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

		failureReason: stringField(m, "failureReason"),
	}
	if ms, ok := positiveInt(m, "meanDurationMs"); ok {
		row.meanDuration = formatMillis(ms, time.Second)
	}
	return row
}

func renderSuiteRows(w io.Writer, rows []suiteRow) error {
	cells := make([][]string, 0, len(rows))
	for _, r := range rows {
		cells = append(cells, []string{r.name, orNone(r.iterations), orNone(r.passed), orNone(r.failed),
			orNone(r.errs), orNone(r.meanDuration), orNone(r.score), r.reason})
	}
	return writeTable(w, []string{"SCENARIO", "ITERATIONS", "GATES PASSED", "GATES FAILED", "ERRORED", "MEAN DURATION", "JUDGE SCORE", "JUDGE REASON"}, cells)
}

// orZero renders an absent count or score as 0, so a field the operator left out
// (an omitempty count) or a hand-written object without it still reads as a number.
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
