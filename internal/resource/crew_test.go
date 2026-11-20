package resource

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

func crewWith(name string, status map[string]any) unstructured.Unstructured {
	return unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": name, "namespace": "ns"},
		"status":   status,
	}}
}

func capacity(status, reason, message string) []any {
	return []any{map[string]any{"type": "CapacityAvailable", "status": status, "reason": reason, "message": message}}
}

func crewFixtures() []unstructured.Unstructured {
	return []unstructured.Unstructured{
		crewWith("available", map[string]any{"ready": true, "phase": "Ready", "coordinatorRef": "available-coordinator", "agentCount": int64(3),
			"conditions": capacity("True", "ModelAvailable", "")}),
		crewWith("waiting", map[string]any{"ready": true, "phase": "WaitingForCapacity", "coordinatorRef": "waiting-coordinator", "agentCount": int64(3),
			"conditions": capacity("False", "NoModelAvailable", "no model in the cluster catalog (2 available)")}),
		crewWith("old", map[string]any{"ready": true, "phase": "Ready", "coordinatorRef": "old-coordinator", "agentCount": int64(3)}),
	}
}

func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs:\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

// The Crew table shows CAPACITY as True, False, or <none> (a Kubemoot without the
// condition); -o wide adds the reason. AGE is dropped here: it moves with the clock.
func TestCrewTableCapacity(t *testing.T) {
	k := Crew
	k.Columns = k.Columns[:len(k.Columns)-1]
	for name, render := range map[string]func(*bytes.Buffer) error{
		"crew-table.golden": func(b *bytes.Buffer) error { return RenderTable(b, k, crewFixtures(), false) },
		"crew-wide.golden":  func(b *bytes.Buffer) error { return RenderTableWide(b, k, crewFixtures(), false) },
	} {
		var buf bytes.Buffer
		if err := render(&buf); err != nil {
			t.Fatal(err)
		}
		assertGolden(t, name, buf.Bytes())
	}
}

func TestCrewDetails(t *testing.T) {
	retention := map[string]any{
		"lastRunTime": "2026-11-20T01:00:00Z",
		"removed":     map[string]any{"conversations": int64(4), "memory": int64(0), "other": int64(12)},
		"error":       "",
	}
	valid := map[string]any{"type": "RetentionValid", "status": "True", "reason": "FinerThanSchedule", "message": "conversations.window 12h is finer than the daily schedule"}
	cases := map[string]map[string]any{
		"crew-details-waiting.golden":   {"conditions": append(capacity("False", "NoModelAvailable", "no model in the cluster catalog (2 available)"), valid), "retention": retention},
		"crew-details-available.golden": {"conditions": capacity("True", "ModelAvailable", "")},
		"crew-details-old.golden":       {"ready": true, "phase": "Ready"},
		"crew-details-no-run.golden":    {"conditions": []any{valid}},
		"crew-details-error.golden":     {"retention": map[string]any{"error": "nats unavailable"}},
	}
	for name, status := range cases {
		var buf bytes.Buffer
		if err := RenderCrewDetails(&buf, crewWith("c", status).Object); err != nil {
			t.Fatal(err)
		}
		assertGolden(t, name, buf.Bytes())
	}
}
