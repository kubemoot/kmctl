package client

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// HeaderAccessReason is the header an operator states why they read another
// caller's conversations with; the crew's gateway audits it.
const HeaderAccessReason = "Kubemoot-Access-Reason"

// MaxReasonLength is the longest reason the gateway accepts.
const MaxReasonLength = 200

// MintedTokenTTL is how long a token minted for a client-certificate kubeconfig
// lives; the API server accepts nothing shorter than ten minutes.
const MintedTokenTTL = 10 * time.Minute

// Caller is who kmctl is at a crew's gateway.
type Caller struct {
	// ServiceAccount is "NAMESPACE/NAME": a ServiceAccount to mint a short-lived
	// token for, when the kubeconfig has no bearer token of its own (it signs in
	// with a client certificate). It wins over the kubeconfig's token.
	ServiceAccount string
	// Audiences bind a minted token to the audiences a crew declares in
	// spec.discussion.access.tokenAudiences.
	Audiences []string
	// Reason is sent as Kubemoot-Access-Reason: why an operator reads another
	// caller's conversations.
	Reason string
}

// CallerError is a failure to be the caller the flags ask for: a malformed flag, or a
// token that could not be minted. It is not a failure to reach the gateway.
type CallerError struct{ Err error }

func (e *CallerError) Error() string { return e.Err.Error() }
func (e *CallerError) Unwrap() error { return e.Err }

// Validate checks the caller's settings before anything is opened.
func (c Caller) Validate() error {
	if c.ServiceAccount != "" {
		if _, _, err := c.account(); err != nil {
			return err
		}
	}
	if n := len([]rune(strings.TrimSpace(c.Reason))); n > MaxReasonLength {
		return fmt.Errorf("--reason is %d characters; the gateway accepts at most %d", n, MaxReasonLength)
	}
	return nil
}

func (c Caller) account() (namespace, name string, err error) {
	namespace, name, ok := strings.Cut(c.ServiceAccount, "/")
	if !ok || namespace == "" || name == "" || strings.Contains(name, "/") {
		return "", "", fmt.Errorf("--token-service-account %q: want NAMESPACE/NAME", c.ServiceAccount)
	}
	return namespace, name, nil
}

// HasToken reports whether the kubeconfig credentials yield a bearer token: a token,
// a token file, or a plugin that returns one. A client-certificate kubeconfig has none.
func HasToken(cfg *rest.Config) bool {
	return cfg.BearerToken != "" || cfg.BearerTokenFile != "" || cfg.ExecProvider != nil || cfg.AuthProvider != nil
}

// MintToken asks the API server for a short-lived token for a ServiceAccount
// (TokenRequest). The caller needs create on serviceaccounts/token; the API server's
// audit log records who asked.
func MintToken(ctx context.Context, cs kubernetes.Interface, namespace, name string, audiences []string) (string, error) {
	return MintTokenFor(ctx, cs, namespace, name, audiences, MintedTokenTTL)
}

// MintTokenFor is MintToken for a token that lives for lifetime; the API server accepts
// nothing shorter than ten minutes.
func MintTokenFor(ctx context.Context, cs kubernetes.Interface, namespace, name string, audiences []string, lifetime time.Duration) (string, error) {
	ttl := int64(lifetime / time.Second)
	tr, err := cs.CoreV1().ServiceAccounts(namespace).CreateToken(ctx, name, &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{Audiences: audiences, ExpirationSeconds: &ttl},
	}, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("mint a token for service account %s/%s: %w", namespace, name, err)
	}
	return tr.Status.Token, nil
}

// GatewayClient is ServiceClient for a crew's discussion gateway: it also sends the
// caller's Kubernetes token (and the access reason, when there is one), which the
// gateway checks when the crew sets spec.discussion.access and ignores otherwise. The
// token is the kubeconfig's, or one minted for Caller.ServiceAccount; it is set on the
// client's configuration and never returned, printed, or logged.
func (f *Factory) GatewayClient(ctx context.Context, namespace, service, port string) (rest.Interface, func(), error) {
	if err := f.Caller.Validate(); err != nil {
		return nil, nil, &CallerError{err}
	}
	return f.serviceClient(ctx, namespace, service, port, f.authenticateAsCaller)
}

// authenticateAsCaller sets the caller's credentials on the gateway client's config.
func (f *Factory) authenticateAsCaller(ctx context.Context, cfg *rest.Config) error {
	kube, err := f.RESTConfig()
	if err != nil {
		return err
	}
	if f.Caller.ServiceAccount != "" {
		ns, name, _ := f.Caller.account()
		cs, err := kubernetes.NewForConfig(kube)
		if err != nil {
			return err
		}
		if cfg.BearerToken, err = MintToken(ctx, cs, ns, name, f.Caller.Audiences); err != nil {
			return &CallerError{err}
		}
	} else {
		cfg.BearerToken, cfg.BearerTokenFile = kube.BearerToken, kube.BearerTokenFile
		cfg.ExecProvider, cfg.AuthProvider = kube.ExecProvider, kube.AuthProvider
	}
	f.addReason(cfg)
	return nil
}

// addReason sends the caller's access reason, when there is one, on every request of cfg.
func (f *Factory) addReason(cfg *rest.Config) {
	if reason := strings.TrimSpace(f.Caller.Reason); reason != "" {
		cfg.Wrap(func(rt http.RoundTripper) http.RoundTripper { return headerAdder{rt, reason} })
	}
}

// headerAdder sets the access reason on every request.
type headerAdder struct {
	next   http.RoundTripper
	reason string
}

func (h headerAdder) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set(HeaderAccessReason, h.reason)
	return h.next.RoundTrip(r)
}

// SendsToken reports whether requests to a gateway carry a bearer token.
func (f *Factory) SendsToken() bool {
	if f.Caller.ServiceAccount != "" {
		return true
	}
	cfg, err := f.RESTConfig()
	return err == nil && HasToken(cfg)
}
