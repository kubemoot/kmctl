package conversation

import (
	"strings"
	"testing"
	"time"
)

func TestParseArtifact(t *testing.T) {
	a := parseArtifact("[ARTIFACT key=ns/crew/t1/agent/agree-1 bytes=8499 - the FULL data is in the file]")
	if a == nil || a.Key != "ns/crew/t1/agent/agree-1" || a.Bytes != 8499 {
		t.Fatalf("parseArtifact = %+v", a)
	}
	for _, text := range []string{"", "plain text", "[ARTIFACT key=x]", "[ARTIFACT key=x bytes=99999999999999999999]"} {
		if got := parseArtifact(text); got != nil {
			t.Errorf("parseArtifact(%q) = %+v, want nil", text, got)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	cases := map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KB", 8499: "8.3 KB", 3 * 1024 * 1024: "3.0 MB"}
	for n, want := range cases {
		if got := formatBytes(n); got != want {
			t.Errorf("formatBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestShortSummary(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"short", "short"},
		{"first\nline\n\nsecond paragraph", "first line"},
		{"one two three four five", "one two..."},
		{"abcdefghijklmnop", "abcdefghijk..."},
		{"one, two, three", "one, two..."},
		{"one, two, six", "one, two, six"},
	}
	for _, c := range cases {
		if got := shortSummary(c.in, 14); got != c.want {
			t.Errorf("shortSummary(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestShortSummary_NeverCutsMidWord(t *testing.T) {
	text := strings.Repeat("deliberation ", 30)
	got := shortSummary(text, summaryWidth)
	if !strings.HasSuffix(got, "deliberation...") || len(got) > summaryWidth {
		t.Errorf("summary %q is cut mid-word or too long", got)
	}
}

func TestShortSummary_Multibyte(t *testing.T) {
	got := shortSummary(strings.Repeat("\u00e9", 40), 10)
	if got != strings.Repeat("\u00e9", 7)+"..." {
		t.Errorf("shortSummary cut a rune: %q", got)
	}
}

func TestFormatDuration(t *testing.T) {
	cases := map[time.Duration]string{
		0:                                      "0.0s",
		1500 * time.Millisecond:                "1.5s",
		59 * time.Second:                       "59.0s",
		62 * time.Second:                       "1m02s",
		10*time.Minute + 5400*time.Millisecond: "10m05s",
	}
	for d, want := range cases {
		if got := formatDuration(d); got != want {
			t.Errorf("formatDuration(%s) = %q, want %q", d, got, want)
		}
	}
}
