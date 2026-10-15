package conversation

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kubemoot/kmctl/internal/discussion"
)

// summaryWidth is the most characters an agent's one-line summary shows.
const summaryWidth = 120

// Artifact is a contribution an agent spilled to the crew's artifact store; the
// discussion carries only a reference line naming its key and size.
type Artifact struct {
	Key   string `json:"key"`
	Bytes int64  `json:"bytes"`
}

// artifactMarker matches the reference line the agent runtime writes in place of
// a spilled contribution: "[ARTIFACT key=<key> bytes=<n> ...]".
var artifactMarker = regexp.MustCompile(`\[ARTIFACT key=(\S+) bytes=(\d+)`)

// artifactOf is the artifact a signal names: from its metadata when the gateway
// forwards it, else from the reference line in its text.
func artifactOf(m *discussion.SignalMeta, text string) *Artifact {
	if m != nil && m.Artifact != nil && m.Artifact.Key != "" {
		return &Artifact{Key: m.Artifact.Key, Bytes: m.Artifact.Bytes}
	}
	return parseArtifact(text)
}

// parseArtifact finds an artifact reference in text, or returns nil.
func parseArtifact(text string) *Artifact {
	m := artifactMarker.FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	n, err := strconv.ParseInt(m[2], 10, 64)
	if err != nil {
		return nil
	}
	return &Artifact{Key: m[1], Bytes: n}
}

// describe is how an artifact reads in an agent's summary.
func (a *Artifact) describe() string {
	return fmt.Sprintf("attached artifact (%s)", formatBytes(a.Bytes))
}

// shownSummary is an agent's summary as the terminal shows it: an artifact also
// points to its fetch command, which the summary at the end lists.
func shownSummary(a *Agent) string {
	if a.Artifact != nil {
		return a.Summary + "; fetch command below"
	}
	return a.Summary
}

// formatBytes renders a size in B, KB, or MB with one decimal (1 KB = 1024 B).
func formatBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}

// shortSummary is the first paragraph of text on one line, cut at a word
// boundary to at most width characters with "..." when it is longer.
func shortSummary(text string, width int) string {
	para, _, _ := strings.Cut(strings.TrimSpace(text), "\n\n")
	line := strings.Join(strings.Fields(para), " ")
	runes := []rune(line)
	if len(runes) <= width {
		return line
	}
	cut := string(runes[:width-3])
	if i := strings.LastIndexByte(cut, ' '); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,;:.") + "..."
}

// formatDuration renders a duration as seconds with one decimal under a minute,
// else as minutes and whole seconds.
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	d = d.Round(time.Second)
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// millis is a duration in whole milliseconds.
func millis(d time.Duration) int64 { return d.Milliseconds() }
