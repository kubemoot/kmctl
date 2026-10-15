package conversation

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func TestResolve(t *testing.T) {
	cases := []struct {
		name, requested string
		tty             bool
		vars            map[string]string
		want            Mode
	}{
		{"default on a terminal is full", "", true, nil, ModeFull},
		{"full on a terminal", "full", true, nil, ModeFull},
		{"full when stdout is not a terminal falls back", "full", false, nil, ModePlain},
		{"default when stdout is not a terminal falls back", "", false, nil, ModePlain},
		{"NO_COLOR forces plain", "full", true, map[string]string{"NO_COLOR": "1"}, ModePlain},
		{"an empty NO_COLOR is not set", "full", true, map[string]string{"NO_COLOR": ""}, ModeFull},
		{"a dumb terminal is plain", "full", true, map[string]string{"TERM": "dumb"}, ModePlain},
		{"plain stays plain on a terminal", "plain", true, nil, ModePlain},
		{"json stays json without a terminal", "json", false, map[string]string{"NO_COLOR": "1"}, ModeJSON},
		{"case and spaces are ignored", " JSON ", true, nil, ModeJSON},
		{"quiet stays quiet without a terminal", "quiet", false, nil, ModeQuiet},
	}
	for _, c := range cases {
		got, err := Resolve(c.requested, c.tty, env(c.vars))
		if err != nil || got != c.want {
			t.Errorf("%s: Resolve(%q) = %q, %v; want %q", c.name, c.requested, got, err, c.want)
		}
	}
}

func TestResolve_RejectsUnknownMode(t *testing.T) {
	for _, bad := range []string{"yaml", "table", "fulll"} {
		_, err := Resolve(bad, true, env(nil))
		if err == nil || !strings.Contains(err.Error(), "full, plain, json") {
			t.Errorf("Resolve(%q): want an error naming the modes, got %v", bad, err)
		}
	}
}

func TestIsTerminal_NonTerminals(t *testing.T) {
	if IsTerminal(new(bytes.Buffer)) {
		t.Error("a buffer is not a terminal")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	if IsTerminal(w) {
		t.Error("a pipe is not a terminal")
	}
}

func TestModeHelp_NamesEveryMode(t *testing.T) {
	help := ModeHelp()
	for _, m := range Modes {
		if !strings.Contains(help, string(m)) {
			t.Errorf("help %q does not name %s", help, m)
		}
	}
}
