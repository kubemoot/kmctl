package resource

import (
	"io"
	"strconv"

	"github.com/kubemoot/kmctl/internal/crewstate"
	"github.com/kubemoot/kmctl/internal/output"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// CrewDetailPaths are the Crew status fields RenderCrewDetails reads; add a path
// here with every field it starts using. The Tier 2 contract test checks each one
// against the CRD schema.
var CrewDetailPaths = []string{
	"status.message",
	"status.observedGeneration",
	"status.conditions",
	"status.retention.lastRunTime",
	"status.retention.removed.conversations",
	"status.retention.removed.memory",
	"status.retention.removed.other",
	"status.retention.error",
}

// RenderCrewDetails writes what a Crew reports beyond its row: why it cannot answer
// (the CapacityAvailable condition with the scheduler's message) and its last
// retention cleanup. A section the Crew does not report, as from a Kubemoot that
// predates it, is left out.
func RenderCrewDetails(w io.Writer, obj map[string]any) error {
	out := output.NewWriter(w)
	if cond := crewstate.FindCondition(obj, crewstate.ConditionCapacity); cond != nil {
		out.Printf("\nCapacity:  %s\n", conditionLine(cond))
		if cond.Message != "" {
			out.Printf("           %s\n", cond.Message)
		}
	}
	renderRetention(out, obj)
	return out.Err()
}

func conditionLine(c *crewstate.Condition) string {
	if c.Reason == "" {
		return orNone(c.Status)
	}
	return c.Status + " (" + c.Reason + ")"
}

// renderRetention writes the last cleanup, then the RetentionValid condition when it
// has something to say.
func renderRetention(out *output.Writer, obj map[string]any) {
	ret, found, _ := unstructured.NestedMap(obj, "status", "retention")
	valid := crewstate.FindCondition(obj, crewstate.ConditionRetention)
	if !found && valid == nil {
		return
	}
	out.Printf("\nRetention: %s\n", lastCleanup(ret))
	if msg := stringField(ret, "error"); msg != "" {
		out.Printf("           error: %s\n", msg)
	}
	if valid != nil {
		out.Printf("           RetentionValid: %s\n", conditionLine(valid))
		if valid.Message != "" {
			out.Printf("           %s\n", valid.Message)
		}
	}
}

// lastCleanup is when the last cleanup finished and what it removed.
func lastCleanup(ret map[string]any) string {
	at := stringField(ret, "lastRunTime")
	if at == "" {
		return "no cleanup has run yet"
	}
	removed, _, _ := unstructured.NestedMap(ret, "removed")
	return "last cleanup " + at + ", removed " +
		count(removed, "conversations") + " conversations, " +
		count(removed, "memory") + " memory, " + count(removed, "other") + " other"
}

func count(m map[string]any, key string) string {
	n, _ := int64Field(m, key)
	return strconv.FormatInt(n, 10)
}
