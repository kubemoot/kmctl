package conversation

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "rewrite the golden files")

var testStart = time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)

// tickingClock returns a clock that starts at testStart and advances one second
// on every reading, so recorded streams render with stable durations.
func tickingClock() func() time.Time {
	now := testStart
	return func() time.Time {
		now = now.Add(time.Second)
		return now
	}
}

func testOptions(mode Mode) Options {
	return Options{
		Mode: mode, Crew: "homelab-pilot", Namespace: "crew-homelab-pilot",
		ConversationID: "6733c361-aa0c-4bde-85c6-e143af23b251", Announce: true,
		Start: testStart, Now: tickingClock(),
	}
}

func renderRecorded(t *testing.T, o Options) string {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "proxmox-nodes.sse"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out bytes.Buffer
	if err := Render(&out, f, o); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return out.String()
}

func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run go test -update to create it): %v", err)
	}
	if got != string(want) {
		t.Errorf("%s differs from the golden file\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func TestRender_PlainGolden(t *testing.T) {
	assertGolden(t, "proxmox-nodes.plain.golden", renderRecorded(t, testOptions(ModePlain)))
}

func TestRender_JSONGolden(t *testing.T) {
	assertGolden(t, "proxmox-nodes.json.golden", renderRecorded(t, testOptions(ModeJSON)))
}

func TestRender_JSONLinesParse(t *testing.T) {
	lines := strings.Split(strings.TrimSpace(renderRecorded(t, testOptions(ModeJSON))), "\n")
	var first, last map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("first line is not JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatalf("last line is not JSON: %v", err)
	}
	if first["type"] != "conversation" || last["type"] != "result" {
		t.Errorf("first and last lines: %v / %v", first["type"], last["type"])
	}
	if !strings.HasPrefix(last["answer"].(string), "## Proxmox nodes") {
		t.Errorf("result answer is not the raw markdown: %v", last["answer"])
	}
	for _, line := range lines {
		if strings.Contains(line, `"type":"heartbeat"`) {
			t.Errorf("heartbeats are not written: %s", line)
		}
		if !strings.Contains(line, `"schemaVersion":1`) {
			t.Errorf("line has no schema version: %s", line)
		}
	}
}

func TestRender_PlainEndsWithSummaryAfterNotesThenFollowUp(t *testing.T) {
	out := renderRecorded(t, testOptions(ModePlain))
	notes := strings.Index(out, "**Notes:**")
	summary := strings.Index(out, "**How this answer was made**")
	if notes < 0 || summary < notes {
		t.Fatalf("the summary follows the answer's notes:\n%s", out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	want := `Next: kmctl conversation ask homelab-pilot "<next question>" -n crew-homelab-pilot --conversation-id 6733c361-aa0c-4bde-85c6-e143af23b251`
	if last := lines[len(lines)-1]; last != want {
		t.Errorf("last line = %q\nwant %q", last, want)
	}
	if strings.Contains(out, "late") {
		t.Error("an event after done is not shown")
	}
}

func TestRender_WatchDoesNotAnnounce(t *testing.T) {
	o := testOptions(ModePlain)
	o.Announce = false
	if out := renderRecorded(t, o); strings.HasPrefix(out, "conversation ") {
		t.Errorf("watch prints no conversation line:\n%s", out)
	}
}

func TestRender_QuietPrintsOnlyAnswer(t *testing.T) {
	for _, mode := range Modes {
		o := testOptions(mode)
		o.Quiet = true
		out := renderRecorded(t, o)
		if !strings.HasPrefix(out, "## Proxmox nodes") || strings.Contains(out, "Next:") {
			t.Errorf("%s --quiet prints only the answer, got:\n%s", mode, out)
		}
	}
}

func TestRender_ErrorEventEndsStream(t *testing.T) {
	stream := "data: {\"type\":\"connected\"}\n\ndata: {\"type\":\"error\",\"error\":\"Discussion stream not available\"}\n\n"
	var out bytes.Buffer
	if err := Render(&out, strings.NewReader(stream), testOptions(ModePlain)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"error: Discussion stream not available", "Errors: Discussion stream not available", "Next: "} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "ANSWER:") {
		t.Error("no answer section without an answer")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestRender_WriteErrorsSurface(t *testing.T) {
	stream := "data: {\"type\":\"connected\"}\n\ndata: {\"type\":\"done\"}\n\n"
	for _, mode := range []Mode{ModePlain, ModeJSON} {
		if err := Render(failingWriter{}, strings.NewReader(stream), testOptions(mode)); err == nil {
			t.Errorf("%s: a failed write is an error", mode)
		}
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("stream reset") }

func TestRender_ReadErrorsSurface(t *testing.T) {
	var out bytes.Buffer
	if err := Render(&out, failingReader{}, testOptions(ModePlain)); err == nil || !strings.Contains(err.Error(), "stream reset") {
		t.Errorf("a failed read is returned, got %v", err)
	}
}
