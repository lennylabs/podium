package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/objectstore"
)

// openTargetStore: unknown kind errors.
func TestOpenTargetStore_UnknownKindErrors(t *testing.T) {
	t.Parallel()
	_, _, err := openTargetStore("bogus", "", "")
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Errorf("err = %v", err)
	}
}

// openTargetStore sqlite happy path.
func TestOpenTargetStore_SQLite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	st, closeFn, err := openTargetStore("sqlite", "", filepath.Join(dir, "x.db"))
	if err != nil {
		t.Fatalf("openTargetStore: %v", err)
	}
	defer closeFn()
	if st == nil {
		t.Errorf("got nil store")
	}
}

// openTargetObjectStore unknown errors.
func TestOpenTargetObjectStore_UnknownErrors(t *testing.T) {
	t.Parallel()
	_, err := openTargetObjectStore("bogus", "", objectstore.S3Config{})
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Errorf("err = %v", err)
	}
}

// openTargetObjectStore filesystem happy.
func TestOpenTargetObjectStore_Filesystem(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	got, err := openTargetObjectStore("filesystem", filepath.Join(dir, "objects"), objectstore.S3Config{})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got == nil {
		t.Errorf("nil store")
	}
}

// copyFile round-trips bytes.
func TestCopyFile_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "sub", "dst.txt")
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile: %v", err)
	}
	body, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(body) != "hello" {
		t.Errorf("body = %q", body)
	}
}

// copyFile with a missing src returns an error.
func TestCopyFile_MissingSourceErrors(t *testing.T) {
	t.Parallel()
	if err := copyFile(filepath.Join(t.TempDir(), "missing.txt"),
		filepath.Join(t.TempDir(), "dst.txt")); err == nil {
		t.Errorf("expected error")
	}
}
