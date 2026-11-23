package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kubemoot/operator/pkg/crewmemory/ompi"
	"github.com/kubemoot/kubemoot/operator/pkg/crewscope"
)

// fakeNATS is a NATS server that answers the one JetStream request the bucket
// open makes: the stream info of the crew-memory bucket. bucketJSON is its reply.
type fakeNATS struct {
	url        string
	bucketJSON string
}

const (
	bucketMissing = `{"type":"io.nats.jetstream.api.v1.stream_info_response","error":{"code":404,"err_code":10059,"description":"stream not found"}}`
	bucketPresent = `{"type":"io.nats.jetstream.api.v1.stream_info_response","config":{"name":"KV_kubemoot_crew_memory",` +
		`"subjects":["$KV.kubemoot_crew_memory.>"],"retention":"limits","max_consumers":-1,"max_msgs":-1,"max_bytes":-1,` +
		`"max_age":0,"max_msgs_per_subject":1,"max_msg_size":-1,"storage":"file","discard":"new","num_replicas":1,` +
		`"allow_rollup_hdrs":true,"deny_delete":true,"allow_direct":true},"created":"2026-01-01T00:00:00Z",` +
		`"state":{"messages":0,"bytes":0,"first_seq":0,"last_seq":0,"consumer_count":0},"cluster":{}}`
)

func startFakeNATS(t *testing.T, bucketJSON string) *fakeNATS {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	s := &fakeNATS{url: "nats://" + ln.Addr().String(), bucketJSON: bucketJSON}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

// serve speaks the few protocol lines a client needs: INFO, PING/PONG, SUB, and a
// PUB with a reply subject, which gets the bucket reply.
func (s *fakeNATS) serve(c net.Conn) {
	defer func() { _ = c.Close() }()
	_, _ = io.WriteString(c, `INFO {"server_id":"fake","version":"2.10.0","proto":1,"headers":true,"jetstream":true,"max_payload":1048576}`+"\r\n")
	r := bufio.NewReader(c)
	sid := ""
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		f := strings.Fields(line)
		switch {
		case len(f) == 0:
		case f[0] == "PING":
			_, _ = io.WriteString(c, "PONG\r\n")
		case f[0] == "SUB" && len(f) >= 3:
			sid = f[len(f)-1]
		case f[0] == "PUB" || f[0] == "HPUB":
			s.answerPublish(c, r, f, sid)
		}
	}
}

func (s *fakeNATS) answerPublish(c net.Conn, r *bufio.Reader, f []string, sid string) {
	var size int
	if _, err := fmt.Sscan(f[len(f)-1], &size); err != nil {
		return
	}
	if _, err := io.ReadFull(r, make([]byte, size+2)); err != nil {
		return
	}
	// PUB <subject> [reply] <size>; HPUB <subject> [reply] <header size> <size>.
	replyAt := map[string]int{"PUB": 4, "HPUB": 5}[f[0]]
	if len(f) != replyAt {
		return // no reply subject
	}
	reply := f[2]
	_, _ = fmt.Fprintf(c, "MSG %s %s %d\r\n%s\r\n", reply, sid, len(s.bucketJSON), s.bucketJSON)
}

func TestOpenMemoryBindsTheBucket(t *testing.T) {
	s := startFakeNATS(t, bucketPresent)
	reader, closeFn, err := openMemory(context.Background(), client.NewFactory(), memoryOptions{natsURL: s.url})
	if err != nil {
		t.Fatalf("openMemory: %v", err)
	}
	if reader == nil {
		t.Error("no reader for a bucket that exists")
	}
	closeFn()
}

func TestOpenMemoryNamesAMissingBucket(t *testing.T) {
	s := startFakeNATS(t, bucketMissing)
	_, _, err := openMemory(context.Background(), client.NewFactory(), memoryOptions{natsURL: s.url})
	if err == nil || !strings.Contains(err.Error(), "could not open the crew-memory bucket") {
		t.Fatalf("error = %v, want the missing bucket named", err)
	}
}

func TestOpenMemoryWithAnUnreadableCredentialsFileIsAnError(t *testing.T) {
	s := startFakeNATS(t, bucketPresent)
	o := memoryOptions{natsURL: s.url, creds: filepath.Join(t.TempDir(), "missing.creds")}
	if _, _, err := openMemory(context.Background(), client.NewFactory(), o); err == nil ||
		!strings.Contains(err.Error(), "could not connect to NATS") {
		t.Fatalf("error = %v, want a connection failure", err)
	}
}

func TestNATSEndpointPrefersTheURLFlag(t *testing.T) {
	url, stop, err := natsEndpoint(context.Background(), client.NewFactory(), memoryOptions{natsURL: "nats://example:4222"})
	if err != nil || url != "nats://example:4222" {
		t.Fatalf("natsEndpoint = %q, %v", url, err)
	}
	stop()
}

func TestNATSEndpointWithoutAUsableKubeconfigIsAnError(t *testing.T) {
	f := client.NewFactory()
	missing := filepath.Join(t.TempDir(), "no-such-kubeconfig")
	f.ConfigFlags.KubeConfig = &missing
	if _, _, err := natsEndpoint(context.Background(), f, memoryOptions{}); err == nil {
		t.Fatal("expected an error when the kubeconfig does not exist")
	}
}

// With no NATS Service in the cluster the port-forward cannot open; the error names
// the Service and how to get past it.
func TestMemoryExportNamesTheNATSServiceItCouldNotReach(t *testing.T) {
	s := newFakeAPIServer(t)
	_, _, err := s.kmctl(t, "memory", "export", "demo", "--dir", filepath.Join(t.TempDir(), "out"),
		"--nats-namespace", "queues", "--nats-service", "bus")
	if err == nil || !strings.Contains(err.Error(), "queues/bus") {
		t.Fatalf("error = %v, want service queues/bus named", err)
	}
}

// failAfter is a writer that accepts n writes and then fails.
type failAfter struct{ n int }

func (w *failAfter) Write(p []byte) (int, error) {
	if w.n <= 0 {
		return 0, errors.New("pipe closed")
	}
	w.n--
	return len(p), nil
}

func TestReportExportStopsWhenStdoutFails(t *testing.T) {
	scope, err := crewscope.New("crew-demo", "demo")
	if err != nil {
		t.Fatal(err)
	}
	exp := ompi.Export{Skipped: []ompi.Omitted{{Key: "k", Reason: "holds a credential"}}}
	for writes := 0; writes < 2; writes++ {
		if err := reportExport(&failAfter{n: writes}, exp, scope, "out"); err == nil {
			t.Errorf("no error when write %d fails", writes+1)
		}
	}
	if err := reportExport(&failAfter{n: 2}, exp, scope, "out"); err != nil {
		t.Errorf("error with a working writer: %v", err)
	}
}
