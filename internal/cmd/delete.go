package cmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/manifest"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// newDeleteCommand deletes Kubemoot resources, by "KIND NAME" or "-f FILE". The
// operator's finalizers handle cascading cleanup; kmctl only removes the CR. It
// names what it will delete and asks first, like the other destructive commands.
func newDeleteCommand(f *client.Factory) *cobra.Command {
	var filename string
	var yes, dryRun bool
	cmd := &cobra.Command{
		Use:         "delete (KIND NAME | -f FILE)",
		Annotations: changesState(),
		Short:       "Delete Kubemoot resources",
		Long: `Delete removes Kubemoot custom resources, either by kind and name
(e.g. "kmctl delete crew demo") or from a manifest (-f). It manages kubemoot.ai
resources only. The operator's finalizers and owner refs handle cascading
cleanup (namespaces, jobs); kmctl just deletes the CR.

It names what it will delete and asks first; answer y to go on. Pass --yes to skip
the question (scripts), or --dry-run to have the API server check the delete and
change nothing.`,
		Example: `  kmctl delete crew demo
  kmctl delete crew demo --dry-run
  kmctl delete -f crew.yaml --yes`,
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
			var targets []deleteTarget
			if filename != "" {
				targets, err = manifestTargets(dyn, mapper, filename, cmd.InOrStdin(), f.Namespace())
			} else {
				targets, err = kindNameTarget(dyn, mapper, args[0], args[1], f.Namespace())
			}
			if err != nil {
				return err
			}
			return runDeletes(cmd, targets, yes, dryRun)
		},
	}
	cmd.Flags().StringVarP(&filename, "filename", "f", "", "Manifest file, directory, or - for stdin")
	addYesFlag(cmd, &yes)
	addDryRunFlag(cmd, &dryRun)
	return cmd
}

// deleteTarget is one resolved object to delete.
type deleteTarget struct {
	ri   dynamic.ResourceInterface
	kind string
	name string
	ns   string
}

func (t deleteTarget) label() string { return t.kind + "/" + t.name }

// runDeletes confirms (unless --yes or --dry-run), then deletes each target; a
// dry run has the API server check the delete and changes nothing.
func runDeletes(cmd *cobra.Command, targets []deleteTarget, yes, dryRun bool) error {
	if !dryRun {
		if err := confirmChange(cmd, yes, deleteQuestion(targets)); err != nil {
			return err
		}
	}
	opts := metav1.DeleteOptions{}
	if dryRun {
		opts.DryRun = []string{metav1.DryRunAll}
	}
	for _, t := range targets {
		if err := t.ri.Delete(cmd.Context(), t.name, opts); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s deleted%s\n", t.label(), dryRunSuffix(dryRun)); err != nil {
			return err
		}
	}
	return nil
}

// deleteQuestion names every object about to be deleted.
func deleteQuestion(targets []deleteTarget) string {
	labels := make([]string, len(targets))
	for i, t := range targets {
		labels[i] = t.label()
		if t.ns != "" {
			labels[i] += " in namespace " + t.ns
		}
	}
	return "Delete " + strings.Join(labels, ", ") + "? The operator then removes what it owns."
}

func kindNameTarget(dyn dynamic.Interface, mapper meta.RESTMapper, kindArg, name, namespace string) ([]deleteTarget, error) {
	gvk, err := mapper.KindFor(schema.GroupVersionResource{Group: client.KubemootGroup, Resource: strings.ToLower(kindArg)})
	if err != nil {
		return nil, fmt.Errorf("unknown kubemoot resource %q: %w", kindArg, err)
	}
	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", gvk.Kind, err)
	}
	return []deleteTarget{newDeleteTarget(dyn, mapping, strings.ToLower(gvk.Kind), name, namespace)}, nil
}

func manifestTargets(dyn dynamic.Interface, mapper meta.RESTMapper, filename string, stdin io.Reader, defaultNS string) ([]deleteTarget, error) {
	objs, err := manifest.FromPath(filename, stdin)
	if err != nil {
		return nil, err
	}
	targets := make([]deleteTarget, 0, len(objs))
	for i := range objs {
		obj := objs[i]
		gvk := obj.GroupVersionKind()
		if gvk.Group != client.KubemootGroup {
			return nil, fmt.Errorf("refusing to delete %s (group %q): kmctl only manages %s resources",
				gvk.Kind, gvk.Group, client.KubemootGroup)
		}
		mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", gvk.Kind, err)
		}
		ns := obj.GetNamespace()
		if ns == "" {
			ns = defaultNS
		}
		targets = append(targets, newDeleteTarget(dyn, mapping, strings.ToLower(gvk.Kind), obj.GetName(), ns))
	}
	return targets, nil
}

// newDeleteTarget binds one object to its (namespaced or cluster) resource client.
func newDeleteTarget(dyn dynamic.Interface, mapping *meta.RESTMapping, kind, name, namespace string) deleteTarget {
	t := deleteTarget{ri: scopedResource(dyn, mapping, namespace), kind: kind, name: name}
	if mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		t.ns = namespace
	}
	return t
}
