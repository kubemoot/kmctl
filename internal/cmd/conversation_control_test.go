package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/clierr"
)

// errorText is what a person sees of err: the message, the hint and the cause.
func errorText(err error) string {
	var e *clierr.Error
	if !errors.As(err, &e) {
		return err.Error()
	}
	var b bytes.Buffer
	_ = clierr.Write(&b, e, clierr.Format{Verbose: true})
	return b.String()
}

const controlBase = "demo-discussion/api/v1/discussions/demo/" + convID

// kmctlWithInput runs a command with stdin text, as an answer to a question.
func (s *fakeAPIServer) kmctlWithInput(t *testing.T, input string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	f := client.NewFactory()
	f.OpenService = s.openService
	root := newRootCommand(f)
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetIn(strings.NewReader(input))
	root.SetArgs(append([]string{"--kubeconfig", s.kubeconfig, "--cache-dir", t.TempDir()}, args...))
	err = root.Execute()
	return out.String(), errOut.String(), err
}

type controlSeen struct {
	method  string
	query   map[string][]string
	headers http.Header
	calls   int
}

func serveControl(s *fakeAPIServer, key string, code int, body string) *controlSeen {
	seen := &controlSeen{}
	s.proxy[key] = func(w http.ResponseWriter, r *http.Request) {
		seen.method, seen.query, seen.headers = r.Method, r.URL.Query(), r.Header.Clone()
		seen.calls++
		if code >= 400 && !strings.HasPrefix(body, "{") {
			http.Error(w, body, code)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}
	return seen
}

func TestConversationControls_SendTheVerb(t *testing.T) {
	for verb, past := range map[string]string{"stop": "stopped", "pause": "paused", "resume": "resumed"} {
		t.Run(verb, func(t *testing.T) {
			s := newFakeAPIServer(t)
			seen := serveControl(s, controlBase+"/"+verb, 200, `{"conversationId":"`+convID+`","action":"`+verb+`","threads":["t-1","t-2"]}`)
			out, _, err := s.kmctl(t, "conversation", verb, "demo", convID, "--reason", "ticket 9")
			if err != nil {
				t.Fatal(err)
			}
			assertContains(t, verb, out, past+" conversation "+convID, "2 thread(s) (t-1, t-2)")
			if seen.method != http.MethodPost {
				t.Errorf("method = %s", seen.method)
			}
			if got := seen.headers.Get("Authorization"); got != "Bearer fake-token" {
				t.Errorf("Authorization = %q", got)
			}
			if got := seen.headers.Get("Kubemoot-Access-Reason"); got != "ticket 9" {
				t.Errorf("reason header = %q", got)
			}
		})
	}
}

func TestConversationControls_Errors(t *testing.T) {
	cases := map[string]struct {
		verb string
		code int
		text string
		want []string
	}{
		"401":        {"stop", 401, "unauthorized", []string{"did not accept your Kubernetes token"}},
		"403":        {"stop", 403, "forbidden", []string{"refused your identity"}},
		"404":        {"pause", 404, "conversation not found", []string{"conversation not found on crew", "may not pause it", "kubemoot-conversation-controller"}},
		"409 stop":   {"stop", 409, "the conversation has no running discussion", []string{"nothing to stop", "no running discussion", "kmctl conversation list demo"}},
		"409 resume": {"resume", 409, "the conversation has no running discussion", []string{"nothing to resume"}},
		"400 reason": {"stop", 400, "acting on another caller's conversations needs a reason of 1 to 200 characters in the Kubemoot-Access-Reason header", []string{"wants a reason to stop", "--reason"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := newFakeAPIServer(t)
			serveControl(s, controlBase+"/"+c.verb, c.code, c.text)
			_, _, err := s.kmctl(t, "conversation", c.verb, "demo", convID)
			if err == nil {
				t.Fatal("want an error")
			}
			assertContains(t, name, errorText(err), c.want...)
		})
	}
}

const erasureBody = `{"conversationId":"` + convID + `","threads":["t-1"],"deleted":{"history":1,"owner":1,"discussionMessages":14,` +
	`"coordinatorEvents":6,"workspaceObjects":2,"scheduled":0,"memoryFacts":3},` +
	`"kept":["logs and backups: erasing from NATS does not erase them"]}`

func TestConversationDelete_PrintsTheErasureReport(t *testing.T) {
	s := newFakeAPIServer(t)
	seen := serveControl(s, controlBase, 200, erasureBody)
	out, errOut, err := s.kmctlWithInput(t, "y\n", "conversation", "delete", "demo", convID, "--reason", "erasure 88")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "question", errOut, "Erase conversation "+convID+" of crew demo", "[y/N]")
	assertContains(t, "report", out, "erased conversation "+convID+" (1 thread(s))", fmt.Sprintf("  %-22s %d", "discussion messages", 14), fmt.Sprintf("  %-22s %d", "memory facts", 3),
		"not removed: logs and backups")
	if seen.method != http.MethodDelete || seen.calls != 1 {
		t.Errorf("method %s, calls %d", seen.method, seen.calls)
	}
	if _, has := seen.query["force"]; has {
		t.Error("force was sent without --force")
	}
	if seen.headers.Get("Kubemoot-Access-Reason") != "erasure 88" || seen.headers.Get("Authorization") != "Bearer fake-token" {
		t.Errorf("headers = %v", seen.headers)
	}
}

func TestConversationDelete_AsksFirst(t *testing.T) {
	for name, input := range map[string]string{"no": "n\n", "nothing": "", "other": "maybe\n"} {
		t.Run(name, func(t *testing.T) {
			s := newFakeAPIServer(t)
			seen := serveControl(s, controlBase, 200, erasureBody)
			_, _, err := s.kmctlWithInput(t, input, "conversation", "delete", "demo", convID)
			if err == nil || !strings.Contains(errorText(err), "nothing was changed") {
				t.Fatalf("err = %v", err)
			}
			if seen.calls != 0 {
				t.Error("the gateway was asked to delete without a yes")
			}
		})
	}
}

func TestConversationDelete_YesAndForce(t *testing.T) {
	s := newFakeAPIServer(t)
	seen := serveControl(s, controlBase, 200, erasureBody)
	_, errOut, err := s.kmctl(t, "conversation", "delete", "demo", convID, "--yes", "--force")
	if err != nil {
		t.Fatal(err)
	}
	if errOut != "" {
		t.Errorf("--yes still asked: %q", errOut)
	}
	if got := seen.query["force"]; len(got) != 1 || got[0] != "true" {
		t.Errorf("force = %v", got)
	}
}

func TestConversationDelete_NoDryRun(t *testing.T) {
	s := newFakeAPIServer(t)
	seen := serveControl(s, controlBase, 200, erasureBody)
	_, _, err := s.kmctl(t, "conversation", "delete", "demo", convID, "--dry-run", "--yes")
	if err == nil || !strings.Contains(errorText(err), "offers no dry run") {
		t.Fatalf("err = %v", err)
	}
	if seen.calls != 0 {
		t.Error("--dry-run reached the gateway")
	}
	help, _, _ := s.kmctl(t, "conversation", "delete", "--help")
	assertContains(t, "help", help, "offers no dry run")
	if strings.Contains(help, "--dry-run") && !strings.Contains(help, "no --dry-run") {
		t.Errorf("help advertises --dry-run:\n%s", help)
	}
}

func TestConversationDelete_Errors(t *testing.T) {
	cases := map[string]struct {
		code int
		text string
		want []string
	}{
		"401": {401, "unauthorized", []string{"did not accept your Kubernetes token"}},
		"403": {403, "forbidden", []string{"refused your identity"}},
		"404": {404, "conversation not found", []string{"conversation not found on crew", "may not delete it", "kubemoot-conversation-controller"}},
		"409": {409, "the conversation has a running discussion; stop it first, or pass force=true", []string{"has a running discussion", "--force", "kmctl conversation stop demo"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := newFakeAPIServer(t)
			serveControl(s, controlBase, c.code, c.text)
			_, _, err := s.kmctl(t, "conversation", "delete", "demo", convID, "--yes")
			if err == nil {
				t.Fatal("want an error")
			}
			assertContains(t, name, errorText(err), c.want...)
		})
	}
}

func TestConversationDelete_PartialFailurePrintsWhatWasRemoved(t *testing.T) {
	s := newFakeAPIServer(t)
	body := strings.Replace(erasureBody, `"kept"`, `"failed":["workspace kubemoot_workspace"],"kept"`, 1)
	serveControl(s, controlBase, 500, body)
	out, _, err := s.kmctl(t, "conversation", "delete", "demo", convID, "--yes")
	if err == nil {
		t.Fatal("want an error")
	}
	assertContains(t, "report", out, "erased conversation "+convID, "failed: workspace kubemoot_workspace")
	assertContains(t, "error", errorText(err), "could not clear every store: workspace kubemoot_workspace", "Repeat the delete")
}

func TestConversationDelete_ServerErrorWithoutReport(t *testing.T) {
	s := newFakeAPIServer(t)
	serveControl(s, controlBase, 500, "boom")
	out, _, err := s.kmctl(t, "conversation", "delete", "demo", convID, "--yes")
	if err == nil || out != "" {
		t.Fatalf("err %v, out %q", err, out)
	}
	assertContains(t, "error", errorText(err), "answered HTTP 500")
}
