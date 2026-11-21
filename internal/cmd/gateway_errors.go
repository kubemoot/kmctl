package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"syscall"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/clierr"
	"github.com/kubemoot/kmctl/internal/conversation"
	"github.com/kubemoot/kmctl/internal/discussion"
	"github.com/kubemoot/kmctl/internal/resource"
	"github.com/kubemoot/kmctl/internal/svcforward"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// explainGateway explains a failed call to a crew's discussion gateway. A 401 or
// 403 is the gateway refusing the caller (the crew verifies who is asking); a 404
// that says the conversation was not found is explained as that; any other 404 or
// 503 first checks that the crew exists in the namespace, and names the namespaces
// where it does when it does not. Errors that are not an HTTP status (an
// unreachable cluster) are returned unchanged.
func explainGateway(ctx context.Context, f *client.Factory, err error, crew, namespace string) error {
	return explainGatewayFor(ctx, f, err, crew, namespace, readAction)
}

// explainGatewayFor is explainGateway for a caller who acts on a conversation: the refusals
// name the action and the role that allows it to an operator.
func explainGatewayFor(ctx context.Context, f *client.Factory, err error, crew, namespace string, act gatewayAction) error {
	var se *apierrors.StatusError
	if !errors.As(err, &se) {
		return err
	}
	if refusal := gatewayRefusal(f, se, crew, namespace, act); refusal != nil {
		return refusal
	}
	code := int(se.Status().Code)
	if code == http.StatusNotFound || code == http.StatusServiceUnavailable {
		if e := crewElsewhere(ctx, f, crew, namespace); e != nil {
			e.Cause = err
			return e
		}
	}
	return gatewayStatus(code, crew, namespace, err)
}

// conversationNotFound is the gateway's one answer for a conversation that does not
// exist and one the caller may not see, so an id cannot be probed.
const conversationNotFound = "conversation not found"

// statusText is what the gateway said: the body of a reply that is not a Status.
func statusText(se *apierrors.StatusError) string {
	st := se.Status()
	if st.Details != nil {
		for _, c := range st.Details.Causes {
			if c.Message != "" {
				return c.Message
			}
		}
	}
	return st.Message
}

// gatewayUnauthorized explains a 401 from the gateway by what kmctl sent.
func gatewayUnauthorized(f *client.Factory, crew, namespace string, err error) *clierr.Error {
	switch {
	case f.Caller.ServiceAccount != "":
		return clierr.New(fmt.Sprintf("the discussion gateway of crew %q did not accept the token minted for service account %s", crew, f.Caller.ServiceAccount),
			"The crew's spec.discussion.access.tokenAudiences may need --token-audience, and the account needs get on the Crew: kubectl auth can-i get crews.kubemoot.ai/"+crew+" -n "+namespace+" --as system:serviceaccount:"+strings.ReplaceAll(f.Caller.ServiceAccount, "/", ":"), err)
	case f.SendsToken():
		return clierr.New(fmt.Sprintf("the discussion gateway of crew %q did not accept your Kubernetes token", crew),
			"The crew verifies who is asking (spec.discussion.access). A token that has expired or has the wrong audience is refused; a kubeconfig token cannot carry an audience, so for a crew with tokenAudiences use --token-service-account NAMESPACE/NAME with --token-audience", err)
	}
	return clierr.New(fmt.Sprintf("the crew %q requires a Kubernetes token, and your kubeconfig has none (it signs in with a client certificate)", crew),
		"Name a ServiceAccount you may create tokens for: --token-service-account NAMESPACE/NAME (needs create on serviceaccounts/token)", err)
}

// gatewayForbidden explains a 403: the caller is known and may not use the crew.
func gatewayForbidden(crew, namespace string, err error) *clierr.Error {
	return clierr.New(fmt.Sprintf("the discussion gateway of crew %q refused your identity", crew),
		"Using a crew needs get on the Crew; check yours with: kubectl auth can-i get crews.kubemoot.ai/"+crew+" -n "+namespace, err)
}

// gatewayAction is what a caller does to a conversation, which decides what an
// operator needs to do it: the permission on crews/conversations and the ClusterRole
// that grants it.
type gatewayAction struct {
	verb string // read, stop, pause, resume, delete
	perm string // get, update, delete
	role string
	// force is set when a delete was told to stop a running discussion first.
	force bool
}

var readAction = gatewayAction{verb: "read", perm: "get", role: "kubemoot-conversation-reader"}

// controlAction is the action of a stop, pause, resume or delete.
func controlAction(verb string) gatewayAction {
	perm := "update"
	if verb == "delete" {
		perm = "delete"
	}
	return gatewayAction{verb: verb, perm: perm, role: "kubemoot-conversation-controller"}
}

// conversationRefused explains the gateway's 404 for a conversation: it does not
// exist, has expired, or belongs to another caller.
func conversationRefused(crew string, act gatewayAction, err error) *clierr.Error {
	return clierr.New(fmt.Sprintf("conversation not found on crew %q", crew),
		fmt.Sprintf(`It does not exist, has expired, or belongs to another caller. An operator with %s on crews/conversations (the ClusterRole %s) can %s it with --reason "<why>"`,
			act.perm, act.role, act.verb), err)
}

// gatewayRefusal explains the gateway refusing the caller or the request: 401, 403, a
// conversation that is not the caller's, or a missing reason. It is nil for any other answer.
func gatewayRefusal(f *client.Factory, se *apierrors.StatusError, crew, namespace string, act gatewayAction) *clierr.Error {
	text := statusText(se)
	switch code := int(se.Status().Code); {
	case code == http.StatusUnauthorized:
		return gatewayUnauthorized(f, crew, namespace, se)
	case code == http.StatusForbidden:
		return gatewayForbidden(crew, namespace, se)
	case code == http.StatusNotFound && strings.Contains(text, conversationNotFound):
		return conversationRefused(crew, act, se)
	case code == http.StatusBadRequest && strings.Contains(text, client.HeaderAccessReason):
		return clierr.New(fmt.Sprintf("the gateway wants a reason to %s another caller's conversation", act.verb),
			`Say why with --reason "<text>" (1 to 200 characters); the gateway audits it`, se)
	}
	return nil
}

// openGateway opens a client to the crew's discussion gateway through a
// port-forward to one of its pods, as kubectl port-forward does, as the caller (it
// sends the caller's Kubernetes token); the caller closes
// it with the returned function. A failure is explained: the Service or crew is
// missing, the forward is not allowed, or no pod is ready.
func openGateway(ctx context.Context, f *client.Factory, crew, namespace string) (rest.Interface, func(), error) {
	rc, stop, err := f.GatewayClient(ctx, namespace, discussion.ServiceName(crew), discussion.GatewayPort)
	if err != nil {
		return nil, nil, explainGatewayOpen(ctx, f, err, crew, namespace)
	}
	return rc, stop, nil
}

// explainGatewayOpen explains why the port-forward to the gateway could not open.
func explainGatewayOpen(ctx context.Context, f *client.Factory, err error, crew, namespace string) error {
	var caller *client.CallerError
	if errors.As(err, &caller) {
		return explainCaller(f, caller)
	}
	switch {
	case apierrors.IsForbidden(err):
		return clierr.New(fmt.Sprintf("you are not allowed to reach the discussion gateway of crew %q in namespace %q", crew, namespace),
			"kmctl port-forwards to it, which takes get on services, list on pods, and create on pods/portforward; check yours with: kubectl auth can-i create pods/portforward -n "+namespace, err)
	case apierrors.IsNotFound(err):
		return explainGateway(ctx, f, err, crew, namespace)
	case errors.Is(err, svcforward.ErrNoReadyPod):
		return gatewayStatus(http.StatusServiceUnavailable, crew, namespace, err)
	case errors.Is(err, svcforward.ErrNoPodsSelected):
		return clierr.New(fmt.Sprintf("the discussion gateway service of crew %q in namespace %q selects no pods", crew, namespace),
			"A Service without a selector has nothing to forward to; check how the crew's chart defines "+discussion.ServiceName(crew), err)
	}
	return clierr.New(fmt.Sprintf("could not reach the discussion gateway of crew %q in namespace %q", crew, namespace),
		"kmctl port-forwards to it, as kubectl port-forward does; check that you can with: kubectl port-forward -n "+namespace+" svc/"+discussion.ServiceName(crew)+" 8080:"+discussion.GatewayPort, err)
}

// explainCaller explains why kmctl could not be the caller asked for: the account it
// may not mint a token for, or a flag that is not valid.
func explainCaller(f *client.Factory, err *client.CallerError) error {
	if apierrors.IsForbidden(err.Err) {
		return clierr.New(fmt.Sprintf("you are not allowed to mint a token for service account %s", f.Caller.ServiceAccount),
			"Minting takes create on serviceaccounts/token for that account; check yours with: kubectl auth can-i create serviceaccounts/token -n "+strings.SplitN(f.Caller.ServiceAccount, "/", 2)[0], err)
	}
	return err
}

// gatewayStatus explains the gateway's HTTP status once the crew is known to be
// in the namespace (or cannot be looked up).
func gatewayStatus(code int, crew, namespace string, err error) *clierr.Error {
	check := fmt.Sprintf("Check the crew with: kmctl crew get %s -n %s", crew, namespace)
	switch code {
	case http.StatusNotFound:
		return clierr.New(fmt.Sprintf("the discussion gateway of crew %q was not found in namespace %q", crew, namespace), check, err)
	case http.StatusServiceUnavailable:
		return clierr.New(fmt.Sprintf("the discussion gateway of crew %q in namespace %q is not ready", crew, namespace),
			"Its pods may still be starting. "+check, err)
	}
	return clierr.New(fmt.Sprintf("the discussion gateway of crew %q answered HTTP %d", crew, code),
		fmt.Sprintf("See its logs with: kubectl logs -n %s svc/%s", namespace, discussion.ServiceName(crew)), err)
}

// crewElsewhere returns an explanation when no crew of that name is in the
// namespace, or nil when it is there or the lookup cannot tell.
func crewElsewhere(ctx context.Context, f *client.Factory, crew, namespace string) *clierr.Error {
	dc, err := f.Dynamic()
	if err != nil {
		return nil
	}
	if _, err := resource.Get(ctx, dc, resource.Crew, namespace, crew); !clierr.MissingObject(err) {
		return nil
	}
	found, err := namespacesWithCrew(ctx, dc, crew)
	return crewNotFound(crew, namespace, found, err)
}

// namespacesWithCrew lists the namespaces that hold a crew of that name.
func namespacesWithCrew(ctx context.Context, dc dynamic.Interface, crew string) ([]string, error) {
	list, err := resource.List(ctx, dc, resource.Crew, "", true)
	if err != nil {
		return nil, err
	}
	var found []string
	for i := range list.Items {
		if list.Items[i].GetName() == crew {
			found = append(found, list.Items[i].GetNamespace())
		}
	}
	slices.Sort(found)
	return found, nil
}

// crewNotFound says the crew is not in the namespace and where to find it:
// the namespaces that hold it, or, when kmctl may not list crews across the
// cluster (listErr), how to look.
func crewNotFound(crew, namespace string, found []string, listErr error) *clierr.Error {
	noun := kindNouns[resource.Crew.Plural]
	msg := noun.notIn(crew, namespace)
	switch {
	case listErr != nil:
		return clierr.New(msg, "Pass its namespace with -n. "+noun.findHint(), nil)
	case len(found) == 0:
		return clierr.New(msg+"; no namespace has a crew of that name", noun.findHint(), nil)
	case len(found) == 1:
		return clierr.New(fmt.Sprintf("%s; found in %q", msg, found[0]), "Pass -n "+found[0], nil)
	}
	return clierr.New(fmt.Sprintf("%s; found in %s", msg, quoteAll(found)), "Pass -n with one of those namespaces", nil)
}

func quoteAll(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	return strings.Join(quoted, ", ")
}

// streamCut explains a discussion stream that ended with an error before the
// crew answered. A closed stdout (a pipe whose reader left) is not the stream's
// fault and is returned unchanged.
func streamCut(err error, o conversation.Options) error {
	if errors.Is(err, syscall.EPIPE) {
		return err
	}
	return clierr.New("the discussion stream closed before the crew answered; the discussion may still finish",
		"Reattach: "+conversation.Reattach(o.Crew, o.Namespace, o.ConversationID), err)
}
