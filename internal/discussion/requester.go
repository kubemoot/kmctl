package discussion

import (
	"os"
	"strings"
	"time"

	// The zone database ships inside kmctl, so a zone name resolves on a machine without one.
	_ "time/tzdata"
)

const localDateTimeLayout = "2006-01-02T15:04:05"

// Where the machine keeps its zone: a symlink into the zoneinfo tree, or a file naming it.
const (
	localtimeLink = "/etc/localtime"
	timezoneFile  = "/etc/timezone"
)

// Requester is where and when the person asking is: their IANA time zone and
// their local date and time. The crew's tools read it for "today" instead of the
// clock of a server that may be in another time zone. Either field may be empty.
type Requester struct {
	TimeZone      string
	LocalDateTime string
}

// CurrentRequester is the person's zone, from the machine, and the local time at now.
// Without a zone name it still carries the local date and time, which hold the date.
func CurrentRequester(now time.Time) Requester {
	return requesterAt(now, LocalZoneName())
}

func requesterAt(now time.Time, zone string) Requester {
	loc, err := time.LoadLocation(zone)
	if zone == "" || zone == "Local" || err != nil {
		return Requester{LocalDateTime: now.Format(localDateTimeLayout)}
	}
	return Requester{TimeZone: zone, LocalDateTime: now.In(loc).Format(localDateTimeLayout)}
}

// LocalZoneName is the machine's IANA time zone name: TZ, else /etc/localtime, else
// /etc/timezone; empty when none names a zone Go knows.
func LocalZoneName() string {
	candidates := []string{os.Getenv("TZ"), zoneFromLink(localtimeLink), zoneFromFile(timezoneFile)}
	for _, name := range candidates {
		name = strings.TrimPrefix(strings.TrimSpace(name), ":")
		if known(name) {
			return name
		}
	}
	return ""
}

func known(name string) bool {
	if name == "" || name == "Local" {
		return false
	}
	_, err := time.LoadLocation(name)
	return err == nil
}

func zoneFromLink(path string) string {
	target, err := os.Readlink(path)
	if err != nil {
		return ""
	}
	return zoneAfterZoneinfo(target)
}

// zoneAfterZoneinfo is the part of a path after "zoneinfo/", such as
// America/New_York in /usr/share/zoneinfo/America/New_York.
func zoneAfterZoneinfo(target string) string {
	_, zone, found := strings.Cut(target, "zoneinfo/")
	if !found {
		return ""
	}
	return zone
}

func zoneFromFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// askBody is the JSON body of a question: the message, the conversation, and the
// requester's zone and local time when there are any.
func askBody(message, conversationID string, who Requester) map[string]string {
	body := map[string]string{"message": message, "conversationId": conversationID}
	if who.TimeZone != "" {
		body["timeZone"] = who.TimeZone
	}
	if who.LocalDateTime != "" {
		body["localDateTime"] = who.LocalDateTime
	}
	return body
}
