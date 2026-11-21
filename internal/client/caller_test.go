package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	authenticationv1 "k8s.io/api/authentication/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func TestCaller_Validate(t *testing.T) {
	cases := map[string]struct {
		c    Caller
		want string
	}{
		"nothing":             {Caller{}, ""},
		"an account":          {Caller{ServiceAccount: "ops/crew-operator"}, ""},
		"a reason of 200":     {Caller{Reason: strings.Repeat("x", 200)}, ""},
		"a reason of 201":     {Caller{Reason: strings.Repeat("x", 201)}, "at most 200"},
		"spaces do not count": {Caller{Reason: "  " + strings.Repeat("x", 200) + "  "}, ""},
		"no namespace":        {Caller{ServiceAccount: "/name"}, "NAMESPACE/NAME"},
		"no name":             {Caller{ServiceAccount: "ops/"}, "NAMESPACE/NAME"},
		"no slash":            {Caller{ServiceAccount: "ops"}, "NAMESPACE/NAME"},
		"a third part":        {Caller{ServiceAccount: "a/b/c"}, "NAMESPACE/NAME"},
		"a multibyte reason":  {Caller{Reason: strings.Repeat("\u00e9", 100)}, ""},
	}
	for name, tc := range cases {
		err := tc.c.Validate()
		switch {
		case tc.want == "" && err != nil, tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: Validate = %v, want %q", name, err, tc.want)
		}
	}
}

func TestHasToken(t *testing.T) {
	cases := map[string]struct {
		cfg  rest.Config
		want bool
	}{
		"a bearer token":       {rest.Config{BearerToken: "t"}, true},
		"a token file":         {rest.Config{BearerTokenFile: "/f"}, true},
		"an exec plugin":       {rest.Config{ExecProvider: &clientcmdapi.ExecConfig{}}, true},
		"a client certificate": {rest.Config{TLSClientConfig: rest.TLSClientConfig{CertData: []byte("c")}}, false},
		"basic auth":           {rest.Config{Username: "u", Password: "p"}, false},
		"nothing":              {rest.Config{}, false},
	}
	for name, tc := range cases {
		if got := HasToken(&tc.cfg); got != tc.want {
			t.Errorf("%s: HasToken = %v, want %v", name, got, tc.want)
		}
	}
}

func TestMintToken_AsksForTenMinutesAndTheAudiences(t *testing.T) {
	cs := fake.NewSimpleClientset()
	var asked *authenticationv1.TokenRequest
	cs.PrependReactor("create", "serviceaccounts", func(a k8stesting.Action) (bool, runtime.Object, error) {
		asked = a.(k8stesting.CreateAction).GetObject().(*authenticationv1.TokenRequest)
		return true, &authenticationv1.TokenRequest{Status: authenticationv1.TokenRequestStatus{Token: "tok"}}, nil
	})
	got, err := MintToken(context.Background(), cs, "ops", "crew-operator", []string{"aud"})
	if err != nil || got != "tok" {
		t.Fatalf("MintToken = %q, %v", got, err)
	}
	if asked.Spec.ExpirationSeconds == nil || *asked.Spec.ExpirationSeconds != 600 ||
		len(asked.Spec.Audiences) != 1 || asked.Spec.Audiences[0] != "aud" {
		t.Errorf("request = %+v", asked.Spec)
	}
}

// echo answers with the Authorization and reason headers it received.
func echo() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, r.Header.Get("Authorization")+"|"+r.Header.Get(HeaderAccessReason))
	}))
}

// tlsFactory is a Factory whose kubeconfig signs in with a bearer token over TLS (client-go
// ignores a token for a plain-HTTP server), reaching the gateway at gw.
func tlsFactory(t *testing.T, apiServer, token, gw string) *Factory {
	t.Helper()
	f := NewFactory()
	insecure := true
	f.ConfigFlags.APIServer = &apiServer
	f.ConfigFlags.Insecure = &insecure
	empty := t.TempDir() + "/kubeconfig"
	if err := writeFile(empty, "apiVersion: v1\nkind: Config\n"); err != nil {
		t.Fatal(err)
	}
	f.ConfigFlags.KubeConfig = &empty
	if token != "" {
		f.ConfigFlags.BearerToken = &token
	}
	f.OpenService = func(context.Context, string, string, string) (string, func(), error) { return gw, func() {}, nil }
	return f
}

func callGateway(t *testing.T, f *Factory) string {
	t.Helper()
	rc, stop, err := f.GatewayClient(context.Background(), "ns", "svc", "80")
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	raw, err := rc.Get().Suffix("x").DoRaw(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestGatewayClient_SendsTheCallersCredentials(t *testing.T) {
	gw := echo()
	defer gw.Close()
	api := httptest.NewTLSServer(http.NotFoundHandler())
	defer api.Close()

	f := tlsFactory(t, api.URL, "kube-token", gw.URL)
	if got := callGateway(t, f); got != "Bearer kube-token|" {
		t.Errorf("the kubeconfig token: got %q", got)
	}
	f.Caller.Reason = " ticket 7 "
	if got := callGateway(t, f); got != "Bearer kube-token|ticket 7" {
		t.Errorf("with a reason: got %q", got)
	}
	if !f.SendsToken() {
		t.Error("SendsToken = false with a kubeconfig token")
	}

	none := tlsFactory(t, api.URL, "", gw.URL)
	if got := callGateway(t, none); got != "|" {
		t.Errorf("no token in the kubeconfig: got %q, want none sent", got)
	}
	if none.SendsToken() {
		t.Error("SendsToken = true without a token")
	}
}

func TestGatewayClient_MintsAndSendsTheMintedToken(t *testing.T) {
	gw := echo()
	defer gw.Close()
	var requested string
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"kind":"TokenRequest","apiVersion":"authentication.k8s.io/v1","status":{"token":"minted"}}`)
	}))
	defer api.Close()
	f := tlsFactory(t, api.URL, "kube-token", gw.URL)
	f.Caller.ServiceAccount = "ops/crew-operator"
	if got := callGateway(t, f); got != "Bearer minted|" {
		t.Errorf("got %q, want the minted token to win over the kubeconfig's", got)
	}
	if requested != "/api/v1/namespaces/ops/serviceaccounts/crew-operator/token" {
		t.Errorf("minted through %q", requested)
	}
	if !f.SendsToken() {
		t.Error("SendsToken = false with a service account")
	}
}

func TestGatewayClient_Failures(t *testing.T) {
	gw := echo()
	defer gw.Close()
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Forbidden","code":403}`)
	}))
	defer api.Close()

	stopped := false
	f := tlsFactory(t, api.URL, "kube-token", gw.URL)
	f.OpenService = func(context.Context, string, string, string) (string, func(), error) {
		return gw.URL, func() { stopped = true }, nil
	}
	f.Caller.ServiceAccount = "ops/crew-operator"
	_, _, err := f.GatewayClient(context.Background(), "ns", "svc", "80")
	var ce *CallerError
	if !asCallerError(err, &ce) || !stopped {
		t.Errorf("a refused TokenRequest: err = %v, stopped = %v; want a CallerError and a closed forward", err, stopped)
	}

	f.Caller.ServiceAccount = "nope"
	opened := false
	f.OpenService = func(context.Context, string, string, string) (string, func(), error) {
		opened = true
		return gw.URL, func() {}, nil
	}
	if _, _, err = f.GatewayClient(context.Background(), "ns", "svc", "80"); !asCallerError(err, &ce) || opened {
		t.Errorf("a malformed account: err = %v, opened = %v; want a CallerError before any forward", err, opened)
	}
}

func asCallerError(err error, target **CallerError) bool { return errors.As(err, target) }

func writeFile(path, content string) error { return os.WriteFile(path, []byte(content), 0o600) }
