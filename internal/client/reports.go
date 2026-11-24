package client

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
)

// ReportsAPIPath is where the operator's aggregated report API is served by the API
// server: group reports.kubemoot.ai, version v1alpha1.
const ReportsAPIPath = "/apis/reports.kubemoot.ai/v1alpha1"

// reportsGroupVersion is the groupVersion the discovery document names.
const reportsGroupVersion = "reports.kubemoot.ai/v1alpha1"

// reportsProbeTimeout bounds the discovery call, so an API server that does not answer
// is an error and not a hang.
const reportsProbeTimeout = 10 * time.Second

// ReportsClient returns a client for the operator's aggregated report API, reached through
// the API server with the kubeconfig's own credentials (a client certificate, OIDC or a
// token): the API server authenticates the caller and the operator checks the caller's own
// RBAC. Request paths are relative to ReportsAPIPath. The bool is false only when the API
// is genuinely absent (not installed, or its APIService not ready), and the caller falls
// back to the operator's report Service. Any other failure to ask, such as credentials the
// API server refuses or an API server that does not answer, is an error, because falling
// back would only hide it. The access reason, when there is one, travels as a header the
// API server forwards.
func (f *Factory) ReportsClient(ctx context.Context) (rest.Interface, bool, error) {
	if err := f.Caller.Validate(); err != nil {
		return nil, false, &CallerError{err}
	}
	cfg, err := f.RESTConfig()
	if err != nil {
		return nil, false, err
	}
	cfg = rest.CopyConfig(cfg)
	cfg.APIPath = ReportsAPIPath
	cfg.NegotiatedSerializer = scheme.Codecs.WithoutConversion()
	f.addReason(cfg)
	rc, err := rest.UnversionedRESTClientFor(cfg)
	if err != nil {
		return nil, false, err
	}
	ok, err := probeReports(ctx, rc)
	return rc, ok, err
}

// probeReports asks for the report API's discovery document. It is absent when the API
// server answers 404 (not installed) or 503 (the APIService is not available), or answers
// with another group; any other failure is returned.
func probeReports(ctx context.Context, rc rest.Interface) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, reportsProbeTimeout)
	defer cancel()
	raw, err := rc.Get().Do(ctx).Raw()
	if err != nil {
		if apierrors.IsNotFound(err) || apierrors.IsServiceUnavailable(err) {
			return false, nil
		}
		return false, fmt.Errorf("ask the API server for the operator's report API: %w", err)
	}
	var list metav1.APIResourceList
	if json.Unmarshal(raw, &list) != nil {
		return false, nil
	}
	return list.GroupVersion == reportsGroupVersion, nil
}
