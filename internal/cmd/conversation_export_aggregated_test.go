package cmd

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kubemoot/kmctl/internal/clierr"
)

const aggregatedExportPath = reportsAPIPath + "/namespaces/" + contextNS + "/crews/demo/conversation-export"

// serveAggregatedExport answers the aggregated report API and records what it was asked.
func serveAggregatedExport(s *fakeAPIServer, body []byte) *exportSeen {
	seen := &exportSeen{}
	s.reports = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != aggregatedExportPath {
			http.NotFound(w, r)
			return
		}
		seen.query, seen.headers = r.URL.Query(), r.Header.Clone()
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(body)
	}
	return seen
}

func TestConversationExport_UsesTheAggregatedAPIWithTheKubeconfigCredentials(t *testing.T) {
	s := newFakeAPIServer(t)
	s.openErr = errors.New("the report Service must not be forwarded to")
	seen := serveAggregatedExport(s, zipWith(t, `{"kind":"kubemoot.conversation-export"}`))
	out := filepath.Join(t.TempDir(), "one.zip")
	stdout, _, err := s.kmctl(t, "conversation", "export", "demo", exportID1, "--output-file", out, "--reason", "audit of a complaint")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "export", stdout, "wrote 1 conversation(s) of "+contextNS+"/demo to "+out)
	if got := seen.query["id"]; len(got) != 1 || got[0] != exportID1 {
		t.Errorf("ids = %v", got)
	}
	if got := seen.headers.Get("Authorization"); got != "Bearer fake-token" {
		t.Errorf("Authorization = %q, want the kubeconfig credential, which the API server authenticates", got)
	}
	if got := seen.headers.Get("Kubemoot-Access-Reason"); got != "audit of a complaint" {
		t.Errorf("access reason = %q", got)
	}
}

func TestConversationExport_AggregatedReport(t *testing.T) {
	s := newFakeAPIServer(t)
	s.openErr = errors.New("the report Service must not be forwarded to")
	s.reports = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != aggregatedExportPath || r.URL.Query().Get("report") != "true" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"kind":"kubemoot.conversation-export","conversationId":"` + exportID1 + `","turns":2,"parts":[],"leftOut":[]}`))
	}
	stdout, _, err := s.kmctl(t, "conversation", "export", "demo", exportID1, "--report")
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, "report", stdout, "2 turn(s)")
}

func TestConversationExport_FallsBackToTheServiceWhenTheAPIIsNotInstalled(t *testing.T) {
	s := newFakeAPIServer(t) // no aggregated API: its discovery answers 404
	seen := serveExport(s, zipWith(t, `{"kind":"kubemoot.conversation-export"}`))
	out := filepath.Join(t.TempDir(), "one.zip")
	if _, _, err := s.kmctl(t, "conversation", "export", "demo", exportID1, "--output-file", out); err != nil {
		t.Fatal(err)
	}
	if got := seen.query["id"]; len(got) != 1 {
		t.Errorf("the report Service was not asked: %v", got)
	}
}

func TestConversationExport_NamingTheServiceOrAServiceAccountSkipsTheAggregatedAPI(t *testing.T) {
	for name, flags := range map[string][]string{
		"the service named":   {"--operator-service", "kubemoot-operator-report"},
		"the namespace named": {"--operator-namespace", "kubemoot"},
		"a service account":   {"--token-service-account", "ops/exporter"},
	} {
		s := newFakeAPIServer(t)
		s.reports = func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }
		seen := serveExport(s, zipWith(t, `{"kind":"kubemoot.conversation-export"}`))
		args := append([]string{"conversation", "export", "demo", exportID1, "--output-file", filepath.Join(t.TempDir(), "o.zip")}, flags...)
		if _, _, err := s.kmctl(t, args...); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if s.reportsProbes != 0 {
			t.Errorf("%s: the aggregated API was asked about %d time(s), want none", name, s.reportsProbes)
		}
		if len(seen.query["id"]) != 1 {
			t.Errorf("%s: the report Service was not used", name)
		}
	}
}

func TestConversationExport_AnAPIServerThatRefusesDiscoveryIsAnErrorNotAFallback(t *testing.T) {
	s := newFakeAPIServer(t)
	serveExport(s, zipWith(t, `{"kind":"kubemoot.conversation-export"}`))
	s.refuse = func(r *http.Request) (int, string) {
		if strings.HasPrefix(r.URL.Path, reportsAPIPath) {
			return http.StatusUnauthorized, "token expired"
		}
		return 0, ""
	}
	_, _, err := s.kmctl(t, "conversation", "export", "demo", exportID1, "--output-file", filepath.Join(t.TempDir(), "o.zip"))
	ce, ok := clierr.As(err)
	if !ok || !strings.Contains(ce.Message, "report API") {
		t.Fatalf("err = %v, want an explained error naming the report API", err)
	}
}

func TestConversationExport_AggregatedUnauthorizedNamesTheCredentials(t *testing.T) {
	s := newFakeAPIServer(t)
	s.reports = func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "no", http.StatusUnauthorized) }
	_, _, err := s.kmctl(t, "conversation", "export", "demo", exportID1, "--output-file", filepath.Join(t.TempDir(), "o.zip"))
	ce, ok := clierr.As(err)
	if !ok || !strings.Contains(ce.Message, "API server did not accept your credentials") {
		t.Fatalf("err = %v", err)
	}
}

func TestConversationExport_AnOverlongReasonIsRefusedOnTheAggregatedPathToo(t *testing.T) {
	s := newFakeAPIServer(t)
	s.reports = func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }
	_, _, err := s.kmctl(t, "conversation", "export", "demo", exportID1, "--output-file", filepath.Join(t.TempDir(), "o.zip"), "--reason", strings.Repeat("x", 201))
	if err == nil || !strings.Contains(err.Error(), "--reason") {
		t.Errorf("err = %v, want the reason refused", err)
	}
}

func TestConversationExport_AggregatedRefusalNamesTheDoorRole(t *testing.T) {
	s := newFakeAPIServer(t)
	s.reports = func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "forbidden by the API server", http.StatusForbidden)
	}
	_, stderr, err := s.kmctl(t, "conversation", "export", "demo", exportID1, "--output-file", filepath.Join(t.TempDir(), "o.zip"))
	if err == nil {
		t.Fatal("want a refusal")
	}
	ce, ok := clierr.As(err)
	if !ok {
		t.Fatalf("not an explained error: %v (%s)", err, stderr)
	}
	if text := ce.Hint; !strings.Contains(text, "reports.kubemoot.ai") {
		t.Errorf("the refusal does not name the report API group: %q", text)
	}
}
