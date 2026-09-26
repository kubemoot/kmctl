// Package manifest reads Kubernetes manifests (file, directory, or stdin) into
// unstructured objects.
package manifest

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// Decode reads possibly multi-document YAML/JSON from r into objects. Empty
// documents (e.g. a trailing "---") are skipped.
func Decode(r io.Reader) ([]unstructured.Unstructured, error) {
	var objs []unstructured.Unstructured
	dec := yaml.NewYAMLOrJSONDecoder(bufio.NewReader(r), 4096)
	for {
		var m map[string]any
		err := dec.Decode(&m)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(m) == 0 {
			continue
		}
		objs = append(objs, unstructured.Unstructured{Object: m})
	}
	return objs, nil
}

// FromPath reads objects from "-" (stdin), a single file, or every
// .yaml/.yml/.json file in a directory.
func FromPath(path string, stdin io.Reader) ([]unstructured.Unstructured, error) {
	if path == "-" {
		return Decode(stdin)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return decodeFile(path)
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var objs []unstructured.Unstructured
	for _, e := range entries {
		if e.IsDir() || !isManifest(e.Name()) {
			continue
		}
		fo, err := decodeFile(filepath.Join(path, e.Name()))
		if err != nil {
			return nil, err
		}
		objs = append(objs, fo...)
	}
	return objs, nil
}

func decodeFile(path string) ([]unstructured.Unstructured, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Decode(bytes.NewReader(b))
}

func isManifest(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".yaml", ".yml", ".json":
		return true
	}
	return false
}
