package cmd

import (
	"io"
	"slices"
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

func TestHuhPrompterMultiSelect(t *testing.T) {
	options := []string{"ollama-gpu", "ollama-cpu", "ollama-big"}
	got, err := typed("1", "3", "0").MultiSelect("Providers:", options)
	if err != nil || !slices.Equal(got, []string{"ollama-gpu", "ollama-big"}) {
		t.Fatalf("toggle 1 and 3: got %v, %v", got, err)
	}
	got, err = typed("2", "2", "0").MultiSelect("Providers:", options)
	if err != nil || len(got) != 0 {
		t.Fatalf("toggling 2 twice leaves nothing selected: got %v, %v", got, err)
	}
	got, err = typed("9", "-1", "0").MultiSelect("Providers:", options)
	if err != nil || len(got) != 0 {
		t.Fatalf("out-of-range answers are re-asked, not selected: got %v, %v", got, err)
	}
}

func TestHuhPrompterSelectOrOther(t *testing.T) {
	options := []string{"qwen", "gemma"}
	cases := []struct {
		name  string
		lines []string
		want  string
	}{
		{"offered option", []string{"2"}, "gemma"},
		{"empty answer takes the first option", []string{""}, "qwen"},
		{"other asks for a name and trims it", []string{"3", "  llama  "}, "llama"},
		{"other with no name", []string{"3", ""}, ""},
		{"skip", []string{"4"}, ""},
		{"out of range is re-asked", []string{"0", "5", "1"}, "qwen"},
	}
	for _, c := range cases {
		got, err := typed(c.lines...).SelectOrOther("Family:", options)
		if err != nil || got != c.want {
			t.Errorf("%s: got %q, %v; want %q", c.name, got, err, c.want)
		}
	}
}

func TestGatherWithHuhPrompter(t *testing.T) {
	opts := scaffold.Options{Name: "demo", Members: 1}
	p := typed("Demo Crew", "3", "2", "0", "1")
	if err := gather(&opts, changedSet(), false, []string{"ollama-gpu", "ollama-cpu"}, p); err != nil {
		t.Fatal(err)
	}
	if opts.DisplayName != "Demo Crew" || opts.Members != 3 || !slices.Equal(opts.Providers, []string{"ollama-cpu"}) || opts.ModelFamily != scaffold.KnownModelFamilies[0] {
		t.Fatalf("unexpected options: %+v", opts)
	}
}
