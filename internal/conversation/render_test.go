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
	return renderFile(t, "proxmox-nodes.sse", o)
}

// renderFile renders the recorded stream in testdata/name.
func renderFile(t *testing.T, name string, o Options) string {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
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
	out := renderRecorded(t, testOptions(ModeQuiet))
	if !strings.HasPrefix(out, "## Proxmox nodes") || strings.Contains(out, "Next:") {
		t.Errorf("quiet prints only the answer, got:\n%s", out)
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

// A gateway that forwards timestamps, signal metadata, and the subcommittee: the step
// times come from the published timestamps, and the summary gains the crew time, the
// subcommittee, tokens, models, the tool error, and the artifact's fetch command.
func TestRender_StampedPlainGolden(t *testing.T) {
	assertGolden(t, "proxmox-nodes-stamped.plain.golden", renderFile(t, "proxmox-nodes-stamped.sse", testOptions(ModePlain)))
}

func TestRender_StampedJSONGolden(t *testing.T) {
	assertGolden(t, "proxmox-nodes-stamped.json.golden", renderFile(t, "proxmox-nodes-stamped.sse", testOptions(ModeJSON)))
}

// stampedSummary is the summary of the stamped recording, as -o json writes it.
func stampedSummary(t *testing.T) Summary {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(renderFile(t, "proxmox-nodes-stamped.sse", testOptions(ModeJSON))), "\n")
	var result struct {
		Summary Summary `json:"summary"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &result); err != nil {
		t.Fatal(err)
	}
	return result.Summary
}

func TestRender_StampedSummaryTimesAndTokens(t *testing.T) {
	s := stampedSummary(t)
	if s.TimeSource != "published" || s.CrewSeconds == nil || *s.CrewSeconds != 31 {
		t.Errorf("times: source %q crew %v", s.TimeSource, s.CrewSeconds)
	}
	if s.InputTokens != 15740 || s.OutputTokens != 2059 {
		t.Errorf("tokens: %d in, %d out", s.InputTokens, s.OutputTokens)
	}
	proxmox := s.Agents[0]
	if proxmox.Name != "pilot-proxmox" || *proxmox.TriageSeconds != 1.5 || *proxmox.EvaluationSeconds != 10.4 || *proxmox.InferenceSeconds != 9.8 {
		t.Errorf("published step times: %+v", proxmox)
	}
}

func TestRender_StampedSummarySubcommitteeErrorsArtifacts(t *testing.T) {
	s := stampedSummary(t)
	sc := s.Subcommittee
	if sc == nil || len(sc.Members) != 3 || sc.Members[0].Reason != "node metrics" || sc.Members[2].Reason != "" {
		t.Errorf("subcommittee keeps the triage reasons of the agents still convened: %+v", sc)
	}
	if len(s.ToolErrors) != 1 || s.ToolErrors[0] != "pilot-storage: zfs_pool_status failed 3 times: connection refused" {
		t.Errorf("tool errors: %v", s.ToolErrors)
	}
	if len(s.Artifacts) != 1 || !strings.HasPrefix(s.Artifacts[0].Fetch, "kmctl conversation artifact homelab-pilot crew-homelab-pilot/") {
		t.Errorf("artifacts: %+v", s.Artifacts)
	}
}

func TestThousands(t *testing.T) {
	for n, want := range map[int64]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567", -1234: "-1,234"} {
		if got := thousands(n); got != want {
			t.Errorf("thousands(%d) = %q, want %q", n, got, want)
		}
	}
}

// An event other than a finding that names an artifact keeps it in -o json.
func TestRender_JSONKeepsTheArtifactOfAStandAside(t *testing.T) {
	stream := `data: {"type":"phase","agent":"a","status":"done","stood_aside":true,"artifact":{"key":"ns/c/t/a/x","bytes":5}}` + "\n\ndata: {\"type\":\"done\"}\n\n"
	var out bytes.Buffer
	if err := Render(&out, strings.NewReader(stream), testOptions(ModeJSON)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"artifact":{"key":"ns/c/t/a/x","bytes":5}`) {
		t.Errorf("artifact dropped:\n%s", out.String())
	}
}
