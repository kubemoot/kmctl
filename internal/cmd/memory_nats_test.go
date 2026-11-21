package cmd

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kubemoot/operator/pkg/crewmemory"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// natsURL starts an in-process NATS server with JetStream and the crew-memory
// bucket, as the Kubemoot operator creates it, and returns its URL.
func natsURL(t *testing.T) string {
	t.Helper()
	srv, err := server.NewServer(&server.Options{Port: -1, JetStream: true, StoreDir: t.TempDir(), NoLog: true, NoSigs: true})
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("NATS server did not start")
	}
	t.Cleanup(srv.Shutdown)
	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.CreateKeyValue(context.Background(), jetstream.KeyValueConfig{Bucket: crewmemory.Bucket}); err != nil {
		t.Fatal(err)
	}
	return srv.ClientURL()
}

// kmctlNATS runs a real kmctl command line against the NATS server at url.
func kmctlNATS(t *testing.T, url string, args ...string) (string, error) {
	t.Helper()
	f := client.NewFactory()
	ns := memNamespace
	f.ConfigFlags.Namespace = &ns
	root := newRootCommand(f)
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(append(args, "--nats-url", url))
	err := root.Execute()
	return out.String(), err
}

// The write commands and the export go through the same NATS connection code and the
// same bucket: what add and edit write, export reads back, and clear removes.
func TestMemoryWritesAreReadBackByExportOverRealNATS(t *testing.T) {
	url := natsURL(t)
	if _, err := kmctlNATS(t, url, "memory", "add", "demo", "hardware", "gpu0", "RTX 5090"); err != nil {
		t.Fatal(err)
	}
	if _, err := kmctlNATS(t, url, "memory", "edit", "demo", "hardware", "gpu0", "RTX 5090, 32 GiB"); err != nil {
		t.Fatal(err)
	}
	if _, err := kmctlNATS(t, url, "memory", "add", "demo", "hardware", "gpu0", "again"); err == nil {
		t.Fatal("a second add must fail")
	}
	dir := filepath.Join(t.TempDir(), "out")
	out, err := kmctlNATS(t, url, "memory", "export", "demo", "-o", dir)
	if err != nil || !strings.Contains(out, "wrote 1 memory object(s)") {
		t.Fatalf("export: %v\n%s", err, out)
	}
	if out, err := kmctlNATS(t, url, "memory", "clear", "demo", "--yes"); err != nil || !strings.Contains(out, "1 fact(s)") {
		t.Fatalf("clear: %v\n%s", err, out)
	}
	if out, err := kmctlNATS(t, url, "memory", "clear", "demo", "--yes"); err != nil || !strings.Contains(out, "holds no facts") {
		t.Fatalf("second clear: %v\n%s", err, out)
	}
}

func TestMemoryWriteToAnUnreachableNATSIsAnError(t *testing.T) {
	_, err := kmctlNATS(t, "nats://127.0.0.1:1", "memory", "add", "demo", "t", "k", "v")
	if err == nil || !strings.Contains(explained(t, err), "could not connect to NATS") {
		t.Fatalf("err = %v", err)
	}
}
