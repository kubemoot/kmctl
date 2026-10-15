package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/clierr"
	"github.com/kubemoot/kmctl/internal/conversation"
)

// runOpts is how a test runs kmctl the way main does: whether stderr counts as a
// terminal, and the environment report reads.
type runOpts struct {
	styled bool
	env    map[string]string
}

// runKmctl runs a command line through report, as Execute does, and returns what
// it wrote to stderr and the exit code.
func runKmctl(t *testing.T, o runOpts, args ...string) (string, int) {
	t.Helper()
	f := client.NewFactory()
	root := newRootCommand(f)
	var stderr bytes.Buffer
	root.SetOut(io.Discard)
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(append([]string{"--cache-dir", t.TempDir()}, args...))
	code := report(root, f, &stderr, o.styled, func(k string) string { return o.env[k] })
	return stderr.String(), code
}

// run runs a command line against the fake API server through report.
func (s *fakeAPIServer) run(t *testing.T, args ...string) (string, int) {
	t.Helper()
	return runKmctl(t, runOpts{}, append([]string{"--kubeconfig", s.kubeconfig}, args...)...)
}

// wantStderr checks that a failed command printed exactly want and exited 1.
func wantStderr(t *testing.T, label, got string, code int, want string) {
	t.Helper()
	if code != 1 {
		t.Errorf("%s: exit code %d, want 1", label, code)
	}
	if got != want {
		t.Errorf("%s:\n got: %q\nwant: %q", label, got, want)
	}
}

// gatewayAnswers serves crew demo's gateway in contextNS with a fixed HTTP status.
func gatewayAnswers(s *fakeAPIServer, code int) {
	s.proxy["demo-discussion/api/v1/discussions/demo"] = func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gateway says no", code)
	}
}

func TestErrors_CrewInAnotherNamespace(t *testing.T) {
	s := newFakeAPIServer(t)
	s.add("crews", "crew-homelab-pilot", "homelab-pilot", nil)
	got, code := s.run(t, "conversation", "ask", "homelab-pilot", "List the pods in the system namespace.")
	wantStderr(t, "one other namespace", got, code,
		"Error: no crew \"homelab-pilot\" in namespace \"team-a\"; found in \"crew-homelab-pilot\"\n"+
			"Hint: Pass -n crew-homelab-pilot\n")

	s.add("crews", "crew-b", "homelab-pilot", nil)
	got, code = s.run(t, "conversation", "watch", "homelab-pilot", "c-1")
	wantStderr(t, "two other namespaces, on watch", got, code,
		"Error: no crew \"homelab-pilot\" in namespace \"team-a\"; found in \"crew-b\", \"crew-homelab-pilot\"\n"+
			"Hint: Pass -n with one of those namespaces\n")
}

func TestErrors_CrewNowhere(t *testing.T) {
	s := newFakeAPIServer(t)
	got, code := s.run(t, "conversation", "ask", "ghost", "hi")
	wantStderr(t, "no such crew", got, code,
		"Error: no crew \"ghost\" in namespace \"team-a\"; no namespace has a crew of that name\n"+
			"Hint: Find it in every namespace with: kmctl crew list -A\n")
}

func TestErrors_CrewLookupForbiddenAcrossCluster(t *testing.T) {
	s := newFakeAPIServer(t)
	s.refuse = func(r *http.Request) (int, string) {
		if r.URL.Path == kubemootAPIPath+"/crews" {
			return http.StatusForbidden, `crews.kubemoot.ai is forbidden: User "dev" cannot list resource "crews" in API group "kubemoot.ai" at the cluster scope`
		}
		return 0, ""
	}
	got, code := s.run(t, "conversation", "ask", "homelab-pilot", "hi")
	wantStderr(t, "no cluster-wide list", got, code,
		"Error: no crew \"homelab-pilot\" in namespace \"team-a\"\n"+
			"Hint: Pass its namespace with -n. Find it in every namespace with: kmctl crew list -A\n")
}

func TestErrors_GatewayNotFound(t *testing.T) {
	s := newFakeAPIServer(t)
	s.add("crews", contextNS, "demo", nil)
	gatewayAnswers(s, http.StatusNotFound)
	got, code := s.run(t, "conversation", "ask", "demo", "hi")
	wantStderr(t, "gateway 404", got, code,
		"Error: the discussion gateway of crew \"demo\" was not found in namespace \"team-a\"\n"+
			"Hint: Check the crew with: kmctl crew get demo -n team-a\n")
}

func TestErrors_GatewayNotReady(t *testing.T) {
	s := newFakeAPIServer(t)
	s.add("crews", contextNS, "demo", nil)
	got, code := s.run(t, "conversation", "ask", "demo", "hi") // the fake answers 503 for a gateway with no endpoints
	wantStderr(t, "gateway 503", got, code,
		"Error: the discussion gateway of crew \"demo\" in namespace \"team-a\" is not ready\n"+
			"Hint: Its pods may still be starting. Check the crew with: kmctl crew get demo -n team-a\n")
}

func TestErrors_GatewayHTTPError(t *testing.T) {
	s := newFakeAPIServer(t)
	gatewayAnswers(s, http.StatusInternalServerError)
	got, code := s.run(t, "conversation", "ask", "demo", "hi")
	wantStderr(t, "gateway 500", got, code,
		"Error: the discussion gateway of crew \"demo\" answered HTTP 500\n"+
			"Hint: See its logs with: kubectl logs -n team-a svc/demo-discussion\n")
}

func TestErrors_StreamCut(t *testing.T) {
	s := newFakeAPIServer(t)
	s.proxy["demo-discussion/api/v1/discussions/demo"] = func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"conversationId": "c-1"})
	}
	s.proxy["demo-discussion/api/v1/discussions/demo/c-1/stream"] = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"connected\"}\n\n")
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler) // drop the connection mid-stream
	}
	got, code := s.run(t, "conversation", "ask", "demo", "hi", "-o", "plain")
	wantStderr(t, "stream cut", got, code,
		"Error: the discussion stream closed before the crew answered; the discussion may still finish\n"+
			"Hint: Reattach: kmctl conversation watch demo c-1 -n team-a\n")
}

func TestErrors_UnknownOutputMode(t *testing.T) {
	s := newFakeAPIServer(t)
	got, code := s.run(t, "conversation", "ask", "demo", "hi", "-o", "yaml")
	wantStderr(t, "conversation -o yaml", got, code, "Error: unknown output \"yaml\": want one of full, plain, json, quiet\n")

	s.srv.Close() // the format is checked before the cluster is called
	got, code = s.run(t, "crew", "list", "-o", "table")
	wantStderr(t, "crew list -o table", got, code, "Error: unsupported output format \"table\" (want one of \"yaml\", \"json\", \"kyaml\")\n")
}

func TestErrors_BadFlagsAndArgs(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"crew", "list", "--bogus"},
			"Error: unknown flag: --bogus\nHint: Usage: kmctl crew list [flags] (see kmctl crew list --help)\n"},
		{[]string{"conversation", "ask", "demo"},
			"Error: accepts 2 arg(s), received 1\nHint: Usage: kmctl conversation ask CREW MESSAGE [flags] (see kmctl conversation ask --help)\n"},
		{[]string{"conversation", "ask", "demo", "hi", "--timeout", "soon"},
			"Error: invalid argument \"soon\" for \"--timeout\" flag: time: invalid duration \"soon\"\n" +
				"Hint: Usage: kmctl conversation ask CREW MESSAGE [flags] (see kmctl conversation ask --help)\n"},
		{[]string{"frobnicate"},
			"Error: unknown command \"frobnicate\" for \"kmctl\"\nHint: Usage: kmctl [flags] (see kmctl --help)\n"},
	}
	for _, c := range cases {
		got, code := runKmctl(t, runOpts{}, c.args...)
		wantStderr(t, strings.Join(c.args, " "), got, code, c.want)
	}
}

func TestErrors_ClusterUnreachable(t *testing.T) {
	s := newFakeAPIServer(t)
	s.srv.Close()
	for _, args := range [][]string{{"crew", "list"}, {"status"}, {"conversation", "ask", "demo", "hi"}} {
		got, code := s.run(t, args...)
		wantStderr(t, strings.Join(args, " "), got, code,
			"Error: cannot reach the Kubernetes API server (context \"fake\")\n"+
				"Hint: Check that the cluster is running and the context points at it: kmctl status\n")
	}
}

func TestErrors_NoKubeconfig(t *testing.T) {
	// An empty kubeconfig has no context; the client then falls back to
	// localhost:8080, which nothing in the test serves.
	empty := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBECONFIG", empty)
	const want = "Error: no Kubernetes cluster is configured (the kubeconfig has no current context)\n" +
		"Hint: Set KUBECONFIG or pass --kubeconfig, and pick a context with --context\n"
	got, code := runKmctl(t, runOpts{}, "crew", "list")
	wantStderr(t, "KUBECONFIG without a context", got, code, want)
	got, code = runKmctl(t, runOpts{}, "crew", "list", "--kubeconfig", empty)
	wantStderr(t, "--kubeconfig without a context", got, code, want)

	missing := filepath.Join(t.TempDir(), "kubeconfig")
	got, code = runKmctl(t, runOpts{}, "crew", "list", "--kubeconfig", missing)
	wantStderr(t, "missing --kubeconfig file", got, code,
		fmt.Sprintf("Error: kubeconfig file %q does not exist\nHint: Check the path passed with --kubeconfig\n", missing))
}

func TestErrors_UnknownContext(t *testing.T) {
	s := newFakeAPIServer(t)
	got, code := s.run(t, "crew", "list", "--context", "nope")
	wantStderr(t, "unknown context", got, code,
		"Error: context \"nope\" is not in the kubeconfig\nHint: List the contexts with: kubectl config get-contexts\n")
}

func TestErrors_Credentials(t *testing.T) {
	s := newFakeAPIServer(t)
	s.refuse = func(*http.Request) (int, string) { return http.StatusUnauthorized, "Unauthorized" }
	got, code := s.run(t, "crew", "list")
	wantStderr(t, "401", got, code,
		"Error: the Kubernetes API server did not accept your credentials (context \"fake\")\n"+
			"Hint: Log in again or refresh the credentials in your kubeconfig\n")
}

func TestErrors_Forbidden(t *testing.T) {
	s := newFakeAPIServer(t)
	s.refuse = func(*http.Request) (int, string) {
		return http.StatusForbidden, `crews.kubemoot.ai is forbidden: User "dev" cannot list resource "crews" in API group "kubemoot.ai" in the namespace "team-a"`
	}
	got, code := s.run(t, "crew", "list")
	wantStderr(t, "403 in a namespace", got, code,
		"Error: you are not allowed to list crews in namespace \"team-a\"\n"+
			"Hint: Ask a cluster admin for access, or check yours with: kubectl auth can-i --list -n team-a\n")

	s.refuse = func(*http.Request) (int, string) {
		return http.StatusForbidden, `services "demo-discussion" is forbidden: User "dev" cannot create resource "services/proxy" in API group "" in the namespace "team-a"`
	}
	got, code = s.run(t, "conversation", "ask", "demo", "hi")
	wantStderr(t, "403 on the service proxy", got, code,
		"Error: you are not allowed to reach the discussion gateway of crew \"demo\" in namespace \"team-a\"\n"+
			"Hint: It takes create on services/proxy; check yours with: kubectl auth can-i create services/proxy -n team-a\n")
}

func TestErrors_ObjectNotFound(t *testing.T) {
	s := newFakeAPIServer(t)
	cases := map[string]string{
		"crew":    "Error: no crew \"ghost\" in namespace \"team-a\"\nHint: Find it in every namespace with: kmctl crew list -A\n",
		"prompt":  "Error: no prompt module \"ghost\" in namespace \"team-a\"\nHint: Find it in every namespace with: kmctl prompt list -A\n",
		"fitness": "Error: no fitness suite \"ghost\" in namespace \"team-a\"\nHint: Find it in every namespace with: kmctl fitness list -A\n",
	}
	for noun, want := range cases {
		got, code := s.run(t, noun, "get", "ghost")
		wantStderr(t, noun+" get", got, code, want)
	}
}

func TestErrors_KubemootNotInstalled(t *testing.T) {
	s := newFakeAPIServer(t)
	s.noKubemoot = true
	got, code := s.run(t, "crew", "list")
	wantStderr(t, "no kubemoot.ai", got, code,
		"Error: the cluster does not serve the Kubemoot API (kubemoot.ai)\n"+
			"Hint: Check that Kubemoot is installed and the context is the right one: kmctl status\n")
}

func TestErrors_UnknownKind(t *testing.T) {
	s := newFakeAPIServer(t)
	got, code := s.run(t, "apply", "-f", writeManifest(t, "apiVersion: kubemoot.ai/v1alpha1\nkind: Crw\nmetadata:\n  name: demo\n"))
	wantStderr(t, "apply kind Crw", got, code,
		"Error: the cluster has no such Kubemoot kind: no matches for kind \"Crw\" in version \"kubemoot.ai/v1alpha1\"\n"+
			"Hint: Check the kind's spelling; kmctl status shows whether Kubemoot is installed\n")
}

func TestErrors_MissingManifest(t *testing.T) {
	got, code := runKmctl(t, runOpts{}, "apply", "-f", "no-such-file.yaml")
	wantStderr(t, "apply -f missing", got, code, "Error: file \"no-such-file.yaml\" does not exist\nHint: Check the path\n")
}

func TestErrors_VerboseShowsTheCause(t *testing.T) {
	s := newFakeAPIServer(t)
	s.srv.Close()
	const head = "Error: cannot reach the Kubernetes API server (context \"fake\")\n" +
		"Hint: Check that the cluster is running and the context points at it: kmctl status\n"
	for _, c := range []struct {
		label string
		o     runOpts
		flag  []string
	}{
		{"--verbose", runOpts{}, []string{"--verbose"}},
		{"-v", runOpts{}, []string{"-v"}},
		{"KMCTL_DEBUG=1", runOpts{env: map[string]string{"KMCTL_DEBUG": "1"}}, nil},
	} {
		args := append([]string{"--kubeconfig", s.kubeconfig, "crew", "list"}, c.flag...)
		got, code := runKmctl(t, c.o, args...)
		if code != 1 || !strings.HasPrefix(got, head+"Cause: ") || !strings.Contains(got, "connection refused") {
			t.Errorf("%s adds the underlying error on a third line, got %d:\n%s", c.label, code, got)
		}
	}
	got, _ := runKmctl(t, runOpts{env: map[string]string{"KMCTL_DEBUG": "0"}}, "--kubeconfig", s.kubeconfig, "crew", "list")
	if got != head {
		t.Errorf("KMCTL_DEBUG=0 is not verbose, got:\n%s", got)
	}
}

func TestErrors_StyledOnlyOnATerminal(t *testing.T) {
	plain, _ := runKmctl(t, runOpts{}, "frobnicate")
	if strings.Contains(plain, "\x1b[") {
		t.Errorf("non-terminal stderr has no escape codes: %q", plain)
	}
	styled, code := runKmctl(t, runOpts{styled: true}, "frobnicate")
	if code != 1 || !strings.Contains(styled, "\x1b[") || !strings.Contains(styled, "unknown command \"frobnicate\" for \"kmctl\"") {
		t.Errorf("terminal stderr colors the labels and keeps the message, got %q", styled)
	}
}

func TestErrors_ExplainedErrorPassesThrough(t *testing.T) {
	e := clierr.New("message", "hint", errors.New("cause"))
	if got := explain(fmt.Errorf("wrapped: %w", e), NewRootCommand(), client.NewFactory()); got != e {
		t.Errorf("an explanation a command gave is kept, got %+v", got)
	}
	plain := errors.New("something kmctl does not know")
	if got := explain(plain, NewRootCommand(), client.NewFactory()); got.Message != plain.Error() || got.Hint != "" {
		t.Errorf("an unknown error keeps its text with no hint, got %+v", got)
	}
}

func TestErrors_StreamCutKeepsBrokenPipe(t *testing.T) {
	pipe := fmt.Errorf("write: %w", syscall.EPIPE)
	if got := streamCut(pipe, conversation.Options{}); got != pipe {
		t.Errorf("a closed stdout is returned unchanged, got %v", got)
	}
}

func TestCrewNotFound_Messages(t *testing.T) {
	cases := []struct {
		found   []string
		listErr error
		want    string
	}{
		{nil, errors.New("forbidden"), `no crew "c" in namespace "ns"`},
		{nil, nil, `no crew "c" in namespace "ns"; no namespace has a crew of that name`},
		{[]string{"a"}, nil, `no crew "c" in namespace "ns"; found in "a"`},
		{[]string{"a", "b"}, nil, `no crew "c" in namespace "ns"; found in "a", "b"`},
	}
	for _, c := range cases {
		if got := crewNotFound("c", "ns", c.found, c.listErr).Message; got != c.want {
			t.Errorf("found %v, err %v: got %q, want %q", c.found, c.listErr, got, c.want)
		}
	}
}
