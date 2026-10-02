package scaffold

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kubemoot/kmctl/internal/manifest"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

func TestCheckName(t *testing.T) {
	longest := strings.Repeat("a", MaxNameLength)
	for _, ok := range []string{"a", "demo", "homelab-health-guide", "lab-2", "2lab", longest} {
		if err := checkName(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	bad := map[string]string{
		"":                "required",
		"Demo":            "lowercase letters, digits and hyphens",
		"lab ops":         "lowercase letters, digits and hyphens",
		"lab_ops":         "lowercase letters, digits and hyphens",
		"lab.ops":         "lowercase letters, digits and hyphens",
		"-lab":            "starting and ending",
		"lab-":            "starting and ending",
		"caf\u00e9":       "Kubernetes name",
		longest + "a":     "keep it to 36",
		"lab\nops":        "lowercase letters",
		"{{ .Release }}x": "lowercase letters",
	}
	for name, want := range bad {
		err := checkName(name)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("checkName(%q) = %v, want an error saying %q", name, err, want)
		}
	}
}

func TestCheckDisplayName(t *testing.T) {
	for _, ok := range []string{"", "Homelab Health Guide", `Lab-Ops "Crew" #2`, "Caf\u00e9 Crew", "{{ .Values.x }}", strings.Repeat("\u00e9", MaxDisplayNameLength), "  " + strings.Repeat("x", MaxDisplayNameLength) + "  "} {
		if err := checkDisplayName(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"two\nlines", "cr\rlf", "tab\there", "nul\x00", "bad \xff utf-8", "line\u2028sep", "para\u2029sep", strings.Repeat("x", MaxDisplayNameLength+1)} {
		if err := checkDisplayName(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestDisplay(t *testing.T) {
	cases := map[string]string{"": "demo", "   ": "demo", " Demo Crew ": "Demo Crew"}
	for display, want := range cases {
		if got := (Options{Name: "demo", DisplayName: display}).Display(); got != want {
			t.Errorf("Display() with %q = %q, want %q", display, got, want)
		}
	}
}

func TestValidate_DisplayName(t *testing.T) {
	if err := (Options{Name: "demo", DisplayName: "a\nb", Members: 1}).Validate(); err == nil {
		t.Error("a display name with a newline must be refused")
	}
	if err := (Options{Name: "demo", DisplayName: "Demo Crew", Members: 1}).Validate(); err != nil {
		t.Errorf("a plain display name was refused: %v", err)
	}
}

// Whatever a display name holds, YAML reads back exactly that text.
func TestYAMLString_RoundTrips(t *testing.T) {
	for _, s := range []string{"Homelab Health Guide", `Lab-Ops "Crew" #2`, `back\slash`, "key: value", "- dash", "# hash", "Caf\u00e9 \u00a0nbsp", "'single'", "{{ x }}", "@at", "*star", "&amp", "!tag", "%pct", "true", "123"} {
		var got map[string]string
		if err := yaml.Unmarshal([]byte("k: "+quoted(s, false)), &got); err != nil {
			t.Fatalf("%q: %v", s, err)
		}
		if got["k"] != s {
			t.Errorf("round trip of %q gave %q", s, got["k"])
		}
	}
}

func TestQuoted(t *testing.T) {
	cases := []struct {
		in   string
		helm bool
		want string
	}{
		{"Lab } {", false, `"Lab } {"`},
		{"Lab } {", true, `"Lab } {"`},
		{"a {{ b }}", false, `"a {{ b }}"`},
		{"a {{ b }}", true, `{{ "a {{ b }}" | quote }}`},
	}
	for _, c := range cases {
		if got := quoted(c.in, c.helm); got != c.want {
			t.Errorf("quoted(%q, %v) = %s, want %s", c.in, c.helm, got, c.want)
		}
	}
}

// The display name reaches the rendered Crew exactly as typed, including text that
// looks like a Helm action: Helm does not run it.
func TestChart_HelmRendersDisplayNameVerbatim(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is not on PATH")
	}
	for _, display := range []string{"Homelab Health Guide", `Ops {{ .Release.Name }} "x" }} {{- fail "boom" }}`, `{{{ a }}}`, `back\slash {{"`} {
		dir := t.TempDir()
		if _, err := Write(Options{Name: "hello", DisplayName: display, Members: 1, OutputDir: dir, Chart: true}); err != nil {
			t.Fatal(err)
		}
		out := helmTemplate(t, helm, filepath.Join(dir, "hello"), nil)
		crew := crewIn(t, out)
		if got := crew.GetAnnotations()[DisplayNameAnnotation]; got != display {
			t.Errorf("rendered display name = %q, want %q", got, display)
		}
		chart, err := os.ReadFile(filepath.Join(dir, "hello", "Chart.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		var meta struct {
			Annotations map[string]string `json:"annotations"`
		}
		if err := yaml.Unmarshal(chart, &meta); err != nil {
			t.Fatal(err)
		}
		if meta.Annotations[DisplayNameAnnotation] != display {
			t.Errorf("Chart.yaml display name = %q, want %q", meta.Annotations[DisplayNameAnnotation], display)
		}
	}
}

func TestGenerate_DisplayNameDefaultsToName(t *testing.T) {
	crew := ofKind(generated(t, bundle(1)), "Crew")[0]
	if got := crew.GetAnnotations()[DisplayNameAnnotation]; got != "demo" {
		t.Errorf("default display name = %q, want the technical name demo", got)
	}
	files, err := Generate(bundle(1))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(files["README.md"], "# demo crew\n\nA small") {
		t.Errorf("without a display name the README keeps its title:\n%s", files["README.md"][:80])
	}
}

func TestGenerate_DisplayNameTitlesTheReadme(t *testing.T) {
	o := bundle(1)
	o.DisplayName = "Demo Crew"
	files, err := Generate(o)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(files["README.md"], "# Demo Crew\n\nIts Kubernetes name is `demo`") {
		t.Errorf("README title:\n%s", files["README.md"][:120])
	}
}

// crewIn is the one Crew in rendered YAML.
func crewIn(t *testing.T, rendered string) *unstructured.Unstructured {
	t.Helper()
	objs, err := manifest.Decode(strings.NewReader(rendered))
	if err != nil {
		t.Fatalf("rendered chart is not YAML: %v", err)
	}
	for i := range objs {
		if objs[i].GetKind() == "Crew" {
			return &objs[i]
		}
	}
	t.Fatal("the chart renders no Crew")
	return nil
}
