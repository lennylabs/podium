package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// filesystemResourceFunc walks the registry's layers in reverse
// precedence and returns the first matching resource. Missing
// resources return (nil, false); path-escape attempts are rejected.
func TestFilesystemResourceFunc(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "art", "sub"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "art", "sub", "r.md"), []byte("body"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	reg := &filesystem.Registry{Layers: []filesystem.Layer{{Path: dir}}}
	fn := filesystemResourceFunc(reg)

	// Happy path.
	data, ok := fn(context.Background(), "art", "sub/r.md")
	if !ok || string(data) != "body" {
		t.Errorf("happy path: ok=%v data=%q", ok, data)
	}

	// Missing path returns (nil, false).
	if data, ok := fn(context.Background(), "art", "sub/missing.md"); ok || data != nil {
		t.Errorf("missing: ok=%v data=%q", ok, data)
	}

	// Missing artifact returns (nil, false).
	if _, ok := fn(context.Background(), "no-such-artifact", "x.md"); ok {
		t.Errorf("missing artifact should return false")
	}

	// Path-escape (containing ..) gets rejected on filepath.HasPrefix.
	if _, ok := fn(context.Background(), "..", "etc/passwd"); ok {
		t.Errorf("escape path should return false")
	}
}

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
