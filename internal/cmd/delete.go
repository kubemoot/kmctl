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
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// newDeleteCommand deletes Kubemoot resources, by "KIND NAME" or "-f FILE". The
// operator's finalizers handle cascading cleanup; kmctl only removes the CR.
func newDeleteCommand(f *client.Factory) *cobra.Command {
	var filename string
	cmd := &cobra.Command{
		Use:   "delete (KIND NAME | -f FILE)",
		Short: "Delete Kubemoot resources",
		Long: `Delete removes Kubemoot custom resources, either by kind and name
(e.g. "kmctl delete crew demo") or from a manifest (-f). It manages kubemoot.ai
resources only. The operator's finalizers and owner refs handle cascading
cleanup (namespaces, jobs); kmctl just deletes the CR.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if filename != "" && len(args) > 0 {
				return fmt.Errorf("specify either KIND NAME or -f, not both")
			}
			if filename == "" && len(args) != 2 {
				return fmt.Errorf("usage: kmctl delete KIND NAME (or -f FILE)")
			}
			dyn, err := f.Dynamic()
			if err != nil {
				return err
			}
			mapper, err := f.RESTMapper()
			if err != nil {
				return err
			}
			if filename != "" {
				return deleteFromManifest(cmd.Context(), dyn, mapper, filename, cmd.InOrStdin(), f.Namespace(), cmd.OutOrStdout())
			}
			return deleteByKindName(cmd.Context(), dyn, mapper, args[0], args[1], f.Namespace(), cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVarP(&filename, "filename", "f", "", "Manifest file, directory, or - for stdin")
	return cmd
}

func deleteByKindName(ctx context.Context, dyn dynamic.Interface, mapper meta.RESTMapper, kindArg, name, namespace string, w io.Writer) error {
	gvk, err := mapper.KindFor(schema.GroupVersionResource{Group: client.KubemootGroup, Resource: strings.ToLower(kindArg)})
	if err != nil {
		return fmt.Errorf("unknown kubemoot resource %q: %w", kindArg, err)
	}
	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", gvk.Kind, err)
	}
	if err := scopedResource(dyn, mapping, namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s/%s deleted\n", strings.ToLower(gvk.Kind), name)
	return err
}

func deleteFromManifest(ctx context.Context, dyn dynamic.Interface, mapper meta.RESTMapper, filename string, stdin io.Reader, defaultNS string, w io.Writer) error {
	objs, err := manifest.FromPath(filename, stdin)
	if err != nil {
		return err
	}
	for i := range objs {
		obj := objs[i]
		gvk := obj.GroupVersionKind()
		if gvk.Group != client.KubemootGroup {
			return fmt.Errorf("refusing to delete %s (group %q): kmctl only manages %s resources",
				gvk.Kind, gvk.Group, client.KubemootGroup)
		}
		mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			return fmt.Errorf("resolve %s: %w", gvk.Kind, err)
		}
		ns := obj.GetNamespace()
		if ns == "" {
			ns = defaultNS
		}
		if err := scopedResource(dyn, mapping, ns).Delete(ctx, obj.GetName(), metav1.DeleteOptions{}); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "%s/%s deleted\n", strings.ToLower(gvk.Kind), obj.GetName()); err != nil {
			return err
		}
	}
	return nil
}
