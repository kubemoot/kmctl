package resource

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
)

// ToolCallingLabel is the catalog label of a model that can call tools, the
// label every crew kmctl create scaffolds requires.
const ToolCallingLabel = "capability/tool-calling"

// CatalogPaths are the ModelServer status fields the catalog reads; add a path
// here with every field it starts using. The Tier 2 contract test checks each one
// against the CRD schema.
var CatalogPaths = []string{
	"status.models.name",
	"status.models.present",
	"status.models.size",
	"status.models.parameterCount",
	"status.models.contextLength",
	"status.models.capabilities",
	"status.models.labels",
}

// ModelAddPaths are the Model fields `kmctl model add` writes; the Tier 2
// contract test checks each one against the CRD schema.
var ModelAddPaths = []string{
	"spec.serverRef",
	"spec.model",
	"spec.parameters",
	"spec.capabilities",
}

// ParameterCountPattern is the form Model spec.parameters takes (14B, 0.5B,
// 751M), the CRD's own pattern; the contract test keeps the two equal.
const ParameterCountPattern = `^[0-9]+(\.[0-9]+)?[KkMmBbTt]$`

// CatalogEntry is one model in the cluster's model catalog as one model server
// offers it: an entry of that ModelServer's status.models.
type CatalogEntry struct {
	Model          string            `json:"model"`
	Server         string            `json:"server"`
	Namespace      string            `json:"namespace"`
	Present        bool              `json:"present"`
	Size           string            `json:"size,omitempty"`
	ParameterCount int64             `json:"parameterCount,omitempty"`
	ContextLength  int64             `json:"contextLength,omitempty"`
	Capabilities   []string          `json:"capabilities,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
}

// CallsTools reports whether the entry carries the tool-calling label, the label
// scheduling rules match.
func (e CatalogEntry) CallsTools() bool {
	return e.Labels[ToolCallingLabel] == "true"
}

// Catalog is the cluster's model catalog: the model servers found and the
// models they offer.
type Catalog struct {
	Servers int
	Entries []CatalogEntry
}

// HasToolCalling reports whether any model in the catalog can call tools.
func (c Catalog) HasToolCalling() bool {
	for _, e := range c.Entries {
		if e.CallsTools() {
			return true
		}
	}
	return false
}

// ReadCatalog lists the ModelServers in namespace, or in every namespace when
// namespace is "", and collects the models each one offers.
func ReadCatalog(ctx context.Context, dc dynamic.Interface, namespace string) (Catalog, error) {
	list, err := List(ctx, dc, ModelServer, namespace, namespace == "")
	if err != nil {
		return Catalog{}, err
	}
	return CatalogOf(list.Items), nil
}

// CatalogOf collects the catalog entries of the given ModelServers, sorted by
// model and then by server.
func CatalogOf(servers []unstructured.Unstructured) Catalog {
	c := Catalog{Servers: len(servers)}
	for i := range servers {
		c.Entries = append(c.Entries, serverEntries(&servers[i])...)
	}
	sort.SliceStable(c.Entries, func(i, j int) bool {
		a, b := c.Entries[i], c.Entries[j]
		if a.Model != b.Model {
			return a.Model < b.Model
		}
		return a.Namespace+"/"+a.Server < b.Namespace+"/"+b.Server
	})
	return c
}

// serverEntries are the entries of one ModelServer's status.models; an item
// without a name is skipped.
func serverEntries(server *unstructured.Unstructured) []CatalogEntry {
	items, _, _ := unstructured.NestedSlice(server.Object, "status", "models")
	out := make([]CatalogEntry, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok || stringField(m, "name") == "" {
			continue
		}
		e := catalogEntry(m)
		e.Server, e.Namespace = server.GetName(), server.GetNamespace()
		out = append(out, e)
	}
	return out
}

// catalogEntry reads one status.models item.
func catalogEntry(m map[string]any) CatalogEntry {
	present, _ := boolField(m, "present")
	caps, _, _ := unstructured.NestedStringSlice(m, "capabilities")
	labels, _, _ := unstructured.NestedStringMap(m, "labels")
	return CatalogEntry{
		Model:          stringField(m, "name"),
		Present:        present,
		Size:           stringField(m, "size"),
		ParameterCount: positiveOrZero(m, "parameterCount"),
		ContextLength:  positiveOrZero(m, "contextLength"),
		Capabilities:   caps,
		Labels:         labels,
	}
}

// positiveOrZero reads a whole-number field above zero, else 0 (unknown).
func positiveOrZero(m map[string]any, key string) int64 {
	if v, ok := positiveInt(m, key); ok {
		return v
	}
	return 0
}

// RenderCatalog writes the catalog as a table, one row per model per server.
func RenderCatalog(w io.Writer, entries []CatalogEntry) error {
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, []string{e.Model, e.Server, orUnknown(e.Size), abilities(e), contextTokens(e.ContextLength), yesNo(e.Present, true)})
	}
	return writeTable(w, []string{"MODEL", "SERVER", "SIZE", "ABILITIES", "CONTEXT", "PRESENT"}, rows)
}

// abilities lists what the model can do, sorted, or "none".
func abilities(e CatalogEntry) string {
	if len(e.Capabilities) == 0 {
		return "none"
	}
	sorted := append([]string(nil), e.Capabilities...)
	sort.Strings(sorted)
	return strings.Join(sorted, ",")
}

// contextTokens renders a context window in tokens, or "unknown" when it is 0.
func contextTokens(n int64) string {
	if n <= 0 {
		return "unknown"
	}
	return fmt.Sprintf("%d", n)
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
