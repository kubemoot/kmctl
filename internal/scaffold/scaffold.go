// Package scaffold generates the Kubemoot starter crew, the `helm create`
// analog: a small, complete, working crew that is a read-only guide to its own
// namespace. It reads that namespace through one Kubernetes MCP server with a
// read-only Role, so it answers on any cluster with no configuration, and its
// fitness suite uses the crew's own pods as ground truth. Agents declare
// capabilities, never a model; a CrewSchedulingPolicy binds Models by them.
package scaffold

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// KnownModelFamilies are offered in the interactive picker (plus free entry / skip).
var KnownModelFamilies = []string{"qwen", "gemma", "llama", "mistral"}

// Options is the resolved input to the generator (from flags or prompts).
type Options struct {
	Name        string   // technical crew name, a DNS-1123 label: the Kubernetes name of the crew's objects
	DisplayName string   // the name people read, any one line of text; "" means Name
	Members     int      // specialists beside the coordinator, 1 to MaxMembers
	Namespace   string   // bundle only: the namespace the RBAC binds and the prompts name; default crew-<Name>
	Providers   []string // allowed model providers (recorded in the policy + README)
	ModelFamily string   // "" means skip / define later
	OutputDir   string   // directory to write into
	Chart       bool     // lay the crew out as a Helm chart (Chart.yaml, templates/, fitness/)
}

// Validate checks the options before generation.
func (o Options) Validate() error {
	if err := checkName(o.Name); err != nil {
		return err
	}
	if err := checkDisplayName(o.DisplayName); err != nil {
		return err
	}
	if o.Members < 1 || o.Members > MaxMembers {
		return fmt.Errorf("the starter crew has 1 to %d specialists (got %d); add more agents by hand once it runs", MaxMembers, o.Members)
	}
	return nil
}

// Display is the crew's display name: DisplayName without surrounding spaces,
// or Name when it has none.
func (o Options) Display() string {
	if shown := strings.TrimSpace(o.DisplayName); shown != "" {
		return shown
	}
	return o.Name
}

// TargetNamespace is where a bundle is applied: Namespace, or crew-<Name>.
func (o Options) TargetNamespace() string {
	if o.Namespace != "" {
		return o.Namespace
	}
	return "crew-" + o.Name
}

// helmNamespace is what the chart's templates write wherever they need the
// crew's namespace; Helm fills it in at install.
const helmNamespace = "{{ .Release.Namespace }}"

// kubernetesMCPImage is the Kubernetes MCP server the starter crew reads its
// namespace with, the release the reference crews run. Bump it here.
const kubernetesMCPImage = "quay.io/containers/kubernetes_mcp_server:v0.0.63"

// modelSize is a built-in default for a model family. A crew needs Model CRs in
// its namespace (the scheduler binds agents to them just-in-time); without them a
// crew comes Ready but cannot run a discussion. These are sane starting defaults
// to edit, not a claim about any cluster's GPUs.
type modelSize struct {
	Model         string
	Params        string
	LatencyClass  string
	ContextWindow string
	VRAMMiB       int
}

// modelFamilies maps a family to a small low/medium/high ladder.
var modelFamilies = map[string][]modelSize{
	"qwen": {
		{"qwen3:8b", "8b", "low", "40960", 5120},
		{"qwen3:14b", "14b", "medium", "40960", 10240},
		{"qwen3:32b", "32b", "high", "40960", 20480},
	},
	"gemma": {
		{"gemma3:4b", "4b", "low", "8192", 4096},
		{"gemma3:12b", "12b", "medium", "8192", 9216},
		{"gemma3:27b", "27b", "high", "8192", 18432},
	},
	"llama": {
		{"llama3.1:8b", "8b", "low", "131072", 5120},
		{"llama3.3:70b", "70b", "high", "131072", 43008},
	},
	"mistral": {
		{"mistral:7b", "7b", "low", "32768", 4608},
		{"mistral-small:24b", "24b", "high", "32768", 15360},
	},
}

type modelCR struct {
	Name          string
	Model         string
	ProviderRef   string
	Family        string
	Params        string
	LatencyClass  string
	ContextWindow string
	VRAMMiB       int
}

type templateData struct {
	Name               string
	DisplayName        string // the display name as a YAML scalar, for Chart.yaml
	CrewDisplayName    string // the display name as the Crew's manifest writes it (Helm-safe in a chart)
	Title              string // the README's title
	Renamed            bool   // the display name differs from the technical name
	Agents             []agent
	HasReviewer        bool
	Has                map[string]bool // specialist key -> in this crew
	NS                 string          // the crew's namespace as the manifests write it
	TargetNS           string          // the namespace the README installs into
	Rules              string          // the read-only RBAC rules
	KubernetesMCPImage string          // the tool server image
	Providers          []string
	ModelFamily        string
	HasFamily          bool
	Models             []modelCR
	HasModels          bool
	Chart              bool
}

func (o Options) data() templateData {
	shown := o.Display()
	d := templateData{
		Name:               o.Name,
		DisplayName:        quoted(shown, false),
		CrewDisplayName:    quoted(shown, o.Chart),
		Title:              o.Name + " crew",
		Renamed:            shown != o.Name,
		Providers:          o.Providers,
		ModelFamily:        o.ModelFamily,
		HasFamily:          o.ModelFamily != "",
		Chart:              o.Chart,
		TargetNS:           o.TargetNamespace(),
		Rules:              rbacRules,
		KubernetesMCPImage: kubernetesMCPImage,
		Models:             o.models(),
		Agents:             o.agents(),
	}
	d.NS = d.TargetNS
	if o.Chart {
		d.NS = helmNamespace
	}
	if d.Renamed {
		d.Title = shown
	}
	d.Has = map[string]bool{}
	for _, a := range d.Agents {
		d.Has[a.Key] = true
	}
	d.HasReviewer = d.Has[reviewer.Key]
	d.HasModels = len(d.Models) > 0
	return d
}

// agents is the crew's specialists, or none when Members is out of range
// (Generate validates first; ModelCount does not need them).
func (o Options) agents() []agent {
	if o.Members < 1 || o.Members > MaxMembers {
		return nil
	}
	return crewAgents(o.Name, o.Members)
}

// models is a Model per (family size x provider), so the scheduler has something to bind.
func (o Options) models() []modelCR {
	family := strings.ToLower(o.ModelFamily)
	var out []modelCR
	for _, size := range modelFamilies[family] {
		for _, p := range o.Providers {
			out = append(out, modelCR{
				Name:          fmt.Sprintf("%s-%s-%s", family, size.Params, p),
				Model:         size.Model,
				ProviderRef:   p,
				Family:        family,
				Params:        size.Params,
				LatencyClass:  size.LatencyClass,
				ContextWindow: size.ContextWindow,
				VRAMMiB:       size.VRAMMiB,
			})
		}
	}
	return out
}

// ModelCount reports how many Model CRs the scaffold will generate for these
// options (one per family-size x provider). Zero means the crew will apply Ready
// but be Unschedulable - no Models for the scheduler to bind agents to - until
// Models are added to models.yaml. Callers (kmctl create) should warn on zero.
func (o Options) ModelCount() int {
	return len(o.models())
}

// Generate returns a map of relative filename -> rendered content.
func Generate(o Options) (map[string]string, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	d := o.data()
	files := layout(o.Chart)
	out := make(map[string]string, len(files))
	for name, tmpl := range files {
		rendered, err := render(name, tmpl, d)
		if err != nil {
			return nil, fmt.Errorf("render %s: %w", name, err)
		}
		out[name] = rendered
	}
	return out, nil
}

// layout maps each output path to its template. Both forms keep the fitness
// suite in fitness/, so installing the crew does not start a run. The bundle puts
// its RBAC in access/: kmctl apply manages only kubemoot.ai kinds, and reads one
// directory without descending, so kubectl applies access/ and kmctl the rest.
// The chart's RBAC is a Helm template, so values.yaml can widen it.
func layout(chart bool) map[string]string {
	files := map[string]string{
		"crew.yaml":          crewTemplate,
		"agents.yaml":        agentsTemplate,
		"promptmodules.yaml": promptModulesTemplate,
		"models.yaml":        modelsTemplate,
		"tools.yaml":         toolsTemplate,
	}
	out := map[string]string{
		"fitness/fitness.yaml": fitnessTemplate,
		"README.md":            readmeTemplate,
	}
	prefix := ""
	if chart {
		prefix = "templates/"
		files["rbac.yaml"] = chartRBACTemplate
		out["Chart.yaml"] = chartTemplate
		out["values.yaml"] = valuesTemplate
	} else {
		out["access/rbac.yaml"] = bundleRBACTemplate
	}
	for name, tmpl := range files {
		out[prefix+name] = tmpl
	}
	return out
}

// Write generates the scaffold and writes it into OutputDir/<name>, returning
// the written file paths. It refuses to overwrite an existing directory.
func Write(o Options) ([]string, error) {
	files, err := Generate(o)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(o.OutputDir, o.Name)
	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("directory %q already exists; choose another name or remove it", dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	written := make([]string, 0, len(files))
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return nil, err
		}
		written = append(written, p)
	}
	return written, nil
}

// render executes a template with [[ ]] delimiters, so the Helm actions the chart
// carries ({{ .Release.Namespace }}, {{ .Values... }}) pass through untouched.
func render(name, tmpl string, d templateData) (string, error) {
	t, err := template.New(name).Delims("[[", "]]").Funcs(template.FuncMap{"indent": indent}).Parse(tmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, d); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// indent prefixes every non-empty line of s with n spaces, for block scalars.
func indent(n int, s string) string {
	pad := strings.Repeat(" ", n)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = pad + l
		}
	}
	return strings.Join(lines, "\n")
}
