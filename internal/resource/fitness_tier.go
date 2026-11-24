package resource

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// tierResultPaths are the status fields the tier and path-coverage lines read.
var tierResultPaths = []string{
	"status.tier.name",
	"status.tier.source",
	"status.tier.available",
	"status.tier.scenarios",
	"status.pathCoverage.calls",
	"status.pathCoverage.transcripts",
	"status.pathCoverage.kinds.name",
	"status.pathCoverage.kinds.calls",
	"status.pathCoverage.phases.name",
	"status.pathCoverage.phases.calls",
	"status.pathCoverage.models.name",
	"status.pathCoverage.models.calls",
	"status.pathCoverage.providers.name",
	"status.pathCoverage.providers.calls",
	"status.pathCoverage.thinking.name",
	"status.pathCoverage.thinking.calls",
	"status.pathCoverage.gaps",
}

// pathCoverageTables are the rows of status.pathCoverage, in the order they print.
var pathCoverageTables = []string{"kinds", "phases", "models", "providers", "thinking"}

// tierLine says which tier a suite resolved its scenarios from, or "" when the
// suite listed its own scripts.
func tierLine(obj map[string]any) string {
	tier := mapAt(obj, "status")
	t, _, _ := unstructured.NestedMap(tier, "tier")
	if len(t) == 0 {
		return ""
	}
	chosen, _, _ := unstructured.NestedStringSlice(t, "scenarios")
	line := fmt.Sprintf("%s, %d of %s scenarios", orNone(stringField(t, "name")), len(chosen), orNone(intField(t, "available")))
	if src := stringField(t, "source"); src != "" {
		line += " from " + src
	}
	return line
}

// pathCoverageLines prints the path-coverage table of a run: what it exercised and
// the gaps. It returns nothing while the operator has not written the table.
func pathCoverageLines(obj map[string]any) []string {
	cov, _, _ := unstructured.NestedMap(obj, "status", "pathCoverage")
	if len(cov) == 0 {
		return nil
	}
	lines := []string{fmt.Sprintf("Path coverage: %s calls in %s transcripts", orZero(intField(cov, "calls")), orZero(intField(cov, "transcripts")))}
	for _, name := range pathCoverageTables {
		if row := coverageRow(cov, name); row != "" {
			lines = append(lines, fmt.Sprintf("  %-10s %s", name+":", row))
		}
	}
	gaps, _, _ := unstructured.NestedStringSlice(cov, "gaps")
	if len(gaps) == 0 {
		return append(lines, "  gaps:      none")
	}
	for i, g := range gaps {
		label := "           "
		if i == 0 {
			label = "  gaps:     "
		}
		lines = append(lines, label+g)
	}
	return lines
}

// coverageRow renders one table as "name count, name count".
func coverageRow(cov map[string]any, table string) string {
	items, _, _ := unstructured.NestedSlice(cov, table)
	parts := make([]string, 0, len(items))
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			parts = append(parts, stringField(m, "name")+" "+orZero(intField(m, "calls")))
		}
	}
	return strings.Join(parts, ", ")
}
