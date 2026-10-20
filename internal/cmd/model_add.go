package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/clierr"
	"github.com/kubemoot/kmctl/internal/resource"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
)

// modelAbilities are the abilities a Model can declare for its catalog entry.
var modelAbilities = []string{"tool-calling", "reasoning", "vision"}

// parameterCount is the form Model spec.parameters takes: 14B, 0.5B, 751M.
var parameterCount = regexp.MustCompile(resource.ParameterCountPattern)

// modelAddition is what `kmctl model add` writes: a Model for one model on one
// model server.
type modelAddition struct {
	model        string
	server       string
	parameters   string
	capabilities []string
}

func newModelAddCommand(f *client.Factory) *cobra.Command {
	var add modelAddition
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "add MODEL --model-server NAME",
		Short: "Add a model to the cluster's model catalog (for cluster owners)",
		Long: `Add writes a Model object into the namespace of the model server --model-server names:
the cluster owner's way to put a model in the cluster's model catalog. Crews carry
no Models; they select from the catalog by label.

MODEL is the name requests use: the Ollama tag (qwen3:14b) or the vLLM served name.
On an Ollama model server the operator downloads the model. A vLLM model server
serves what it was started with; add its models to say what they are.

--parameters declares the total parameter count (14B, 0.5B), which sets the size
class: small under 12B, medium from 12B to under 30B, large from 30B. A vLLM model
server cannot report it. --capabilities declares abilities the model server cannot
report (tool-calling, reasoning, vision); they replace what it reports.

The Model is named after the model server and the model (ollama-gpu, qwen3:14b ->
ollama-gpu-qwen3-14b-<hash>), so adding the same model again updates it. Without
-n, add looks for the model server in every namespace and needs exactly one.
kmctl model list shows the entry once the operator has merged it.`,
		Example: `  kmctl model add qwen3:14b --model-server ollama-gpu
  kmctl model add qwen3-14b --model-server vllm-gpu --parameters 14B --capabilities tool-calling,reasoning
  kmctl model add qwen3:8b --model-server ollama-gpu --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			add.model = args[0]
			add.capabilities = trimmed(add.capabilities)
			if err := add.validate(); err != nil {
				return err
			}
			dc, err := f.Dynamic()
			if err != nil {
				return fmt.Errorf("build client: %w", err)
			}
			ns, err := serverNamespace(cmd.Context(), dc, add.server, explicitNamespace(f))
			if err != nil {
				return err
			}
			return applyModel(cmd, dc, add.object(ns), dryRun)
		},
	}
	cmd.Flags().StringVar(&add.server, "model-server", "", "The model server that offers the model (required)")
	cmd.Flags().StringVar(&add.parameters, "parameters", "", "Total parameter count, such as 14B or 0.5B")
	cmd.Flags().StringSliceVar(&add.capabilities, "capabilities", nil, "Abilities the model server cannot report: "+strings.Join(modelAbilities, ", "))
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Submit a server-side dry-run without persisting")
	_ = cmd.MarkFlagRequired("model-server")
	return cmd
}

// validate checks the inputs before kmctl calls the cluster.
func (a modelAddition) validate() error {
	if strings.TrimSpace(a.model) == "" || strings.ContainsAny(a.model, " \t\n") {
		return fmt.Errorf("MODEL is the name requests use for the model, with no spaces (got %q)", a.model)
	}
	if strings.TrimSpace(a.server) == "" {
		return fmt.Errorf("--model-server names the model server that offers the model")
	}
	if a.parameters != "" && !parameterCount.MatchString(a.parameters) {
		return fmt.Errorf("--parameters is a count with a unit, such as 14B or 0.5B (got %q)", a.parameters)
	}
	for _, c := range a.capabilities {
		if !slices.Contains(modelAbilities, c) {
			return fmt.Errorf("unknown capability %q (want %s)", c, strings.Join(modelAbilities, ", "))
		}
	}
	return nil
}

// object is the Model to apply in namespace ns.
func (a modelAddition) object(ns string) *unstructured.Unstructured {
	spec := map[string]any{"serverRef": a.server, "model": a.model}
	if a.parameters != "" {
		spec["parameters"] = a.parameters
	}
	if caps := uniqueSorted(a.capabilities); len(caps) > 0 {
		spec["capabilities"] = caps
	}
	gvk := resource.Model.GVR().GroupVersion().WithKind("Model")
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": gvk.GroupVersion().String(),
		"kind":       gvk.Kind,
		"metadata":   map[string]any{"name": modelObjectName(a.server, a.model), "namespace": ns},
		"spec":       spec,
	}}
}

// trimmed is values with surrounding spaces removed, so "a, b" reads as a and b.
func trimmed(values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = strings.TrimSpace(v)
	}
	return out
}

// uniqueSorted is values sorted with duplicates dropped, as the []any an
// unstructured object holds.
func uniqueSorted(values []string) []any {
	sorted := slices.Compact(slices.Sorted(slices.Values(values)))
	out := make([]any, len(sorted))
	for i, v := range sorted {
		out[i] = v
	}
	return out
}

// nonNameRun is a run of characters a Kubernetes object name cannot hold.
var nonNameRun = regexp.MustCompile(`[^a-z0-9-]+`)

// maxObjectName is the longest name a Kubernetes object may have.
const maxObjectName = 253

// nameHashLength is how many hex digits of the hash a changed name carries.
const nameHashLength = 8

// modelObjectName is the Model's name: <server>-<model> when that is already a
// valid name, else the lowercased form with every run of other characters turned
// into one hyphen, followed by a hash of the exact server and model. The hash keeps
// two models whose names differ only in such characters (qwen3:14b and qwen3-14b)
// from landing on one Model; the result is always a DNS-1123 name.
func modelObjectName(server, model string) string {
	raw := server + "-" + model
	name := strings.Trim(nonNameRun.ReplaceAllString(strings.ToLower(raw), "-"), "-")
	for strings.Contains(name, "--") {
		name = strings.ReplaceAll(name, "--", "-")
	}
	if name == raw && len(name) <= maxObjectName {
		return name
	}
	sum := sha256.Sum256([]byte(server + "/" + model))
	hash := hex.EncodeToString(sum[:])[:nameHashLength]
	if limit := maxObjectName - nameHashLength - 1; len(name) > limit {
		name = strings.TrimRight(name[:limit], "-")
	}
	if name == "" {
		return hash
	}
	return name + "-" + hash
}

// serverNamespace is the namespace of the model server named server: the one -n
// names, or the only namespace that has a model server of that name.
func serverNamespace(ctx context.Context, dc dynamic.Interface, server, namespace string) (string, error) {
	if namespace != "" {
		if _, err := resource.Get(ctx, dc, resource.ModelServer, namespace, server); err != nil {
			return "", err
		}
		return namespace, nil
	}
	list, err := resource.List(ctx, dc, resource.ModelServer, "", true)
	if err != nil {
		return "", err
	}
	var found []string
	for i := range list.Items {
		if list.Items[i].GetName() == server {
			found = append(found, list.Items[i].GetNamespace())
		}
	}
	return oneNamespace(server, found)
}

// oneNamespace is the single namespace a model server was found in, or an error
// that says what to do when it was found in none or several.
func oneNamespace(server string, found []string) (string, error) {
	switch len(found) {
	case 0:
		return "", clierr.New(fmt.Sprintf("no model server %q in the cluster", server), "List them with: kmctl model server list -A", nil)
	case 1:
		return found[0], nil
	}
	return "", clierr.New(fmt.Sprintf("model server %q is in several namespaces: %s", server, strings.Join(found, ", ")), "Pass -n NAMESPACE", nil)
}

// applyModel server-side applies the Model and says where it went.
func applyModel(cmd *cobra.Command, dc dynamic.Interface, obj *unstructured.Unstructured, dryRun bool) error {
	ri := dc.Resource(resource.Model.GVR()).Namespace(obj.GetNamespace())
	applied, err := serverSideApply(cmd.Context(), ri, obj, applyOpts{dryRun: dryRun})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "model/%s applied in namespace %s%s\n", applied.GetName(), obj.GetNamespace(), dryRunSuffix(dryRun))
	return err
}
