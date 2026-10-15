package clierr

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"regexp"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/tools/clientcmd"
)

// Env is what kmctl was pointed at when the error happened: the kubeconfig file
// passed with --kubeconfig (empty when none was), the context, and the namespace.
type Env struct {
	Kubeconfig string
	Context    string
	Namespace  string
}

// KubemootAPI is the API group the messages name when the cluster lacks it.
const KubemootAPI = "kubemoot.ai"

// explainer returns the explanation of err, or nil when err is not its kind.
type explainer func(err error, env Env) *Error

// explainers are tried in order; the first match wins.
var explainers = []explainer{
	explainMissingFile,
	explainNoConfig,
	explainNoContext,
	explainUnreachable,
	explainUnauthorized,
	explainForbidden,
	explainNoMatch,
	explainMissingAPI,
}

// Explain returns err explained: an *Error already in its chain, the first
// matching Kubernetes client failure, or, for anything else, err's own text.
func Explain(err error, env Env) *Error {
	if e, ok := As(err); ok {
		return e
	}
	for _, x := range explainers {
		if e := x(err, env); e != nil {
			e.Cause = err
			return e
		}
	}
	return &Error{Message: err.Error(), Cause: err}
}

// inChain reports whether pred holds for err or any error it wraps. clientcmd's
// own predicates do not unwrap, so they are applied link by link.
func inChain(err error, pred func(error) bool) bool {
	for ; err != nil; err = errors.Unwrap(err) {
		if pred(err) {
			return true
		}
	}
	return false
}

func explainMissingFile(err error, env Env) *Error {
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) || !errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if env.Kubeconfig != "" && pathErr.Path == env.Kubeconfig {
		return &Error{
			Message: fmt.Sprintf("kubeconfig file %q does not exist", pathErr.Path),
			Hint:    "Check the path passed with --kubeconfig",
		}
	}
	return &Error{Message: fmt.Sprintf("file %q does not exist", pathErr.Path), Hint: "Check the path"}
}

// noConfig is the explanation when kmctl has no cluster to talk to.
func noConfig() *Error {
	return &Error{
		Message: "no Kubernetes cluster is configured (the kubeconfig has no current context)",
		Hint:    "Set KUBECONFIG or pass --kubeconfig, and pick a context with --context",
	}
}

func explainNoConfig(err error, _ Env) *Error {
	if !inChain(err, clientcmd.IsEmptyConfig) {
		return nil
	}
	return noConfig()
}

// unknownContext matches the kubeconfig loader's error for a --context the
// kubeconfig does not have; the loader returns it untyped.
var unknownContext = regexp.MustCompile(`context "[^"]*" does not exist`)

func explainNoContext(err error, env Env) *Error {
	if !inChain(err, clientcmd.IsContextNotFound) && !unknownContext.MatchString(err.Error()) {
		return nil
	}
	if env.Context == "" {
		return noConfig()
	}
	return &Error{
		Message: fmt.Sprintf("context %q is not in the kubeconfig", env.Context),
		Hint:    "List the contexts with: kubectl config get-contexts",
	}
}

func explainUnreachable(err error, env Env) *Error {
	var opErr *net.OpError
	var dnsErr *net.DNSError
	if !errors.As(err, &opErr) && !errors.As(err, &dnsErr) {
		return nil
	}
	if env.Context == "" {
		// Without a context the client falls back to localhost:8080, so the
		// connection failure means no cluster is configured.
		return noConfig()
	}
	return &Error{
		Message: fmt.Sprintf("cannot reach the Kubernetes API server (context %q)", env.Context),
		Hint:    "Check that the cluster is running and the context points at it: kmctl status",
	}
}

func explainUnauthorized(err error, env Env) *Error {
	if !apierrors.IsUnauthorized(err) {
		return nil
	}
	return &Error{
		Message: fmt.Sprintf("the Kubernetes API server did not accept your credentials (context %q)", env.Context),
		Hint:    "Log in again or refresh the credentials in your kubeconfig",
	}
}

// forbiddenRule pulls the verb, the resource, and the namespace out of the API
// server's Forbidden message: `User "u" cannot list resource "crews" in API group
// "kubemoot.ai" in the namespace "default"`.
var forbiddenRule = regexp.MustCompile(`cannot (\S+) resource "([^"]+)"(?: in API group "[^"]*")?(?: in the namespace "([^"]+)")?`)

func explainForbidden(err error, _ Env) *Error {
	if !apierrors.IsForbidden(err) {
		return nil
	}
	e := &Error{
		Message: "the Kubernetes API server denied the request (forbidden)",
		Hint:    "Ask a cluster admin for access, or check yours with: kubectl auth can-i --list",
	}
	// A message in another wording keeps the general explanation above.
	m := forbiddenRule.FindStringSubmatch(err.Error())
	if m == nil {
		return e
	}
	if m[3] == "" {
		e.Message = fmt.Sprintf("you are not allowed to %s %s across the cluster", m[1], m[2])
		return e
	}
	e.Message = fmt.Sprintf("you are not allowed to %s %s in namespace %q", m[1], m[2], m[3])
	e.Hint = "Ask a cluster admin for access, or check yours with: kubectl auth can-i --list -n " + m[3]
	return e
}

func explainNoMatch(err error, _ Env) *Error {
	var kindErr *meta.NoKindMatchError
	var resErr *meta.NoResourceMatchError
	switch {
	case errors.As(err, &kindErr):
		return noKind(kindErr.Error())
	case errors.As(err, &resErr):
		return noKind(resErr.Error())
	}
	return nil
}

func noKind(detail string) *Error {
	return &Error{
		Message: "the cluster has no such Kubemoot kind: " + detail,
		Hint:    "Check the kind's spelling; kmctl status shows whether Kubemoot is installed",
	}
}

func explainMissingAPI(err error, _ Env) *Error {
	if !MissingAPI(err) {
		return nil
	}
	return &Error{
		Message: "the cluster does not serve the Kubemoot API (" + KubemootAPI + ")",
		Hint:    "Check that Kubemoot is installed and the context is the right one: kmctl status",
	}
}

// MissingAPI reports whether err is a 404 for the request's path rather than for
// a named object: the API server does not serve that resource at all.
func MissingAPI(err error) bool {
	var se *apierrors.StatusError
	if !errors.As(err, &se) || !apierrors.IsNotFound(err) {
		return false
	}
	st := se.Status()
	if strings.HasPrefix(st.Message, "the server could not find the requested resource") {
		return true
	}
	return st.Details == nil || st.Details.Name == ""
}

// MissingObject reports whether err says a named object does not exist.
func MissingObject(err error) bool {
	return apierrors.IsNotFound(err) && !MissingAPI(err)
}
