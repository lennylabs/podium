package materialize

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/lennylabs/podium/pkg/adapter"
)

// Spec: §6.6 step 5 / §6.9 "Materialization destination unwritable" — a write
// that fails mid-batch leaves the destination unchanged: nothing partial is
// left on disk. The earlier files must not be committed when a later file's
// staging fails.
//
// This fails on the old per-file write-then-rename loop (good.txt would be
// renamed into place before sub/inner.txt failed) and passes with the
// stage-all-then-rename-all guarantee.
func TestWrite_MidBatchFailureLeavesNothing(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	// Pre-create a regular file named "sub" so MkdirAll(dest/sub) fails when
	// staging the second file, simulating an unwritable directory mid-batch.
	if err := os.WriteFile(filepath.Join(dest, "sub"), []byte("blocker"), 0o644); err != nil {
		t.Fatalf("seed blocker: %v", err)
	}
	err := Write(dest, []adapter.File{
		{Path: "good.txt", Content: []byte("first")},
		{Path: "sub/inner.txt", Content: []byte("second")},
	})
	if err == nil {
		t.Fatal("Write succeeded; want a staging failure")
	}
	// The first file must not be committed.
	if _, statErr := os.Stat(filepath.Join(dest, "good.txt")); !os.IsNotExist(statErr) {
		t.Errorf("good.txt was committed even though the batch failed (partial tree)")
	}
	// No staged temporaries may be left behind.
	if _, statErr := os.Stat(filepath.Join(dest, "good.txt.tmp")); !os.IsNotExist(statErr) {
		t.Errorf("good.txt.tmp staged temporary was left behind")
	}
	// The pre-existing blocker must be untouched.
	if b, _ := os.ReadFile(filepath.Join(dest, "sub")); string(b) != "blocker" {
		t.Errorf("pre-existing sub = %q, want blocker", b)
	}
}

// Spec: §6.6 step 5 — when every staged write succeeds, the renames commit the
// whole tree, so a multi-file batch lands completely.
func TestWrite_StagesThenCommitsWholeTree(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	files := []adapter.File{
		{Path: "a.txt", Content: []byte("a")},
		{Path: "nested/b.txt", Content: []byte("b")},
		{Path: "nested/deep/c.txt", Content: []byte("c")},
	}
	if err := Write(dest, files); err != nil {
		t.Fatalf("Write: %v", err)
	}
	for _, f := range files {
		got, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(f.Path)))
		if err != nil {
			t.Fatalf("read %s: %v", f.Path, err)
		}
		if string(got) != string(f.Content) {
			t.Errorf("%s = %q, want %q", f.Path, got, f.Content)
		}
		// No leftover staging temporaries.
		if _, statErr := os.Stat(filepath.Join(dest, filepath.FromSlash(f.Path)) + ".tmp"); !os.IsNotExist(statErr) {
			t.Errorf("%s.tmp left behind", f.Path)
		}
	}
}

// Spec: §6.6 step 5 / §6.7 sandbox contract — a write whose path traverses a
// symlinked intermediate directory pointing outside the destination is
// rejected with ErrOutOfDestination, and nothing is written through the
// symlink. The lexical check cannot see this; the symlink-resolution check
// must.
func TestWrite_SymlinkedDirEscapeRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	t.Parallel()
	dest := t.TempDir()
	outside := t.TempDir()
	// Pre-existing symlink inside the destination pointing at an external dir.
	if err := os.Symlink(outside, filepath.Join(dest, "sub")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	err := Write(dest, []adapter.File{
		{Path: "sub/escaped.txt", Content: []byte("leak")},
	})
	if !errors.Is(err, ErrOutOfDestination) {
		t.Fatalf("err = %v, want ErrOutOfDestination", err)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "escaped.txt")); !os.IsNotExist(statErr) {
		t.Errorf("file was written through the symlink into %q (sandbox escape)", outside)
	}
}

// Spec: §6.7 — a symlinked destination root is itself legitimate: Write
// resolves it and writes within the real directory it points at.
func TestWrite_SymlinkedDestinationRootResolves(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	t.Parallel()
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "dest-link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := Write(link, []adapter.File{
		{Path: "ok.txt", Content: []byte("hi")},
	}); err != nil {
		t.Fatalf("Write through symlinked root: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(real, "ok.txt")); string(b) != "hi" {
		t.Errorf("ok.txt = %q, want hi", b)
	}
}
