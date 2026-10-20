package cmd

import (
	"regexp"
	"strings"
	"testing"
)

func TestModelAddition_Validate(t *testing.T) {
	ok := modelAddition{model: "qwen3:14b", server: "ollama-gpu", parameters: "14B", capabilities: []string{"tool-calling", "reasoning"}}
	if err := ok.validate(); err != nil {
		t.Errorf("valid addition: %v", err)
	}
	for _, p := range []string{"0.5B", "751M", "8.2b", "1T", "3K"} {
		a := ok
		a.parameters = p
		if err := a.validate(); err != nil {
			t.Errorf("--parameters %s: %v", p, err)
		}
	}
	cases := map[string]modelAddition{
		"no spaces":                  {model: "qwen 3", server: "s"},
		"MODEL is the name":          {model: " ", server: "s"},
		"--model-server names":       {model: "m", server: ""},
		"a count with a unit":        {model: "m", server: "s", parameters: "14"},
		`"14 B"`:                     {model: "m", server: "s", parameters: "14 B"},
		`"B14"`:                      {model: "m", server: "s", parameters: "B14"},
		`unknown capability "tools"`: {model: "m", server: "s", capabilities: []string{"tools"}},
	}
	for want, a := range cases {
		if err := a.validate(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%+v: want an error saying %q, got %v", a, want, err)
		}
	}
}

// dns1123 is a DNS-1123 subdomain made of labels without dots, as kmctl writes them.
var dns1123 = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

func TestModelObjectName(t *testing.T) {
	if got := modelObjectName("vllm-gpu", "qwen3-14b"); got != "vllm-gpu-qwen3-14b" {
		t.Errorf("a name that is already valid stays as it is, got %q", got)
	}
	prefixes := map[[2]string]string{
		{"ollama-gpu", "qwen3:14b"}:        "ollama-gpu-qwen3-14b-",
		{"vllm-gpu", "Qwen/Qwen3-14B-AWQ"}: "vllm-gpu-qwen-qwen3-14b-awq-",
		{"s", "a::b..c"}:                   "s-a-b-c-",
		{"s", "model-"}:                    "s-model-",
		{"s", "::"}:                        "s-",
		{"-s", "::"}:                       "s-",
	}
	for in, prefix := range prefixes {
		got := modelObjectName(in[0], in[1])
		if !strings.HasPrefix(got, prefix) || len(got) != len(prefix)+nameHashLength || !dns1123.MatchString(got) {
			t.Errorf("modelObjectName(%q, %q) = %q, want %s<hash> and a DNS-1123 name", in[0], in[1], got, prefix)
		}
	}
	long := modelObjectName("s", strings.Repeat("x", 300))
	if len(long) > maxObjectName || !dns1123.MatchString(long) {
		t.Errorf("a long model name must fit %d characters: %d, %q", maxObjectName, len(long), long)
	}
	if got := modelObjectName("::", "::"); !dns1123.MatchString(got) || len(got) != nameHashLength {
		t.Errorf("an all-symbol name is the hash alone, got %q", got)
	}
}

// Two models whose names differ only in characters a name cannot hold get two
// Models, never one that the second add overwrites.
func TestModelObjectName_NoCollisions(t *testing.T) {
	seen := map[string][2]string{}
	for _, in := range [][2]string{
		{"ollama-gpu", "qwen3:14b"}, {"ollama-gpu", "qwen3-14b"}, {"ollama-gpu", "qwen3/14b"}, {"ollama-gpu", "Qwen3:14B"},
		{"s", strings.Repeat("x", 300)}, {"s", strings.Repeat("x", 301)},
	} {
		name := modelObjectName(in[0], in[1])
		if prev, dup := seen[name]; dup {
			t.Errorf("%v and %v both name %q", prev, in, name)
		}
		seen[name] = in
	}
}

func TestModelAddition_Object(t *testing.T) {
	a := modelAddition{model: "qwen3:14b", server: "vllm-gpu", parameters: "14B", capabilities: []string{"tool-calling", "reasoning", "tool-calling"}}
	obj := a.object("kubemoot")
	if obj.GetKind() != "Model" || obj.GetAPIVersion() != "kubemoot.ai/v1alpha1" || obj.GetNamespace() != "kubemoot" || obj.GetName() != modelObjectName("vllm-gpu", "qwen3:14b") {
		t.Errorf("object meta: %v", obj.Object)
	}
	spec := obj.Object["spec"].(map[string]any)
	caps := spec["capabilities"].([]any)
	if spec["serverRef"] != "vllm-gpu" || spec["model"] != "qwen3:14b" || spec["parameters"] != "14B" || len(caps) != 2 || caps[0] != "reasoning" {
		t.Errorf("spec: %v", spec)
	}
}

func TestModelAddition_ObjectWithoutOptions(t *testing.T) {
	bare := modelAddition{model: "m", server: "s"}.object("ns").Object["spec"].(map[string]any)
	if _, has := bare["parameters"]; has {
		t.Errorf("no --parameters writes no parameters: %v", bare)
	}
	if _, has := bare["capabilities"]; has {
		t.Errorf("no --capabilities writes no capabilities: %v", bare)
	}
}

// add writes the Model into the namespace of the model server it names.
func TestModelAdd_AppliesInTheServersNamespace(t *testing.T) {
	s := newFakeAPIServer(t)
	s.add("modelservers", "kubemoot", "vllm-gpu", nil)
	out, _, err := s.kmctl(t, "model", "add", "qwen3-14b", "--model-server", "vllm-gpu", "--parameters", "14B", "--capabilities", "tool-calling")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "applied", out, "model/vllm-gpu-qwen3-14b applied in namespace kubemoot")
	if strings.Contains(out, "(dry run)") {
		t.Errorf("a real apply is not a dry run: %q", out)
	}
	obj := s.storedObject("models", "kubemoot", "vllm-gpu-qwen3-14b")
	if obj == nil {
		t.Fatal("the Model was not stored in the server's namespace")
	}
	spec := obj["spec"].(map[string]any)
	if spec["serverRef"] != "vllm-gpu" || spec["model"] != "qwen3-14b" || spec["parameters"] != "14B" {
		t.Errorf("spec: %v", spec)
	}
}

// The model server is --model-server, so the global -s/--server keeps naming the
// Kubernetes API server.
func TestModelAdd_KeepsTheGlobalServerFlag(t *testing.T) {
	cmd := NewRootCommand()
	add, _, err := cmd.Find([]string{"model", "add"})
	if err != nil {
		t.Fatal(err)
	}
	if add.LocalNonPersistentFlags().Lookup("server") != nil {
		t.Error("model add must not define --server: it would hide the API server flag")
	}
	if add.Flag("model-server") == nil || add.InheritedFlags().Lookup("server") == nil {
		t.Error("want --model-server here and the inherited --server")
	}
}

func TestModelAdd_DryRunStoresNothing(t *testing.T) {
	s := newFakeAPIServer(t)
	s.add("modelservers", "kubemoot", "ollama-gpu", nil)
	out, _, err := s.kmctl(t, "model", "add", "qwen3:8b", "--model-server", "ollama-gpu", "--dry-run", "--capabilities", "tool-calling, reasoning")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "dry run", out, "(dry run)")
	if s.storedObject("models", "kubemoot", modelObjectName("ollama-gpu", "qwen3:8b")) != nil {
		t.Error("a dry run must not store the Model")
	}
}

func TestModelAdd_FindsTheServerOrSaysWhy(t *testing.T) {
	s := newFakeAPIServer(t)
	if _, _, err := s.kmctl(t, "model", "add", "m", "--model-server", "nowhere"); err == nil || !strings.Contains(err.Error(), `no model server "nowhere" in the cluster`) {
		t.Errorf("want a no-such-server error, got %v", err)
	}
	s.add("modelservers", "a", "gpu", nil)
	s.add("modelservers", "b", "gpu", nil)
	if _, _, err := s.kmctl(t, "model", "add", "m", "--model-server", "gpu"); err == nil || !strings.Contains(err.Error(), "several namespaces: a, b") {
		t.Errorf("want an ambiguous-server error, got %v", err)
	}
	out, _, err := s.kmctl(t, "model", "add", "m", "--model-server", "gpu", "-n", "b")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "-n picks", out, "applied in namespace b")
	if _, _, err := s.kmctl(t, "model", "add", "m", "--model-server", "gpu", "-n", "c"); err == nil {
		t.Error("-n c has no such server: want an error")
	}
}

func TestModelAdd_RejectsBadInputBeforeTheCluster(t *testing.T) {
	s := newFakeAPIServer(t)
	s.srv.Close() // a request would fail with a connection error instead
	cases := map[string][]string{
		`required flag(s) "model-server" not set`: {"model", "add", "m"},
		"accepts 1 arg(s)":                        {"model", "add", "--model-server", "s"},
		"a count with a unit":                     {"model", "add", "m", "--model-server", "s", "--parameters", "lots"},
		"unknown capability":                      {"model", "add", "m", "--model-server", "s", "--capabilities", "flying"},
	}
	for want, args := range cases {
		if _, _, err := s.kmctl(t, args...); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%v: want an error saying %q, got %v", args, want, err)
		}
	}
}
