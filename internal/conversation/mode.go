package conversation

import (
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// Mode is how a discussion is shown.
type Mode string

// The output modes of the conversation commands.
const (
	ModeFull  Mode = "full"
	ModePlain Mode = "plain"
	ModeJSON  Mode = "json"
	ModeQuiet Mode = "quiet"
)

// Modes lists the output modes, default first.
var Modes = []Mode{ModeFull, ModePlain, ModeJSON, ModeQuiet}

// ModeNames lists the output modes by name, default first.
func ModeNames() []string {
	names := make([]string, len(Modes))
	for i, m := range Modes {
		names[i] = string(m)
	}
	return names
}

// ModeHelp is the --output flag's help text.
func ModeHelp() string {
	return "Output: " + strings.Join(ModeNames(), "|") + " (full falls back to plain when stdout is not a terminal or NO_COLOR is set)"
}

// Resolve turns the requested mode into the one to use. full becomes plain when
// stdout is not a terminal, NO_COLOR is set, or TERM is dumb.
func Resolve(requested string, tty bool, getenv func(string) string) (Mode, error) {
	m := Mode(strings.ToLower(strings.TrimSpace(requested)))
	if m == "" {
		m = ModeFull
	}
	switch m {
	case ModePlain, ModeJSON, ModeQuiet:
		return m, nil
	case ModeFull:
		if !tty || getenv("NO_COLOR") != "" || getenv("TERM") == "dumb" {
			return ModePlain, nil
		}
		return ModeFull, nil
	}
	return "", fmt.Errorf("unknown output %q: want one of %s", requested, strings.Join(ModeNames(), ", "))
}

// IsTerminal reports whether w is a terminal.
func IsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}
