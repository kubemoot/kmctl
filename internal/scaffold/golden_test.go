package scaffold

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// helmGolden are the chart-1 scaffold as helm renders it into namespace team-a, for
// each value of access.clusterWide, kept byte for byte so the switch's effect on the
// prompts and the RBAC shows as a reviewable diff.
var helmGolden = map[string][]string{
	"namespaced.yaml":   nil,
	"cluster-wide.yaml": {"--set", "access.clusterWide=true"},
}

func TestChart_HelmRenderedGolden(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is not on PATH")
	}
	dir := t.TempDir()
	o := goldenCases["chart-1"]
	o.OutputDir = dir
	if _, err := Write(o); err != nil {
		t.Fatal(err)
	}
	for file, extra := range helmGolden {
		t.Run(file, func(t *testing.T) {
			got := helmTemplate(t, helm, filepath.Join(dir, o.Name), extra)
			golden := filepath.Join("testdata", "golden", "helm", file)
			if *update {
				if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("missing golden %s (run go test ./internal/scaffold -update): %v", golden, err)
			}
			if string(want) != got {
				t.Errorf("helm template %v differs from %s; run go test ./internal/scaffold -update and review the diff", extra, golden)
			}
		})
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
	cases := map[string]struct {
		extra  []string
		prompt string // how the prompts name the release namespace
	}{
		"Role":        {nil, `pass namespace "team-a"`},
		"ClusterRole": {[]string{"--set", "access.clusterWide=true"}, "it is installed in the namespace team-a"},
	}
	for kind, c := range cases {
		extra := c.extra
		out := helmTemplate(t, helm, filepath.Join(dir, "hello"), extra)
		kinds := kindCounts(t, out)
		if kinds[kind] != 1 || kinds[kind+"Binding"] != 1 {
			t.Errorf("%v: want one %s and its binding, got %v", extra, kind, kinds)
		}
		if !strings.Contains(out, c.prompt) || !strings.Contains(out, "namespace: team-a") {
			t.Errorf("%v: the release namespace must reach the prompts and the binding", extra)
		}
	}
}

// The chart deploys its fitness files as a ConfigMap, built by Helm from fitness/.
func TestChart_HelmDeploysItsScenarios(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is not on PATH")
	}
	dir := t.TempDir()
	if _, err := Write(Options{Name: "hello", Members: 2, OutputDir: dir, Chart: true}); err != nil {
		t.Fatal(err)
	}
	fitness, err := os.ReadFile(filepath.Join(dir, "hello", "fitness", "fitness.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	out := helmTemplate(t, helm, filepath.Join(dir, "hello"), []string{"--show-only", "templates/fitness-scenarios.yaml"})
	if cm := scenariosConfigMap(t, out, "hello"); cm.data["fitness.yaml"] != string(fitness) || len(cm.data) != 1 {
		t.Errorf("the ConfigMap must hold exactly fitness/fitness.yaml, got keys %d", len(cm.data))
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
	return normalizeHelmOutput(string(out))
}

// blankBeforeSeparator matches the blank lines helm 4 leaves between a rendered
// template and the next document separator, which helm 3 omits.
var blankBeforeSeparator = regexp.MustCompile(`\n(?:[ \t]*\n)+---`)

// normalizeHelmOutput drops the blank lines before each "---" separator, so the helm
// goldens compare the rendered manifests, not the layout of whichever helm renders them.
func normalizeHelmOutput(s string) string {
	return blankBeforeSeparator.ReplaceAllString(s, "\n---")
}

func TestNormalizeHelmOutput(t *testing.T) {
	helm3 := "# Source: a.yaml\nkind: A\n---\n# Source: b.yaml\nkind: B\n"
	cases := map[string]struct{ in, want string }{
		"helm 3 layout is unchanged":           {helm3, helm3},
		"helm 4 blank line before separator":   {"# Source: a.yaml\nkind: A\n\n---\n# Source: b.yaml\nkind: B\n", helm3},
		"several blank and whitespace lines":   {"kind: A\n\n  \n\t\n---\nkind: B\n", "kind: A\n---\nkind: B\n"},
		"blank line inside a document is kept": {"kind: A\n\nspec: {}\n---\nkind: B\n", "kind: A\n\nspec: {}\n---\nkind: B\n"},
		"no separator":                         {"kind: A\n\n", "kind: A\n\n"},
		"empty":                                {"", ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if got := normalizeHelmOutput(c.in); got != c.want {
				t.Errorf("normalizeHelmOutput(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
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
