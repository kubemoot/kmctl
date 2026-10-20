package cmd

import (
	"fmt"
	"io"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/output"
	"github.com/kubemoot/kmctl/internal/resource"
	"github.com/spf13/cobra"
)

// modelServerNoun is how the server subcommands are reached, for their examples.
const modelServerNoun = "model server"

// newModelCommand groups the model subcommands: `list` reads the cluster's model
// catalog, `add` puts a model in it, `server list|get` covers the model servers
// and their calibration, and `footprint` lists the Model objects and their state.
func newModelCommand(f *client.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "model",
		Short:   "Read the cluster's model catalog and its model servers",
		Aliases: []string{"models"},
		Args:    cobra.NoArgs,
	}
	footprint := newListCommand(f, cmd.Name(), resource.Model)
	footprint.Use = "footprint"
	footprint.Aliases = []string{"resident"}
	footprint.Short = "List Model objects and their state on their model servers"
	footprint.Example = "  kmctl model footprint\n  kmctl model footprint -A -o yaml"
	cmd.AddCommand(newCatalogListCommand(f), newModelAddCommand(f), newModelServerCommand(f), footprint)
	return cmd
}

// newModelServerCommand lists model servers and shows one with its calibration.
func newModelServerCommand(f *client.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "server",
		Short:   "List model servers and show what calibration measured",
		Aliases: []string{"servers", "modelserver", "modelservers"},
		Args:    cobra.NoArgs,
	}
	cmd.AddCommand(newListCommand(f, modelServerNoun, resource.ModelServer), newGetCommand(f, modelServerNoun, resource.ModelServer))
	return cmd
}

// newCatalogListCommand lists the cluster's model catalog: every model each
// model server offers, as ModelServer status.models reports it.
func newCatalogListCommand(f *client.Factory) *cobra.Command {
	var outFmt string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the cluster's model catalog: the models each model server offers",
		Long: `List shows the cluster's model catalog, one row per model per model server: its
size class (small, medium, large, or unknown until its parameter count is known),
its abilities (tool-calling, reasoning, vision), its context window in tokens, and
whether the server has it now. Crews select from this catalog by label; they ship
no models of their own.

It reads the model servers in every namespace, or only those in the namespace -n
names. It warns when no model can call tools, because crews require one.`,
		Example: "  kmctl model list\n  kmctl model list -o yaml\n  kmctl model list -n kubemoot",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := checkOutput(outFmt); err != nil {
				return err
			}
			dc, err := f.Dynamic()
			if err != nil {
				return fmt.Errorf("build client: %w", err)
			}
			ns := explicitNamespace(f)
			cat, err := resource.ReadCatalog(cmd.Context(), dc, ns)
			if err != nil {
				return err
			}
			if err := printCatalog(cmd.OutOrStdout(), cmd.ErrOrStderr(), cat, outFmt); err != nil {
				return err
			}
			warnIfNoToolCalling(cmd.ErrOrStderr(), cat, ns)
			return nil
		},
	}
	cmd.Flags().StringVarP(&outFmt, "output", "o", "", output.FormatHelp("table"))
	return cmd
}

// printCatalog writes the entries as a table, or as a list for -o; with no
// entries the table form says why on errOut.
func printCatalog(out, errOut io.Writer, cat resource.Catalog, outFmt string) error {
	if outFmt != "" {
		entries := cat.Entries
		if entries == nil {
			entries = []resource.CatalogEntry{}
		}
		return output.Print(out, output.Format(outFmt), entries)
	}
	if len(cat.Entries) == 0 {
		_, err := fmt.Fprintln(errOut, emptyCatalogMessage(cat))
		return err
	}
	return resource.RenderCatalog(out, cat.Entries)
}

// emptyCatalogMessage says why the catalog has no entries.
func emptyCatalogMessage(cat resource.Catalog) string {
	if cat.Servers == 0 {
		return "No model servers found."
	}
	return fmt.Sprintf("No models in the catalog: %d model server(s) found, offering none yet.", cat.Servers)
}

// warnIfNoToolCalling warns when no model in the catalog can call tools: a crew
// that requires tool-calling, as every crew kmctl create scaffolds does, applies
// Ready but cannot run a discussion. namespace, when set, is the only namespace
// read, and the warning says so.
func warnIfNoToolCalling(w io.Writer, cat resource.Catalog, namespace string) {
	if cat.HasToolCalling() {
		return
	}
	where := "the cluster's model catalog"
	if namespace != "" {
		where = fmt.Sprintf("the model catalog in namespace %q", namespace)
	}
	// Best-effort warning to stderr; a write failure here is not worth failing on.
	_, _ = fmt.Fprintf(w,
		"\nwarning: no model in %s can call tools (label %s: \"true\").\n"+
			"A crew that requires one, as every crew kmctl create scaffolds does, applies Ready but\n"+
			"stays Unschedulable until a model server offers one. A cluster owner adds a model with\n"+
			"kmctl model add MODEL --model-server NAME.\n",
		where, resource.ToolCallingLabel)
}
