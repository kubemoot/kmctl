// Package output renders command results in kubectl-consistent formats.
package output

import (
	"encoding/json"
	"fmt"
	"io"

	"sigs.k8s.io/yaml"
)

// Format is a supported -o value.
type Format string

// Supported output formats. Table/name printing for resource lists lands with
// the resource commands; these cover structured single-object output.
const (
	FormatYAML Format = "yaml"
	FormatJSON Format = "json"
)

// Print writes v to w in the requested structured format.
func Print(w io.Writer, format Format, v any) error {
	switch format {
	case FormatJSON:
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(b))
		return err
	case FormatYAML:
		b, err := yaml.Marshal(v)
		if err != nil {
			return err
		}
		_, err = fmt.Fprint(w, string(b))
		return err
	default:
		return fmt.Errorf("unsupported output format %q (want %q or %q)", format, FormatYAML, FormatJSON)
	}
}

// Writer is a thin io.Writer wrapper that records the first write error, so
// command code can emit several lines and check the error once at the end.
type Writer struct {
	w   io.Writer
	err error
}

// NewWriter wraps w.
func NewWriter(w io.Writer) *Writer { return &Writer{w: w} }

// Printf writes a formatted line, skipping work once an error has occurred.
func (x *Writer) Printf(format string, a ...any) {
	if x.err != nil {
		return
	}
	_, x.err = fmt.Fprintf(x.w, format, a...)
}

// Err returns the first write error, if any.
func (x *Writer) Err() error { return x.err }
