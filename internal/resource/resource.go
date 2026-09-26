// Package resource describes Kubemoot CRD kinds and provides generic list/get +
// table rendering over the dynamic client, so command code stays thin and no
// compile-time dependency on the operator's Go types is needed.
package resource

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/javajon-homelab/kmctl/internal/output"
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
)

// Column is one table column: a header and a dot-path into the object. The
// "AGE" header is rendered as a humanized duration from a timestamp field.
type Column struct {
	Header string
	Path   string
}

// Kind describes a Kubemoot resource type for the generic commands.
type Kind struct {
	Plural   string
	Singular string
	Columns  []Column
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
			{"COORDINATOR", "status.coordinatorRef"},
			{"AGENTS", "status.agentCount"},
			{"AGE", fieldMetadataCreationTimestamp},
		},
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
	ModelProvider = Kind{
		Plural:   "modelproviders",
		Singular: "modelprovider",
		Columns: []Column{
			{"NAME", fieldMetadataName},
			{"TYPE", "spec.type"},
			{"ENDPOINT", "spec.endpoint"},
			{"READY", fieldStatusReady},
			{"AGE", fieldMetadataCreationTimestamp},
		},
	}
	Model = Kind{
		Plural:   "models",
		Singular: "model",
		Columns: []Column{
			{"NAME", fieldMetadataName},
			{"PROVIDER", "spec.providerRef"},
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
			{"PASSED", "status.passed"},
			{"FAILED", "status.failed"},
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
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	out := output.NewWriter(tw)

	headers := make([]string, 0, len(k.Columns)+1)
	if withNamespace {
		headers = append(headers, "NAMESPACE")
	}
	for _, c := range k.Columns {
		headers = append(headers, c.Header)
	}
	out.Printf("%s\n", strings.Join(headers, "\t"))

	for i := range items {
		obj := items[i].Object
		cells := make([]string, 0, len(headers))
		if withNamespace {
			cells = append(cells, items[i].GetNamespace())
		}
		for _, c := range k.Columns {
			cells = append(cells, Cell(obj, c))
		}
		out.Printf("%s\n", strings.Join(cells, "\t"))
	}
	if out.Err() != nil {
		return out.Err()
	}
	return tw.Flush()
}

// Cell extracts a column's display value from an object's map form.
func Cell(obj map[string]any, c Column) string {
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

func humanAge(ts string) string {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return none
	}
	return duration.HumanDuration(time.Since(t))
}
