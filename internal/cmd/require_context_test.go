package cmd

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/spf13/cobra"
)

// stateCommands lists every command that changes Kubemoot state, with arguments
// that pass its own argument checks, so the context check is what stops it.
var stateCommands = map[string][]string{
	"kmctl apply":               {"apply", "-f", "-"},
	"kmctl delete":              {"delete", "crew", "demo"},
	"kmctl conversation ask":    {"conversation", "ask", "demo", "hi"},
	"kmctl conversation replay": {"conversation", "replay", "demo", "abc"},
	"kmctl conversation stop":   {"conversation", "stop", "demo", "abc"},
	"kmctl conversation pause":  {"conversation", "pause", "demo", "abc"},
	"kmctl conversation resume": {"conversation", "resume", "demo", "abc"},
	"kmctl conversation delete": {"conversation", "delete", "demo", "abc", "--yes"},
	"kmctl fitness run":         {"fitness", "run", "suite"},
	"kmctl fitness pause":       {"fitness", "pause", "suite"},
	"kmctl fitness resume":      {"fitness", "resume", "suite"},
	"kmctl fitness stop":        {"fitness", "stop", "suite", "--yes"},
	"kmctl mcp report pin":      {"mcp", "report", "pin", "r", "--verdict", "use"},
	"kmctl mcp report unpin":    {"mcp", "report", "unpin", "r"},
	"kmctl memory add":          {"memory", "add", "demo", "t", "k", "v"},
	"kmctl memory edit":         {"memory", "edit", "demo", "t", "k", "v"},
	"kmctl memory delete":       {"memory", "delete", "demo", "t", "k", "--yes"},
	"kmctl memory clear":        {"memory", "clear", "demo", "--yes"},
	"kmctl model add":           {"model", "add", "m", "--model-server", "s"},
}

// readOrLocalCommands lists every command that does not change Kubemoot state: it
// reads the cluster, or works on this machine only.
var readOrLocalCommands = []string{
	"kmctl agent get", "kmctl agent list", "kmctl conversation artifact", "kmctl conversation export",
	"kmctl conversation list", "kmctl conversation placements",
	"kmctl conversation watch", "kmctl conversation workspace", "kmctl create", "kmctl crew get",
	"kmctl crew list", "kmctl crew wait", "kmctl fitness download", "kmctl fitness get", "kmctl fitness list",
	"kmctl fitness scenarios", "kmctl info", "kmctl mcp report get", "kmctl mcp report list",
	"kmctl memory export", "kmctl model footprint", "kmctl model list", "kmctl model server get",
	"kmctl model server list", "kmctl prompt get", "kmctl prompt list", "kmctl status", "kmctl version",
}

func runnableCommands(c *cobra.Command) []*cobra.Command {
	var all []*cobra.Command
	if c.Runnable() {
		all = append(all, c)
	}
	for _, s := range c.Commands() {
		all = append(all, runnableCommands(s)...)
	}
	return all
}

// classificationProblem says what is wrong with how c is classified, or "".
func classificationProblem(c *cobra.Command) string {
	path := c.CommandPath()
	_, isState := stateCommands[path]
	isRead := slices.Contains(readOrLocalCommands, path)
	switch {
	case isState && isRead:
		return path + " is listed as both state-changing and read-only"
	case !isState && !isRead:
		return path + " is not classified: add changesState() to its Annotations, or list it in readOrLocalCommands"
	case isState && !changesClusterState(c):
		return path + " changes state but is not declared with changesState()"
	case isRead && changesClusterState(c):
		return path + " is declared state-changing but listed as read or local"
	}
	return ""
}

// Every command is classified: a new command is either declared state-changing
// (and then gets the context check) or listed as read or local, or this fails.
func TestEveryCommandIsClassified(t *testing.T) {
	root := newRootCommand(client.NewFactory())
	for _, c := range runnableCommands(root) {
		if msg := classificationProblem(c); msg != "" {
			t.Error(msg)
		}
	}
	for path := range stateCommands {
		if c, _, err := root.Find(strings.Fields(path)[1:]); err != nil || c.CommandPath() != path {
			t.Errorf("stateCommands lists %q, which is not a command", path)
		}
	}
}

// recorder counts the requests a fake API server receives and can refuse them.
type recorder struct {
	mu       sync.Mutex
	requests []string
}

func (r *recorder) saw() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.requests)
}

func (r *recorder) hook(unauthorized bool) func(*http.Request) (int, string) {
	return func(req *http.Request) (int, string) {
		r.mu.Lock()
		r.requests = append(r.requests, req.Method+" "+req.URL.Path)
		r.mu.Unlock()
		if unauthorized {
			return http.StatusUnauthorized, "Unauthorized"
		}
		return 0, ""
	}
}

func writeKubeconfig(t *testing.T, server, current string) string {
	t.Helper()
	kc := `apiVersion: v1
kind: Config
clusters:
- name: fake
  cluster:
    server: ` + server + `
    insecure-skip-tls-verify: true
users:
- name: fake
  user:
    token: fake-token
contexts:
- name: fake
  context:
    cluster: fake
    user: fake
current-context: ` + current + "\n"
	p := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(p, []byte(kc), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// runState runs a command line and returns its stderr and exit code.
func runState(t *testing.T, s *fakeAPIServer, args ...string) (string, int) {
	t.Helper()
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	f := client.NewFactory()
	f.OpenService = s.openService
	root := newRootCommand(f)
	var stderr bytes.Buffer
	root.SetOut(io.Discard)
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(append([]string{"--cache-dir", t.TempDir()}, args...))
	code := report(root, f, &stderr, false, func(string) string { return "" })
	return stderr.String(), code
}

func TestStateChangingCommandsNeedAUsableContext(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, s *fakeAPIServer) []string // kubeconfig flags
		want  string
		reqs  []string // the only requests the server may see
	}{
		{"no kubeconfig", func(t *testing.T, _ *fakeAPIServer) []string {
			return []string{"--kubeconfig", filepath.Join(t.TempDir(), "absent")}
		}, "kubeconfig cannot be read", nil},
		{"empty current-context", func(t *testing.T, s *fakeAPIServer) []string {
			return []string{"--kubeconfig", writeKubeconfig(t, s.srv.URL, `""`)}
		}, "no Kubernetes context is selected", nil},
		{"missing --context", func(t *testing.T, s *fakeAPIServer) []string {
			return []string{"--kubeconfig", writeKubeconfig(t, s.srv.URL, "fake"), "--context", "nope"}
		}, `context "nope" is not in the kubeconfig`, nil},
		{"credentials refused", func(t *testing.T, s *fakeAPIServer) []string {
			return []string{"--kubeconfig", writeKubeconfig(t, s.srv.URL, "fake")}
		}, `did not accept the credentials of context "fake"`, []string{"GET /version"}},
	}
	for _, tc := range cases {
		for path, args := range stateCommands {
			t.Run(tc.name+"/"+path, func(t *testing.T) {
				s := newFakeAPIServer(t)
				rec := &recorder{}
				s.refuse = rec.hook(tc.name == "credentials refused")
				stderr, code := runState(t, s, append(tc.setup(t, s), args...)...)
				if code != 1 || !strings.Contains(stderr, tc.want) || !strings.Contains(stderr, "Hint:") {
					t.Errorf("exit %d, stderr %q; want exit 1 naming %q with a hint", code, stderr, tc.want)
				}
				if got := rec.saw(); !slices.Equal(got, tc.reqs) {
					t.Errorf("requests = %v, want %v", got, tc.reqs)
				}
			})
		}
	}
}

func TestStateChangingCommandsRefuseADirectNATSURL(t *testing.T) {
	s := newFakeAPIServer(t)
	rec := &recorder{}
	s.refuse = rec.hook(false)
	stderr, code := runState(t, s, "--kubeconfig", s.kubeconfig, "memory", "add", "demo", "t", "k", "v", "--nats-url", "nats://127.0.0.1:1")
	if code != 1 || !strings.Contains(stderr, "--nats-url is not accepted") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
	if got := rec.saw(); len(got) != 0 {
		t.Errorf("requests = %v, want none", got)
	}
}

func TestStateChangingCommandWithAUsableContextProceeds(t *testing.T) {
	s := newFakeAPIServer(t)
	s.add("crews", contextNS, "demo", nil)
	if _, stderr, err := s.kmctl(t, "delete", "crew", "demo"); err != nil {
		t.Fatalf("delete with a good context: %v (%s)", err, stderr)
	}
}

func TestLocalCommandsWorkWithoutAKubeconfig(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	absent := filepath.Join(t.TempDir(), "absent")
	dir := t.TempDir()
	for _, args := range [][]string{
		{"version", "--client"},
		{"completion", "bash"},
		{"create", "demo", "--no-input", "-o", dir},
	} {
		f := client.NewFactory()
		root := newRootCommand(f)
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		root.SetArgs(append([]string{"--kubeconfig", absent}, args...))
		if err := root.Execute(); err != nil {
			t.Errorf("%v: %v", args, err)
		}
	}
}
