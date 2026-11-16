// Package natsforward reaches a cluster's NATS server from the machine kmctl runs
// on, with the credentials of the active kubeconfig context. NATS is not HTTP, so
// the package forwards a local port to a ready pod behind the NATS Service (see
// svcforward) and gives back a local nats:// URL.
package natsforward

import (
	"context"
	"strconv"

	"github.com/kubemoot/kmctl/internal/svcforward"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// ClientPort is the port a NATS server listens on for clients.
const ClientPort = 4222

// Open forwards a free local port to the NATS client port of a ready pod behind
// the Service and returns the URL to connect to and a function that closes the
// forward. The forward also ends when ctx is done.
func Open(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, namespace, service string) (string, func(), error) {
	addr, stop, err := svcforward.Open(ctx, cfg, cs, namespace, service, strconv.Itoa(ClientPort))
	if err != nil {
		return "", nil, err
	}
	return "nats://" + addr, stop, nil
}
