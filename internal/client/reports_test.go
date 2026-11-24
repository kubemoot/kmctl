package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
)

func probe(t *testing.T, h http.HandlerFunc) (bool, error) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	rc, err := rest.UnversionedRESTClientFor(&rest.Config{
		Host: srv.URL, APIPath: ReportsAPIPath, ContentConfig: rest.ContentConfig{NegotiatedSerializer: scheme.Codecs.WithoutConversion()},
	})
	if err != nil {
		t.Fatal(err)
	}
	return probeReports(context.Background(), rc)
}

func TestProbeReports(t *testing.T) {
	status := func(code int, body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != ReportsAPIPath {
				t.Errorf("asked %s, want %s", r.URL.Path, ReportsAPIPath)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_, _ = w.Write([]byte(body))
		}
	}
	cases := []struct {
		name    string
		h       http.HandlerFunc
		want    bool
		wantErr bool
	}{
		{"served", status(200, `{"kind":"APIResourceList","groupVersion":"reports.kubemoot.ai/v1alpha1"}`), true, false},
		{"not installed", status(404, `{"kind":"Status","code":404,"reason":"NotFound"}`), false, false},
		{"APIService not ready", status(503, `{"kind":"Status","code":503,"reason":"ServiceUnavailable"}`), false, false},
		{"another group", status(200, `{"kind":"APIResourceList","groupVersion":"other.example/v1"}`), false, false},
		{"not JSON", status(200, `<html>`), false, false},
		{"credentials refused", status(401, `{"kind":"Status","code":401,"reason":"Unauthorized"}`), false, true},
		{"forbidden", status(403, `{"kind":"Status","code":403,"reason":"Forbidden"}`), false, true},
		{"server error", status(500, `{"kind":"Status","code":500,"reason":"InternalError"}`), false, true},
	}
	for _, c := range cases {
		got, err := probe(t, c.h)
		if got != c.want || (err != nil) != c.wantErr {
			t.Errorf("%s: available = %v, err = %v; want %v, error %v", c.name, got, err, c.want, c.wantErr)
		}
	}
}
