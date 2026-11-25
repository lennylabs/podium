package materialize

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// confinedFixture writes content at each slash-separated rel below dir.
func confinedFixture(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
}

// mustSymlink creates link pointing at target.
func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

// wantExists fails the test unless path is present (as a link itself when it
// is one).
func wantExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Errorf("%s must survive: %v", path, err)
	}
}

// wantGone fails the test unless path is absent.
func wantGone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s must be removed; lstat err = %v", path, err)
	}
}

// Spec: §7.5.3 — the stale-file cleanup removes a regular file below the
// target and prunes the directories that held it while they are empty,
// stopping at the first non-empty directory and never removing the root.
func TestRemoveStale_RemovesFileAndPrunesEmptyParents(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	confinedFixture(t, dest, map[string]string{
		"skills/old/nested/SKILL.md": "x",
		"skills/other/SKILL.md":      "y",
	})
	if err := RemoveStale(dest, "skills/old/nested/SKILL.md"); err != nil {
		t.Fatalf("RemoveStale: %v", err)
	}
	wantGone(t, filepath.Join(dest, "skills", "old"))
	wantExists(t, filepath.Join(dest, "skills", "other", "SKILL.md"))

	// The last file below the root prunes up to, and not including, the root.
	if err := RemoveStale(dest, "skills/other/SKILL.md"); err != nil {
		t.Fatalf("RemoveStale: %v", err)
	}
	wantGone(t, filepath.Join(dest, "skills"))
	wantExists(t, dest)
}

// Spec: §7.5.3 — a file the operator already deleted still lets its empty
// parents go, and a missing destination is a no-op.
func TestRemoveStale_AbsentFile(t *testing.T) {
	t.Parallel()
	dest := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dest, "a", "b"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := RemoveStale(dest, "a/b/gone.md"); err != nil {
		t.Fatalf("RemoveStale: %v", err)
	}
	wantGone(t, filepath.Join(dest, "a"))
	if err := RemoveStale(filepath.Join(dest, "missing"), "a/b.md"); err != nil {
		t.Errorf("RemoveStale on a missing destination = %v, want nil", err)
	}
	if err := RemoveStale(dest, "missing-dir/x/y.md"); err != nil {
		t.Errorf("RemoveStale below a missing directory = %v, want nil", err)
	}
}

// Spec: §6.7, §7.5.3 — a recorded path reached through a symbolic link, a
// symbolic link at the path, and a directory at the path are refused, and
// nothing outside the root or behind the link is removed.
func TestRemoveStale_RefusesUnconfinedPaths(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	dest := filepath.Join(base, "target")
	outside := filepath.Join(base, "outside")
	confinedFixture(t, outside, map[string]string{"secret.txt": "s", "leaf.txt": "l"})
	confinedFixture(t, dest, map[string]string{
		"real/inner.txt": "i",
		"dir/keep/a.txt": "a",
	})
	mustSymlink(t, outside, filepath.Join(dest, "claude", "link"))
	mustSymlink(t, filepath.Join(dest, "real"), filepath.Join(dest, "inlink"))
	mustSymlink(t, filepath.Join(outside, "leaf.txt"), filepath.Join(dest, "leaflink"))

	cases := []struct {
		rel     string
		wantErr error
		keep    string
	}{
		{"claude/link/secret.txt", ErrUnconfinedPath, filepath.Join(outside, "secret.txt")},
		{"inlink/inner.txt", ErrUnconfinedPath, filepath.Join(dest, "real", "inner.txt")},
		{"inlink/gone.md", ErrUnconfinedPath, filepath.Join(dest, "real")},
		{"leaflink", ErrUnconfinedPath, filepath.Join(outside, "leaf.txt")},
		{"dir/keep", ErrUnconfinedPath, filepath.Join(dest, "dir", "keep", "a.txt")},
		{"real/inner.txt/x", ErrUnconfinedPath, filepath.Join(dest, "real", "inner.txt")},
		{"../outside/secret.txt", ErrOutOfDestination, filepath.Join(outside, "secret.txt")},
		{"/etc/hosts", ErrOutOfDestination, ""},
		{"", ErrOutOfDestination, ""},
		{"a//b", ErrOutOfDestination, ""},
		{"a/./b", ErrOutOfDestination, ""},
	}
	for _, tc := range cases {
		err := RemoveStale(dest, tc.rel)
		if !errors.Is(err, tc.wantErr) {
			t.Errorf("RemoveStale(%q) = %v, want %v", tc.rel, err, tc.wantErr)
		}
		if tc.keep != "" {
			wantExists(t, tc.keep)
		}
	}
	wantExists(t, filepath.Join(dest, "claude", "link"))
	wantExists(t, filepath.Join(dest, "leaflink"))
	wantExists(t, filepath.Join(dest, "inlink"))
	if err := RemoveStale("", "a.txt"); !errors.Is(err, ErrEmptyDestination) {
		t.Errorf("RemoveStale with an empty destination = %v, want ErrEmptyDestination", err)
	}
}

// Spec: §7.5.3 — the target root itself may be reached through a symbolic
// link the operator configured.
func TestRemoveStale_SymlinkedRoot(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	realDir := filepath.Join(base, "real")
	confinedFixture(t, realDir, map[string]string{"a/b.md": "x"})
	link := filepath.Join(base, "link")
	mustSymlink(t, realDir, link)
	if err := RemoveStale(link, "a/b.md"); err != nil {
		t.Fatalf("RemoveStale through a symlinked root: %v", err)
	}
	wantGone(t, filepath.Join(realDir, "a"))
	wantExists(t, realDir)
}

// Spec: §7.5.3 — ReadConfined reads a regular file reached without a symbolic
// link, and refuses a link, a directory, and an escaping path.
func TestReadConfined(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	dest := filepath.Join(base, "target")
	confinedFixture(t, dest, map[string]string{"a/b.txt": "content"})
	confinedFixture(t, base, map[string]string{"outside.txt": "o"})
	mustSymlink(t, filepath.Join(base, "outside.txt"), filepath.Join(dest, "leaflink"))

	got, err := ReadConfined(dest, "a/b.txt")
	if err != nil || string(got) != "content" {
		t.Errorf("ReadConfined = (%q, %v), want (content, nil)", got, err)
	}
	if _, err := ReadConfined(dest, "a/missing.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadConfined of a missing file = %v, want fs.ErrNotExist", err)
	}
	for _, rel := range []string{"leaflink", "a"} {
		if _, err := ReadConfined(dest, rel); !errors.Is(err, ErrUnconfinedPath) {
			t.Errorf("ReadConfined(%q) = %v, want ErrUnconfinedPath", rel, err)
		}
	}
	if _, err := ReadConfined(dest, "../outside.txt"); !errors.Is(err, ErrOutOfDestination) {
		t.Errorf("ReadConfined(../outside.txt) = %v, want ErrOutOfDestination", err)
	}
}

// Spec: §6.7, §7.5.3 — the config-merge reconciliation rewrites a regular
// file in place, is a no-op on a missing file, and leaves a file behind a
// symbolic link untouched.
func TestRewriteConfined(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	dest := filepath.Join(base, "target")
	outside := filepath.Join(base, "outside")
	confinedFixture(t, dest, map[string]string{".claude/settings.json": "old"})
	confinedFixture(t, outside, map[string]string{"settings.json": "old"})
	mustSymlink(t, outside, filepath.Join(dest, "link"))
	upper := func(b []byte) []byte { return []byte("new:" + string(b)) }

	if err := RewriteConfined(dest, ".claude/settings.json", upper); err != nil {
		t.Fatalf("RewriteConfined: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dest, ".claude", "settings.json")); string(got) != "new:old" {
		t.Errorf("rewritten content = %q, want new:old", got)
	}
	if err := RewriteConfined(dest, "missing.json", upper); err != nil {
		t.Errorf("RewriteConfined of a missing file = %v, want nil", err)
	}
	wantGone(t, filepath.Join(dest, "missing.json"))
	if err := RewriteConfined(dest, "link/settings.json", upper); !errors.Is(err, ErrUnconfinedPath) {
		t.Errorf("RewriteConfined through a link = %v, want ErrUnconfinedPath", err)
	}
	if got, _ := os.ReadFile(filepath.Join(outside, "settings.json")); string(got) != "old" {
		t.Errorf("file behind the link was rewritten: %q", got)
	}
	if err := RewriteConfined(dest, "../outside/settings.json", upper); !errors.Is(err, ErrOutOfDestination) {
		t.Errorf("RewriteConfined(../outside/settings.json) = %v, want ErrOutOfDestination", err)
	}
}
