package clierr

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/clientcmd"
)

var env = Env{Kubeconfig: "/home/me/.kube/lab", Context: "lab", Namespace: "team-a"}

func statusErr(code int32, reason metav1.StatusReason, msg string, details *metav1.StatusDetails) error {
	return &apierrors.StatusError{ErrStatus: metav1.Status{
		Status: metav1.StatusFailure, Code: code, Reason: reason, Message: msg, Details: details,
	}}
}

func TestExplain(t *testing.T) {
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")}
	cases := []struct {
		label string
		err   error
		msg   string
		hint  string
	}{
		{"unreachable", fmt.Errorf("list: %w", refused),
			`cannot reach the Kubernetes API server (context "lab")`,
			"Check that the cluster is running and the context points at it: kmctl status"},
		{"unknown host", &net.DNSError{Name: "k8s.example", Err: "no such host"},
			`cannot reach the Kubernetes API server (context "lab")`,
			"Check that the cluster is running and the context points at it: kmctl status"},
		{"empty config", fmt.Errorf("build client: %w", clientcmd.ErrEmptyConfig),
			"no Kubernetes cluster is configured (the kubeconfig has no current context)",
			"Set KUBECONFIG or pass --kubeconfig, and pick a context with --context"},
		{"context not found", errors.New(`context "lab" does not exist`),
			`context "lab" is not in the kubeconfig`, "List the contexts with: kubectl config get-contexts"},
		{"kubeconfig missing", &fs.PathError{Op: "stat", Path: "/home/me/.kube/lab", Err: fs.ErrNotExist},
			`kubeconfig file "/home/me/.kube/lab" does not exist`, "Check the path passed with --kubeconfig"},
		{"other file missing", &fs.PathError{Op: "stat", Path: "crew.yaml", Err: fs.ErrNotExist},
			`file "crew.yaml" does not exist`, "Check the path"},
		{"unauthorized", statusErr(401, metav1.StatusReasonUnauthorized, "Unauthorized", nil),
			`the Kubernetes API server did not accept your credentials (context "lab")`,
			"Log in again or refresh the credentials in your kubeconfig"},
		{"forbidden in a namespace", statusErr(403, metav1.StatusReasonForbidden,
			`agents.kubemoot.ai "a" is forbidden: User "dev" cannot get resource "agents" in API group "kubemoot.ai" in the namespace "ops"`, nil),
			`you are not allowed to get agents in namespace "ops"`,
			"Ask a cluster admin for access, or check yours with: kubectl auth can-i --list -n ops"},
		{"forbidden across the cluster", statusErr(403, metav1.StatusReasonForbidden,
			`crews.kubemoot.ai is forbidden: User "dev" cannot list resource "crews" in API group "kubemoot.ai" at the cluster scope`, nil),
			"you are not allowed to list crews across the cluster",
			"Ask a cluster admin for access, or check yours with: kubectl auth can-i --list"},
		{"forbidden without a rule", statusErr(403, metav1.StatusReasonForbidden, "unknown", nil),
			"the Kubernetes API server denied the request (forbidden)",
			"Ask a cluster admin for access, or check yours with: kubectl auth can-i --list"},
		{"no kind match", &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: "kubemoot.ai", Kind: "Crw"}, SearchedVersions: []string{"v1alpha1"}},
			`the cluster has no such Kubemoot kind: no matches for kind "Crw" in version "kubemoot.ai/v1alpha1"`,
			"Check the kind's spelling; kmctl status shows whether Kubemoot is installed"},
		{"no resource match", &meta.NoResourceMatchError{PartialResource: schema.GroupVersionResource{Group: "kubemoot.ai", Resource: "crw"}},
			`the cluster has no such Kubemoot kind: no matches for kubemoot.ai/, Resource=crw`,
			"Check the kind's spelling; kmctl status shows whether Kubemoot is installed"},
		{"route missing", statusErr(404, metav1.StatusReasonNotFound, "the server could not find the requested resource (get crews.kubemoot.ai demo)",
			&metav1.StatusDetails{Name: "demo", Kind: "crews"}),
			"the cluster does not serve the Kubemoot API (kubemoot.ai)",
			"Check that Kubemoot is installed and the context is the right one: kmctl status"},
		{"unknown error keeps its text", errors.New("something else"), "something else", ""},
	}
	for _, c := range cases {
		e := Explain(c.err, env)
		if e.Message != c.msg || e.Hint != c.hint {
			t.Errorf("%s:\n got %q / %q\nwant %q / %q", c.label, e.Message, e.Hint, c.msg, c.hint)
		}
		if !errors.Is(e, c.err) {
			t.Errorf("%s: the explanation keeps the error underneath", c.label)
		}
	}
}

func TestExplain_NoContextMeansNoConfig(t *testing.T) {
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	for _, err := range []error{refused, clientcmd.ErrNoContext} {
		if got := Explain(err, Env{}).Message; got != "no Kubernetes cluster is configured (the kubeconfig has no current context)" {
			t.Errorf("%v without a context: got %q", err, got)
		}
	}
}

func TestExplain_KeepsAnExplanation(t *testing.T) {
	e := New("crew is missing", "pass -n", nil)
	if got := Explain(fmt.Errorf("outer: %w", e), env); got != e {
		t.Errorf("got %+v, want the explanation in the chain", got)
	}
}

func TestMissingAPIAndObject(t *testing.T) {
	object := statusErr(404, metav1.StatusReasonNotFound, `crews.kubemoot.ai "x" not found`, &metav1.StatusDetails{Name: "x", Kind: "crews"})
	noDetails := statusErr(404, metav1.StatusReasonNotFound, "no route", nil)
	other := statusErr(500, metav1.StatusReasonInternalError, "boom", nil)
	cases := []struct {
		label          string
		err            error
		api, objectGot bool
	}{
		{"named object", object, false, true},
		{"no details", noDetails, true, false},
		{"not a 404", other, false, false},
		{"not a status", errors.New("x"), false, false},
		{"nil", nil, false, false},
	}
	for _, c := range cases {
		if MissingAPI(c.err) != c.api || MissingObject(c.err) != c.objectGot {
			t.Errorf("%s: MissingAPI=%v MissingObject=%v", c.label, MissingAPI(c.err), MissingObject(c.err))
		}
	}
}

func TestWrite(t *testing.T) {
	e := New("no crew \"x\"", "Pass -n crew-x", errors.New("get crews x: not found"))
	cases := []struct {
		label string
		e     *Error
		f     Format
		want  string
	}{
		{"default", e, Format{}, "Error: no crew \"x\"\nHint: Pass -n crew-x\n"},
		{"verbose", e, Format{Verbose: true}, "Error: no crew \"x\"\nHint: Pass -n crew-x\nCause: get crews x: not found\n"},
		{"no hint", New("bad", "", nil), Format{Verbose: true}, "Error: bad\n"},
		{"cause the same as the message", New("bad", "", errors.New("bad")), Format{Verbose: true}, "Error: bad\n"},
	}
	for _, c := range cases {
		var b bytes.Buffer
		if err := Write(&b, c.e, c.f); err != nil {
			t.Fatal(err)
		}
		if b.String() != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.label, b.String(), c.want)
		}
	}
}

func TestWrite_Styled(t *testing.T) {
	var b bytes.Buffer
	if err := Write(&b, New("bad", "fix it", errors.New("cause")), Format{Styled: true, Verbose: true}); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"\x1b[", "Error:", " bad\n", "Hint:", " fix it\n", "Cause:", " cause\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("styled output lacks %q: %q", want, out)
		}
	}
}

func TestStyled(t *testing.T) {
	envOf := func(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }
	cases := []struct {
		label string
		tty   bool
		env   map[string]string
		want  bool
	}{
		{"terminal", true, nil, true},
		{"not a terminal", false, nil, false},
		{"NO_COLOR", true, map[string]string{"NO_COLOR": "1"}, false},
		{"TERM=dumb", true, map[string]string{"TERM": "dumb"}, false},
	}
	for _, c := range cases {
		if got := styledFor(c.tty, envOf(c.env)); got != c.want {
			t.Errorf("%s: got %v", c.label, got)
		}
	}
	if Styled(&bytes.Buffer{}, envOf(nil)) {
		t.Error("a buffer is not a terminal")
	}
}

func TestDebug(t *testing.T) {
	for value, want := range map[string]bool{"": false, "0": false, "false": false, "FALSE": false, "1": true, "true": true, "yes": true} {
		if got := Debug(func(string) string { return value }); got != want {
			t.Errorf("KMCTL_DEBUG=%q: got %v", value, got)
		}
	}
}

func TestErrorAndUnwrap(t *testing.T) {
	cause := errors.New("cause")
	e := New("message", "hint", cause)
	if e.Error() != "message" || !errors.Is(e, cause) {
		t.Errorf("Error()=%q, Is(cause)=%v", e.Error(), errors.Is(e, cause))
	}
	if _, ok := As(errors.New("plain")); ok {
		t.Error("a plain error has no explanation")
	}
}
