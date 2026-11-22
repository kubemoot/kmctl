package scaffold

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/kubemoot/kubemoot/operator/pkg/workspacetools"
)

// deprecatedNames lists the names Kubemoot renamed when the discussion's shared
// store became the workspace. A crew that names one runs only because the operator
// still honours it for one release, and the MCPServer reports it in its
// DeprecatedNames condition. The list is Kubemoot's own, published as
// workspacetools.DeprecatedNames in the operator module go.mod pins, so a name
// renamed later is caught here with no edit.
func deprecatedNames() workspacetools.Deprecated {
	return workspacetools.DeprecatedNames()
}

// findDeprecated reports each deprecated name the files use, as "file: name". A name
// counts only where it starts a word, so the current WORKSPACE_QUERY_MAX_ARTIFACT_BYTES
// is not the old ARTIFACT_ prefix.
func findDeprecated(files map[string]string, names workspacetools.Deprecated) []string {
	var found []string
	for _, file := range slices.Sorted(maps.Keys(files)) {
		for _, name := range names.Find(files[file]) {
			found = append(found, file+": "+name)
		}
	}
	return found
}

// TestScaffoldNamesNoDeprecatedName checks every file the scaffold writes, bundle
// and chart at every size, README and values included.
func TestScaffoldNamesNoDeprecatedName(t *testing.T) {
	names := deprecatedNames()
	if len(names.Names()) < 7+7 {
		t.Fatalf("the deprecated-name list is %d long; the published tool list alone is 7", len(names.Names()))
	}
	for _, chart := range []bool{false, true} {
		for members := 1; members <= MaxMembers; members++ {
			files, err := Generate(Options{Name: "demo", Members: members, Chart: chart})
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			for _, hit := range findDeprecated(files, names) {
				t.Errorf("chart=%v members=%d: %s is deprecated", chart, members, hit)
			}
		}
	}
}

// TestFindDeprecatedFlagsAFileThatNamesOne shows the scan can fail: files that name
// an old tool, an old environment variable, or the old mount path are reported, and
// a file that names only current names is not.
func TestFindDeprecatedFlagsAFileThatNamesOne(t *testing.T) {
	files := map[string]string{
		"templates/agents.yaml": "tools: [artifact_head, workspace_query]",
		"templates/tools.yaml":  "env:\n  - name: ARTIFACT_MODE\n    value: mcp\n  - mountPath: /artifacts",
		"templates/crew.yaml":   "tools: [workspace_head]\nmountPath: /kubemoot/workspace",
	}
	got := findDeprecated(files, deprecatedNames())
	for _, want := range []string{
		"templates/agents.yaml: artifact_head",
		"templates/tools.yaml: ARTIFACT_",
		"templates/tools.yaml: /artifacts",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("findDeprecated missed %q; found %v", want, got)
		}
	}
	for _, hit := range got {
		if strings.HasPrefix(hit, "templates/crew.yaml") {
			t.Errorf("a file with only current names was flagged: %s", hit)
		}
	}
}

// TestCurrentNamesAreNotFlagged keeps the current workspace names out of the findings, including
// the one that contains the old environment prefix.
func TestCurrentNamesAreNotFlagged(t *testing.T) {
	files := map[string]string{"values.yaml": "WORKSPACE_QUERY_MAX_ARTIFACT_BYTES WORKSPACE_MODE workspace-access workspace_query"}
	if got := findDeprecated(files, deprecatedNames()); len(got) != 0 {
		t.Errorf("current names flagged: %v", got)
	}
}

// TestFindDeprecatedFlagsTheOldAcceleratorNames shows the published list carries the accelerator
// renames: a Model that sets vramMib, or a manifest that names the gpu-busy reason, is reported,
// and the current names are not.
func TestFindDeprecatedFlagsTheOldAcceleratorNames(t *testing.T) {
	files := map[string]string{
		"templates/model.yaml":  "spec:\n  model: qwen3:14b\n  vramMib: 11000",
		"templates/reason.yaml": "reason: gpu-busy",
		"templates/ok.yaml":     "spec:\n  acceleratorMemoryMiB: 11000\nreason: accelerator-busy",
	}
	got := findDeprecated(files, deprecatedNames())
	for _, want := range []string{"templates/model.yaml: vramMib", "templates/reason.yaml: gpu-busy"} {
		if !slices.Contains(got, want) {
			t.Errorf("findDeprecated missed %q; found %v", want, got)
		}
	}
	for _, hit := range got {
		if strings.HasPrefix(hit, "templates/ok.yaml") {
			t.Errorf("a file with only current names was flagged: %s", hit)
		}
	}
}
