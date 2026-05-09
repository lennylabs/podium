package testharness

import (
	"os"
	"path/filepath"
	"testing"
)

// WriteTreeOption configures a single file in the tree written by WriteTree.
type WriteTreeOption struct {
	Path    string
	Content string
	Mode    os.FileMode
}

// WriteTree writes a set of files (and the directories they live in) under
// root. It is the building block for filesystem fixtures: tests describe a
// tree as a slice of (relative-path, content) entries and WriteTree
// materializes it under t.TempDir().
//
// File mode defaults to 0o644 when zero; directory mode is 0o755.
func WriteTree(t testing.TB, root string, entries ...WriteTreeOption) {
	t.Helper()
	for _, e := range entries {
		mode := e.Mode
		if mode == 0 {
			mode = 0o644
		}
		full := filepath.Join(root, filepath.FromSlash(e.Path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(e.Content), mode); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
}

// ReadTree returns a flat map of relative paths under root mapped to their
// contents as strings. Used by tests that assert what materialization wrote
// into a target directory.
func ReadTree(t testing.TB, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}
