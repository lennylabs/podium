package materialize

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Spec: §7.5.3 — the file hash is "sha256:" followed by the lowercase
// hexadecimal SHA-256 digest of the file's bytes.
func TestFileHash_Format(t *testing.T) {
	t.Parallel()
	const emptySHA256 = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if got := FileHash(nil); got != emptySHA256 {
		t.Errorf("FileHash(nil) = %q, want %q", got, emptySHA256)
	}
	if FileHash([]byte("a")) == FileHash([]byte("b")) {
		t.Error("distinct bytes produced the same file hash")
	}
}

// Spec: §7.5.3 — the cleanup deletes a stale file whose bytes hash to the
// recorded file_hash and prunes the directories it leaves empty, and it stops
// at a directory that still holds a file it left in place.
func TestRemoveStale_HashMatchRemovesAndPrunesOnlyEmptyDirs(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	confinedFixture(t, dest, map[string]string{
		"skills/a/SKILL.md":    "written by sync",
		"skills/b/SKILL.md":    "edited by alice",
		"skills/b/ref/note.md": "kept",
	})
	if err := RemoveStale(dest, "skills/a/SKILL.md", FileHash([]byte("written by sync"))); err != nil {
		t.Fatalf("RemoveStale with a matching hash: %v", err)
	}
	wantGone(t, filepath.Join(dest, "skills", "a"))

	// The edited file stays, so its directory and the shared parent stay too.
	err := RemoveStale(dest, "skills/b/SKILL.md", FileHash([]byte("written by sync")))
	if !errors.Is(err, ErrStaleHashMismatch) {
		t.Fatalf("RemoveStale with a differing hash = %v, want ErrStaleHashMismatch", err)
	}
	wantContent(t, filepath.Join(dest, "skills", "b", "SKILL.md"), "edited by alice")

	// Removing the last matching file below skills/b/ref prunes ref only:
	// skills/b still holds the file the cleanup left in place.
	if err := RemoveStale(dest, "skills/b/ref/note.md", FileHash([]byte("kept"))); err != nil {
		t.Fatalf("RemoveStale of the nested file: %v", err)
	}
	wantGone(t, filepath.Join(dest, "skills", "b", "ref"))
	wantExists(t, filepath.Join(dest, "skills", "b", "SKILL.md"))
	wantExists(t, filepath.Join(dest, "skills"))
}

// Spec: §7.5.3 — a lock entry that records no file_hash, such as one an older
// release wrote or one a forged lock names, does not delete the file it names.
func TestRemoveStale_MissingHashKeepsFile(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	confinedFixture(t, dest, map[string]string{"src/main.go": "package main"})
	err := RemoveStale(dest, "src/main.go", "")
	if !errors.Is(err, ErrStaleHashMissing) {
		t.Fatalf("RemoveStale with no hash = %v, want ErrStaleHashMissing", err)
	}
	wantContent(t, filepath.Join(dest, "src", "main.go"), "package main")
}

// Spec: §7.5.3 — a forged entry that names a file the operator wrote and
// carries a hash of other bytes leaves the file in place.
func TestRemoveStale_ForgedHashKeepsFile(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	confinedFixture(t, dest, map[string]string{"src/main.go": "package main"})
	err := RemoveStale(dest, "src/main.go", "sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if !errors.Is(err, ErrStaleHashMismatch) {
		t.Fatalf("RemoveStale with a forged hash = %v, want ErrStaleHashMismatch", err)
	}
	wantContent(t, filepath.Join(dest, "src", "main.go"), "package main")
}

// Spec: §7.5.3 — a stale file the cleanup cannot read is left in place, and
// the error wraps ErrStaleUnreadable rather than a hash sentinel.
func TestRemoveStale_UnreadableFileKept(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("file permission bits do not deny reads on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads a file whatever its permission bits")
	}
	dest := t.TempDir()
	confinedFixture(t, dest, map[string]string{"a/locked.md": "content"})
	path := filepath.Join(dest, "a", "locked.md")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
	err := RemoveStale(dest, "a/locked.md", FileHash([]byte("content")))
	if !errors.Is(err, ErrStaleUnreadable) || errors.Is(err, ErrStaleHashMismatch) || errors.Is(err, ErrStaleHashMissing) {
		t.Fatalf("RemoveStale of an unreadable file = %v, want ErrStaleUnreadable", err)
	}
	wantExists(t, path)
}

// Spec: §7.5.3, §7.8 — CheckStale returns what RemoveStale would return
// before its removal and changes nothing: nil for a matching file and for an
// absent path, and the hash sentinels otherwise.
func TestCheckStale_ChangesNothing(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	confinedFixture(t, dest, map[string]string{"a/match.md": "m", "a/edited.md": "e"})
	if err := CheckStale(dest, "a/match.md", FileHash([]byte("m"))); err != nil {
		t.Errorf("CheckStale of a matching file = %v, want nil", err)
	}
	wantExists(t, filepath.Join(dest, "a", "match.md"))
	if err := CheckStale(dest, "a/edited.md", FileHash([]byte("x"))); !errors.Is(err, ErrStaleHashMismatch) {
		t.Errorf("CheckStale of an edited file = %v, want ErrStaleHashMismatch", err)
	}
	if err := CheckStale(dest, "a/edited.md", ""); !errors.Is(err, ErrStaleHashMissing) {
		t.Errorf("CheckStale with no hash = %v, want ErrStaleHashMissing", err)
	}
	if err := CheckStale(dest, "a/gone.md", ""); err != nil {
		t.Errorf("CheckStale of an absent file = %v, want nil", err)
	}
	if err := CheckStale(dest, "missing/x.md", ""); err != nil {
		t.Errorf("CheckStale below an absent directory = %v, want nil", err)
	}
	if err := CheckStale(dest, "a", ""); !errors.Is(err, ErrUnconfinedPath) {
		t.Errorf("CheckStale of a directory = %v, want ErrUnconfinedPath", err)
	}
}

// Spec: §7.5.3 — a matching stale file whose removal fails, here because its
// directory is read-only, stays and returns ErrStaleNotRemoved.
func TestRemoveStale_RemoveFailure(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("directory permission bits do not deny removal on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root removes a file whatever its directory's permission bits")
	}
	dest := t.TempDir()
	confinedFixture(t, dest, map[string]string{"ro/a.md": "a"})
	dir := filepath.Join(dest, "ro")
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if err := RemoveStale(dest, "ro/a.md", FileHash([]byte("a"))); !errors.Is(err, ErrStaleNotRemoved) {
		t.Fatalf("RemoveStale in a read-only directory = %v, want ErrStaleNotRemoved", err)
	}
	wantExists(t, filepath.Join(dir, "a.md"))
}
