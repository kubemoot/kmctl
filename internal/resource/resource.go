// Package resource describes Kubemoot CRD kinds and provides generic list/get +
// table rendering over the dynamic client, so command code stays thin and no
// compile-time dependency on the operator's Go types is needed.
package resource

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/kubemoot/kmctl/internal/crewstate"
	"github.com/kubemoot/kmctl/internal/output"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/duration"
	"k8s.io/client-go/dynamic"
)

const (
	group   = "kubemoot.ai"
	version = "v1alpha1"
	none    = "<none>"
)

// Common column dot-paths, extracted to consts because they recur across many
// kinds' Column definitions.
const (
	fieldMetadataName              = "metadata.name"
	fieldStatusPhase               = "status.phase"
	fieldStatusReady               = "status.ready"
	fieldMetadataCreationTimestamp = "metadata.creationTimestamp"
	fieldStatusConditions          = "status.conditions"
)

// Computed column headers: their cell is derived from several fields, so Cell does
// not read the column's Path as a dot-path (the Path still names the status field
// the value comes from, which the contract test checks against the CRD).
const (
	headerCapacity       = "CAPACITY"
	headerCapacityReason = "CAPACITY-REASON"
)

// Column is one table column: a header and a dot-path into the object. The
// "AGE" header is rendered as a humanized duration from a timestamp field.
type Column struct {
	Header string
	Path   string
}

// Kind describes a Kubemoot resource type for the generic commands. Wide are the
// columns `-o wide` adds after Columns. Details, when set, writes more of one
// object's status after the row `get` prints.
type Kind struct {
	Plural   string
	Singular string
	Columns  []Column
	Wide     []Column
	Details  func(w io.Writer, obj map[string]any) error
}

// GVR returns the GroupVersionResource for the kind.
func (k Kind) GVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: group, Version: version, Resource: k.Plural}
}

// Crew and Agent mirror the CRDs' additionalPrinterColumns.
var (
	Crew = Kind{
		Plural:   "crews",
		Singular: "crew",
		Columns: []Column{
			{"NAME", fieldMetadataName},
			{"READY", fieldStatusReady},
			{"PHASE", fieldStatusPhase},
			{headerCapacity, fieldStatusConditions},
			{"COORDINATOR", "status.coordinatorRef"},
			{"AGENTS", "status.agentCount"},
			{"AGE", fieldMetadataCreationTimestamp},
		},
		Wide:    []Column{{headerCapacityReason, fieldStatusConditions}},
		Details: RenderCrewDetails,
	}
	Agent = Kind{
		Plural:   "agents",
		Singular: "agent",
		Columns: []Column{
			{"NAME", fieldMetadataName},
			{"TYPE", "spec.type"},
			{"READY", fieldStatusReady},
			{"PHASE", fieldStatusPhase},
			{"ENDPOINT", "status.endpoint"},
			{"AGE", fieldMetadataCreationTimestamp},
		},
	}
	PromptModule = Kind{
		Plural:   "promptmodules",
		Singular: "promptmodule",
		Columns: []Column{
			{"NAME", fieldMetadataName},
			{"ORDER", "spec.order"},
			{"READY", fieldStatusReady},
			{"REFS", "status.referencedBy"},
			{"AGE", fieldMetadataCreationTimestamp},
		},
	}
	ModelServer = Kind{
		Plural:   "modelservers",
		Singular: "modelserver",
		Columns: []Column{
			{"NAME", fieldMetadataName},
			{"TYPE", "spec.type"},
			{"ENDPOINT", "spec.endpoint"},
			{"READY", fieldStatusReady},
			{"CALIBRATION", "status.calibration.phase"},
			{"AGE", fieldMetadataCreationTimestamp},
		},
		Details: RenderCalibration,
	}
	Model = Kind{
		Plural:   "models",
		Singular: "model",
		Columns: []Column{
			{"NAME", fieldMetadataName},
			{"SERVER", "spec.serverRef"},
			{"MODEL", "spec.model"},
			{"STATE", "status.state"},
			{"SIZE", "status.modelInfo.size"},
			{"AGE", fieldMetadataCreationTimestamp},
		},
	}
	CrewFitnessSuite = Kind{
		Plural:   "crewfitnesssuites",
		Singular: "crewfitnesssuite",
		Columns: []Column{
			{"NAME", fieldMetadataName},
			{"PHASE", fieldStatusPhase},
			{"DONE", "status.iterationsCompleted"},
			{"TOTAL", "status.iterationsTotal"},
			{"GATES PASSED", "status.passed"},
			{"GATES FAILED", "status.failed"},
			{"JUDGE", "status.judge.phase"},
			{"JUDGE SCORE", "status.judge.mean"},
			{"AGE", fieldMetadataCreationTimestamp},
		},
		Details: RenderSuiteResults,
	}
	// MCPServerReport is what the MCP gateway learned about an MCP server: the
	// verdict it computed and the one an administrator pinned.
	MCPServerReport = Kind{
		Plural:   "mcpserverreports",
		Singular: "mcpserverreport",
		Columns: []Column{
			{"NAME", fieldMetadataName},
			{"VERDICT", "status.verdict"},
			{"PINNED", "spec.adminVerdict"},
			{"AGE", fieldMetadataCreationTimestamp},
		},
	}
	CrewFitness = Kind{
		Plural:   "crewfitnesses",
		Singular: "crewfitness",
		Columns: []Column{
			{"NAME", fieldMetadataName},
			{"CREW", "spec.crewRef"},
			{"TEST", "spec.testRef"},
			{"PHASE", fieldStatusPhase},
			{"AGE", fieldMetadataCreationTimestamp},
		},
	}
)

// List returns the items of a kind in a namespace, or across all namespaces.
func List(ctx context.Context, dc dynamic.Interface, k Kind, namespace string, allNamespaces bool) (*unstructured.UnstructuredList, error) {
	ri := dc.Resource(k.GVR())
	if allNamespaces {
		return ri.List(ctx, metav1.ListOptions{})
	}
	return ri.Namespace(namespace).List(ctx, metav1.ListOptions{})
}

// Get returns a single named object.
func Get(ctx context.Context, dc dynamic.Interface, k Kind, namespace, name string) (*unstructured.Unstructured, error) {
	return dc.Resource(k.GVR()).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
}

// RenderTable writes items as an aligned table using the kind's columns.
func RenderTable(w io.Writer, k Kind, items []unstructured.Unstructured, withNamespace bool) error {
	return renderColumns(w, k.Columns, items, withNamespace)
}

// RenderTableWide is RenderTable with the kind's wide columns added.
func RenderTableWide(w io.Writer, k Kind, items []unstructured.Unstructured, withNamespace bool) error {
	return renderColumns(w, append(slices.Clone(k.Columns), k.Wide...), items, withNamespace)
}

func renderColumns(w io.Writer, columns []Column, items []unstructured.Unstructured, withNamespace bool) error {
	headers := make([]string, 0, len(columns)+1)
	if withNamespace {
		headers = append(headers, "NAMESPACE")
	}
	for _, c := range columns {
		headers = append(headers, c.Header)
	}
	rows := make([][]string, 0, len(items))
	for i := range items {
		cells := make([]string, 0, len(headers))
		if withNamespace {
			cells = append(cells, items[i].GetNamespace())
		}
		for _, c := range columns {
			cells = append(cells, Cell(items[i].Object, c))
		}
		rows = append(rows, cells)
	}
	return writeTable(w, headers, rows)
}

// writeTable writes a header line and rows as tab-aligned columns.
func writeTable(w io.Writer, headers []string, rows [][]string) error {
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	out := output.NewWriter(tw)
	out.Printf("%s\n", strings.Join(headers, "\t"))
	for _, cells := range rows {
		out.Printf("%s\n", strings.Join(cells, "\t"))
	}
	if out.Err() != nil {
		return out.Err()
	}
	return tw.Flush()
}

// Cell extracts a column's display value from an object's map form.
func Cell(obj map[string]any, c Column) string {
	switch c.Header {
	case headerCapacity:
		return capacityCell(obj, func(cond *crewstate.Condition) string { return cond.Status })
	case headerCapacityReason:
		return capacityCell(obj, func(cond *crewstate.Condition) string { return cond.Reason })
	}
	v, found, err := unstructured.NestedFieldNoCopy(obj, strings.Split(c.Path, ".")...)
	if err != nil || !found || v == nil {
		return none
	}
	if c.Header == "AGE" {
		if ts, ok := v.(string); ok {
			return humanAge(ts)
		}
	}
	return fmt.Sprintf("%v", v)
}

// capacityCell reads one part of a Crew's CapacityAvailable condition. A Crew from a
// Kubemoot that predates the condition shows <none>, as an absent field does.
func capacityCell(obj map[string]any, part func(*crewstate.Condition) string) string {
	cond := crewstate.FindCondition(obj, crewstate.ConditionCapacity)
	if cond == nil || part(cond) == "" {
		return none
	}
	return part(cond)
}

func humanAge(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return none
	}
	return duration.HumanDuration(time.Since(t))
}
