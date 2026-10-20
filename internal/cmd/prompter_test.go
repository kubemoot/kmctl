package cmd

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/kubemoot/kmctl/internal/scaffold"
)

// lineReader hands out one line per Read, so each accessible prompt (which wraps
// the reader in its own scanner) consumes only its own answer.
type lineReader struct{ lines []string }

func (r *lineReader) Read(b []byte) (int, error) {
	if len(r.lines) == 0 {
		return 0, io.EOF
	}
	n := copy(b, r.lines[0]+"\n")
	r.lines = r.lines[1:]
	return n, nil
}

// typed is a prompter that answers from the given lines in accessible mode.
func typed(lines ...string) huhPrompter {
	return huhPrompter{in: &lineReader{lines: lines}, out: io.Discard, accessible: true}
}

func TestHuhPrompterText(t *testing.T) {
	got, err := typed("Homelab Health Guide").Text("Display name:", "demo")
	if err != nil || got != "Homelab Health Guide" {
		t.Fatalf("typed answer: got %q, %v", got, err)
	}
	got, err = typed("").Text("Display name:", "demo")
	if err != nil || got != "demo" {
		t.Fatalf("empty answer keeps the default: got %q, %v", got, err)
	}
	got, err = typed().Text("Display name:", "demo")
	if err != nil || got != "demo" {
		t.Fatalf("end of input keeps the default: got %q, %v", got, err)
	}
}

func TestHuhPrompterInt(t *testing.T) {
	cases := []struct {
		answer  string
		want    int
		wantErr bool
	}{
		{"4", 4, false},
		{" 3 ", 3, false},
		{"", 1, false},
		{"three", 0, true},
		{"2.5", 0, true},
	}
	for _, c := range cases {
		got, err := typed(c.answer).Int("How many?", 1)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("answer %q: got %d, err %v; want %d, err %v", c.answer, got, err, c.want, c.wantErr)
		}
	}
	if _, err := typed("x").Int("How many?", 1); err == nil || !strings.Contains(err.Error(), `expected a number, got "x"`) {
		t.Errorf("want the expected-a-number error, got %v", err)
	}
}

func TestGatherWithHuhPrompter(t *testing.T) {
	opts := scaffold.Options{Name: "demo", Members: 1}
	p := typed("Demo Crew", "3")
	if err := gather(&opts, changedSet(), false, p); err != nil {
		t.Fatal(err)
	}
	if opts.DisplayName != "Demo Crew" || opts.Members != 3 {
		t.Fatalf("unexpected options: %+v", opts)
	}
}

// brokenTerminal is an input that fails every read, like a terminal that went away.
type brokenTerminal struct{}

func (brokenTerminal) Read([]byte) (int, error) { return 0, errors.New("terminal gone") }

// On a terminal that fails, every prompt returns the form's error, never a default
// answer that would scaffold a crew nobody chose.
func TestHuhPrompterFailingTerminal(t *testing.T) {
	p := huhPrompter{in: brokenTerminal{}, out: io.Discard}
	if got, err := p.Text("Display name:", "demo"); err == nil {
		t.Errorf("Text: want an error, got %q", got)
	}
	if got, err := p.Int("How many?", 1); err == nil || got != 0 {
		t.Errorf("Int: want 0 and an error, got %d, %v", got, err)
	}
}
