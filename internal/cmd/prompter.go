package cmd

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"charm.land/huh/v2"
)

// huhPrompter is the interactive terminal implementation of prompter. The zero
// value prompts on the terminal; in, out and accessible let tests drive the
// line-based accessible mode through readers and writers instead.
type huhPrompter struct {
	in         io.Reader
	out        io.Writer
	accessible bool
}

// run shows one field as a single-field form.
func (p huhPrompter) run(field huh.Field) error {
	form := huh.NewForm(huh.NewGroup(field)).WithAccessible(p.accessible)
	if p.in != nil {
		form = form.WithInput(p.in)
	}
	if p.out != nil {
		form = form.WithOutput(p.out)
	}
	return form.Run()
}

func (p huhPrompter) Text(label, def string) (string, error) {
	answer := def
	err := p.run(huh.NewInput().Title(label).Value(&answer))
	return answer, err
}

func (p huhPrompter) Int(label string, def int) (int, error) {
	answer, err := p.Text(label, strconv.Itoa(def))
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(answer))
	if err != nil {
		return 0, fmt.Errorf("expected a number, got %q", answer)
	}
	return n, nil
}
