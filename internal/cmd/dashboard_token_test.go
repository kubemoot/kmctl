package cmd

import (
	"strings"
	"testing"
)

func TestDashboardToken_PrintsTheKubeconfigToken(t *testing.T) {
	s := newFakeAPIServer(t)
	out, _, err := s.kmctl(t, "dashboard", "token")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "fake-token" {
		t.Errorf("printed %q, want the kubeconfig token", out)
	}
	if len(s.tokenRequests) != 0 {
		t.Errorf("a token was minted without --token-service-account: %v", s.tokenRequests)
	}
}

// The fake API server records the TokenRequest as protobuf; \x80\xe1\x01 is expirationSeconds 28800.
func TestDashboardToken_MintsForAServiceAccountForEightHours(t *testing.T) {
	s := newFakeAPIServer(t)
	s.withoutToken(t)
	out, _, err := s.kmctl(t, "dashboard", "token", "--token-service-account", "kubemoot/dash-user", "--token-audience", "api")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "minted-dash-user" {
		t.Errorf("printed %q, want the minted token", out)
	}
	if len(s.tokenRequests) != 1 || !strings.HasPrefix(s.tokenRequests[0], "kubemoot/dash-user ") ||
		!strings.Contains(s.tokenRequests[0], "\x80\xe1\x01") || !strings.Contains(s.tokenRequests[0], "api") {
		t.Errorf("token requests = %q, want one for kubemoot/dash-user lasting 28800 seconds", s.tokenRequests)
	}
}

func TestDashboardToken_Refuses(t *testing.T) {
	s := newFakeAPIServer(t)
	s.withoutToken(t)
	stderr, code := s.run(t, "dashboard", "token")
	wantInStderr(t, stderr, code, []string{"holds no bearer token", "--token-service-account NAMESPACE/NAME"}, nil)

	if _, _, err := s.kmctl(t, "dashboard", "token", "--token-service-account", "no-slash"); err == nil || !strings.Contains(err.Error(), "NAMESPACE/NAME") {
		t.Errorf("a malformed account: want a format error, got %v", err)
	}
	if _, _, err := s.kmctl(t, "dashboard", "token", "--token-service-account", "ns/sa", "--duration", "1m"); err == nil || !strings.Contains(err.Error(), "nothing shorter") {
		t.Errorf("a too short duration: want an error, got %v", err)
	}
	s.denyTokens = true
	stderr, code = s.run(t, "dashboard", "token", "--token-service-account", "ns/sa")
	wantInStderr(t, stderr, code, []string{"not allowed to mint a token for service account ns/sa", "create on serviceaccounts/token"}, nil)
}
