package scaffold

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kubemoot/kmctl/internal/manifest"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata/golden")

// goldenCases are the scaffolds kept byte for byte in testdata/golden, so a change
// to any template shows up as a reviewable diff of what users get.
var goldenCases = map[string]Options{
	"chart-1":  {Name: "hello", Members: 1, ModelFamily: "qwen", Providers: []string{"ollama"}, Chart: true},
	"chart-5":  {Name: "hello", Members: 5, ModelFamily: "qwen", Providers: []string{"ollama"}, Chart: true},
	"bundle-1": {Name: "hello", Members: 1, ModelFamily: "qwen", Providers: []string{"ollama"}},
	// With a display name: the Crew's annotation, the chart's annotation, and the README title.
	"chart-display":  {Name: "homelab-health-guide", DisplayName: "Homelab Health Guide", Members: 1, ModelFamily: "qwen", Providers: []string{"ollama"}, Chart: true},
	"bundle-display": {Name: "lab-ops-crew-2", DisplayName: `Lab-Ops "Crew" #2`, Members: 1, ModelFamily: "qwen", Providers: []string{"ollama"}},
}

func TestGenerate_Golden(t *testing.T) {
	for name, o := range goldenCases {
		t.Run(name, func(t *testing.T) {
			files, err := Generate(o)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			root := filepath.Join("testdata", "golden", name)
			if *update {
				writeGolden(t, root, files)
			}
			for rel, content := range files {
				want, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
				if err != nil {
					t.Fatalf("missing golden %s (run go test ./internal/scaffold -update): %v", rel, err)
				}
				if string(want) != content {
					t.Errorf("%s differs from its golden file; run go test ./internal/scaffold -update and review the diff", rel)
				}
			}
			assertNoExtraGolden(t, root, files)
		})
	}
}

func writeGolden(t *testing.T, root string, files map[string]string) {
	t.Helper()
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func assertNoExtraGolden(t *testing.T, root string, files map[string]string) {
	t.Helper()
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if _, ok := files[filepath.ToSlash(rel)]; !ok {
			t.Errorf("golden file %s is no longer generated", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The chart renders with the real helm when it is installed: a Role by default,
// a ClusterRole with access.clusterWide, and the release namespace in the prompts.
func TestChart_HelmRenders(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is not on PATH")
	}
	dir := t.TempDir()
	if _, err := Write(Options{Name: "hello", Members: MaxMembers, OutputDir: dir, Chart: true}); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]string{"Role": nil, "ClusterRole": {"--set", "access.clusterWide=true"}}
	for kind, extra := range cases {
		out := helmTemplate(t, helm, filepath.Join(dir, "hello"), extra)
		kinds := kindCounts(t, out)
		if kinds[kind] != 1 || kinds[kind+"Binding"] != 1 {
			t.Errorf("%v: want one %s and its binding, got %v", extra, kind, kinds)
		}
		if !strings.Contains(out, `pass namespace "team-a"`) || !strings.Contains(out, "namespace: team-a") {
			t.Errorf("%v: the release namespace must reach the prompts and the binding", extra)
		}
	}
}

// helmTemplate renders the chart into namespace team-a.
func helmTemplate(t *testing.T, helm, chart string, extra []string) string {
	t.Helper()
	args := append([]string{"template", "hello", chart, "--namespace", "team-a"}, extra...)
	out, err := exec.Command(helm, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("helm %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func kindCounts(t *testing.T, yaml string) map[string]int {
	t.Helper()
	objs, err := manifest.Decode(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("helm output is not YAML: %v", err)
	}
	kinds := map[string]int{}
	for _, o := range objs {
		kinds[o.GetKind()]++
	}
	return kinds
}
