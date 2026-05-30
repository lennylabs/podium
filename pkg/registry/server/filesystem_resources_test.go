package server

import (
	"os"
	"path/filepath"
	"testing"
)

// dirFS wraps os.DirFS-like semantics; the tests exercise each method
// the server depends on. The functions are simple wrappers but
// nothing else in the package calls them.
func TestDirFS_OpenReadDirStat(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	fsys := newDirFS(dir)
	f, err := fsys.Open("x.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	f.Close()

	statFS := fsys.(interface {
		Stat(string) (os.FileInfo, error)
	})
	if _, err := statFS.Stat("x.txt"); err != nil {
		t.Errorf("Stat: %v", err)
	}

	readDirFS := fsys.(interface {
		ReadDir(string) ([]os.DirEntry, error)
	})
	entries, err := readDirFS.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) == 0 {
		t.Errorf("expected entries, got none")
	}
}
