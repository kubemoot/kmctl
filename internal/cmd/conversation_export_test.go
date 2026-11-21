package cmd

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	exportKey = "kubemoot-operator-report/conversation-export/" + contextNS + "/demo"
	exportID1 = "11111111-1111-4111-8111-111111111111"
	exportID2 = "22222222-2222-4222-8222-222222222222"
	exportID3 = "33333333-3333-4333-8333-333333333333"
)

// zipWith builds a zip holding one file.
func zipWith(t *testing.T, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte(content))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type exportSeen struct {
	query   map[string][]string
	headers http.Header
}

// serveExport answers the report Service with body and records what it was asked.
func serveExport(s *fakeAPIServer, body []byte) *exportSeen {
	seen := &exportSeen{}
	s.proxy[exportKey] = func(w http.ResponseWriter, r *http.Request) {
		seen.query, seen.headers = r.URL.Query(), r.Header.Clone()
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(body)
	}
	return seen
}

func refuseExport(s *fakeAPIServer, code int, text string) {
	s.proxy[exportKey] = func(w http.ResponseWriter, _ *http.Request) { http.Error(w, text, code) }
}

func TestConversationExport_OneConversation(t *testing.T) {
	s := newFakeAPIServer(t)
	manifest := `{"kind":"kubemoot.conversation-export","leftOut":[{"key":"k.a","reason":"holds a credential"}]}`
	seen := serveExport(s, zipWith(t, manifest))
	dir := t.TempDir()
	out := filepath.Join(dir, "one.zip")
	stdout, _, err := s.kmctl(t, "conversation", "export", "demo", exportID1, "-o", out, "--time-zone", "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("zip not written: %v", err)
	}
	assertContains(t, "export", stdout, `left out "k.a": holds a credential`, "wrote 1 conversation(s) of "+contextNS+"/demo to "+out)
	if got := seen.query["id"]; len(got) != 1 || got[0] != exportID1 {
		t.Errorf("ids = %v", got)
	}
	if got := seen.query["timeZone"]; len(got) != 1 || got[0] != "America/New_York" {
		t.Errorf("timeZone = %v", got)
	}
	if got := seen.headers.Get("Authorization"); got != "Bearer fake-token" {
		t.Errorf("Authorization = %q, want the kubeconfig token", got)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".kmctl-export-*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func TestConversationExport_DefaultNames(t *testing.T) {
	s := newFakeAPIServer(t)
	serveExport(s, zipWith(t, `{"kind":"kubemoot.conversation-export-bundle"}`))
	t.Chdir(t.TempDir())
	if _, _, err := s.kmctl(t, "conversation", "export", "demo", exportID1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("demo-conversation-" + exportID1 + ".zip"); err != nil {
		t.Error(err)
	}
	if _, _, err := s.kmctl(t, "conversation", "export", "demo", exportID1, exportID2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("demo-conversations.zip"); err != nil {
		t.Error(err)
	}
}

func TestConversationExport_BundleListsRefusedAndExpired(t *testing.T) {
	s := newFakeAPIServer(t)
	m, _ := json.Marshal(map[string]any{
		"kind":     "kubemoot.conversation-export-bundle",
		"included": []map[string]string{{"conversationId": exportID1, "file": "a.zip"}},
		"refused":  []map[string]string{{"conversationId": exportID2, "reason": "not yours"}},
		"expired":  []map[string]string{{"conversationId": exportID3, "reason": "no longer held"}},
	})
	serveExport(s, zipWith(t, string(m)))
	out := filepath.Join(t.TempDir(), "b.zip")
	stdout, _, err := s.kmctl(t, "conversation", "export", "demo", exportID1, exportID2, exportID3, "-o", out)
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "bundle", stdout, "refused "+exportID2+": not yours", "expired "+exportID3+": no longer held", "wrote 3 conversation(s)")
	if strings.Contains(stdout, exportID1+":") {
		t.Errorf("an included conversation was listed as left out:\n%s", stdout)
	}
}

func TestConversationExport_Report(t *testing.T) {
	s := newFakeAPIServer(t)
	m, _ := json.Marshal(map[string]any{
		"kind":     "kubemoot.conversation-export-bundle",
		"included": []map[string]string{{"conversationId": exportID1}},
		"refused":  []map[string]string{{"conversationId": exportID2, "reason": "not yours"}},
	})
	seen := serveExport(s, m)
	out, _, err := s.kmctl(t, "conversation", "export", "demo", exportID1, exportID2, "--report")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "report", out, "included "+exportID1, "refused "+exportID2+": not yours")
	if seen.query["report"][0] != "true" {
		t.Errorf("report param = %v", seen.query["report"])
	}
	js, _, err := s.kmctl(t, "conversation", "export", "demo", exportID1, "--report", "--json")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "json report", js, `"kind": "kubemoot.conversation-export-bundle"`)
	if _, err := os.Stat("demo-conversations.zip"); err == nil {
		t.Error("--report wrote a zip")
	}
}

func TestConversationExport_SingleReport(t *testing.T) {
	s := newFakeAPIServer(t)
	serveExport(s, []byte(`{"kind":"kubemoot.conversation-export","conversationId":"`+exportID1+`","turns":3,`+
		`"parts":[{"path":"memory","status":"not-kept","count":0,"reason":"this installation keeps none"}],`+
		`"leftOut":[{"key":"k","reason":"credential"}]}`))
	out, _, err := s.kmctl(t, "conversation", "export", "demo", exportID1, "--report")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "single report", out, "3 turn(s)", "memory", "not-kept", "this installation keeps none", `left out "k": credential`)
}

func TestConversationExport_Errors(t *testing.T) {
	cases := map[string]struct {
		code int
		text string
		want []string
	}{
		"401": {http.StatusUnauthorized, "unauthorized", []string{"did not accept your Kubernetes token"}},
		"403": {http.StatusForbidden, "export refused", []string{"refused the export", "export refused", "kubectl auth can-i get crews.kubemoot.ai/demo"}},
		"404": {http.StatusNotFound, "conversation not held", []string{"holds no such conversation", "conversation not held", "kmctl conversation list demo"}},
		"400": {http.StatusBadRequest, "ask for at most 50 conversations at once", []string{"ask for at most 50"}},
		"502": {http.StatusBadGateway, "the conversation stores are not reachable", []string{"could not export", "stores are not reachable"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			s := newFakeAPIServer(t)
			refuseExport(s, c.code, c.text)
			out := filepath.Join(t.TempDir(), "x.zip")
			_, _, err := s.kmctl(t, "conversation", "export", "demo", exportID1, "-o", out)
			if err == nil {
				t.Fatal("want an error")
			}
			assertContains(t, name, errorText(err), c.want...)
			if _, statErr := os.Stat(out); statErr == nil {
				t.Error("a failed export left a file")
			}
		})
	}
}

func TestConversationExport_RefusesToOverwriteAndBadFlags(t *testing.T) {
	s := newFakeAPIServer(t)
	serveExport(s, zipWith(t, `{}`))
	out := filepath.Join(t.TempDir(), "x.zip")
	if err := os.WriteFile(out, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.kmctl(t, "conversation", "export", "demo", exportID1, "-o", out)
	if err == nil || !strings.Contains(errorText(err), "already exists") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(out); string(b) != "keep" {
		t.Error("the existing file was overwritten")
	}
	if _, _, err := s.kmctl(t, "conversation", "export", "demo", exportID1, "--json"); err == nil {
		t.Error("--json without --report should fail")
	}
	if _, _, err := s.kmctl(t, "conversation", "export", "demo"); err == nil {
		t.Error("an export with no ID should fail")
	}
}

func TestConversationExport_ReportServiceUnreachable(t *testing.T) {
	s := newFakeAPIServer(t)
	s.openErr = http.ErrServerClosed
	_, _, err := s.kmctl(t, "conversation", "export", "demo", exportID1)
	if err == nil || !strings.Contains(errorText(err), "could not reach the operator's report service kubemoot/kubemoot-operator-report") {
		t.Fatalf("err = %v", err)
	}
}

func TestConversationExport_ResolvesPrefixesThroughTheGateway(t *testing.T) {
	s := newFakeAPIServer(t)
	s.proxy["demo-discussion/api/v1/discussions/demo"] = func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"conversations": []map[string]any{
			{"conversationId": exportID1, "turns": 1}, {"conversationId": exportID2, "turns": 1},
		}})
	}
	seen := serveExport(s, zipWith(t, `{"kind":"kubemoot.conversation-export"}`))
	out := filepath.Join(t.TempDir(), "p.zip")
	if _, _, err := s.kmctl(t, "conversation", "export", "demo", "1111", exportID2, "-o", out); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(seen.query["id"], ","); got != exportID1+","+exportID2 {
		t.Errorf("ids sent = %s", got)
	}
}
