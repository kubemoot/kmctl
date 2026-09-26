package output

import (
	"bytes"
	"strings"
	"testing"
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

func TestPrint_Unsupported(t *testing.T) {
	buf := new(bytes.Buffer)
	if err := Print(buf, Format("xml"), nil); err == nil {
		t.Fatal("expected error for unsupported format, got nil")
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
