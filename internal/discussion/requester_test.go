package discussion

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

var moment = time.Date(2026, 10, 24, 16, 15, 30, 0, time.UTC)

func TestRequesterAtUsesTheZonesLocalTime(t *testing.T) {
	got := requesterAt(moment, "America/Los_Angeles")
	if got.TimeZone != "America/Los_Angeles" || got.LocalDateTime != "2026-10-24T09:15:30" {
		t.Fatalf("got %+v", got)
	}
}

// The date is the requester's, not the server's: 02:00 UTC is still the day before in Los Angeles.
func TestRequesterAtKeepsTheRequestersDate(t *testing.T) {
	got := requesterAt(time.Date(2026, 10, 25, 2, 0, 0, 0, time.UTC), "America/Los_Angeles")
	if got.LocalDateTime != "2026-10-24T19:00:00" {
		t.Fatalf("got %q", got.LocalDateTime)
	}
}

func TestRequesterAtWithoutAUsableZoneStillSendsTheLocalTime(t *testing.T) {
	for _, zone := range []string{"", "Local", "Mars/Base", "../etc/passwd"} {
		got := requesterAt(moment, zone)
		if got.TimeZone != "" || got.LocalDateTime != "2026-10-24T16:15:30" {
			t.Errorf("zone %q: got %+v", zone, got)
		}
	}
}

func TestZoneAfterZoneinfo(t *testing.T) {
	cases := map[string]string{
		"/usr/share/zoneinfo/America/New_York": "America/New_York",
		"../usr/share/zoneinfo/Europe/Paris":   "Europe/Paris",
		"/usr/share/zoneinfo/UTC":              "UTC",
		"/somewhere/else":                      "",
		"":                                     "",
	}
	for target, want := range cases {
		if got := zoneAfterZoneinfo(target); got != want {
			t.Errorf("%q: got %q, want %q", target, got, want)
		}
	}
}

func TestZoneFromLinkAndFile(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "localtime")
	if err := os.Symlink("/usr/share/zoneinfo/Asia/Tokyo", link); err != nil {
		t.Fatal(err)
	}
	if got := zoneFromLink(link); got != "Asia/Tokyo" {
		t.Errorf("link: got %q", got)
	}
	if got := zoneFromLink(filepath.Join(dir, "missing")); got != "" {
		t.Errorf("missing link: got %q", got)
	}
	file := filepath.Join(dir, "timezone")
	if err := os.WriteFile(file, []byte("Europe/Berlin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := zoneFromFile(file); got != "Europe/Berlin" {
		t.Errorf("file: got %q", got)
	}
	if got := zoneFromFile(filepath.Join(dir, "missing")); got != "" {
		t.Errorf("missing file: got %q", got)
	}
}

func TestLocalZoneNamePrefersTZ(t *testing.T) {
	t.Setenv("TZ", "America/Chicago")
	if got := LocalZoneName(); got != "America/Chicago" {
		t.Fatalf("got %q", got)
	}
	t.Setenv("TZ", ":Europe/London")
	if got := LocalZoneName(); got != "Europe/London" {
		t.Fatalf("leading colon: got %q", got)
	}
}

func TestLocalZoneNameIgnoresAnInvalidTZ(t *testing.T) {
	t.Setenv("TZ", "Not/AZone")
	got := LocalZoneName()
	if got == "Not/AZone" {
		t.Fatalf("an unknown TZ was used")
	}
}

func TestAskBodyCarriesTheRequesterWhenKnown(t *testing.T) {
	body := askBody("hi", "c1", Requester{TimeZone: "Asia/Tokyo", LocalDateTime: "2026-10-25T01:15:30"})
	if body["timeZone"] != "Asia/Tokyo" || body["localDateTime"] != "2026-10-25T01:15:30" ||
		body["message"] != "hi" || body["conversationId"] != "c1" {
		t.Fatalf("got %v", body)
	}
}

func TestAskBodyOmitsWhatIsUnknown(t *testing.T) {
	body := askBody("hi", "", Requester{})
	if _, ok := body["timeZone"]; ok {
		t.Errorf("timeZone present: %v", body)
	}
	if _, ok := body["localDateTime"]; ok {
		t.Errorf("localDateTime present: %v", body)
	}
}
