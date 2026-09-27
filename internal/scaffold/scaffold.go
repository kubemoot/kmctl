// Package scaffold generates a minimal, valid, ready-to-apply Kubemoot crew -
// the `helm create` analog. The output is a starting point to customize: it
// applies cleanly and wires the resources together with loose model coupling
// (agents declare capabilities; a CrewSchedulingPolicy picks models), but the
// ADL is intentionally minimal.
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
	Name        string   // crew name (DNS-1123)
	Members     int      // number of tooler agents (>= 1)
	Providers   []string // allowed model providers (recorded in the policy + README)
	ModelFamily string   // "" means skip / define later
	OutputDir   string   // directory to write into
	Chart       bool     // lay the crew out as a Helm chart (Chart.yaml, templates/, fitness/)
}

// Validate checks the options before generation.
func (o Options) Validate() error {
	if o.Name == "" {
		return fmt.Errorf("crew name is required")
	}
	if strings.ToLower(o.Name) != o.Name || strings.ContainsAny(o.Name, " _.") {
		return fmt.Errorf("crew name %q must be lowercase DNS-1123 (letters, digits, -)", o.Name)
	}
	if o.Members < 1 {
		return fmt.Errorf("a crew needs at least 1 tooler member (got %d)", o.Members)
	}
	return nil
}

type tooler struct {
	Name    string
	Channel string
}

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
	Name        string
	Members     []tooler
	Providers   []string
	ModelFamily string
	HasFamily   bool
	Models      []modelCR
	HasModels   bool
	Chart       bool
}

func (o Options) data() templateData {
	d := templateData{
		Name:        o.Name,
		Providers:   o.Providers,
		ModelFamily: o.ModelFamily,
		HasFamily:   o.ModelFamily != "",
		Chart:       o.Chart,
	}
	for i := 1; i <= o.Members; i++ {
		d.Members = append(d.Members, tooler{
			Name:    fmt.Sprintf("%s-tooler-%d", o.Name, i),
			Channel: fmt.Sprintf("topic-%d", i),
		})
	}
	// A Model per (family-size x provider), so the scheduler has something to bind.
	for _, size := range modelFamilies[strings.ToLower(o.ModelFamily)] {
		for _, p := range o.Providers {
			d.Models = append(d.Models, modelCR{
				Name:          fmt.Sprintf("%s-%s-%s", strings.ToLower(o.ModelFamily), size.Params, p),
				Model:         size.Model,
				ProviderRef:   p,
				Family:        strings.ToLower(o.ModelFamily),
				Params:        size.Params,
				LatencyClass:  size.LatencyClass,
				ContextWindow: size.ContextWindow,
				VRAMMiB:       size.VRAMMiB,
			})
		}
	}
	d.HasModels = len(d.Models) > 0
	return d
}

// ModelCount reports how many Model CRs the scaffold will generate for these
// options (one per family-size x provider). Zero means the crew will apply Ready
// but be Unschedulable - no Models for the scheduler to bind agents to - until
// Models are added to models.yaml. Callers (kmctl create) should warn on zero.
func (o Options) ModelCount() int {
	return len(o.data().Models)
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

// layout maps each output path to its template. The chart form keeps the same
// manifests: Helm installs them as they are into the release namespace, and the
// fitness suite sits outside templates/ so an install does not start a run.
func layout(chart bool) map[string]string {
	if !chart {
		return map[string]string{
			"crew.yaml":          crewTemplate,
			"agents.yaml":        agentsTemplate,
			"promptmodules.yaml": promptModulesTemplate,
			"models.yaml":        modelsTemplate,
			"fitness.yaml":       fitnessTemplate,
			"README.md":          readmeTemplate,
		}
	}
	return map[string]string{
		"Chart.yaml":                   chartTemplate,
		"values.yaml":                  valuesTemplate,
		"templates/crew.yaml":          crewTemplate,
		"templates/agents.yaml":        agentsTemplate,
		"templates/promptmodules.yaml": promptModulesTemplate,
		"templates/models.yaml":        modelsTemplate,
		"fitness/fitness.yaml":         fitnessTemplate,
		"README.md":                    readmeTemplate,
	}
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

func render(name, tmpl string, d templateData) (string, error) {
	t, err := template.New(name).Parse(tmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, d); err != nil {
		return "", err
	}
	return buf.String(), nil
}
