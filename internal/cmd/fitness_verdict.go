package cmd

import (
	"fmt"
	"strings"

	"github.com/kubemoot/kmctl/internal/clierr"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// toolSmokeCondition is the CrewFitnessSuite condition that says how the tool
// smoke ended; its message names the failing tools.
const toolSmokeCondition = "ToolSmoke"

// suiteVerdict is the result of a finished suite as an exit status: nil when every
// iteration passed its gates (the deterministic assertions), an explained error
// when the suite failed. It reads the gates only; the judge's score is separate
// and never changes the verdict.
func suiteVerdict(suite *unstructured.Unstructured) error {
	name := suite.GetName()
	phase, _, _ := unstructured.NestedString(suite.Object, "status", "phase")
	switch phase {
	case "Completed":
		return completedVerdict(suite, name)
	case "ToolSmokeFailed":
		return suiteFailure(name, "the tool smoke failed, so no model ran: "+toolSmokeMessage(suite))
	case "Cancelled":
		done, _, _ := unstructured.NestedInt64(suite.Object, "status", "iterationsCompleted")
		total, _, _ := unstructured.NestedInt64(suite.Object, "status", "iterationsTotal")
		return suiteFailure(name, fmt.Sprintf("it was stopped after %d of %d iterations", done, total))
	case "Failed", "Error":
		msg, _, _ := unstructured.NestedString(suite.Object, "status", "error")
		return suiteFailure(name, orUnknown(msg, "the operator recorded no reason (see status.error)"))
	}
	return nil
}

// completedVerdict fails a Completed suite when any iteration failed its gates or
// could not run, and names each scenario that did with its reason.
func completedVerdict(suite *unstructured.Unstructured, name string) error {
	failed, _, _ := unstructured.NestedInt64(suite.Object, "status", "failed")
	errored, _, _ := unstructured.NestedInt64(suite.Object, "status", "errored")
	if failed == 0 && errored == 0 {
		return nil
	}
	total, _, _ := unstructured.NestedInt64(suite.Object, "status", "iterationsTotal")
	msg := fmt.Sprintf("%d of %d iterations failed their gates and %d could not run", failed, total, errored)
	if lines := failedScenarioLines(suite); len(lines) > 0 {
		msg += ":\n  " + strings.Join(lines, "\n  ")
	}
	return suiteFailure(name, msg)
}

// failedScenarioLines is one "scenario: reason" line for each scenario with a
// failed or errored iteration. The reason is status.scenarios[].failureReason,
// or the counts when the operator recorded none.
func failedScenarioLines(suite *unstructured.Unstructured) []string {
	scenarios, _, _ := unstructured.NestedSlice(suite.Object, "status", "scenarios")
	var lines []string
	for _, item := range scenarios {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		failed, errored := intOf(m["failed"]), intOf(m["errored"])
		if failed == 0 && errored == 0 {
			continue
		}
		reason, _ := m["failureReason"].(string)
		lines = append(lines, fmt.Sprintf("%v: %s", m["name"],
			orUnknown(reason, fmt.Sprintf("%d failed, %d errored", failed, errored))))
	}
	return lines
}

// toolSmokeMessage is the message of the ToolSmoke condition.
func toolSmokeMessage(suite *unstructured.Unstructured) string {
	conds, _, _ := unstructured.NestedSlice(suite.Object, "status", "conditions")
	for _, c := range conds {
		if m, ok := c.(map[string]any); ok && m["type"] == toolSmokeCondition {
			if msg, _ := m["message"].(string); msg != "" {
				return msg
			}
		}
	}
	return "the operator recorded no message (see status.toolSmoke)"
}

// scenarioVerdict is the verdict of a single-scenario CrewFitness run: nil when
// it passed, an explained error naming the first failed assertion when it did not.
func scenarioVerdict(cf *unstructured.Unstructured, scenario string) error {
	phase, _, _ := unstructured.NestedString(cf.Object, "status", "phase")
	if phase != "Failed" && phase != "Error" {
		return nil
	}
	reason, _, _ := unstructured.NestedString(cf.Object, "status", "error")
	assertions, _, _ := unstructured.NestedSlice(cf.Object, "status", "assertions")
	for _, a := range assertions {
		if m, ok := a.(map[string]any); ok && m["passed"] == false {
			reason, _ = m["message"].(string)
			break
		}
	}
	return clierr.New(fmt.Sprintf("scenario %s %s: %s", scenario, scenarioOutcome(phase), orUnknown(reason, "no reason recorded")),
		fmt.Sprintf("kubectl describe crewfitness %s shows every assertion; a judge score is separate and is not part of this verdict", cf.GetName()), nil)
}

func scenarioOutcome(phase string) string {
	if phase == "Error" {
		return "could not run"
	}
	return "failed its gates"
}

func suiteFailure(name, why string) error {
	return clierr.New(fmt.Sprintf("fitness suite %s failed: %s", name, why),
		fmt.Sprintf("kmctl fitness get %s shows the reason for each scenario; pass/fail is decided by the gates, the judge's score is separate", name), nil)
}

func orUnknown(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// intOf reads a JSON number as the dynamic client decodes it.
func intOf(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
}
