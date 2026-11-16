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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// explainGateway explains a failed call to a crew's discussion gateway. A 404 or
// 503 first checks that the crew exists in the namespace, and names the
// namespaces where it does when it does not. Errors that are not an HTTP status
// (an unreachable cluster) and rejected credentials are returned unchanged.
func explainGateway(ctx context.Context, f *client.Factory, err error, crew, namespace string) error {
	var se *apierrors.StatusError
	if !errors.As(err, &se) || apierrors.IsUnauthorized(err) {
		return err
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

// openGateway opens a client to the crew's discussion gateway through a
// port-forward to one of its pods, as kubectl port-forward does; the caller closes
// it with the returned function. A failure is explained: the Service or crew is
// missing, the forward is not allowed, or no pod is ready.
func openGateway(ctx context.Context, f *client.Factory, crew, namespace string) (rest.Interface, func(), error) {
	rc, stop, err := f.ServiceClient(ctx, namespace, discussion.ServiceName(crew), discussion.GatewayPort)
	if err != nil {
		return nil, nil, explainGatewayOpen(ctx, f, err, crew, namespace)
	}
	return rc, stop, nil
}

// explainGatewayOpen explains why the port-forward to the gateway could not open.
func explainGatewayOpen(ctx context.Context, f *client.Factory, err error, crew, namespace string) error {
	switch {
	case apierrors.IsForbidden(err):
		return clierr.New(fmt.Sprintf("you are not allowed to reach the discussion gateway of crew %q in namespace %q", crew, namespace),
			"kmctl port-forwards to it, which takes get on services, list on pods, and create on pods/portforward; check yours with: kubectl auth can-i create pods/portforward -n "+namespace, err)
	case apierrors.IsNotFound(err):
		return explainGateway(ctx, f, err, crew, namespace)
	case strings.Contains(err.Error(), "no ready pod"):
		return gatewayStatus(http.StatusServiceUnavailable, crew, namespace, err)
	}
	return clierr.New(fmt.Sprintf("could not reach the discussion gateway of crew %q in namespace %q", crew, namespace),
		"kmctl port-forwards to it, as kubectl port-forward does; check that you can with: kubectl port-forward -n "+namespace+" svc/"+discussion.ServiceName(crew)+" 8080:"+discussion.GatewayPort, err)
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
