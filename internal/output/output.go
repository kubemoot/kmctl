// Package output renders command results in kubectl-consistent formats.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"sigs.k8s.io/yaml"
	"sigs.k8s.io/yaml/kyaml"
)

// Format is a supported -o value.
type Format string

// Supported output formats. Table/name printing for resource lists lands with
// the resource commands; these cover structured single-object output.
const (
	FormatYAML  Format = "yaml"
	FormatJSON  Format = "json"
	FormatKYAML Format = "kyaml" // KYAML, the strict, quoted, bracketed YAML subset kubectl also prints
)

// Formats lists the supported formats in the order help text names them.
var Formats = []Format{FormatYAML, FormatJSON, FormatKYAML}

// printers writes v in each supported format.
var printers = map[Format]func(io.Writer, any) error{
	FormatYAML:  printYAML,
	FormatJSON:  printJSON,
	FormatKYAML: printKYAML,
}

// FormatHelp is the -o flag's help: the supported formats, then the default.
func FormatHelp(fallback string) string {
	names := make([]string, len(Formats))
	for i, f := range Formats {
		names[i] = string(f)
	}
	help := "Output format: " + strings.Join(names[:len(names)-1], ", ") + ", or " + names[len(names)-1]
	if fallback != "" {
		help += " (default: " + fallback + ")"
	}
	return help
}

// Print writes v to w in the requested structured format.
func Print(w io.Writer, format Format, v any) error {
	p, ok := printers[format]
	if !ok {
		return fmt.Errorf("unsupported output format %q (want one of %s)", format, quotedFormats())
	}
	return p(w, v)
}

func quotedFormats() string {
	quoted := make([]string, len(Formats))
	for i, f := range Formats {
		quoted[i] = fmt.Sprintf("%q", f)
	}
	return strings.Join(quoted, ", ")
}

func printJSON(w io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}

func printYAML(w io.Writer, v any) error {
	b, err := yaml.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(w, string(b))
	return err
}

// printKYAML writes one KYAML document, opened by the "---" header that marks it
// as YAML rather than JSON, as kubectl -o kyaml does.
func printKYAML(w io.Writer, v any) error {
	enc := &kyaml.Encoder{}
	return enc.FromObject(v, w)
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
