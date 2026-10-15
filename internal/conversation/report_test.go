package conversation

import (
	"strings"
	"testing"
)

func TestFollowUp(t *testing.T) {
	got := FollowUp("homelab-pilot", "crew-homelab-pilot", "6733c361-aa0c-4bde-85c6-e143af23b251")
	want := `kmctl conversation ask homelab-pilot "<next question>" -n crew-homelab-pilot --conversation-id 6733c361-aa0c-4bde-85c6-e143af23b251`
	if got != want {
		t.Errorf("FollowUp = %q\nwant %q", got, want)
	}
	if got := FollowUp("demo", "", "c-1"); strings.Contains(got, " -n ") {
		t.Errorf("no namespace, no -n: %q", got)
	}
}

func TestSummary_OmitsWhatTheStreamLacks(t *testing.T) {
	md := NewTracker(testStart).Summarize(SinceAsk).Markdown()
	want := "**How this answer was made**\n\n- Total time (as received by kmctl): 0.0s\n"
	if md != want {
		t.Errorf("an empty discussion summarizes as just the time:\n%q\nwant\n%q", md, want)
	}
	for _, absent := range []string{"Tokens", "Tool calls", "Retries", "Agents:", "Signals:", "Models:"} {
		if strings.Contains(md, absent) {
			t.Errorf("summary shows %q without data", absent)
		}
	}
}

func TestOrderedKinds(t *testing.T) {
	got := orderedKinds(map[string]int{"zeta": 1, signalStandAside: 1, signalAgree: 2, "alpha": 1, signalBlock: 1})
	want := []string{signalAgree, signalBlock, signalStandAside, "alpha", "zeta"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("orderedKinds = %v, want %v", got, want)
	}
}

func TestSummary_WatchLabelsItsClock(t *testing.T) {
	md := NewTracker(testStart).Summarize(SinceWatch).Markdown()
	if !strings.Contains(md, "Time since watch began (as received by kmctl)") {
		t.Errorf("a watch says its clock starts when it attached:\n%s", md)
	}
}
