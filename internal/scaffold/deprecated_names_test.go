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
			for file, content := range files {
				for _, name := range names {
					if strings.Contains(content, name) {
						t.Errorf("%s (chart=%v, members=%d) names the deprecated %q", file, chart, members, name)
					}
				}
			}
		}
	}
}

// TestDeprecatedNamesCatchesOne shows the check can fail: a scaffold that names an
// old tool is flagged.
func TestDeprecatedNamesCatchesOne(t *testing.T) {
	content := "call artifact_head on it"
	for _, name := range deprecatedNames() {
		if strings.Contains(content, name) {
			return
		}
	}
	t.Error("no entry of the list matched a text that names artifact_head")
}
