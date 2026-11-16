package natsforward

import (
	"context"
	"strings"
	"testing"

	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

func TestOpenNamesAMissingService(t *testing.T) {
	_, _, err := Open(context.Background(), &rest.Config{}, fake.NewSimpleClientset(), "nats", "nats")
	if err == nil || !strings.Contains(err.Error(), "nats/nats") {
		t.Errorf("Open() error = %v, want the service named", err)
	}
}
