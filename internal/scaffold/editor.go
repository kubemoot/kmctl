package scaffold

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// DefaultSchemaURL is the JSON Schema of every Kubemoot kind, an asset of Kubemoot's
// newest GitHub Release. A release publishes it beside the version of the CRDs it
// ships (releases/download/<tag>/...); the scaffold follows the newest release.
const DefaultSchemaURL = "https://github.com/kubemoot/kubemoot/releases/latest/download/kubemoot_v1alpha1.json"

// Recommended extensions: Red Hat YAML checks the manifests against the schema,
// the Kubernetes extension supplies the Helm language the chart's templates need,
// and CrewForge adds what only Kubemoot knows.
var recommendedExtensions = []string{
	"redhat.vscode-yaml",
	"ms-kubernetes-tools.vscode-kubernetes-tools",
	"kubemoot.crewforge",
}

const (
	settingsPath   = ".vscode/settings.json"
	extensionsPath = ".vscode/extensions.json"
)

// schemaURL is the schema the editor files point at: the option, else the default.
func (o Options) schemaURL() string {
	if o.SchemaURL != "" {
		return o.SchemaURL
	}
	return DefaultSchemaURL
}

// validateSchemaURL accepts an http(s) address or an absolute file URL, which an
// editor can fetch, and nothing that would break the settings file.
func validateSchemaURL(raw string) error {
	if raw == "" {
		return nil
	}
	bad := fmt.Errorf("--schema-url %q is not a URL an editor can fetch (use https://..., http://... or file:///...)", raw)
	u, err := url.Parse(raw)
	if err != nil || strings.ContainsAny(raw, " \t\r\n\"") {
		return bad
	}
	if (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
		return nil
	}
	if u.Scheme == "file" && u.Path != "" {
		return nil
	}
	return bad
}

// editorFiles are the workspace files that make a fresh scaffold check itself in
// VS Code: the schema mapping and the extensions that use it. Every YAML file of the
// crew maps to the one schema, which checks a document only when its kind is a
// Kubemoot kind. A chart's templates hold Helm actions, so they are not YAML until
// Helm renders them: the Helm language owns them and the YAML checks leave them be.
func editorFiles(o Options) (map[string]string, error) {
	settings := map[string]any{
		"yaml.schemas": map[string]any{o.schemaURL(): []string{"**/*.yaml"}},
	}
	if o.Chart {
		settings["files.associations"] = map[string]string{"**/templates/**/*.yaml": "helm"}
	}
	s, err := indentedJSON(settings)
	if err != nil {
		return nil, err
	}
	e, err := indentedJSON(map[string]any{"recommendations": recommendedExtensions})
	if err != nil {
		return nil, err
	}
	files := map[string]string{settingsPath: s, extensionsPath: e}
	if o.Chart {
		files[".helmignore"] = helmIgnore
	}
	return files, nil
}

// helmIgnore keeps the editor's workspace files out of the packaged chart.
const helmIgnore = `# Files that stay in the repository and out of the packaged chart.
.git/
.vscode/
`

func indentedJSON(v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b) + "\n", nil
}
