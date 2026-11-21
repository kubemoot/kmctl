package cmd

import (
	"strings"
	"testing"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/spf13/cobra"
	"k8s.io/client-go/dynamic"
)

func runMCP(t *testing.T, dc *writeDyn, args ...string) (string, string, error) {
	t.Helper()
	build := func(root *cobra.Command, f *client.Factory) {
		mcp := &cobra.Command{Use: "mcp"}
		mcp.AddCommand(newMCPReportCommand(f, func() (dynamic.Interface, error) { return dc, nil }))
		root.AddCommand(mcp)
	}
	return runWrite(t, "", build, append([]string{"mcp", "report"}, args...)...)
}

func TestMCPReportPinSetsTheVerdictNotesAndAuthor(t *testing.T) {
	dc := newWriteDyn(reportObj())
	out, _, err := runMCP(t, dc, "pin", "k8s", "--verdict", "avoid", "--notes", "crashes under load", "--author", "ops")
	if err != nil || !strings.Contains(out, "mcpserverreport/k8s pinned to avoid") {
		t.Fatalf("pin: %v\n%s", err, out)
	}
	spec := dc.specOf(t, reportGVR, "k8s")
	if spec["adminVerdict"] != "avoid" || spec["adminNotes"] != "crashes under load" || spec["adminAuthor"] != "ops" {
		t.Errorf("spec = %v", spec)
	}
}

func TestMCPReportPinRejectsAVerdictTheCRDDoesNotAllow(t *testing.T) {
	dc := newWriteDyn(reportObj())
	for _, args := range [][]string{{"pin", "k8s"}, {"pin", "k8s", "--verdict", "maybe"}} {
		_, _, err := runMCP(t, dc, args...)
		if err == nil || !isUsageError(err) || !strings.Contains(err.Error(), "use, caution, avoid") {
			t.Errorf("%v: err = %v", args, err)
		}
	}
	if len(dc.patches) != 0 {
		t.Error("an invalid verdict reached the API server")
	}
}

func TestMCPReportUnpinRemovesTheThreeFields(t *testing.T) {
	dc := newWriteDyn(reportObj())
	out, _, err := runMCP(t, dc, "unpin", "k8s")
	if err != nil || !strings.Contains(out, "mcpserverreport/k8s unpinned") {
		t.Fatalf("unpin: %v\n%s", err, out)
	}
	if spec := dc.specOf(t, reportGVR, "k8s"); len(spec) != 0 {
		t.Errorf("spec = %v, want the three admin fields gone", spec)
	}
}

func TestMCPReportDryRun(t *testing.T) {
	dc := newWriteDyn(reportObj())
	for _, args := range [][]string{{"pin", "k8s", "--verdict", "use", "--dry-run"}, {"unpin", "k8s", "--dry-run"}} {
		out, _, err := runMCP(t, dc, args...)
		if err != nil || !strings.Contains(out, "dry run, nothing changed") {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	for _, p := range dc.patches {
		if len(p.PatchOptions.DryRun) != 1 {
			t.Errorf("patch without a dry-run option: %+v", p)
		}
	}
	if spec := dc.specOf(t, reportGVR, "k8s"); spec["adminVerdict"] != "use" || spec["adminNotes"] != "old" {
		t.Errorf("a dry run changed the report: %v", spec)
	}
}

func TestMCPReportPinDeniedByRBAC(t *testing.T) {
	dc := newWriteDyn(reportObj())
	dc.deny("mcpserverreports")
	_, _, err := runMCP(t, dc, "pin", "k8s", "--verdict", "use")
	if err == nil || !strings.Contains(explained(t, err), `you are not allowed to patch mcpserverreports in namespace "crew-demo"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestMCPReportPinOfAMissingReport(t *testing.T) {
	_, _, err := runMCP(t, newWriteDyn(), "pin", "nope", "--verdict", "use")
	if err == nil {
		t.Fatal("want not found")
	}
}

func TestMCPReportIsInTheRootCommand(t *testing.T) {
	root := newRootCommand(client.NewFactory())
	for _, path := range [][]string{{"mcp", "report", "list"}, {"mcp", "report", "get"}, {"mcp", "report", "pin"}, {"mcp", "report", "unpin"}} {
		if c, _, err := root.Find(path); err != nil || c.Name() != path[2] {
			t.Errorf("%v: %v", path, err)
		}
	}
}
