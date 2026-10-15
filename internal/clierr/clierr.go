// Package clierr turns kmctl's failures into short messages for the person at the
// terminal: what went wrong, what to do next, and, on request, the error
// underneath. It maps the Kubernetes client's errors (no kubeconfig, an unknown
// context, an unreachable API server, rejected credentials, missing permission, a
// cluster without the Kubemoot API) and writes the result to stderr.
package clierr

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"golang.org/x/term"
)

// Error is a failure explained for a person: Message says what went wrong, Hint
// what to do next, and Cause is the error underneath, shown with --verbose.
type Error struct {
	Message string
	Hint    string
	Cause   error
}

// Error returns the message; the hint and cause are written by Write.
func (e *Error) Error() string { return e.Message }

// Unwrap returns the underlying error.
func (e *Error) Unwrap() error { return e.Cause }

// New returns an explained error.
func New(message, hint string, cause error) *Error {
	return &Error{Message: message, Hint: hint, Cause: cause}
}

// As returns the explained error in err's chain, if there is one.
func As(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}

// Format is how Write prints an error: Verbose adds the underlying error, Styled
// colors the labels.
type Format struct {
	Verbose bool
	Styled  bool
}

var (
	styleError = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1"))
	styleHint  = lipgloss.NewStyle().Bold(true)
	styleCause = lipgloss.NewStyle().Faint(true)
)

// Write prints e as "Error: <message>", then "Hint: <hint>" when there is one,
// then "Cause: <underlying error>" in verbose mode when it says more than the
// message.
func Write(w io.Writer, e *Error, f Format) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", label(styleError, "Error:", f.Styled), e.Message)
	if e.Hint != "" {
		fmt.Fprintf(&b, "%s %s\n", label(styleHint, "Hint:", f.Styled), e.Hint)
	}
	if f.Verbose && e.Cause != nil && e.Cause.Error() != e.Message {
		fmt.Fprintf(&b, "%s %s\n", label(styleCause, "Cause:", f.Styled), e.Cause.Error())
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func label(s lipgloss.Style, text string, styled bool) string {
	if !styled {
		return text
	}
	return s.Render(text)
}

// Styled reports whether errors written to w get color: w is a terminal, NO_COLOR
// is unset, and TERM is not dumb.
func Styled(w io.Writer, getenv func(string) string) bool {
	f, ok := w.(*os.File)
	return styledFor(ok && term.IsTerminal(int(f.Fd())), getenv)
}

// styledFor decides on color once it is known whether stderr is a terminal.
func styledFor(tty bool, getenv func(string) string) bool {
	return tty && getenv("NO_COLOR") == "" && getenv("TERM") != "dumb"
}

// Debug reports whether KMCTL_DEBUG asks for the underlying errors: any value
// except empty, "0", and "false".
func Debug(getenv func(string) string) bool {
	switch strings.ToLower(strings.TrimSpace(getenv("KMCTL_DEBUG"))) {
	case "", "0", "false":
		return false
	}
	return true
}
