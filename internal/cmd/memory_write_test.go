package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/memorywrite"
	"github.com/kubemoot/kubemoot/operator/pkg/crewmemory"
	"github.com/kubemoot/kubemoot/operator/pkg/crewscope"
	"github.com/spf13/cobra"
)

// memBucket is an in-memory crew-memory bucket that is both the write side and the
// read-only reader, and counts the writes it receives.
type memBucket struct {
	data    map[string]memorywrite.Entry
	rev     uint64
	writes  int
	purged  []string
	failGet error
}

func newMemBucket(kv map[string]string) *memBucket {
	b := &memBucket{data: map[string]memorywrite.Entry{}}
	for k, v := range kv {
		b.rev++
		b.data[k] = memorywrite.Entry{Value: []byte(v), Revision: b.rev}
	}
	return b
}

func (b *memBucket) Get(_ context.Context, key string) (memorywrite.Entry, error) {
	if b.failGet != nil {
		return memorywrite.Entry{}, b.failGet
	}
	e, ok := b.data[key]
	if !ok {
		return memorywrite.Entry{}, memorywrite.ErrNotFound
	}
	return e, nil
}

func (b *memBucket) store(key string, v []byte) {
	b.rev++
	b.writes++
	b.data[key] = memorywrite.Entry{Value: v, Revision: b.rev}
}

func (b *memBucket) Create(_ context.Context, key string, v []byte) error {
	if _, ok := b.data[key]; ok {
		return memorywrite.ErrExists
	}
	b.store(key, v)
	return nil
}

func (b *memBucket) Update(_ context.Context, key string, v []byte, _ uint64) error {
	b.store(key, v)
	return nil
}

func (b *memBucket) Delete(_ context.Context, key string) error {
	b.writes++
	delete(b.data, key)
	return nil
}

func (b *memBucket) PurgePrefix(_ context.Context, prefix string) error {
	b.writes++
	b.purged = append(b.purged, prefix)
	for k := range b.data {
		if strings.HasPrefix(k, prefix) {
			delete(b.data, k)
		}
	}
	return nil
}

func (b *memBucket) ListKVKeys(string) ([]string, error) {
	keys := make([]string, 0, len(b.data))
	for k := range b.data {
		keys = append(keys, k)
	}
	return keys, nil
}

func (b *memBucket) GetKVEntry(_, key string) (crewmemory.Entry, error) {
	e := b.data[key]
	return crewmemory.Entry{Value: e.Value, Revision: e.Revision, Created: time.Now()}, nil
}

func opensBucket(b *memBucket) memoryStoreOpener {
	return func(context.Context, *client.Factory, memoryOptions) (memoryConn, func(), error) {
		return memoryConn{store: b, reader: b}, func() {}, nil
	}
}

func runMemoryWrite(t *testing.T, open memoryStoreOpener, stdin string, args ...string) (string, string, error) {
	t.Helper()
	build := func(root *cobra.Command, f *client.Factory) {
		mem := &cobra.Command{Use: "memory"}
		addMemoryWriteCommands(mem, f, open)
		root.AddCommand(mem)
	}
	return runWrite(t, stdin, build, append([]string{"memory"}, args...)...)
}

const (
	gpuKey  = "crew-demo.demo.hardware.gpu0"
	gpuJSON = `{"value":"RTX 5090","learnedBy":"demo-specialist-0","learnedAt":"2026-10-30T10:00:00Z"}`
)

func storedValue(t *testing.T, b *memBucket, key string) crewmemory.Fact {
	t.Helper()
	scope, _ := crewscope.New("crew-demo", "demo")
	f, err := crewmemory.ParseFact(scope, key, crewmemory.Entry{Value: b.data[key].Value})
	if err != nil {
		t.Fatalf("the operator's parser rejects %s: %v", key, err)
	}
	return f
}

func TestMemoryAddStoresAFact(t *testing.T) {
	b := newMemBucket(nil)
	out, _, err := runMemoryWrite(t, opensBucket(b), "", "add", "demo", "hardware", "gpu0", "RTX 5090")
	if err != nil || !strings.Contains(out, "fact "+gpuKey+" added") {
		t.Fatalf("add: %v\n%s", err, out)
	}
	if f := storedValue(t, b, gpuKey); f.Value != "RTX 5090" || f.LearnedBy != "kmctl" {
		t.Errorf("fact = %+v", f)
	}
}

func TestMemoryAddOfAnExistingFactPointsToEdit(t *testing.T) {
	b := newMemBucket(map[string]string{gpuKey: gpuJSON})
	_, _, err := runMemoryWrite(t, opensBucket(b), "", "add", "demo", "hardware", "gpu0", "x")
	if err == nil || !strings.Contains(explained(t, err), "kmctl memory edit") {
		t.Fatalf("err = %v", err)
	}
	if b.writes != 0 {
		t.Error("add changed an existing fact")
	}
}

func TestMemoryEditChangesTheValueAndKeepsLearnedAt(t *testing.T) {
	b := newMemBucket(map[string]string{gpuKey: gpuJSON})
	if _, _, err := runMemoryWrite(t, opensBucket(b), "", "edit", "demo", "hardware", "gpu0", "RTX 5090, water cooled"); err != nil {
		t.Fatal(err)
	}
	f := storedValue(t, b, gpuKey)
	if f.Value != "RTX 5090, water cooled" || f.LearnedAt.Format(time.RFC3339) != "2026-10-30T10:00:00Z" {
		t.Errorf("fact = %+v", f)
	}
}

func TestMemoryEditOfAMissingFactPointsToAdd(t *testing.T) {
	b := newMemBucket(nil)
	_, _, err := runMemoryWrite(t, opensBucket(b), "", "edit", "demo", "hardware", "gpu0", "x")
	if err == nil || !strings.Contains(explained(t, err), "kmctl memory add") {
		t.Fatalf("err = %v", err)
	}
}

func TestMemoryValueFromFileAndStdin(t *testing.T) {
	file := filepath.Join(t.TempDir(), "v.txt")
	if err := os.WriteFile(file, []byte("from a file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := newMemBucket(nil)
	if _, _, err := runMemoryWrite(t, opensBucket(b), "", "add", "demo", "notes", "a", "--from-file", file); err != nil {
		t.Fatal(err)
	}
	if got := storedValue(t, b, "crew-demo.demo.notes.a").Value; got != "from a file" {
		t.Errorf("value = %q", got)
	}
	if _, _, err := runMemoryWrite(t, opensBucket(b), "from stdin\n", "add", "demo", "notes", "b", "--from-file", "-"); err != nil {
		t.Fatal(err)
	}
	if got := storedValue(t, b, "crew-demo.demo.notes.b").Value; got != "from stdin" {
		t.Errorf("value = %q", got)
	}
}

func TestMemoryValueMustComeFromOneSource(t *testing.T) {
	b := newMemBucket(nil)
	for _, args := range [][]string{
		{"add", "demo", "t", "k"},
		{"add", "demo", "t", "k", "v", "--from-file", "x"},
		{"add", "demo", "t", "k", "   "},
		{"add", "demo", "t"},
		{"edit", "demo", "t", "k", "v", "extra"},
	} {
		_, _, err := runMemoryWrite(t, opensBucket(b), "", args...)
		if err == nil {
			t.Errorf("%v: want an error", args)
		}
	}
	if b.writes != 0 {
		t.Error("a malformed command wrote")
	}
}

func TestMemoryRefusesACredential(t *testing.T) {
	b := newMemBucket(nil)
	secret := "Authorization: Bearer abcDEF1234567890abcDEF1234567890"
	_, _, err := runMemoryWrite(t, opensBucket(b), "", "add", "demo", "notes", "a", secret)
	if err == nil || !strings.Contains(explained(t, err), "credential") || strings.Contains(explained(t, err), "abcDEF") {
		t.Fatalf("err = %v", err)
	}
	if b.writes != 0 {
		t.Error("a credential was stored")
	}
	if _, _, err := runMemoryWrite(t, opensBucket(b), "", "add", "demo", "notes", "a", secret, "--allow-unscreened"); err != nil {
		t.Fatalf("--allow-unscreened: %v", err)
	}
}

func TestMemoryNoteWhenNamesAreChanged(t *testing.T) {
	b := newMemBucket(nil)
	out, _, err := runMemoryWrite(t, opensBucket(b), "", "add", "demo", "my topic", "a.b", "v")
	if err != nil || !strings.Contains(out, "crew-demo.demo.my_topic.a_b") || !strings.Contains(out, "note:") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestMemoryDryRunWritesNothing(t *testing.T) {
	b := newMemBucket(map[string]string{gpuKey: gpuJSON, "crew-demo.demo.notes.a": gpuJSON})
	for _, args := range [][]string{
		{"add", "demo", "hardware", "gpu1", "v", "--dry-run"},
		{"edit", "demo", "hardware", "gpu0", "v", "--dry-run"},
		{"delete", "demo", "hardware", "gpu0", "--dry-run"},
		{"clear", "demo", "--dry-run"},
	} {
		out, errOut, err := runMemoryWrite(t, opensBucket(b), "", args...)
		if err != nil || !strings.Contains(out, "would be") || errOut != "" {
			t.Fatalf("%v: %v\n%s\n%s", args, err, out, errOut)
		}
	}
	if b.writes != 0 || len(b.data) != 2 {
		t.Errorf("a dry run wrote: writes %d, facts %d", b.writes, len(b.data))
	}
}

func TestMemoryDeleteAsksFirst(t *testing.T) {
	b := newMemBucket(map[string]string{gpuKey: gpuJSON})
	_, errOut, err := runMemoryWrite(t, opensBucket(b), "no\n", "delete", "demo", "hardware", "gpu0")
	if err == nil || !strings.Contains(errOut, "Delete fact "+gpuKey+"?") || b.writes != 0 {
		t.Fatalf("declined: %v %q writes=%d", err, errOut, b.writes)
	}
	out, _, err := runMemoryWrite(t, opensBucket(b), "y\n", "delete", "demo", "hardware", "gpu0")
	if err != nil || !strings.Contains(out, "deleted") || len(b.data) != 0 {
		t.Fatalf("confirmed: %v\n%s", err, out)
	}
}

func TestMemoryDeleteOfAMissingFactNeedsNoQuestion(t *testing.T) {
	b := newMemBucket(nil)
	_, errOut, err := runMemoryWrite(t, opensBucket(b), "", "delete", "demo", "hardware", "gpu0")
	if err == nil || !strings.Contains(explained(t, err), "no such fact") || errOut != "" {
		t.Fatalf("err = %v %q", err, errOut)
	}
}

func TestMemoryClearPurgesOnlyThisCrewAfterAsking(t *testing.T) {
	b := newMemBucket(map[string]string{
		gpuKey: gpuJSON, "crew-demo.demo.notes.a": gpuJSON,
		"crew-demo.demo-prose.notes.a": gpuJSON, "other.demo.notes.a": gpuJSON,
	})
	_, errOut, err := runMemoryWrite(t, opensBucket(b), "n\n", "clear", "demo")
	if err == nil || !strings.Contains(errOut, "Delete all 2 fact(s) of crew demo in namespace crew-demo?") || b.writes != 0 {
		t.Fatalf("declined: %v %q", err, errOut)
	}
	out, _, err := runMemoryWrite(t, opensBucket(b), "", "clear", "demo", "--yes")
	if err != nil || !strings.Contains(out, "2 fact(s) of crew demo in namespace crew-demo cleared") {
		t.Fatalf("clear: %v\n%s", err, out)
	}
	if len(b.purged) != 1 || b.purged[0] != "crew-demo.demo." || len(b.data) != 2 {
		t.Errorf("purged %v, left %v", b.purged, b.data)
	}
}

func TestMemoryClearOfACrewWithoutFacts(t *testing.T) {
	b := newMemBucket(nil)
	out, _, err := runMemoryWrite(t, opensBucket(b), "", "clear", "demo")
	if err != nil || !strings.Contains(out, "holds no facts") || b.writes != 0 {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestMemoryWriteWithoutNATSIsAnError(t *testing.T) {
	open := func(context.Context, *client.Factory, memoryOptions) (memoryConn, func(), error) {
		return memoryConn{}, nil, errors.New("no route to NATS")
	}
	for _, args := range [][]string{{"add", "demo", "t", "k", "v"}, {"edit", "demo", "t", "k", "v"}, {"delete", "demo", "t", "k", "--yes"}, {"clear", "demo", "--yes"}} {
		if _, _, err := runMemoryWrite(t, open, "", args...); err == nil || !strings.Contains(err.Error(), "no route to NATS") {
			t.Errorf("%v: err = %v", args, err)
		}
	}
}

func TestMemoryStoreErrorIsExplained(t *testing.T) {
	b := newMemBucket(nil)
	b.failGet = errors.New("jetstream not enabled")
	_, _, err := runMemoryWrite(t, opensBucket(b), "", "add", "demo", "t", "k", "v")
	if err == nil || !strings.Contains(explained(t, err), "could not change the crew's memory") {
		t.Fatalf("err = %v", err)
	}
}

func TestMemoryInvalidCrewOrNamespaceIsAUsageError(t *testing.T) {
	_, _, err := runMemoryWrite(t, opensBucket(newMemBucket(nil)), "", "add", "bad.crew", "t", "k", "v")
	if err == nil || !isUsageError(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestMemoryCommandsAreOnTheMemoryNoun(t *testing.T) {
	root := newRootCommand(client.NewFactory())
	for _, verb := range []string{"export", "add", "edit", "delete", "clear"} {
		c, _, err := root.Find([]string{"memory", verb})
		if err != nil || c.Name() != verb || c.Example == "" || c.Flags().Lookup("nats-url") == nil {
			t.Errorf("memory %s: %v", verb, err)
		}
	}
	for _, verb := range []string{"add", "edit", "delete", "clear"} {
		c, _, _ := root.Find([]string{"memory", verb})
		if c.Flags().Lookup("dry-run") == nil {
			t.Errorf("memory %s has no --dry-run", verb)
		}
	}
}
