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
// DeprecatedNames condition.
//
// The old tool names come from Kubemoot's published list (workspacetools.Legacy in
// the operator module go.mod pins), so a tool renamed later is caught here with no
// edit. The rest has no published list; it is kept by hand from the operator's
// operator/internal/controller/workspace_names.go (deprecatedWorkspaceNames and
// legacyWorkspaceEnv), the rename table in kubemoot's docs/concepts/workspace.md,
// and the KubemootConfig and chart fields they name. Add to it when Kubemoot
// renames something else the scaffold could write.
func deprecatedNames() []string {
	names := slices.Sorted(maps.Keys(workspacetools.Legacy()))
	return append(names,
		"artifactAccess",  // spec.kubemootImage value and KubemootConfig image; now workspaceAccess
		"artifactReadOps", // crew access field; now the workspace access fields
		"artifact-access", // the old image repository; now workspace-access
		"/artifacts",      // the old emptyDir mount; now /kubemoot/workspace
		"spec.artifacts",  // KubemootConfig; now the workspace settings
		"ARTIFACT_",       // every old workspace-access environment variable (ARTIFACT_MODE, ARTIFACT_DIR, ...)
		"[ARTIFACT",       // the old finding marker; now [WORKSPACE ...]
	)
}

// findDeprecated reports each deprecated name the files use, as "file: name".
func findDeprecated(files map[string]string, names []string) []string {
	var found []string
	for _, file := range slices.Sorted(maps.Keys(files)) {
		for _, name := range names {
			if strings.Contains(files[file], name) {
				found = append(found, file+": "+name)
			}
		}
	}
	return found
}

// TestScaffoldNamesNoDeprecatedName checks every file the scaffold writes, bundle
// and chart at every size, README and values included.
func TestScaffoldNamesNoDeprecatedName(t *testing.T) {
	names := deprecatedNames()
	if len(names) < 7+7 {
		t.Fatalf("the deprecated-name list is %d long; the published tool list alone is 7", len(names))
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
