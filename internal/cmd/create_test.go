package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/scaffold"
)

// fakePrompter records calls and returns canned answers, or err from every prompt.
type fakePrompter struct {
	textVal string
	intVal  int
	err     error
	called  bool
}

func (f *fakePrompter) Text(string, string) (string, error) {
	f.called = true
	return f.textVal, f.err
}
func (f *fakePrompter) Int(string, int) (int, error) { f.called = true; return f.intVal, f.err }

func changedSet(names ...string) func(string) bool {
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	return func(s string) bool { return set[s] }
}

func TestGather_NoInputKeepsTheMembersDefault(t *testing.T) {
	opts := scaffold.Options{Name: "demo", Members: 1}
	fp := &fakePrompter{intVal: 4}
	if err := gather(&opts, changedSet(), true, fp); err != nil {
		t.Fatalf("gather: %v", err)
	}
	if fp.called {
		t.Error("--no-input must never prompt")
	}
	if opts.Members != 1 {
		t.Errorf("members = %d, want the default 1", opts.Members)
	}
}

func TestGather_FlagsProvided_NoPrompting(t *testing.T) {
	opts := scaffold.Options{Name: "demo", Members: 4}
	fp := &fakePrompter{}
	// Both inputs marked as set via flags -> prompter must not be called.
	if err := gather(&opts, changedSet("display-name", "members"), false, fp); err != nil {
		t.Fatalf("gather: %v", err)
	}
	if fp.called {
		t.Error("prompter was called despite all flags being provided")
	}
	if opts.Members != 4 {
		t.Errorf("members changed unexpectedly: %d", opts.Members)
	}
}

func TestGather_PromptsFillMissing(t *testing.T) {
	opts := scaffold.Options{Name: "demo"}
	fp := &fakePrompter{textVal: "  Demo Crew ", intVal: 5}
	if err := gather(&opts, changedSet(), false, fp); err != nil {
		t.Fatalf("gather: %v", err)
	}
	if opts.DisplayName != "Demo Crew" || opts.Members != 5 {
		t.Errorf("prompted values not applied: %+v", opts)
	}
}

func TestCreateCommand_RequiresName(t *testing.T) {
	cmd := newCreateCommand(client.NewFactory())
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error when NAME is missing")
	}
}

// TestCreateCommand_ScaffoldsAndHints exercises the create RunE success path:
// scaffold files are written and the apply next-step hint is printed. Kept
// hermetic by pointing KUBECONFIG at an absent file so the catalog check fails
// closed (no warning) and the test never reaches a real cluster.
func TestCreateCommand_ScaffoldsAndHints(t *testing.T) {
	t.Setenv("KUBECONFIG", t.TempDir()+"/absent-kubeconfig")
	dir := t.TempDir()
	cmd := newCreateCommand(client.NewFactory())
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"demo", "--no-input", "--members", "2", "-o", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("create: %v", err)
	}
	want := "Next: kubectl create namespace crew-demo && kubectl apply -n crew-demo -f " + dir + "/demo/access && kmctl apply -n crew-demo -f " + dir + "/demo"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("want %q, got %q", want, out.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "demo", "access", "rbac.yaml")); err != nil {
		t.Errorf("the bundle needs its RBAC in access/: %v", err)
	}
}

// The bundle binds its RBAC and names its namespace in the prompts, so -n
// decides both, and the hint applies to the same namespace.
func TestCreateCommand_BundleFollowsTheNamespaceFlag(t *testing.T) {
	t.Setenv("KUBECONFIG", t.TempDir()+"/absent-kubeconfig")
	dir := t.TempDir()
	f := client.NewFactory()
	team := "team-a"
	f.ConfigFlags.Namespace = &team
	cmd := newCreateCommand(f)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"demo", "--no-input", "-o", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains(out.String(), "kmctl apply -n team-a") {
		t.Errorf("want the hint to apply to team-a, got %q", out.String())
	}
	rbac, err := os.ReadFile(filepath.Join(dir, "demo", "access", "rbac.yaml"))
	if err != nil || !strings.Contains(string(rbac), "namespace: team-a") {
		t.Errorf("the RoleBinding must name team-a (err %v):\n%s", err, rbac)
	}
}

func TestExplicitNamespace(t *testing.T) {
	f := client.NewFactory()
	f.ConfigFlags.Namespace = nil
	if got := explicitNamespace(f); got != "" {
		t.Errorf("no namespace flag: got %q, want empty", got)
	}
	empty, set := "", "team-a"
	f.ConfigFlags.Namespace = &empty
	if got := explicitNamespace(f); got != "" {
		t.Errorf("empty -n: got %q, want empty", got)
	}
	f.ConfigFlags.Namespace = &set
	if got := explicitNamespace(f); got != "team-a" {
		t.Errorf("-n team-a: got %q", got)
	}
}

// Interactive gathering asks only for what no flag set, and stops at a prompt error.
func TestGather_AsksOnlyForUnsetInputs(t *testing.T) {
	opts := scaffold.Options{Name: "demo", Members: 2}
	fp := &fakePrompter{textVal: "Demo", intVal: 4}
	if err := gather(&opts, changedSet("members"), false, fp); err != nil {
		t.Fatalf("gather: %v", err)
	}
	if opts.Members != 2 || opts.DisplayName != "Demo" {
		t.Errorf("want members kept and the display name asked: %+v", opts)
	}
	failing := &fakePrompter{err: errors.New("interrupted")}
	if err := gather(&opts, changedSet(), false, failing); err == nil || err.Error() != "interrupted" {
		t.Errorf("want the prompt error, got %v", err)
	}
}

// The flags that fed Model objects are gone: a crew ships no models and names no
// model server, so --servers, --model-family, and the older --providers are
// unknown flags, and no models.yaml is written.
func TestCreateCommand_NoModelFlags(t *testing.T) {
	t.Setenv("KUBECONFIG", t.TempDir()+"/absent-kubeconfig")
	for _, flag := range []string{"--servers", "--model-family", "--providers"} {
		cmd := newCreateCommand(client.NewFactory())
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs([]string{"demo", "--no-input", flag, "x", "-o", t.TempDir()})
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "unknown flag: "+flag) {
			t.Errorf("want %s rejected as an unknown flag, got %v", flag, err)
		}
	}
	dir := t.TempDir()
	cmd := newCreateCommand(client.NewFactory())
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"demo", "--no-input", "-o", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "demo", "models.yaml")); !os.IsNotExist(err) {
		t.Errorf("a crew ships no models.yaml: %v", err)
	}
}

func TestCreateCommand_RejectsTooManyMembers(t *testing.T) {
	t.Setenv("KUBECONFIG", t.TempDir()+"/absent-kubeconfig")
	cmd := newCreateCommand(client.NewFactory())
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"demo", "--no-input", "--members", "6", "-o", t.TempDir()})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "1 to 5") {
		t.Fatalf("want a 1 to 5 members error, got %v", err)
	}
}

func TestCreateCommand_HelpListsTheSpecialists(t *testing.T) {
	long := newCreateCommand(client.NewFactory()).Long
	for _, key := range []string{"1. workloads", "2. events", "3. networking", "4. config", "5. reviewer"} {
		if !strings.Contains(long, key) {
			t.Errorf("help lacks %q:\n%s", key, long)
		}
	}
}

func TestCreateCommand_ChartHintsHelm(t *testing.T) {
	t.Setenv("KUBECONFIG", t.TempDir()+"/absent-kubeconfig")
	dir := t.TempDir()
	cmd := newCreateCommand(client.NewFactory())
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"demo", "--chart", "--no-input", "--members", "1", "-o", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("create: %v", err)
	}
	want := "Next: helm upgrade --install demo " + dir + "/demo --namespace crew-demo --create-namespace"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("want %q, got %q", want, out.String())
	}
}

// --display-name lands on the Crew and the chart, and the summary names both names.
func TestCreateCommand_DisplayName(t *testing.T) {
	t.Setenv("KUBECONFIG", t.TempDir()+"/absent-kubeconfig")
	dir := t.TempDir()
	cmd := newCreateCommand(client.NewFactory())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"homelab-health-guide", "--display-name", "Homelab Health Guide", "--chart", "--no-input", "-o", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.Contains(out.String(), `Scaffolded crew "Homelab Health Guide" (homelab-health-guide)`) {
		t.Errorf("the summary names the display name and the technical name: %q", out.String())
	}
	for _, file := range []string{"Chart.yaml", "templates/crew.yaml"} {
		body, err := os.ReadFile(filepath.Join(dir, "homelab-health-guide", file))
		if err != nil || !strings.Contains(string(body), `kubemoot.ai/display-name: "Homelab Health Guide"`) {
			t.Errorf("%s lacks the display name (err %v):\n%s", file, err, body)
		}
	}
}

func TestCreateCommand_RefusesBadNames(t *testing.T) {
	cases := map[string][]string{
		"lowercase letters, digits and hyphens": {"Homelab Guide"},
		"one line":                              {"demo", "--display-name", "two\nlines"},
		"keep it to 36":                         {strings.Repeat("a", 37)},
	}
	for want, args := range cases {
		t.Setenv("KUBECONFIG", t.TempDir()+"/absent-kubeconfig")
		cmd := newCreateCommand(client.NewFactory())
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(append(args, "--no-input", "-o", t.TempDir()))
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%v: want an error saying %q, got %v", args, want, err)
		}
	}
}

// Without --display-name the Crew's display name is its technical name.
func TestCreateCommand_DisplayNameDefaultsToName(t *testing.T) {
	t.Setenv("KUBECONFIG", t.TempDir()+"/absent-kubeconfig")
	dir := t.TempDir()
	cmd := newCreateCommand(client.NewFactory())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"demo", "--no-input", "-o", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("create: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "demo", "crew.yaml"))
	if err != nil || !strings.Contains(string(body), `kubemoot.ai/display-name: "demo"`) {
		t.Errorf("crew.yaml must default the display name to demo (err %v):\n%s", err, body)
	}
	if !strings.Contains(out.String(), `Scaffolded crew "demo" (`) {
		t.Errorf("summary: %q", out.String())
	}
}

func TestAskDisplayName_PassesThePromptError(t *testing.T) {
	opts := scaffold.Options{Name: "demo"}
	if err := askDisplayName(&opts, &fakePrompter{err: errors.New("interrupted")}); err == nil {
		t.Fatal("want the prompt error")
	}
	if opts.DisplayName != "" {
		t.Errorf("a failed prompt must leave the display name unset, got %q", opts.DisplayName)
	}
}

func TestCrewNamed(t *testing.T) {
	if got := crewNamed(scaffold.Options{Name: "demo"}); got != `"demo"` {
		t.Errorf("no display name: %s", got)
	}
	if got := crewNamed(scaffold.Options{Name: "demo", DisplayName: "demo"}); got != `"demo"` {
		t.Errorf("display name equal to the name: %s", got)
	}
	if got := crewNamed(scaffold.Options{Name: "demo", DisplayName: "Demo Crew"}); got != `"Demo Crew" (demo)` {
		t.Errorf("display name: %s", got)
	}
}
