package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/clierr"
	"github.com/kubemoot/kubemoot/operator/pkg/crewmemory"
	"github.com/kubemoot/kubemoot/operator/pkg/crewmemory/ompi"
	"github.com/spf13/cobra"
)

const (
	memNamespace = "crew-demo"
	gpuFactKey   = "crew-demo.demo.hardware.gpu0"
	gpuFact      = `{"value":"RTX 5090","learnedBy":"demo-specialist-0","learnedAt":"2026-10-30T10:00:00Z"}`
)

// fakeBucket is a read-only reader over a map of key to stored JSON.
type fakeBucket map[string]string

func (b fakeBucket) ListKVKeys(string) ([]string, error) {
	keys := make([]string, 0, len(b))
	for k := range b {
		keys = append(keys, k)
	}
	return keys, nil
}

func (b fakeBucket) GetKVEntry(_, key string) (crewmemory.Entry, error) {
	return crewmemory.Entry{Value: []byte(b[key]), Revision: 4, Created: time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)}, nil
}

// deadBucket is a server that goes away while listing.
type deadBucket struct{ fakeBucket }

func (deadBucket) ListKVKeys(string) ([]string, error) { return nil, errors.New("server went away") }

func opening(r crewmemory.EntryReader) memoryOpener {
	return func(context.Context, *client.Factory, memoryOptions) (crewmemory.EntryReader, func(), error) {
		return r, func() {}, nil
	}
}

// runExport runs "memory export" with args under a root that carries the
// kubeconfig flags, in namespace memNamespace, and returns what it printed.
func runExport(t *testing.T, open memoryOpener, args ...string) (string, error) {
	t.Helper()
	f := client.NewFactory()
	ns := memNamespace
	f.ConfigFlags.Namespace = &ns
	root := &cobra.Command{Use: "kmctl", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(&cobra.Command{Use: "memory"})
	mem := newMemoryExportCommand(f, open)
	root.Commands()[0].AddCommand(mem)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"memory", "export"}, args...))
	err := root.Execute()
	return out.String(), err
}

func TestMemoryExportWritesTheOMPILayout(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mem")
	out, err := runExport(t, opening(fakeBucket{gpuFactKey: gpuFact}), "demo", "-o", dir, "--markdown")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "wrote 1 memory object(s) of crew-demo/demo to "+dir) {
		t.Errorf("output = %q", out)
	}
	for _, p := range []string{"manifest.json", "memories", "markdown/hardware/gpu0.md"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
}

func TestMemoryExportValidatesAgainstTheSchema(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mem")
	if _, err := runExport(t, opening(fakeBucket{gpuFactKey: gpuFact}), "demo", "-o", dir); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ompi.ValidateManifest(manifest); err != nil {
		t.Errorf("manifest does not validate: %v", err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "memories", "*"))
	if len(files) != 1 {
		t.Fatalf("memory files = %v, want one", files)
	}
	memory, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := ompi.ValidateMemory(memory); err != nil {
		t.Errorf("memory object does not validate: %v", err)
	}
	var obj struct {
		Scope []string `json:"scope"`
	}
	if err := json.Unmarshal(memory, &obj); err != nil {
		t.Fatal(err)
	}
	if len(obj.Scope) != 1 || obj.Scope[0] != "crew:crew-demo/demo" {
		t.Errorf("scope = %v, want [crew:crew-demo/demo]", obj.Scope)
	}
}

func TestMemoryExportOfAnEmptyBucketIsAnEmptyValidExport(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mem")
	out, err := runExport(t, opening(fakeBucket{}), "demo", "-o", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "wrote 0 memory object(s)") {
		t.Errorf("output = %q", out)
	}
	manifest, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil || ompi.ValidateManifest(manifest) != nil {
		t.Errorf("manifest = %s, err = %v", manifest, err)
	}
}

func TestMemoryExportLeavesOutOtherCrewsAndReportsBadEntries(t *testing.T) {
	bucket := fakeBucket{
		gpuFactKey:                    gpuFact,
		"crew-demo.demo.hardware.bad": "not json",
		"crew-demo.other.hardware.x":  `{"value":"theirs"}`,
	}
	out, err := runExport(t, opening(bucket), "demo", "-o", filepath.Join(t.TempDir(), "mem"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `left out "crew-demo.demo.hardware.bad"`) || !strings.Contains(out, "wrote 1 memory object(s)") {
		t.Errorf("output = %q", out)
	}
	if strings.Contains(out, "other") {
		t.Errorf("another crew's key was reported: %q", out)
	}
}

func TestMemoryExportFailsWhenTheServerCannotBeListed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mem")
	_, err := runExport(t, opening(deadBucket{}), "demo", "-o", dir)
	if err == nil || !strings.Contains(err.Error(), "could not export") {
		t.Fatalf("error = %v, want an export failure", err)
	}
	var ce *clierr.Error
	if !errors.As(err, &ce) || !strings.Contains(ce.Cause.Error(), "server went away") {
		t.Errorf("cause = %v, want the server error", err)
	}
	if _, statErr := os.Stat(dir); statErr == nil {
		t.Error("a directory was created although the server failed")
	}
}

func TestMemoryExportFailsWhenNATSCannotBeOpened(t *testing.T) {
	boom := errors.New("no route to NATS")
	open := func(context.Context, *client.Factory, memoryOptions) (crewmemory.EntryReader, func(), error) {
		return nil, nil, boom
	}
	dir := filepath.Join(t.TempDir(), "mem")
	if _, err := runExport(t, open, "demo", "-o", dir); !errors.Is(err, boom) {
		t.Errorf("error = %v, want the open error", err)
	}
	if _, statErr := os.Stat(dir); statErr == nil {
		t.Error("a directory was created although NATS was unreachable")
	}
}

func TestMemoryExportToAnUnreachableNATSURLIsAnError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mem")
	_, err := runExport(t, openMemory, "demo", "-o", dir, "--nats-url", "nats://127.0.0.1:1")
	if err == nil || !strings.Contains(err.Error(), "could not connect to NATS") {
		t.Fatalf("error = %v, want a connection failure", err)
	}
	if _, statErr := os.Stat(dir); statErr == nil {
		t.Error("a directory was created although NATS was unreachable")
	}
}

func TestMemoryExportRefusesANonEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	keep := filepath.Join(dir, "keep")
	if err := os.WriteFile(keep, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runExport(t, opening(fakeBucket{gpuFactKey: gpuFact}), "demo", "-o", dir); err == nil {
		t.Fatal("export into a non-empty directory succeeded")
	}
	if b, _ := os.ReadFile(keep); string(b) != "x" {
		t.Error("an existing file was changed")
	}
}

func TestMemoryExportDefaultsTheDirectoryToTheCrewName(t *testing.T) {
	var got memoryOptions
	open := func(_ context.Context, _ *client.Factory, o memoryOptions) (crewmemory.EntryReader, func(), error) {
		got = o
		return nil, nil, errors.New("stop here")
	}
	_, _ = runExport(t, open, "demo")
	want := "." + string(filepath.Separator) + "demo-memory"
	if got.out != want || got.namespace != memNamespace || got.crew != "demo" {
		t.Errorf("options = %+v, want out %q in namespace %s", got, want, memNamespace)
	}
}

func TestMemoryExportPassesItsFlagsOn(t *testing.T) {
	var got memoryOptions
	open := func(_ context.Context, _ *client.Factory, o memoryOptions) (crewmemory.EntryReader, func(), error) {
		got = o
		return nil, nil, errors.New("stop here")
	}
	_, _ = runExport(t, open, "demo", "-o", "x", "--markdown", "--origin", "o1", "--nats-url", "nats://h:1",
		"--creds", "c.creds", "--nats-namespace", "ns2", "--nats-service", "svc2")
	want := memoryOptions{
		namespace: memNamespace, crew: "demo", out: "x", origin: "o1", markdown: true,
		natsURL: "nats://h:1", creds: "c.creds", natsNamespace: "ns2", natsService: "svc2",
	}
	if got != want {
		t.Errorf("options = %+v, want %+v", got, want)
	}
}

func TestMemoryExportFlagDefaults(t *testing.T) {
	cmd := newMemoryExportCommand(client.NewFactory(), opening(fakeBucket{}))
	for flag, want := range map[string]string{"nats-namespace": "nats", "nats-service": "nats", "markdown": "false", "nats-url": ""} {
		if got := cmd.Flags().Lookup(flag).DefValue; got != want {
			t.Errorf("--%s default = %q, want %q", flag, got, want)
		}
	}
	if cmd.Flags().Lookup("output").Shorthand != "o" {
		t.Error("--output has no -o shorthand")
	}
}

func TestMemoryExportNeedsExactlyOneCrew(t *testing.T) {
	for _, args := range [][]string{{}, {"a", "b"}} {
		if _, err := runExport(t, opening(fakeBucket{}), args...); err == nil {
			t.Errorf("export %v succeeded, want an argument error", args)
		}
	}
}

func TestMemoryExportRejectsACrewNameThatIsNotOneToken(t *testing.T) {
	_, err := runExport(t, opening(fakeBucket{}), "a.b", "-o", filepath.Join(t.TempDir(), "mem"))
	if err == nil || !isUsageError(err) {
		t.Errorf("error = %v, want a usage error", err)
	}
}

func TestMemoryCommandIsOnTheRootAndHasExport(t *testing.T) {
	root := newRootCommand(client.NewFactory())
	cmd, _, err := root.Find([]string{"memory", "export"})
	if err != nil || cmd.Name() != "export" {
		t.Fatalf("Find(memory export) = %v, %v", cmd, err)
	}
	if cmd.Example == "" || cmd.Short == "" {
		t.Error("memory export has no short description or example")
	}
}
