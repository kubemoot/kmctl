package output

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestPrint_JSON(t *testing.T) {
	buf := new(bytes.Buffer)
	if err := Print(buf, FormatJSON, map[string]string{"a": "b"}); err != nil {
		t.Fatalf("Print json: %v", err)
	}
	if !strings.Contains(buf.String(), "\"a\": \"b\"") {
		t.Errorf("json output missing key: %s", buf.String())
	}
}

func TestPrint_YAML(t *testing.T) {
	buf := new(bytes.Buffer)
	if err := Print(buf, FormatYAML, map[string]string{"a": "b"}); err != nil {
		t.Fatalf("Print yaml: %v", err)
	}
	if !strings.Contains(buf.String(), "a: b") {
		t.Errorf("yaml output missing key: %s", buf.String())
	}
}

// sampleObject is a Kubernetes object as kmctl prints it: nested maps, a list,
// a number, a boolean, and strings a YAML parser could misread unquoted.
func sampleObject() map[string]any {
	return map[string]any{
		"apiVersion": "kubemoot.ai/v1alpha1",
		"kind":       "Crew",
		"metadata":   map[string]any{"name": "demo", "labels": map[string]any{"kubemoot.ai/crew": "demo"}},
		"spec": map[string]any{
			"description": "A read-only guide\nto its namespace.",
			"enabled":     true,
			"replicas":    int64(2),
			"answer":      "no",
			"version":     "1.10",
			"channels":    []any{"kubernetes", "general"},
		},
	}
}

func TestPrint_KYAML(t *testing.T) {
	buf := new(bytes.Buffer)
	if err := Print(buf, FormatKYAML, sampleObject()); err != nil {
		t.Fatalf("Print kyaml: %v", err)
	}
	got := buf.String()
	if !strings.HasPrefix(got, "---\n") || !strings.HasSuffix(got, "\n") {
		t.Errorf("kyaml opens with the --- header and ends with a newline:\n%s", got)
	}
	for _, want := range []string{`kind: "Crew"`, `answer: "no"`, `version: "1.10"`, "replicas: 2", "enabled: true", `kubemoot.ai/crew: "demo"`} {
		if !strings.Contains(got, want) {
			t.Errorf("kyaml lacks %s:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "{") || !strings.Contains(got, "[") {
		t.Errorf("kyaml writes maps in {} and lists in []:\n%s", got)
	}
}

// KYAML is YAML: the output reads back as the object it was printed from.
func TestPrint_KYAMLReadsBackAsYAML(t *testing.T) {
	buf := new(bytes.Buffer)
	if err := Print(buf, FormatKYAML, sampleObject()); err != nil {
		t.Fatalf("Print kyaml: %v", err)
	}
	got := buf.String()
	var back map[string]any
	if err := yaml.Unmarshal([]byte(got), &back); err != nil {
		t.Fatalf("kyaml output is not valid YAML: %v\n%s", err, got)
	}
	want := map[string]any{}
	if err := yaml.Unmarshal([]byte(mustYAML(t, sampleObject())), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, want) {
		t.Errorf("kyaml read back as YAML = %v, want %v", back, want)
	}
}

func TestPrint_KYAMLNil(t *testing.T) {
	buf := new(bytes.Buffer)
	if err := Print(buf, FormatKYAML, nil); err != nil {
		t.Fatalf("Print kyaml nil: %v", err)
	}
	if got := buf.String(); got != "---\nnull\n" {
		t.Errorf("kyaml of nil = %q, want a null document", got)
	}
}

func TestPrint_KYAMLUnmarshalable(t *testing.T) {
	if err := Print(new(bytes.Buffer), FormatKYAML, map[string]any{"c": make(chan int)}); err == nil {
		t.Fatal("expected an error for a value JSON cannot encode")
	}
}

func mustYAML(t *testing.T, v any) string {
	t.Helper()
	b, err := yaml.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPrint_Unsupported(t *testing.T) {
	buf := new(bytes.Buffer)
	err := Print(buf, Format("xml"), nil)
	if err == nil {
		t.Fatal("expected error for unsupported format, got nil")
	}
	for _, f := range []string{`"xml"`, `"yaml"`, `"json"`, `"kyaml"`} {
		if !strings.Contains(err.Error(), f) {
			t.Errorf("error %q must name %s", err, f)
		}
	}
	if buf.Len() != 0 {
		t.Errorf("an unsupported format writes nothing, got %q", buf.String())
	}
}

func TestFormatHelp(t *testing.T) {
	cases := map[string]string{
		"table": "Output format: yaml, json, or kyaml (default: table)",
		"":      "Output format: yaml, json, or kyaml",
	}
	for fallback, want := range cases {
		if got := FormatHelp(fallback); got != want {
			t.Errorf("FormatHelp(%q) = %q, want %q", fallback, got, want)
		}
	}
}

func TestPrinters_CoverEveryFormat(t *testing.T) {
	if len(printers) != len(Formats) {
		t.Fatalf("%d printers for %d formats", len(printers), len(Formats))
	}
	for _, f := range Formats {
		if printers[f] == nil {
			t.Errorf("format %q has no printer", f)
		}
	}
}

func TestWriter_CapturesFirstError(t *testing.T) {
	// A failing writer makes the first Printf set err; later calls are no-ops.
	fw := failWriter{}
	w := NewWriter(fw)
	w.Printf("one")
	w.Printf("two")
	if w.Err() == nil {
		t.Fatal("expected Writer to capture the write error")
	}
}

func TestWriter_OK(t *testing.T) {
	buf := new(bytes.Buffer)
	w := NewWriter(buf)
	w.Printf("hello %s\n", "world")
	if w.Err() != nil {
		t.Fatalf("unexpected error: %v", w.Err())
	}
	if buf.String() != "hello world\n" {
		t.Errorf("got %q", buf.String())
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errBoom }

var errBoom = &boomError{}

type boomError struct{}

func (*boomError) Error() string { return "boom" }
