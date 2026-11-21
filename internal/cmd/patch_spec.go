package cmd

import (
	"context"
	"encoding/json"

	"github.com/kubemoot/kmctl/internal/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

// patchSpec merge-patches the spec of one Kubemoot object with the caller's own
// credentials, so the caller's RBAC decides whether it is allowed. A null value in
// the patch removes the field. With dryRun the API server validates and authorizes
// the patch and stores nothing, as kubectl --dry-run=server does.
func patchSpec(ctx context.Context, dc dynamic.Interface, k resource.Kind, namespace, name string, spec map[string]any, dryRun bool) error {
	body, err := json.Marshal(map[string]any{"spec": spec})
	if err != nil {
		return err
	}
	opts := metav1.PatchOptions{}
	if dryRun {
		opts.DryRun = []string{metav1.DryRunAll}
	}
	_, err = dc.Resource(k.GVR()).Namespace(namespace).Patch(ctx, name, types.MergePatchType, body, opts)
	return err
}
