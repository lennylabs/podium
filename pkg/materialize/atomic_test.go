package materialize

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/adapter"
)

// Spec: §6.6 Materialization — Write writes each file under destination
// and creates intermediate directories as needed.
// Phase: 0
func TestWrite_CreatesDestinationTree(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	files := []adapter.File{
		{Path: "a/b/c.txt", Content: []byte("first")},
		{Path: "top.txt", Content: []byte("second")},
	}
	if err := Write(dest, files); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := testharness.ReadTree(t, dest)
	if got["a/b/c.txt"] != "first" {
		t.Errorf("a/b/c.txt = %q", got["a/b/c.txt"])
	}
	if got["top.txt"] != "second" {
		t.Errorf("top.txt = %q", got["top.txt"])
	}
}

// Spec: §6.6 Materialization — atomic write means the destination either
// holds the previous content or the new content; no .tmp files are left
// behind on success.
// Phase: 0
func TestWrite_NoTmpFilesAfterSuccess(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	if err := Write(dest, []adapter.File{
		{Path: "x.txt", Content: []byte("hello")},
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

// Spec: §6.7 sandbox contract — paths with .. that escape the destination
// fail with ErrOutOfDestination, no files are written.
// Phase: 0
// Matrix: §6.10 (materialize.sandbox_violation)
func TestWrite_RejectsParentEscape(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	err := Write(dest, []adapter.File{
		{Path: "../escape.txt", Content: []byte("nope")},
	})
	if !errors.Is(err, ErrOutOfDestination) {
		t.Fatalf("got %v, want ErrOutOfDestination", err)
	}
	if _, statErr := os.Stat(filepath.Join(dest, "..", "escape.txt")); !os.IsNotExist(statErr) {
		t.Errorf("escape.txt was written: %v", statErr)
	}
}

// Spec: §6.7 sandbox contract — absolute paths are rejected.
// Phase: 0
func TestWrite_RejectsAbsolutePath(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	err := Write(dest, []adapter.File{
		{Path: "/etc/passwd", Content: []byte("nope")},
	})
	if !errors.Is(err, ErrOutOfDestination) {
		t.Fatalf("got %v, want ErrOutOfDestination", err)
	}
}

// Spec: §6.6 Materialization — when validation fails for one file, no
// files are written (the whole batch is atomic at the directory level).
// Phase: 0
func TestWrite_RejectionLeavesNoPartialState(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	err := Write(dest, []adapter.File{
		{Path: "good.txt", Content: []byte("ok")},
		{Path: "../bad.txt", Content: []byte("nope")},
	})
	if !errors.Is(err, ErrOutOfDestination) {
		t.Fatalf("got %v, want ErrOutOfDestination", err)
	}
	if _, statErr := os.Stat(filepath.Join(dest, "good.txt")); !os.IsNotExist(statErr) {
		t.Errorf("good.txt was written even though the batch failed")
	}
}

// Spec: §6.6 — Write replaces existing files in place; readers seeing the
// destination see either the old or new content.
// Phase: 0
func TestWrite_ReplacesExistingFile(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	if err := Write(dest, []adapter.File{
		{Path: "file.txt", Content: []byte("v1")},
	}); err != nil {
		t.Fatalf("Write v1: %v", err)
	}
	if err := Write(dest, []adapter.File{
		{Path: "file.txt", Content: []byte("v2")},
	}); err != nil {
		t.Fatalf("Write v2: %v", err)
	}
	got := testharness.ReadTree(t, dest)
	if got["file.txt"] != "v2" {
		t.Errorf("file.txt = %q, want v2", got["file.txt"])
	}
}

// Spec: §6.6 — empty destination is rejected to prevent accidental writes
// to the working directory or filesystem root.
// Phase: 0
func TestWrite_EmptyDestinationRejected(t *testing.T) {
	t.Parallel()
	err := Write("", []adapter.File{
		{Path: "x.txt", Content: []byte("y")},
	})
	if !errors.Is(err, ErrEmptyDestination) {
		t.Fatalf("got %v, want ErrEmptyDestination", err)
	}
}
