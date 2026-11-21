package discussion

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
)

func listClient(t *testing.T, h http.HandlerFunc) rest.Interface {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	rc, err := rest.UnversionedRESTClientFor(&rest.Config{Host: srv.URL,
		ContentConfig: rest.ContentConfig{NegotiatedSerializer: scheme.Codecs.WithoutConversion()}})
	if err != nil {
		t.Fatal(err)
	}
	return rc
}

func TestListAs_AllAsksForEveryCallersAndKeepsTheOwner(t *testing.T) {
	var query string
	rc := listClient(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = io.WriteString(w, `{"conversations":[{"conversationId":"c1","turns":1,"owner":"abc"}]}`)
	})
	got, err := ListAs(context.Background(), rc, "demo", 100, true)
	if err != nil || len(got) != 1 || got[0].Owner != "abc" {
		t.Fatalf("got %+v, %v", got, err)
	}
	if query != "all=true&limit=100" {
		t.Errorf("query = %q", query)
	}
	if _, err = List(context.Background(), rc, "demo", 5); err != nil || query != "limit=5" {
		t.Errorf("a plain list: query %q, err %v; want no all parameter", query, err)
	}
}

func TestListAs_ANotFoundIsRefusedForAllButMissingForAPlainList(t *testing.T) {
	rc := listClient(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "conversation not found", http.StatusNotFound)
	})
	if _, err := List(context.Background(), rc, "demo", 5); err != ErrListUnsupported {
		t.Errorf("a plain list: err = %v, want ErrListUnsupported", err)
	}
	_, err := ListAs(context.Background(), rc, "demo", 5, true)
	if !apierrors.IsNotFound(err) {
		t.Errorf("--all: err = %v, want the gateway's 404 passed on", err)
	}
}
