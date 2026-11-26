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

// wantContent fails the test unless the file at path holds want.
func wantContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Errorf("%s = (%q, %v), want %q", path, got, err, want)
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

// Spec: §6.7, §7.5.3 — the cleanup refuses a link that resolves outside the
// root, a link at the path, a directory at the path, a link into .git, a
// .podium directory that is a link, and a path the recorded-path rule
// refuses, and nothing outside the root or behind the link is removed.
func TestRemoveStale_RefusesUnconfinedPaths(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	dest := filepath.Join(base, "target")
	outside := filepath.Join(base, "outside")
	confinedFixture(t, outside, map[string]string{"secret.txt": "s", "leaf.txt": "l"})
	confinedFixture(t, dest, map[string]string{
		"real/inner.txt":                 "i",
		"dir/keep/a.txt":                 "a",
		".git/HEAD":                      "ref",
		"podium-real/resources/a/run.sh": "r",
	})
	mustSymlink(t, outside, filepath.Join(dest, "claude", "link"))
	mustSymlink(t, filepath.Join(outside, "leaf.txt"), filepath.Join(dest, "leaflink"))
	mustSymlink(t, filepath.Join(dest, ".git"), filepath.Join(dest, "gitlink"))
	mustSymlink(t, filepath.Join(dest, "podium-real"), filepath.Join(dest, ".podium"))
	mustSymlink(t, "loop", filepath.Join(dest, "loop"))
	mustSymlink(t, "real/inner.txt", filepath.Join(dest, "inleaf"))

	cases := []struct {
		rel     string
		wantErr error
		keep    string
	}{
		{"claude/link/secret.txt", ErrUnconfinedPath, filepath.Join(outside, "secret.txt")},
		{"claude/link/sub/secret.txt", ErrUnconfinedPath, filepath.Join(outside, "secret.txt")},
		{"loop/x.txt", ErrUnconfinedPath, ""},
		{"leaflink", ErrUnconfinedPath, filepath.Join(outside, "leaf.txt")},
		{"inleaf", ErrUnconfinedPath, filepath.Join(dest, "real", "inner.txt")},
		{"dir/keep", ErrUnconfinedPath, filepath.Join(dest, "dir", "keep", "a.txt")},
		{"real/inner.txt/x", ErrUnconfinedPath, filepath.Join(dest, "real", "inner.txt")},
		{"gitlink/HEAD", ErrUnconfinedPath, filepath.Join(dest, ".git", "HEAD")},
		{".podium/resources/a/run.sh", ErrUnconfinedPath, filepath.Join(dest, "podium-real", "resources", "a", "run.sh")},
		{"../outside/secret.txt", ErrUnsafeRecordedPath, filepath.Join(outside, "secret.txt")},
		{".git/HEAD", ErrUnsafeRecordedPath, filepath.Join(dest, ".git", "HEAD")},
		{"/etc/hosts", ErrUnsafeRecordedPath, ""},
		{"", ErrUnsafeRecordedPath, ""},
		{"a//b", ErrUnsafeRecordedPath, ""},
		{"a/./b", ErrUnsafeRecordedPath, ""},
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
	for _, link := range []string{"claude/link", "leaflink", "gitlink", ".podium"} {
		wantExists(t, filepath.Join(dest, filepath.FromSlash(link)))
	}
	if err := RemoveStale("", "a.txt"); !errors.Is(err, ErrEmptyDestination) {
		t.Errorf("RemoveStale with an empty destination = %v, want ErrEmptyDestination", err)
	}
}

// Spec: §6.7, §7.5.3 — the cleanup accepts the directory links the writer
// accepts. A stale file behind a link that resolves inside the root is
// removed, the pruning removes the empty directories the path itself reaches
// below the link, and the directory the link names stays with the link.
func TestRemoveStale_FollowsInRootDirectoryLink(t *testing.T) {
	t.Parallel()
	dest := filepath.Join(t.TempDir(), "target")
	confinedFixture(t, dest, map[string]string{
		"vendor-pack/a/ARTIFACT.md":   "a",
		"vendor-pack/b/c/ARTIFACT.md": "b",
	})
	mustSymlink(t, "vendor-pack", filepath.Join(dest, "pack"))
	if err := RemoveStale(dest, "pack/b/c/ARTIFACT.md"); err != nil {
		t.Fatalf("RemoveStale through an in-root link: %v", err)
	}
	wantGone(t, filepath.Join(dest, "vendor-pack", "b"))
	wantExists(t, filepath.Join(dest, "vendor-pack", "a", "ARTIFACT.md"))
	if err := RemoveStale(dest, "pack/a/ARTIFACT.md"); err != nil {
		t.Fatalf("RemoveStale of the last file behind the link: %v", err)
	}
	wantGone(t, filepath.Join(dest, "vendor-pack", "a"))
	wantExists(t, filepath.Join(dest, "vendor-pack"))
	wantExists(t, filepath.Join(dest, "pack"))
	if err := RemoveStale(dest, "pack/gone/x.md"); err != nil {
		t.Errorf("RemoveStale below a missing directory behind the link = %v, want nil", err)
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

// Spec: §7.5.3 — ReadRecorded reads a regular file the cleanup accepts, and
// refuses a link at the path, a directory, and an escaping path.
func TestReadRecorded(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	dest := filepath.Join(base, "target")
	confinedFixture(t, dest, map[string]string{"a/b.txt": "content", ".git/HEAD": "ref"})
	confinedFixture(t, base, map[string]string{"outside.txt": "o"})
	mustSymlink(t, filepath.Join(base, "outside.txt"), filepath.Join(dest, "leaflink"))
	mustSymlink(t, "a", filepath.Join(dest, "alink"))
	mustSymlink(t, "a/b.txt", filepath.Join(dest, "inleaf"))
	mustSymlink(t, ".git/HEAD", filepath.Join(dest, "gitleaf"))
	mustSymlink(t, "a/gone.txt", filepath.Join(dest, "dangling"))

	for _, rel := range []string{"a/b.txt", "alink/b.txt", "inleaf"} {
		got, err := ReadRecorded(dest, rel)
		if err != nil || string(got) != "content" {
			t.Errorf("ReadRecorded(%q) = (%q, %v), want (content, nil)", rel, got, err)
		}
	}
	for _, rel := range []string{"a/missing.txt", "dangling"} {
		if _, err := ReadRecorded(dest, rel); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("ReadRecorded(%q) = %v, want fs.ErrNotExist", rel, err)
		}
	}
	for _, rel := range []string{"leaflink", "a", "gitleaf"} {
		if _, err := ReadRecorded(dest, rel); !errors.Is(err, ErrUnconfinedPath) {
			t.Errorf("ReadRecorded(%q) = %v, want ErrUnconfinedPath", rel, err)
		}
	}
	if _, err := ReadRecorded(dest, "../outside.txt"); !errors.Is(err, ErrUnsafeRecordedPath) {
		t.Errorf("ReadRecorded(../outside.txt) = %v, want ErrUnsafeRecordedPath", err)
	}
}

// Spec: §6.7, §7.5.3 — the config-merge reconciliation rewrites a regular
// file in place, is a no-op on a missing file, and leaves a file behind a
// link that leaves the root untouched.
func TestRewriteStale(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	dest := filepath.Join(base, "target")
	outside := filepath.Join(base, "outside")
	confinedFixture(t, dest, map[string]string{".claude/settings.json": "old"})
	confinedFixture(t, outside, map[string]string{"settings.json": "old"})
	mustSymlink(t, outside, filepath.Join(dest, "link"))
	upper := func(b []byte) []byte { return []byte("new:" + string(b)) }

	if err := RewriteStale(dest, ".claude/settings.json", upper); err != nil {
		t.Fatalf("RewriteStale: %v", err)
	}
	wantContent(t, filepath.Join(dest, ".claude", "settings.json"), "new:old")
	for _, rel := range []string{"missing.json", "missing-dir/x.json"} {
		if err := RewriteStale(dest, rel, upper); err != nil {
			t.Errorf("RewriteStale(%q) of a missing file = %v, want nil", rel, err)
		}
	}
	wantGone(t, filepath.Join(dest, "missing.json"))
	if err := RewriteStale(dest, "link/settings.json", upper); !errors.Is(err, ErrUnconfinedPath) {
		t.Errorf("RewriteStale through a link = %v, want ErrUnconfinedPath", err)
	}
	wantContent(t, filepath.Join(outside, "settings.json"), "old")
	if err := RewriteStale(dest, "../outside/settings.json", upper); !errors.Is(err, ErrUnsafeRecordedPath) {
		t.Errorf("RewriteStale(../outside/settings.json) = %v, want ErrUnsafeRecordedPath", err)
	}
}

// Spec: §6.7, §7.5.3 — a config-merge path that is a link to a regular file
// inside the target, such as GEMINI.md linked to AGENTS.md, is rewritten
// through the link and the link stays. A link that resolves outside the
// target, into .git, or into the sync state is refused, and nothing changes.
func TestRewriteStale_LeafLink(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	dest := filepath.Join(base, "target")
	confinedFixture(t, dest, map[string]string{
		"AGENTS.md":         "agents",
		".git/config":       "git",
		".podium/sync.lock": "lock",
	})
	confinedFixture(t, base, map[string]string{"outside.md": "outside"})
	mustSymlink(t, "AGENTS.md", filepath.Join(dest, "GEMINI.md"))
	mustSymlink(t, ".git/config", filepath.Join(dest, "GIT.md"))
	mustSymlink(t, ".podium/sync.lock", filepath.Join(dest, "STATE.md"))
	mustSymlink(t, filepath.Join(base, "outside.md"), filepath.Join(dest, "OUT.md"))
	upper := func(b []byte) []byte { return []byte("new:" + string(b)) }

	if err := RewriteStale(dest, "GEMINI.md", upper); err != nil {
		t.Fatalf("RewriteStale through an in-target link: %v", err)
	}
	wantContent(t, filepath.Join(dest, "AGENTS.md"), "new:agents")
	if info, err := os.Lstat(filepath.Join(dest, "GEMINI.md")); err != nil || info.Mode()&fs.ModeSymlink == 0 {
		t.Errorf("GEMINI.md must stay a link; lstat = (%v, %v)", info, err)
	}
	for _, rel := range []string{"GIT.md", "STATE.md", "OUT.md"} {
		if err := RewriteStale(dest, rel, upper); !errors.Is(err, ErrUnconfinedPath) {
			t.Errorf("RewriteStale(%q) = %v, want ErrUnconfinedPath", rel, err)
		}
	}
	wantContent(t, filepath.Join(dest, ".git", "config"), "git")
	wantContent(t, filepath.Join(dest, ".podium", "sync.lock"), "lock")
	wantContent(t, filepath.Join(base, "outside.md"), "outside")
}
