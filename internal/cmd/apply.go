package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/javajon-homelab/kmctl/internal/client"
	"github.com/javajon-homelab/kmctl/internal/manifest"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
)

// newApplyCommand applies Kubemoot CRD manifests via server-side apply. It is a
// CRD editor only: non-kubemoot.ai kinds are refused (use kubectl for those).
func newApplyCommand(f *client.Factory) *cobra.Command {
	var filename string
	var dryRun bool
	var force bool
	cmd := &cobra.Command{
		Use:   "apply -f FILE|DIR|-",
		Short: "Apply Kubemoot resource manifests (server-side apply)",
		Long: `Apply creates or updates Kubemoot custom resources via server-side apply.

It manages kubemoot.ai resources only (Crew, Agent, PromptModule, and the rest);
other kinds are refused - use kubectl for those. Lifecycle (namespaces, jobs,
cascading cleanup) belongs to the operator's finalizers, not kmctl.`,
		Example: `  kmctl apply -f crew.yaml
  kmctl apply -f ./demo/
  kmctl create demo && kmctl apply -f demo/`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if filename == "" {
				return fmt.Errorf("a manifest is required: -f FILE|DIR|-")
			}
			objs, err := manifest.FromPath(filename, cmd.InOrStdin())
			if err != nil {
				return err
			}
			if len(objs) == 0 {
				return fmt.Errorf("no objects found in %q", filename)
			}
			dyn, err := f.Dynamic()
			if err != nil {
				return err
			}
			mapper, err := f.RESTMapper()
			if err != nil {
				return err
			}
			opts := applyOpts{defaultNS: f.Namespace(), dryRun: dryRun, force: force}
			for i := range objs {
				if err := applyObject(cmd.Context(), dyn, mapper, objs[i], opts, cmd.OutOrStdout()); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&filename, "filename", "f", "", "Manifest file, directory, or - for stdin")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Submit a server-side dry-run without persisting")
	cmd.Flags().BoolVar(&force, "force", false, "Force apply, taking ownership of conflicting fields")
	return cmd
}

// applyOpts groups the per-apply behavior knobs so applyObject keeps a small
// signature.
type applyOpts struct {
	defaultNS string
	dryRun    bool
	force     bool
}

func applyObject(ctx context.Context, dyn dynamic.Interface, mapper meta.RESTMapper, obj unstructured.Unstructured, ao applyOpts, w io.Writer) error {
	gvk := obj.GroupVersionKind()
	if gvk.Group != client.KubemootGroup {
		return fmt.Errorf("refusing to apply %s (group %q): kmctl only manages %s resources - use kubectl for others",
			gvk.Kind, gvk.Group, client.KubemootGroup)
	}
	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", gvk.Kind, err)
	}

	ns := ""
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		ns = obj.GetNamespace()
		if ns == "" {
			ns = ao.defaultNS
			obj.SetNamespace(ns)
		}
	}
	ri := scopedResource(dyn, mapping, ns)
	opts := metav1.ApplyOptions{FieldManager: "kmctl", Force: ao.force}
	if ao.dryRun {
		opts.DryRun = []string{metav1.DryRunAll}
	}
	applied, err := ri.Apply(ctx, obj.GetName(), &obj, opts)
	if err != nil {
		return err
	}

	suffix := ""
	if ao.dryRun {
		suffix = " (dry run)"
	}
	_, err = fmt.Fprintf(w, "%s/%s applied%s\n", strings.ToLower(gvk.Kind), applied.GetName(), suffix)
	return err
}

// scopedResource returns the namespaced or cluster-scoped dynamic client for a
// mapping. For cluster-scoped kinds the namespace is ignored.
func scopedResource(dyn dynamic.Interface, mapping *meta.RESTMapping, namespace string) dynamic.ResourceInterface {
	if mapping.Scope.Name() != meta.RESTScopeNameNamespace {
		return dyn.Resource(mapping.Resource)
	}
	return dyn.Resource(mapping.Resource).Namespace(namespace)
}
