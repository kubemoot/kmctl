package memorywrite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kubemoot/kubemoot/operator/pkg/crewmemory"
	"github.com/kubemoot/kubemoot/operator/pkg/crewmemory/natskv"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// startNATS runs an in-process NATS server with JetStream and the crew-memory
// bucket, as the Kubemoot operator creates it, and returns a connection to it.
func startNATS(t *testing.T) *nats.Conn {
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
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.CreateKeyValue(context.Background(), jetstream.KeyValueConfig{Bucket: crewmemory.Bucket}); err != nil {
		t.Fatal(err)
	}
	return nc
}

func TestNATSStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	nc := startNATS(t)
	st, err := OpenNATS(ctx, nc)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := natskv.Open(nc)
	if err != nil {
		t.Fatal(err)
	}
	addEditAndRead(t, st, reader)
	if _, err := Delete(ctx, st, gpuFact(""), false); err != nil {
		t.Fatal(err)
	}
	if _, err := Get(st, gpuFact("")); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete err = %v, want ErrNotFound", err)
	}
}

func addEditAndRead(t *testing.T, st Store, reader crewmemory.EntryReader) {
	t.Helper()
	ctx := context.Background()
	if _, err := Add(ctx, st, gpuFact("one"), t0, false); err != nil {
		t.Fatal(err)
	}
	if _, err := Add(ctx, st, gpuFact("again"), t0, false); !errors.Is(err, ErrExists) {
		t.Fatalf("second add err = %v, want ErrExists", err)
	}
	if _, err := Edit(ctx, st, gpuFact("two"), t1, false); err != nil {
		t.Fatal(err)
	}
	facts, err := Facts(reader, scope)
	if err != nil || len(facts) != 1 || facts[0].Value != "two" || !facts[0].LearnedAt.Equal(t0) {
		t.Fatalf("facts = %+v, %v", facts, err)
	}
}

// Get reads one fact's entry, for the tests.
func Get(st Store, f Fact) (Entry, error) { return st.Get(context.Background(), f.StoreKey()) }

func TestNATSEditLosesToAConcurrentChange(t *testing.T) {
	ctx := context.Background()
	st, err := OpenNATS(ctx, startNATS(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Add(ctx, st, gpuFact("one"), t0, false); err != nil {
		t.Fatal(err)
	}
	cur, err := st.Get(ctx, gpuFact("").StoreKey())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Edit(ctx, st, gpuFact("theirs"), t1, false); err != nil {
		t.Fatal(err)
	}
	if err := st.Update(ctx, gpuFact("").StoreKey(), []byte("{}"), cur.Revision); !errors.Is(err, ErrChanged) {
		t.Fatalf("stale update err = %v, want ErrChanged", err)
	}
}

func TestNATSClearKeepsOtherCrews(t *testing.T) {
	ctx := context.Background()
	nc := startNATS(t)
	st, err := OpenNATS(ctx, nc)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := natskv.Open(nc)
	if err != nil {
		t.Fatal(err)
	}
	others := []Fact{
		{Scope: mustScope("crew-demo", "demo-prose"), Topic: "a", Key: "b", Value: "x"},
		{Scope: mustScope("other-ns", "demo"), Topic: "a", Key: "b", Value: "x"},
	}
	addAll(t, st, append(others, gpuFact("one"), Fact{Scope: scope, Topic: "a", Key: "b", Value: "y"}))
	if _, err := Clear(ctx, st, scope, 2, false); err != nil {
		t.Fatal(err)
	}
	if facts, err := Facts(reader, scope); err != nil || len(facts) != 0 {
		t.Fatalf("crew facts after clear = %+v, %v", facts, err)
	}
	for _, o := range others {
		if facts, err := Facts(reader, o.Scope); err != nil || len(facts) != 1 {
			t.Errorf("%s/%s lost its facts: %+v, %v", o.Scope.Namespace, o.Scope.Crew, facts, err)
		}
	}
}

func addAll(t *testing.T, st Store, facts []Fact) {
	t.Helper()
	for _, f := range facts {
		if _, err := Add(context.Background(), st, f, t0, false); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpenNATSWithoutTheBucketFails(t *testing.T) {
	srv, err := server.NewServer(&server.Options{Port: -1, JetStream: true, StoreDir: t.TempDir(), NoLog: true, NoSigs: true})
	if err != nil {
		t.Fatal(err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(10 * time.Second) {
		t.Fatal("NATS server did not start")
	}
	defer srv.Shutdown()
	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()
	if _, err := OpenNATS(context.Background(), nc); err == nil {
		t.Fatal("want an error for a missing bucket")
	}
}
