package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const twoDocs = `
apiVersion: kubemoot.ai/v1alpha1
kind: Crew
metadata:
  name: a
---
apiVersion: kubemoot.ai/v1alpha1
kind: Agent
metadata:
  name: b
---
`

func TestDecode_MultiDocSkipsEmpty(t *testing.T) {
	objs, err := Decode(strings.NewReader(twoDocs))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(objs) != 2 {
		t.Fatalf("got %d objects, want 2 (trailing --- must be skipped)", len(objs))
	}
	if objs[0].GetKind() != "Crew" || objs[1].GetKind() != "Agent" {
		t.Errorf("unexpected kinds: %q, %q", objs[0].GetKind(), objs[1].GetKind())
	}
}

func TestFromPath_Stdin(t *testing.T) {
	objs, err := FromPath("-", strings.NewReader(twoDocs))
	if err != nil {
		t.Fatalf("FromPath stdin: %v", err)
	}
	if len(objs) != 2 {
		t.Errorf("got %d, want 2", len(objs))
	}
}

func TestFromPath_FileAndDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.yaml"), []byte(twoDocs), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ignore.txt"), []byte("not a manifest"), 0o600); err != nil {
		t.Fatal(err)
	}

	fromFile, err := FromPath(filepath.Join(dir, "a.yaml"), nil)
	if err != nil || len(fromFile) != 2 {
		t.Fatalf("file: got %d objs, err %v", len(fromFile), err)
	}

	fromDir, err := FromPath(dir, nil)
	if err != nil || len(fromDir) != 2 {
		t.Fatalf("dir: got %d objs (only .yaml should be read), err %v", len(fromDir), err)
	}
}

func TestFromPath_Missing(t *testing.T) {
	if _, err := FromPath(filepath.Join(t.TempDir(), "nope.yaml"), nil); err == nil {
		t.Fatal("expected error for missing path, got nil")
	}
}
