package scaffold

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kubemoot/kmctl/internal/manifest"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// generated parses every YAML file a scaffold writes, keyed by its path.
func generated(t *testing.T, o Options) map[string][]unstructured.Unstructured {
	t.Helper()
	files, err := Generate(o)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	parsed := map[string][]unstructured.Unstructured{}
	for name, content := range files {
		if !strings.HasSuffix(name, ".yaml") || strings.Contains(content, "{{") {
			continue
		}
		objs, err := manifest.Decode(strings.NewReader(content))
		if err != nil {
			t.Fatalf("generated %s is not valid YAML: %v\n%s", name, err, content)
		}
		parsed[name] = objs
	}
	return parsed
}

func bundle(members int) Options {
	return Options{Name: "demo", Members: members, ModelFamily: "qwen", Providers: []string{"ollama-gpu"}}
}

func all(p map[string][]unstructured.Unstructured) []unstructured.Unstructured {
	var out []unstructured.Unstructured
	for _, objs := range p {
		out = append(out, objs...)
	}
	return out
}

func ofKind(p map[string][]unstructured.Unstructured, kind string) []unstructured.Unstructured {
	var out []unstructured.Unstructured
	for _, o := range all(p) {
		if o.GetKind() == kind {
			out = append(out, o)
		}
	}
	return out
}

func named(objs []unstructured.Unstructured, name string) *unstructured.Unstructured {
	for i := range objs {
		if objs[i].GetName() == name {
			return &objs[i]
		}
	}
	return nil
}

func names(objs []unstructured.Unstructured) []string {
	out := make([]string, 0, len(objs))
	for _, o := range objs {
		out = append(out, o.GetName())
	}
	return out
}

func str(o *unstructured.Unstructured, path ...string) string {
	s, _, _ := unstructured.NestedString(o.Object, path...)
	return s
}

func strs(o *unstructured.Unstructured, path ...string) []string {
	s, _, _ := unstructured.NestedStringSlice(o.Object, path...)
	return s
}

// Every size from 1 to 5 is a whole crew: the specialists in order, a prompt
// module each, one tool server and gateway, and the RBAC that lets it read.
func TestGenerate_EverySizeIsAWholeCrew(t *testing.T) {
	order := []string{"demo-coordinator", "demo-workloads", "demo-events", "demo-networking", "demo-config", "demo-reviewer"}
	for n := 1; n <= MaxMembers; n++ {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			p := generated(t, bundle(n))
			if got := names(ofKind(p, "Agent")); !slices.Equal(got, order[:n+1]) {
				t.Errorf("agents = %v, want %v", got, order[:n+1])
			}
			counts := map[string]int{"Crew": 1, "CrewSchedulingPolicy": 1, "MCPServer": 1, "MCPGateway": 1, "CrewFitnessSuite": 1,
				"ServiceAccount": 1, "Role": 1, "RoleBinding": 1, "Model": 3, "PromptModule": 5 + n}
			for kind, want := range counts {
				if got := len(ofKind(p, kind)); got != want {
					t.Errorf("%s count = %d, want %d", kind, got, want)
				}
			}
		})
	}
}

func TestGenerate_PromptRefIntegrity(t *testing.T) {
	p := generated(t, bundle(MaxMembers))
	modules := ofKind(p, "PromptModule")
	for _, a := range ofKind(p, "Agent") {
		for _, ref := range strs(&a, "spec", "promptRefs") {
			if named(modules, ref) == nil {
				t.Errorf("agent %q references PromptModule %q, which is not generated", a.GetName(), ref)
			}
		}
	}
}

// The tools every tooler may enable: the read-only Kubernetes MCP server's tools.
var readOnlyTools = []string{"pods_list_in_namespace", "pods_get", "pods_log", "resources_list", "resources_get", "events_list"}

// Each tooler enables only a handful of read tools; the coordinator and the
// reviewer call none, so the gateway is off for them.
func TestGenerate_ToolsPerRole(t *testing.T) {
	p := generated(t, bundle(MaxMembers))
	for _, a := range ofKind(p, "Agent") {
		role := str(&a, "spec", "discussRole")
		tools := strs(&a, "spec", "enabledTools")
		gatewayOff := envValue(&a, "KUBEMOOT_GATEWAY_ENABLED") == "false"
		switch role {
		case "tooler":
			if len(tools) == 0 || len(tools) > 5 {
				t.Errorf("tooler %q enables %d tools, want 1 to 5", a.GetName(), len(tools))
			}
			for _, tool := range tools {
				if !slices.Contains(readOnlyTools, tool) {
					t.Errorf("tooler %q enables %q, which is not a read tool", a.GetName(), tool)
				}
			}
		case "coordinator", "analyst":
			if len(tools) != 0 || !gatewayOff {
				t.Errorf("%s %q must call no tools (enabledTools %v, gateway off %v)", role, a.GetName(), tools, gatewayOff)
			}
		default:
			t.Errorf("agent %q has unexpected role %q", a.GetName(), role)
		}
	}
}

// envValue is the value of the named env var in an Agent's deployment, or "".
func envValue(a *unstructured.Unstructured, name string) string {
	env, _, _ := unstructured.NestedSlice(a.Object, "spec", "deployment", "env")
	for _, e := range env {
		if m := e.(map[string]any); m["name"] == name {
			return fmt.Sprint(m["value"])
		}
	}
	return ""
}

// Agents declare capabilities and never name a model or a family, in the spec
// or in a prompt; the scheduler binds Models.
func TestGenerate_NoModelNamedOutsideModels(t *testing.T) {
	p := generated(t, bundle(MaxMembers))
	for _, a := range ofKind(p, "Agent") {
		if len(strs(&a, "spec", "capabilities")) == 0 {
			t.Errorf("agent %q declares no capabilities", a.GetName())
		}
		if _, ok := a.Object["spec"].(map[string]any)["model"]; ok {
			t.Errorf("agent %q names a model", a.GetName())
		}
	}
	for _, m := range ofKind(p, "PromptModule") {
		for _, family := range KnownModelFamilies {
			if strings.Contains(strings.ToLower(str(&m, "spec", "content")), family) {
				t.Errorf("PromptModule %q names the model family %q", m.GetName(), family)
			}
		}
	}
}

// Every module is ADL, and each specialist's module carries real rules.
func TestGenerate_PromptModulesAreADL(t *testing.T) {
	p := generated(t, bundle(MaxMembers))
	for _, m := range ofKind(p, "PromptModule") {
		c := str(&m, "spec", "content")
		if !strings.HasPrefix(c, "DEFINE DOMAIN ") || !strings.Contains(c, "\nDESCRIPTION ") {
			t.Errorf("PromptModule %q does not open with DEFINE DOMAIN and DESCRIPTION:\n%s", m.GetName(), c)
		}
		if !strings.HasSuffix(m.GetName(), "-system") {
			continue
		}
		for _, kw := range []string{"WHEN ", " THEN ", "NEVER ", "ASSERT "} {
			if !strings.Contains(c, kw) {
				t.Errorf("specialist module %q has no %q rule", m.GetName(), strings.TrimSpace(kw))
			}
		}
	}
}

// The prompts and the tool server both keep the crew read-only.
func TestGenerate_ReadOnly(t *testing.T) {
	p := generated(t, bundle(MaxMembers))
	server := ofKind(p, "MCPServer")[0]
	if !slices.Contains(strs(&server, "spec", "command"), "--read-only") {
		t.Error("the Kubernetes MCP server must run with --read-only")
	}
	protocol := str(named(ofKind(p, "PromptModule"), "discussion-protocol"), "spec", "content")
	decision := str(named(ofKind(p, "PromptModule"), "coordinator-decision-logic"), "spec", "content")
	if !strings.Contains(protocol, "READ-ONLY") || !strings.Contains(decision, "decline on principle") {
		t.Error("the shared protocol and the coordinator must both keep the crew read-only")
	}
}

// The Role grants get, list, and watch only, and nothing on Secrets: RBAC cannot
// grant Secret names without their data.
func TestGenerate_RBACIsReadOnlyWithoutSecrets(t *testing.T) {
	p := generated(t, bundle(1))
	role := ofKind(p, "Role")[0]
	rules, _, _ := unstructured.NestedSlice(role.Object, "rules")
	if len(rules) == 0 {
		t.Fatal("the Role has no rules")
	}
	for _, r := range rules {
		rule := r.(map[string]any)
		for _, v := range rule["verbs"].([]any) {
			if !slices.Contains([]string{"get", "list", "watch"}, v.(string)) {
				t.Errorf("the Role grants %q", v)
			}
		}
		if slices.Contains(rule["resources"].([]any), any("secrets")) {
			t.Error("the Role must grant nothing on Secrets")
		}
	}
	binding := ofKind(p, "RoleBinding")[0]
	if !strings.Contains(fmt.Sprint(binding.Object["subjects"]), "namespace:crew-demo") {
		t.Errorf("the RoleBinding subject must be in crew-demo: %v", binding.Object["subjects"])
	}
	server := ofKind(p, "MCPServer")[0]
	if str(&server, "spec", "serviceAccountName") != ofKind(p, "ServiceAccount")[0].GetName() {
		t.Error("the MCP server must run as the ServiceAccount the Role binds")
	}
}

// The gateway selects the crew's own MCP server by a label the server carries. It
// leaves adminUI to its default: the operator writes that field back as true, so a
// value here makes every later helm upgrade conflict with the operator.
func TestGenerate_GatewaySelectsTheServer(t *testing.T) {
	p := generated(t, bundle(1))
	gw, server := ofKind(p, "MCPGateway")[0], ofKind(p, "MCPServer")[0]
	if _, set, _ := unstructured.NestedFieldNoCopy(gw.Object, "spec", "adminUI"); set {
		t.Error("the gateway must not set adminUI")
	}
	sel, _, _ := unstructured.NestedStringMap(gw.Object, "spec", "mcpServerSelector", "matchLabels")
	if len(sel) == 0 {
		t.Fatal("the gateway needs a selector")
	}
	for k, v := range sel {
		if server.GetLabels()[k] != v {
			t.Errorf("the gateway selects %s=%s, which the server lacks", k, v)
		}
	}
}

func scenarios(t *testing.T, p map[string][]unstructured.Unstructured) map[string]string {
	t.Helper()
	out := map[string]string{}
	suite := ofKind(p, "CrewFitnessSuite")[0]
	list, _, _ := unstructured.NestedSlice(suite.Object, "spec", "scripts")
	for _, s := range list {
		m := s.(map[string]any)
		out[m["testRef"].(string)] = m["testContent"].(string)
	}
	return out
}

// The suite grows with the crew: three scenarios for one specialist, one more per
// specialist after it, and a cross-domain question once the reviewer is there.
func TestGenerate_FitnessScalesWithTheCrew(t *testing.T) {
	base := []string{"coordinator-deployment", "pods-in-namespace", "refuse-delete"}
	want := map[int][]string{
		1: base,
		2: append(slices.Clone(base), "warnings-and-restarts"),
		3: append(slices.Clone(base), "services-and-endpoints", "warnings-and-restarts"),
		4: append(slices.Clone(base), "config-and-accounts", "services-and-endpoints", "warnings-and-restarts"),
		5: append(slices.Clone(base), "anything-unhealthy", "config-and-accounts", "services-and-endpoints", "warnings-and-restarts"),
	}
	for n, refs := range want {
		got := scenarios(t, generated(t, bundle(n)))
		keys := slices.Sorted(maps.Keys(got))
		slices.Sort(refs)
		if !slices.Equal(keys, refs) {
			t.Errorf("members=%d scenarios = %v, want %v", n, keys, refs)
		}
	}
}

// Every scenario asks a question, carries a judge reference, and grounds itself in
// the crew's own namespace; the delete scenario checks no write is claimed.
func TestGenerate_ScenariosAreGroundedAndJudged(t *testing.T) {
	for ref, body := range scenarios(t, generated(t, bundle(MaxMembers))) {
		for _, want := range []string{"DESCRIPTION ", `DEFINE CONST QUESTION AS "`, "DEFINE CONST MAX_DURATION", "ASSERT(DEFER synthesis REFLECTS \""} {
			if !strings.Contains(body, want) {
				t.Errorf("scenario %s lacks %q", ref, want)
			}
		}
	}
	got := scenarios(t, generated(t, bundle(1)))
	if !strings.Contains(got["refuse-delete"], `synthesis does NOT CONTAIN "I have deleted"`) {
		t.Error("refuse-delete must assert that no deletion is claimed")
	}
	if !strings.Contains(got["pods-in-namespace"], "demo-workloads") || !strings.Contains(got["pods-in-namespace"], `CONTAINS "demo-coordinator"`) {
		t.Errorf("pods-in-namespace must ground the answer in the crew's own pods:\n%s", got["pods-in-namespace"])
	}
}

// The coordinator still answers general knowledge directly and never leaks the
// discussion's mechanics into the answer.
func TestGenerate_SynthesisRules(t *testing.T) {
	synth := str(named(ofKind(generated(t, bundle(1)), "PromptModule"), "synthesis-prompt"), "spec", "content")
	for _, want := range []string{"general knowledge", "NEVER mention agent names", "internal signals", "NEVER fabricate"} {
		if !strings.Contains(synth, want) {
			t.Errorf("synthesis-prompt lacks %q:\n%s", want, synth)
		}
	}
	if strings.Contains(synth, "review flagged") {
		t.Error("a crew without the reviewer must not mention its review")
	}
	full := str(named(ofKind(generated(t, bundle(MaxMembers)), "PromptModule"), "synthesis-prompt"), "spec", "content")
	if !strings.Contains(full, "review flagged") {
		t.Error("with the reviewer, synthesis must drop the claims it flags")
	}
}

func TestValidate(t *testing.T) {
	bad := []Options{
		{Name: "", Members: 1},
		{Name: "Bad_Name", Members: 1},
		{Name: "ok", Members: 0},
		{Name: "ok", Members: MaxMembers + 1},
	}
	for _, o := range bad {
		if err := o.Validate(); err == nil {
			t.Errorf("expected validation error for %+v", o)
		}
	}
	for n := 1; n <= MaxMembers; n++ {
		if err := (Options{Name: "ok", Members: n}).Validate(); err != nil {
			t.Errorf("members=%d rejected: %v", n, err)
		}
	}
	if _, err := Generate(Options{Name: "ok", Members: 9}); err == nil {
		t.Error("Generate must refuse a size the starter crew does not have")
	}
}

func TestTargetNamespace(t *testing.T) {
	if got := (Options{Name: "demo"}).TargetNamespace(); got != "crew-demo" {
		t.Errorf("default = %q, want crew-demo", got)
	}
	if got := (Options{Name: "demo", Namespace: "team-a"}).TargetNamespace(); got != "team-a" {
		t.Errorf("explicit = %q, want team-a", got)
	}
}

// A bundle names its namespace in the prompts and the RoleBinding; the chart
// leaves both to Helm.
func TestGenerate_NamespaceByForm(t *testing.T) {
	o := bundle(1)
	o.Namespace = "team-a"
	flat, err := Generate(o)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(flat["promptmodules.yaml"], `namespace "team-a"`) || !strings.Contains(flat["access/rbac.yaml"], "namespace: team-a") {
		t.Error("the bundle must name team-a in its prompts and RoleBinding")
	}
	o.Chart = true
	chart, err := Generate(o)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(chart["templates/promptmodules.yaml"], `namespace "{{ .Release.Namespace }}"`) || strings.Contains(chart["templates/promptmodules.yaml"], "team-a") {
		t.Error("the chart's prompts must take the namespace from Helm")
	}
}

func TestModelCount(t *testing.T) {
	cases := []struct {
		o    Options
		want int
	}{
		{Options{ModelFamily: "qwen", Providers: []string{"a", "b"}}, 6},
		{Options{ModelFamily: "qwen"}, 0},
		{Options{Providers: []string{"a"}}, 0},
		{Options{ModelFamily: "nope", Providers: []string{"a"}}, 0},
		{Options{ModelFamily: "Gemma", Providers: []string{"a"}}, 3},
	}
	for _, c := range cases {
		if got := c.o.ModelCount(); got != c.want {
			t.Errorf("%+v: ModelCount = %d, want %d", c.o, got, c.want)
		}
	}
}

func flatAndChart(t *testing.T, members int) (map[string]string, map[string]string) {
	t.Helper()
	o := bundle(members)
	flat, err := Generate(o)
	if err != nil {
		t.Fatalf("Generate flat: %v", err)
	}
	o.Chart = true
	chart, err := Generate(o)
	if err != nil {
		t.Fatalf("Generate chart: %v", err)
	}
	return flat, chart
}

// The chart holds the same manifests as the bundle. Only the namespace in the
// prompts and the RBAC differ: Helm supplies both.
func TestGenerate_ChartLayoutKeepsTheManifests(t *testing.T) {
	flat, chart := flatAndChart(t, MaxMembers)
	for _, name := range []string{"crew.yaml", "agents.yaml", "models.yaml", "tools.yaml"} {
		if chart["templates/"+name] != flat[name] {
			t.Errorf("templates/%s differs from the bundle's %s", name, name)
		}
	}
	if strings.ReplaceAll(chart["templates/promptmodules.yaml"], helmNamespace, "crew-demo") != flat["promptmodules.yaml"] {
		t.Error("the chart's prompt modules may differ from the bundle's only in the namespace")
	}
	if chart["fitness/fitness.yaml"] != flat["fitness/fitness.yaml"] {
		t.Error("the fitness suite differs between the forms")
	}
	if _, ok := chart["templates/fitness.yaml"]; ok {
		t.Error("the fitness suite must stay out of templates/ so an install does not start a run")
	}
}

// Helm actions appear only where Helm renders them: the chart's prompts and RBAC.
func TestGenerate_HelmActionsOnlyWhereHelmRenders(t *testing.T) {
	flat, chart := flatAndChart(t, MaxMembers)
	helmFiles := map[string]bool{"templates/promptmodules.yaml": true, "templates/rbac.yaml": true}
	for name, content := range chart {
		if strings.Contains(content, "{{") != helmFiles[name] {
			t.Errorf("chart file %s: holds a Helm action = %v, want %v", name, !helmFiles[name], helmFiles[name])
		}
	}
	for name, content := range flat {
		if strings.Contains(content, "{{") {
			t.Errorf("bundle file %s holds a Helm action", name)
		}
	}
	if !strings.Contains(chart["templates/rbac.yaml"], ".Values.access.clusterWide") || !strings.Contains(chart["values.yaml"], "clusterWide: false") {
		t.Error("the chart's RBAC must follow access.clusterWide, default false")
	}
}

func TestGenerate_ChartMetadataAndReadme(t *testing.T) {
	flat, chart := flatAndChart(t, 1)
	for _, want := range []string{"name: demo", "apiVersion: v2", `kubemoot.ai/crew-chart: "true"`} {
		if !strings.Contains(chart["Chart.yaml"], want) {
			t.Errorf("Chart.yaml lacks %q:\n%s", want, chart["Chart.yaml"])
		}
	}
	if !strings.Contains(chart["README.md"], "helm upgrade --install demo . --namespace crew-demo") || strings.Contains(flat["README.md"], "helm upgrade") {
		t.Error("only the chart README gives the helm install")
	}
	if !strings.Contains(flat["README.md"], "kubectl apply -n crew-demo -f access/") {
		t.Error("the bundle README must apply access/ with kubectl")
	}
	for _, want := range []string{"Your first five minutes", "kmctl conversation ask demo", "kmctl fitness run -f fitness/fitness.yaml", "synthesis-prompt", "--members 5"} {
		if !strings.Contains(chart["README.md"], want) {
			t.Errorf("README lacks %q", want)
		}
	}
	_, full := flatAndChart(t, MaxMembers)
	if strings.Contains(full["README.md"], "--members 5") {
		t.Error("the full crew's README need not point at --members 5")
	}
}

func TestWrite_RefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	o := Options{Name: "demo", Members: 1, OutputDir: dir}
	if _, err := Write(o); err != nil {
		t.Fatalf("first Write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "demo", "crew.yaml")); err != nil {
		t.Fatalf("expected crew.yaml written: %v", err)
	}
	if _, err := Write(o); err == nil {
		t.Error("expected Write to refuse an existing directory")
	}
}

func TestWrite_RejectsInvalidOptions(t *testing.T) {
	if _, err := Write(Options{Name: "demo", Members: 0, OutputDir: t.TempDir()}); err == nil {
		t.Error("Write must validate first")
	}
}

func TestWrite_ChartCreatesItsFolders(t *testing.T) {
	dir := t.TempDir()
	written, err := Write(Options{Name: "demo", Members: 1, OutputDir: dir, Chart: true})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(written) != 10 {
		t.Errorf("want 10 files, got %d: %v", len(written), written)
	}
	for _, rel := range []string{"Chart.yaml", "templates/agents.yaml", "templates/rbac.yaml", "fitness/fitness.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, "demo", filepath.FromSlash(rel))); err != nil {
			t.Errorf("expected %s written: %v", rel, err)
		}
	}
}

func TestSizeSummary(t *testing.T) {
	cases := map[int]int{-1: 0, 0: 0, 1: 1, 3: 3, MaxMembers: MaxMembers, 9: MaxMembers}
	for n, want := range cases {
		if got := len(SizeSummary(n)); got != want {
			t.Errorf("SizeSummary(%d) has %d lines, want %d", n, got, want)
		}
	}
	if got := SizeSummary(1)[0]; !strings.HasPrefix(got, "workloads: ") {
		t.Errorf("the first specialist is workloads, got %q", got)
	}
}

func TestIndent(t *testing.T) {
	if got := indent(2, "a\n\nb"); got != "  a\n\n  b" {
		t.Errorf("indent = %q", got)
	}
}
