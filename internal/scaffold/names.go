package scaffold

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// DisplayNameAnnotation holds the crew's display name, the name people read: any
// text, such as "Homelab Health Guide". The crew's technical name (its
// Kubernetes name) stays a DNS-1123 label.
const DisplayNameAnnotation = "kubemoot.ai/display-name"

// MaxNameLength is the longest technical crew name. Kubernetes names the crew's
// objects after it, and the longest such chain is the starter crew's tool server:
// the MCPServer <crew>-kubernetes-mcp, whose tool index the operator names
// <crew>-kubernetes-mcp-tools and serves from the Service
// <crew>-kubernetes-mcp-tools-query, a DNS label of at most 63 characters. 63
// less that 27-character suffix is 36, well under Helm's 53 for a release name.
const MaxNameLength = 63 - len("-kubernetes-mcp-tools-query")

// MaxDisplayNameLength is the longest display name, in characters.
const MaxDisplayNameLength = 100

var dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// nameRule says, in plain words, what a technical crew name may hold.
const nameRule = "use lowercase letters, digits and hyphens, starting and ending with a letter or digit; it becomes the Kubernetes name of the crew's objects"

// checkName reports why name cannot be a technical crew name, or nil.
func checkName(name string) error {
	if name == "" {
		return fmt.Errorf("crew name is required")
	}
	if !dnsLabel.MatchString(name) {
		return fmt.Errorf("crew name %q: %s", name, nameRule)
	}
	if len(name) > MaxNameLength {
		return fmt.Errorf("crew name %q is %d characters; keep it to %d, so the names Kubernetes builds on it fit", name, len(name), MaxNameLength)
	}
	return nil
}

// checkDisplayName reports why s cannot be a display name, or nil. A display
// name is one line of any text, without surrounding spaces; empty means the
// technical name.
func checkDisplayName(s string) error {
	s = strings.TrimSpace(s)
	if !utf8.ValidString(s) {
		return fmt.Errorf("display name %q is not valid UTF-8 text", s)
	}
	if strings.IndexFunc(s, unicode.IsControl) >= 0 {
		return fmt.Errorf("display name %q must be one line, without tabs or other control characters", s)
	}
	if n := utf8.RuneCountInString(s); n > MaxDisplayNameLength {
		return fmt.Errorf("display name is %d characters; keep it to %d", n, MaxDisplayNameLength)
	}
	return nil
}

// quoted is s as a YAML double-quoted scalar. Go's quoting escapes only what
// YAML's double-quoted style also reads as an escape (\" \\ \uXXXX), for valid
// UTF-8 without control characters, which checkDisplayName ensures. In a file
// Helm renders (helm true), text holding "{{" would open a Helm action, so it is
// written as one instead: the same string literal, which Helm prints with quote,
// giving the same YAML scalar.
func quoted(s string, helm bool) string {
	q := strconv.Quote(s)
	if helm && strings.Contains(s, "{{") {
		return "{{ " + q + " | quote }}"
	}
	return q
}
