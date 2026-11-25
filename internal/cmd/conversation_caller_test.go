package cmd

import (
	"net/http"
	"os"
	"strings"
	"testing"
)

const (
	convID     = "11111111-1111-4111-8111-111111111111"
	gwBase     = "demo-discussion/api/v1/discussions/demo"
	askPath    = gwBase
	streamPath = gwBase + "/" + convID + "/stream"
)

// withoutToken rewrites the kubeconfig as a client-certificate one: no bearer token.
func (s *fakeAPIServer) withoutToken(t *testing.T) {
	t.Helper()
	raw, err := os.ReadFile(s.kubeconfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.kubeconfig, []byte(strings.Replace(string(raw), "    token: fake-token\n", "    username: nobody\n    password: secret\n", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// headersSeen records the headers of every request the handlers at paths answer.
func headersSeen(s *fakeAPIServer, paths ...string) *[]http.Header {
	var seen []http.Header
	for _, p := range paths {
		next := s.proxy[p]
		s.proxy[p] = func(w http.ResponseWriter, r *http.Request) {
			seen = append(seen, r.Header.Clone())
			next(w, r)
		}
	}
	return &seen
}

func gatewayWithHeaders(t *testing.T) (*fakeAPIServer, *[]http.Header) {
	t.Helper()
	s := newFakeAPIServer(t)
	var asked string
	withGateway(s, &asked)
	return s, headersSeen(s, askPath, streamPath)
}

func wantAuthorization(t *testing.T, seen []http.Header, want string) {
	t.Helper()
	if len(seen) != 2 {
		t.Fatalf("the gateway saw %d requests, want the turn and its stream", len(seen))
	}
	for _, h := range seen {
		if got := h.Get("Authorization"); got != want {
			t.Errorf("Authorization = %q, want %q", got, want)
		}
	}
}

func TestConversationAsk_SendsTheKubeconfigToken(t *testing.T) {
	s, seen := gatewayWithHeaders(t)
	out, errOut, err := s.kmctl(t, "conversation", "ask", "demo", "hi")
	if err != nil {
		t.Fatal(err)
	}
	wantAuthorization(t, *seen, "Bearer fake-token")
	if strings.Contains(out+errOut, "fake-token") {
		t.Error("the token was printed")
	}
	for _, h := range *seen {
		if h.Get("Kubemoot-Access-Reason") != "" {
			t.Error("a reason was sent that nobody gave")
		}
	}
}

func TestConversationAsk_NoTokenInTheKubeconfigSendsNone(t *testing.T) {
	s, seen := gatewayWithHeaders(t)
	s.withoutToken(t)
	if _, _, err := s.kmctl(t, "conversation", "ask", "demo", "hi"); err != nil {
		t.Fatal(err)
	}
	// No bearer token, and the kubeconfig's basic-auth password is never sent to a gateway.
	wantAuthorization(t, *seen, "")
	if len(s.tokenRequests) != 0 {
		t.Errorf("a token was minted without --token-service-account: %v", s.tokenRequests)
	}
}

func TestConversationAsk_CertificateKubeconfigMintsAToken(t *testing.T) {
	s, seen := gatewayWithHeaders(t)
	s.withoutToken(t)
	out, errOut, err := s.kmctl(t, "conversation", "ask", "demo", "hi",
		"--token-service-account", "ops/crew-operator", "--token-audience", "demo-gateway")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.tokenRequests) != 1 || !strings.HasPrefix(s.tokenRequests[0], "ops/crew-operator ") ||
		!strings.Contains(s.tokenRequests[0], "demo-gateway") {
		t.Fatalf("token requests = %q, want one for ops/crew-operator naming the audience", s.tokenRequests)
	}
	wantAuthorization(t, *seen, "Bearer minted-crew-operator")
	if strings.Contains(out+errOut, "minted-crew-operator") {
		t.Error("the minted token was printed")
	}
}

func TestConversationAsk_MintingErrors(t *testing.T) {
	s, _ := gatewayWithHeaders(t)
	s.withoutToken(t)
	s.denyTokens = true
	stderr, code := s.run(t, "conversation", "ask", "demo", "hi", "--token-service-account", "ops/crew-operator")
	if code != 1 || !strings.Contains(stderr, "not allowed to mint a token for service account ops/crew-operator") ||
		!strings.Contains(stderr, "create on serviceaccounts/token") {
		t.Errorf("a forbidden TokenRequest (exit %d): want the RBAC named, got:\n%s", code, stderr)
	}
	_, _, err := s.kmctl(t, "conversation", "ask", "demo", "hi", "--token-service-account", "no-slash")
	if err == nil || !strings.Contains(err.Error(), "NAMESPACE/NAME") {
		t.Errorf("a malformed account: want a format error, got %v", err)
	}
	if len(s.tokenRequests) != 1 {
		t.Errorf("a malformed account must fail before any request: %q", s.tokenRequests)
	}
}

func TestConversationAsk_GatewayRefusals(t *testing.T) {
	cases := []struct {
		name   string
		cert   bool
		args   []string
		code   int
		body   string
		wanted []string
		absent []string
	}{
		{"401 with a token", false, nil, 401, "no", []string{"did not accept your Kubernetes token"}, []string{"has none"}},
		{"401 with a certificate", true, nil, 401, "no", []string{"requires a Kubernetes token (HTTP 401)", "has none", "--token-service-account NAMESPACE/NAME", "OIDC login", "--token-audience"}, []string{"did not accept your credentials"}},
		{"401 with a minted token", true, []string{"--token-service-account", "ops/op"}, 401, "no", []string{"minted for service account ops/op", "--token-audience"}, nil},
		{"403", false, nil, 403, "no", []string{"refused your identity", "kubectl auth can-i get crews.kubemoot.ai/demo -n team-a"}, nil},
		{"404 for a conversation", false, nil, 404, "conversation not found", []string{"conversation not found on crew", "belongs to another caller", "--reason", "kubemoot-conversation-reader"}, []string{"was not found in namespace"}},
		{"400 wanting a reason", false, nil, 400, "reading another caller's conversations needs a reason in the Kubemoot-Access-Reason header", []string{"wants a reason", "--reason"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := gatewayWithHeaders(t)
			s.add("crews", contextNS, "demo", nil)
			if tc.cert {
				s.withoutToken(t)
			}
			s.proxy[askPath] = func(w http.ResponseWriter, _ *http.Request) { http.Error(w, tc.body, tc.code) }
			stderr, code := s.run(t, append([]string{"conversation", "ask", "demo", "hi"}, tc.args...)...)
			wantInStderr(t, stderr, code, tc.wanted, tc.absent)
		})
	}
}

// wantInStderr checks a failed command's stderr: it exited 1, says every wanted text, says
// none of the absent ones, and carries no token.
func wantInStderr(t *testing.T, stderr string, code int, wanted, absent []string) {
	t.Helper()
	if code != 1 {
		t.Errorf("exit code %d, want 1", code)
	}
	for _, w := range wanted {
		if !strings.Contains(stderr, w) {
			t.Errorf("stderr %q lacks %q", stderr, w)
		}
	}
	for _, a := range absent {
		if strings.Contains(stderr, a) {
			t.Errorf("stderr %q should not say %q", stderr, a)
		}
	}
	if strings.Contains(stderr, "fake-token") || strings.Contains(stderr, "minted-") {
		t.Error("an error carries a token")
	}
}

func TestConversationWatch_SendsTheReasonOnlyWhenGiven(t *testing.T) {
	s, seen := gatewayWithHeaders(t)
	if _, _, err := s.kmctl(t, "conversation", "watch", "demo", convID); err != nil {
		t.Fatal(err)
	}
	if got := (*seen)[0].Get("Kubemoot-Access-Reason"); got != "" {
		t.Errorf("reason = %q, want none", got)
	}
	if _, _, err := s.kmctl(t, "conversation", "watch", "demo", convID, "--reason", " ticket 42: wrong answer "); err != nil {
		t.Fatal(err)
	}
	if got := (*seen)[1].Get("Kubemoot-Access-Reason"); got != "ticket 42: wrong answer" {
		t.Errorf("reason = %q", got)
	}
	_, _, err := s.kmctl(t, "conversation", "watch", "demo", convID, "--reason", strings.Repeat("x", 201))
	if err == nil || !strings.Contains(err.Error(), "at most 200") {
		t.Errorf("a reason over 200 characters: want a length error, got %v", err)
	}
}

const conversationList = `{"conversations":[
 {"conversationId":"` + convID + `","question":"how are the pods?","startedAt":"2026-01-01T00:00:00Z","lastTurnAt":"2026-01-01T00:01:00Z","turns":2,"owner":"abc123"},
 {"conversationId":"22222222-2222-4222-8222-222222222222","startedAt":"2026-01-01T00:00:00Z","lastTurnAt":"2026-01-01T00:02:00Z","turns":1}]}`

func serveList(s *fakeAPIServer) *http.Request {
	got := new(http.Request)
	s.proxy[gwBase] = func(w http.ResponseWriter, r *http.Request) {
		*got = *r.Clone(r.Context())
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(conversationList))
	}
	return got
}

func TestConversationList_YoursByDefault(t *testing.T) {
	s := newFakeAPIServer(t)
	req := serveList(s)
	out, _, err := s.kmctl(t, "conversation", "list", "demo")
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.Query().Get("all") != "" || req.Header.Get("Kubemoot-Access-Reason") != "" {
		t.Errorf("a plain list sent query %q reason %q", req.URL.RawQuery, req.Header.Get("Kubemoot-Access-Reason"))
	}
	if req.Header.Get("Authorization") != "Bearer fake-token" {
		t.Error("the list did not carry the token")
	}
	assertContains(t, "list", out, "CONVERSATION ID", "how are the pods?")
	if strings.Contains(out, "OWNER") {
		t.Errorf("a caller's own conversations show no owner column:\n%s", out)
	}
}

func TestConversationList_AllNeedsAReasonAndShowsOwners(t *testing.T) {
	s := newFakeAPIServer(t)
	req := serveList(s)
	if _, _, err := s.kmctl(t, "conversation", "list", "demo", "--all"); err == nil || !strings.Contains(err.Error(), "--reason") {
		t.Errorf("--all without --reason: want an error naming --reason, got %v", err)
	}
	out, _, err := s.kmctl(t, "conversation", "list", "demo", "--all", "--reason", "weekly review")
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.Query().Get("all") != "true" {
		t.Errorf("query = %q, want all=true", req.URL.RawQuery)
	}
	if got := req.Header.Get("Kubemoot-Access-Reason"); got != "weekly review" {
		t.Errorf("reason = %q", got)
	}
	assertContains(t, "list --all", out, "OWNER", "abc123")
}

func TestConversationList_AllRefusedIsNotAnOlderGateway(t *testing.T) {
	s := newFakeAPIServer(t)
	s.add("crews", contextNS, "demo", nil)
	s.proxy[gwBase] = func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "conversation not found", http.StatusNotFound)
	}
	stderr, code := s.run(t, "conversation", "list", "demo", "--all", "--reason", "review")
	if code != 1 || !strings.Contains(stderr, "conversation not found on crew") || strings.Contains(stderr, "does not list") {
		t.Errorf("a refused --all (exit %d): want the refusal, not the older-gateway message:\n%s", code, stderr)
	}
}
