package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The commands below run end to end against fakeAPIServer: flags, kubeconfig,
// clients, the request, and what the reader sees.

// assertContains fails for each want missing from got.
func assertContains(t *testing.T, label, got string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("%s: missing %q in:\n%s", label, want, got)
		}
	}
}

// writeManifest writes a manifest file and returns its path.
func writeManifest(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "manifest.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const crewManifest = `apiVersion: kubemoot.ai/v1alpha1
kind: Crew
metadata:
  name: demo
spec: {}
`

func TestStatus_AgainstCluster(t *testing.T) {
	s := newFakeAPIServer(t)
	out, _, err := s.kmctl(t, "status")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "installed", out, "reachable (context fake, v1.33.1)", "Kubemoot:  installed (kubemoot.ai)")

	s.noKubemoot = true
	out, _, err = s.kmctl(t, "status")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "not installed", out, "Kubemoot:  not found (no kubemoot.ai API group)")
}

func TestStatus_ClusterUnreachable(t *testing.T) {
	s := newFakeAPIServer(t)
	s.srv.Close() // closing again in cleanup is a no-op
	_, _, err := s.kmctl(t, "status")
	if err == nil || !strings.Contains(err.Error(), `cannot reach cluster (context "fake")`) {
		t.Fatalf("want a cannot-reach-cluster error naming the context, got %v", err)
	}
}

func TestInfo_AgainstCluster(t *testing.T) {
	s := newFakeAPIServer(t)
	out, _, err := s.kmctl(t, "info")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "table", out, "Context:        fake", "Namespace:      team-a", "Server:         "+s.srv.URL, "Server version: v1.33.1")

	out, _, err = s.kmctl(t, "info", "-o", "json", "-n", "other")
	if err != nil {
		t.Fatal(err)
	}
	var info map[string]string
	if err := json.Unmarshal([]byte(out), &info); err != nil || info["namespace"] != "other" || info["version"] != "v1.33.1" {
		t.Errorf("-o json -n other: got %v (%v)", info, err)
	}
}

func TestInfo_ClusterUnreachableStillReports(t *testing.T) {
	s := newFakeAPIServer(t)
	s.srv.Close()
	out, _, err := s.kmctl(t, "info")
	if err != nil {
		t.Fatalf("info reports what it can resolve, not an error: %v", err)
	}
	assertContains(t, "unreachable", out, "Context:        fake", "Server version: (unreachable)")
}

func TestCrewList_AgainstCluster(t *testing.T) {
	s := newFakeAPIServer(t)
	_, errOut, err := s.kmctl(t, "crew", "list")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "empty namespace", errOut, "No crews found in team-a namespace.")
	_, errOut, err = s.kmctl(t, "crew", "list", "-A")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "empty cluster", errOut, "No crews found.")

	s.add("crews", "team-a", "demo", map[string]any{"status": map[string]any{"ready": true, "phase": "Running", "agentCount": 3}})
	s.add("crews", "team-b", "other", nil)
	out, _, err := s.kmctl(t, "crew", "list")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "one namespace", out, "NAME", "demo", "Running")
	if strings.Contains(out, "other") {
		t.Errorf("list without -A shows another namespace's crew:\n%s", out)
	}
	out, _, err = s.kmctl(t, "crew", "list", "-A")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "every namespace", out, "NAMESPACE", "team-a", "team-b", "other")

	out, _, err = s.kmctl(t, "crew", "list", "-o", "json")
	if err != nil || !strings.Contains(out, `"CrewList"`) {
		t.Errorf("-o json prints the list object: %v\n%s", err, out)
	}
	if _, _, err = s.kmctl(t, "crew", "list", "-o", "toml"); err == nil {
		t.Error("an unsupported -o value must fail")
	}
}

func TestCrewGet_AgainstCluster(t *testing.T) {
	s := newFakeAPIServer(t)
	s.add("crews", "team-a", "demo", map[string]any{"status": map[string]any{"phase": "Running"}})
	out, _, err := s.kmctl(t, "crew", "get", "demo")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "table", out, "NAME", "demo", "Running")

	out, _, err = s.kmctl(t, "crew", "get", "demo", "-o", "yaml")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "yaml", out, "kind: Crew", "name: demo")

	_, _, err = s.kmctl(t, "crew", "get", "missing")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("a missing crew is a not-found error, got %v", err)
	}
}

func TestCrewGet_CompletesNames(t *testing.T) {
	s := newFakeAPIServer(t)
	s.add("crews", "team-a", "demo", nil)
	s.add("crews", "team-a", "deploy-guide", nil)
	s.add("crews", "team-a", "health", nil)
	out, _, err := s.kmctl(t, "__complete", "crew", "get", "de")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "prefix de", out, "demo\n", "deploy-guide\n")
	if strings.Contains(out, "health") {
		t.Errorf("completion offers a name without the prefix:\n%s", out)
	}
	out, _, _ = s.kmctl(t, "__complete", "crew", "get", "demo", "")
	if strings.Contains(out, "deploy-guide") {
		t.Errorf("get takes one name; nothing completes after it:\n%s", out)
	}
	s.srv.Close()
	out, _, err = s.kmctl(t, "__complete", "crew", "get", "")
	if err != nil || strings.Contains(out, "demo") {
		t.Errorf("an unreachable cluster completes nothing and is no error: %v\n%s", err, out)
	}
}

func TestApply_AgainstCluster(t *testing.T) {
	s := newFakeAPIServer(t)
	path := writeManifest(t, crewManifest)

	out, _, err := s.kmctl(t, "apply", "-f", path, "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "dry run", out, "crew/demo applied (dry run)")
	if s.get("crews", "demo") != nil {
		t.Error("a dry run must not store the crew")
	}

	out, _, err = s.kmctl(t, "apply", "-f", path)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "apply", out, "crew/demo applied")
	if s.get("crews", "demo") == nil {
		t.Error("apply stores the crew in the context's namespace")
	}
}

func TestApply_RejectsBadInput(t *testing.T) {
	s := newFakeAPIServer(t)
	cases := map[string]struct {
		args []string
		want string
	}{
		"no manifest":     {[]string{"apply"}, "a manifest is required"},
		"empty manifest":  {[]string{"apply", "-f", writeManifest(t, "# nothing\n")}, "no objects found"},
		"missing file":    {[]string{"apply", "-f", filepath.Join(t.TempDir(), "absent.yaml")}, "no such file"},
		"not kubemoot.ai": {[]string{"apply", "-f", writeManifest(t, "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: x\n")}, "refusing to apply Namespace"},
	}
	for name, c := range cases {
		if _, _, err := s.kmctl(t, c.args...); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want an error containing %q, got %v", name, c.want, err)
		}
	}
}

func TestDelete_AgainstCluster(t *testing.T) {
	s := newFakeAPIServer(t)
	s.add("crews", "team-a", "demo", nil)
	out, _, err := s.kmctl(t, "delete", "crew", "demo")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "by name", out, "crew/demo deleted")
	if s.get("crews", "demo") != nil {
		t.Error("delete by name leaves the crew")
	}

	s.add("crews", "team-a", "demo", nil)
	out, _, err = s.kmctl(t, "delete", "-f", writeManifest(t, crewManifest))
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "from a manifest", out, "crew/demo deleted")
	if s.get("crews", "demo") != nil {
		t.Error("delete -f leaves the crew")
	}

	if _, _, err = s.kmctl(t, "delete", "crew", "demo"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("deleting a crew that is gone is a not-found error, got %v", err)
	}
}

func TestDelete_RejectsBadInput(t *testing.T) {
	s := newFakeAPIServer(t)
	manifest := writeManifest(t, crewManifest)
	cases := map[string]struct {
		args []string
		want string
	}{
		"name and -f":     {[]string{"delete", "crew", "demo", "-f", manifest}, "either KIND NAME or -f"},
		"kind only":       {[]string{"delete", "crew"}, "usage: kmctl delete KIND NAME"},
		"unknown kind":    {[]string{"delete", "pod", "x"}, `unknown kubemoot resource "pod"`},
		"not kubemoot.ai": {[]string{"delete", "-f", writeManifest(t, "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: x\n")}, "refusing to delete Namespace"},
	}
	for name, c := range cases {
		if _, _, err := s.kmctl(t, c.args...); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want an error containing %q, got %v", name, c.want, err)
		}
	}
}

// starterSuite is a CrewFitnessSuite with two scripts and the given status.
func starterSuite(status map[string]any) map[string]any {
	return map[string]any{
		"spec": map[string]any{
			"crewRef": "demo",
			"scripts": []any{
				map[string]any{"testRef": "smoke", "testContent": "ask: hello"},
				map[string]any{"testRef": "pods", "testContent": "ask: list pods"},
			},
		},
		"status": status,
	}
}

func TestFitnessScenarios_AgainstCluster(t *testing.T) {
	s := newFakeAPIServer(t)
	s.add("crewfitnesssuites", "team-a", "starter", starterSuite(nil))
	s.add("crewfitnesssuites", "team-a", "empty", nil)
	out, _, err := s.kmctl(t, "fitness", "scenarios", "starter")
	if err != nil || out != "smoke\npods\n" {
		t.Errorf("scenarios: got %q, %v; want smoke and pods, one per line", out, err)
	}
	out, errOut, err := s.kmctl(t, "fitness", "scenarios", "empty")
	if err != nil || out != "" || !strings.Contains(errOut, "no scenarios found") {
		t.Errorf("a suite without scripts says so on stderr: out %q, err %q, %v", out, errOut, err)
	}
	if _, _, err = s.kmctl(t, "fitness", "scenarios", "missing"); err == nil {
		t.Error("a missing suite is an error")
	}
}

func TestFitnessRun_Suite(t *testing.T) {
	s := newFakeAPIServer(t)
	s.add("crewfitnesssuites", "team-a", "starter", starterSuite(map[string]any{
		"phase": "Completed", "iterationsCompleted": 2, "iterationsTotal": 2, "passed": 1, "failed": 1,
		"judge": map[string]any{"phase": "Judging"},
	}))
	out, _, err := s.kmctl(t, "fitness", "run", "starter")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "suite run", out, "Completed  2/2 done  (passed 1, failed 1)", "The judge is still scoring this run (judge Judging)")

	if _, _, err = s.kmctl(t, "fitness", "run", "missing"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("running a missing suite is a not-found error, got %v", err)
	}
}

func TestFitnessRun_OneScenario(t *testing.T) {
	s := newFakeAPIServer(t)
	s.add("crewfitnesssuites", "team-a", "starter", starterSuite(nil))
	s.react = func(obj map[string]any) { obj["status"] = map[string]any{"phase": "Passed"} } // the operator finishes the run
	out, _, err := s.kmctl(t, "fitness", "run", "starter", "--scenario", "pods")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "scenario run", out, `running scenario "pods" as crewfitness/starter-pods-kmctl-x7k2p`, "Passed")
	cf := s.get("crewfitnesses", "starter-pods-kmctl-x7k2p")
	if spec, _ := cf["spec"].(map[string]any); spec["crewRef"] != "demo" || spec["testContent"] != "ask: list pods" {
		t.Errorf("the CrewFitness runs the suite's crew on the scenario's script, got %v", cf)
	}

	_, _, err = s.kmctl(t, "fitness", "run", "starter", "--scenario", "nope")
	if err == nil || !strings.Contains(err.Error(), `scenario "nope" not found in suite "starter"`) {
		t.Errorf("an unknown scenario names itself and the suite, got %v", err)
	}
}

func TestFitnessRun_FromManifest(t *testing.T) {
	s := newFakeAPIServer(t)
	s.react = func(obj map[string]any) { obj["status"] = map[string]any{"phase": "Completed"} }
	suite := writeManifest(t, `apiVersion: kubemoot.ai/v1alpha1
kind: CrewFitnessSuite
metadata:
  name: starter
spec:
  crewRef: demo
`)
	out, _, err := s.kmctl(t, "fitness", "run", "-f", suite)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "run -f", out, "crewfitnesssuite/starter applied", "Completed")

	cases := map[string]struct {
		args []string
		want string
	}{
		"no suite":       {[]string{"fitness", "run"}, "provide a SUITE name or -f FILE"},
		"suite and -f":   {[]string{"fitness", "run", "starter", "-f", suite}, "not both"},
		"no suite in -f": {[]string{"fitness", "run", "-f", writeManifest(t, crewManifest)}, "no CrewFitnessSuite found"},
	}
	for name, c := range cases {
		if _, _, err := s.kmctl(t, c.args...); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want an error containing %q, got %v", name, c.want, err)
		}
	}
}

func TestFitnessDownload_AgainstCluster(t *testing.T) {
	s := newFakeAPIServer(t)
	s.services["kubemoot"] = []map[string]any{{
		"metadata": map[string]any{"name": "kubemoot-dashboard", "namespace": "kubemoot"},
		"spec":     map[string]any{"ports": []any{map[string]any{"name": "http", "port": 80}}},
	}}
	s.proxy["kubemoot-dashboard/dashboard/api/kubemoot/crewfitnesssuites/team-a/starter/artifact"] = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "xlsx-bytes")
	}
	file := filepath.Join(t.TempDir(), "results.xlsx")
	out, _, err := s.kmctl(t, "fitness", "download", "starter", "-o", file)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "download", out, "wrote "+file+" (10 bytes)")
	if data, err := os.ReadFile(file); err != nil || string(data) != "xlsx-bytes" {
		t.Errorf("downloaded file: %q, %v", data, err)
	}

	_, _, err = s.kmctl(t, "fitness", "download", "missing", "-o", file)
	if err == nil || !strings.Contains(err.Error(), `download artifact for suite "missing"`) {
		t.Errorf("a suite without an artifact is an error naming it, got %v", err)
	}
}

// discussionEvents is a gateway SSE stream: a heartbeat comment, then a short discussion.
const discussionEvents = `: heartbeat

data: {"type":"connected"}

data: {"type":"phase","agent":"demo-workloads","status":"gathering","gpu":"gpu-0"}

data: {"type":"synthesis","content":"All pods are Running."}

data: {"type":"done"}

`

// withGateway serves crew demo's discussion gateway: a turn starts conversation
// c-1, and its stream is discussionEvents.
func withGateway(s *fakeAPIServer, asked *string) {
	s.proxy["demo-discussion/api/v1/discussions/demo"] = func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		*asked = body["message"]
		writeJSON(w, http.StatusOK, map[string]string{"conversationId": "c-1"})
	}
	s.proxy["demo-discussion/api/v1/discussions/demo/c-1/stream"] = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, discussionEvents)
	}
}

func TestConversationAsk_AgainstCluster(t *testing.T) {
	s := newFakeAPIServer(t)
	var asked string
	withGateway(s, &asked)
	out, _, err := s.kmctl(t, "conversation", "ask", "demo", "how are the pods?")
	if err != nil {
		t.Fatal(err)
	}
	if asked != "how are the pods?" {
		t.Errorf("gateway got message %q", asked)
	}
	assertContains(t, "ask", out, "conversation c-1", "* connected", "demo-workloads", "[gpu-0]", "ANSWER:\nAll pods are Running.", "* done")

	out, _, err = s.kmctl(t, "conversation", "ask", "demo", "again", "--quiet")
	if err != nil || out != "All pods are Running.\n" {
		t.Errorf("--quiet prints only the answer: got %q, %v", out, err)
	}
}

func TestConversationAsk_GatewayFailures(t *testing.T) {
	s := newFakeAPIServer(t)
	if _, _, err := s.kmctl(t, "conversation", "ask", "demo", "hi"); err == nil || !strings.Contains(err.Error(), `start discussion with crew "demo"`) {
		t.Errorf("no gateway: want a start-discussion error, got %v", err)
	}
	s.proxy["demo-discussion/api/v1/discussions/demo"] = func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{})
	}
	if _, _, err := s.kmctl(t, "conversation", "ask", "demo", "hi"); err == nil || !strings.Contains(err.Error(), "no conversationId") {
		t.Errorf("a reply without a conversation: want a no-conversationId error, got %v", err)
	}
}

func TestConversationWatch_AgainstCluster(t *testing.T) {
	s := newFakeAPIServer(t)
	var asked string
	withGateway(s, &asked)
	out, _, err := s.kmctl(t, "conversation", "watch", "demo", "c-1")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "watch", out, "* connected", "ANSWER:", "* done")
	if strings.Contains(out, "conversation c-1") {
		t.Errorf("watch does not start a turn, so it prints no conversation line:\n%s", out)
	}
	if _, _, err = s.kmctl(t, "conversation", "watch", "demo", "c-2"); err == nil {
		t.Error("watching an unknown conversation is an error")
	}
}

// A stream that stays open past --timeout ends with the timeout message, not a
// bare context error.
func TestConversationWatch_TimesOut(t *testing.T) {
	s := newFakeAPIServer(t)
	s.proxy["demo-discussion/api/v1/discussions/demo/c-1/stream"] = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"connected\"}\n\n")
		w.(http.Flusher).Flush()
		select { // hold the stream open until kmctl gives up; the bound keeps a regression from hanging the test
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}
	_, _, err := s.kmctl(t, "conversation", "watch", "demo", "c-1", "--timeout", "200ms")
	if err == nil || !strings.Contains(err.Error(), "timed out after 200ms waiting for the discussion to finish") {
		t.Errorf("want the timeout message, got %v", err)
	}
}
