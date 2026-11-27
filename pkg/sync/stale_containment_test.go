package sync

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/materialize"
)

// staleAttack is a target directory, a directory beside it that the target's
// prior lock must not reach, and the lock entries that try to reach it. A
// marketplace checkout carries its lock in the repository (§7.8), and a team
// can commit a workspace lock (§7.5.3), so whoever pushes to the repository
// writes these entries.
type staleAttack struct {
	target  string
	outside string
	// entries are the hostile lock entries, each skipped with a warning.
	entries []LockArtifact
	// survivors are the files the run must leave in place.
	survivors map[string]string
}

// newStaleAttack lays out the files the hostile entries name: a victim beside
// the target, a directory reached through a committed symbolic link, a
// symbolic link at a recorded path, a directory at a recorded path, a link to
// .git, a config-merge file behind a link, and the .git and .podium state the
// rule protects. It also lays out two valid stale files: one whose removal
// prunes up to a non-empty directory, and one behind a link to a directory
// inside the target, which the writer accepts and the cleanup therefore
// removes.
func newStaleAttack(t *testing.T, target string) *staleAttack {
	t.Helper()
	base := filepath.Dir(target)
	outside := filepath.Join(base, "outside")
	a := &staleAttack{target: target, outside: outside, survivors: map[string]string{
		filepath.Join(base, "victim", "file.txt"):           "victim\n",
		filepath.Join(outside, "secret.txt"):                "secret\n",
		filepath.Join(outside, "leaf.txt"):                  "leaf\n",
		filepath.Join(outside, "settings.json"):             `{"hooks":{"x-podium-id":"evil","run":"echo"}}`,
		filepath.Join(target, "dirpath", "full", "keep.md"): "operator\n",
		filepath.Join(target, "real", "inner.txt"):          "inner\n",
		filepath.Join(target, ".git", "HEAD"):               "ref: refs/heads/main\n",
		filepath.Join(target, ".podium", "sync.yaml"):       "defaults: {}\n",
		filepath.Join(target, "stale", "other.txt"):         "operator\n",
	}}
	for path, content := range a.survivors {
		writeFile(t, path, content)
	}
	writeFile(t, filepath.Join(target, "stale", "a", "b", "file.md"), "stale\n")
	writeFile(t, filepath.Join(target, "real", "stale.md"), "stale\n")
	if err := os.MkdirAll(filepath.Join(target, "dirpath", "empty"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for link, dest := range map[string]string{
		filepath.Join(target, "claude", "link"): outside,
		filepath.Join(target, "leaflink"):       filepath.Join(outside, "leaf.txt"),
		filepath.Join(target, "inlink"):         filepath.Join(target, "real"),
		filepath.Join(target, "gitlink"):        filepath.Join(target, ".git"),
	} {
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.Symlink(dest, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	a.entries = []LockArtifact{
		{ID: "evil/escape", MaterializedPath: "../victim/file.txt"},
		{ID: "evil/nested-escape", MaterializedPath: "claude/../../victim/file.txt"},
		{ID: "evil/abs", MaterializedPath: filepath.ToSlash(filepath.Join(base, "victim", "file.txt"))},
		{ID: "evil/link-dir", MaterializedPath: "claude/link/secret.txt"},
		{ID: "evil/link-leaf", MaterializedPath: "leaflink"},
		{ID: "evil/dir", MaterializedPath: "dirpath/full"},
		{ID: "evil/empty-dir", MaterializedPath: "dirpath/empty"},
		{ID: "evil/gitlink", MaterializedPath: "gitlink/HEAD"},
		{ID: "evil/config", MaterializedPath: "claude/link/settings.json", Merge: "json"},
		{ID: "evil/git", MaterializedPath: ".git/HEAD"},
		{ID: "evil/state", MaterializedPath: ".podium/sync.yaml"},
	}
	return a
}

// seedLock appends the hostile entries and the valid stale entries to the lock
// at the target. The valid entries record the file hash of the bytes at their
// paths, so the §7.5.3 hash check admits their removal.
func (a *staleAttack) seedLock(t *testing.T) {
	t.Helper()
	lock, err := ReadLock(a.target)
	if err != nil || lock == nil {
		t.Fatalf("ReadLock = (%v, %v)", lock, err)
	}
	lock.Artifacts = append(lock.Artifacts, a.entries...)
	lock.Artifacts = append(lock.Artifacts,
		LockArtifact{ID: "old/stale", MaterializedPath: "stale/a/b/file.md", FileHash: materialize.FileHash([]byte("stale\n"))},
		LockArtifact{ID: "old/stale", MaterializedPath: "inlink/stale.md", FileHash: materialize.FileHash([]byte("stale\n"))},
	)
	if err := WriteLock(a.target, lock); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
}

// verify asserts that every protected file and link survived unchanged, that
// the valid stale file went with its empty parents and the pruning stopped at
// the non-empty stale/ directory, that stderr names every hostile entry, and
// that the lock the run wrote omits them.
func (a *staleAttack) verify(t *testing.T, stderr string) {
	t.Helper()
	for path, want := range a.survivors {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Errorf("%s = (%q, %v), want %q unchanged", path, got, err, want)
		}
	}
	for _, rel := range []string{"claude/link", "leaflink", "inlink", "gitlink", "dirpath/empty"} {
		if _, err := os.Lstat(filepath.Join(a.target, rel)); err != nil {
			t.Errorf("%s must survive: %v", rel, err)
		}
	}
	for _, rel := range []string{"stale/a", "real/stale.md"} {
		if _, err := os.Lstat(filepath.Join(a.target, rel)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s must be removed; lstat err = %v", rel, err)
		}
	}
	for _, e := range a.entries {
		if !strings.Contains(stderr, `"`+e.MaterializedPath+`"`) {
			t.Errorf("stderr must name the skipped entry %q; stderr = %q", e.MaterializedPath, stderr)
		}
	}
	lock, err := ReadLock(a.target)
	if err != nil || lock == nil {
		t.Fatalf("ReadLock after the run = (%v, %v)", lock, err)
	}
	for _, la := range lock.Artifacts {
		if strings.HasPrefix(la.ID, "evil/") || la.ID == "old/stale" {
			t.Errorf("the lock the run wrote still records %s at %q", la.ID, la.MaterializedPath)
		}
	}
}

// Spec: §7.5.3 — a workspace sync whose prior lock names a path outside the
// target, a path through a symbolic link, a symbolic link, a directory, or
// the sync's own state skips each entry with a warning, removes and rewrites
// nothing outside the target or behind the link, still removes a valid stale
// path with its empty parents, and writes a lock without the skipped entries.
//
// Not parallel: the test swaps the process-wide os.Stderr.
func TestRun_StaleCleanupStaysInsideTarget(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	opts := Options{RegistryPath: agentRegistry(t, "company-glossary"), Target: target, AdapterID: "none"}
	syncChanged(t, opts)
	attack := newStaleAttack(t, target)
	attack.seedLock(t)

	var changed bool
	stderr := captureStderr(t, func() { changed = syncChanged(t, opts) })
	attack.verify(t, stderr)
	if !changed {
		t.Errorf("a re-sync that removed a stale file must report Changed true")
	}
	if _, err := os.Stat(filepath.Join(target, "company-glossary", "ARTIFACT.md")); err != nil {
		t.Errorf("the current artifact must stay materialized: %v", err)
	}
}

// Spec: §7.5.3, §7.8 — a marketplace render reconciles against the lock
// committed to the marketplace repository under the same rule: the hostile
// entries are skipped with a warning and the render completes.
//
// Not parallel: the test swaps the process-wide os.Stderr.
func TestRender_StaleCleanupStaysInsideCheckout(t *testing.T) {
	workdir := filepath.Join(t.TempDir(), "checkout")
	opts := renderOpts(t, renderFixtureRegistry(t), workdir, []string{"claude-code"})
	if _, err := Render(context.Background(), opts); err != nil {
		t.Fatalf("first Render: %v", err)
	}
	attack := newStaleAttack(t, workdir)
	attack.seedLock(t)

	var res *RenderResult
	var err error
	stderr := captureStderr(t, func() { res, err = Render(context.Background(), opts) })
	if err != nil {
		t.Fatalf("Render over a hostile lock: %v", err)
	}
	attack.verify(t, stderr)
	if !res.Changed || !containsStr(res.ChangedArtifacts, removedOwner) {
		t.Errorf("Render = (Changed %v, %v), want the stale removal reported", res.Changed, res.ChangedArtifacts)
	}
	if _, err := os.Stat(filepath.Join(workdir, "claude", "finance-pack", "skills", "pay-invoice", "SKILL.md")); err != nil {
		t.Errorf("the rendered plugin content must stay: %v", err)
	}
}

// Spec: §7.5.3 — the exported Reconcile applies the rule to paths its caller
// did not filter, so a prior map read straight from a hostile lock removes
// nothing outside the checkout.
//
// Not parallel: the test swaps the process-wide os.Stderr.
func TestReconcile_RefusesUnfilteredHostilePaths(t *testing.T) {
	checkout := filepath.Join(t.TempDir(), "checkout")
	attack := newStaleAttack(t, checkout)
	if err := WriteLock(checkout, &LockFile{}); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
	attack.seedLock(t)
	raw, err := ReadLock(checkout)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	stderr := captureStderr(t, func() { Reconcile(checkout, PriorPaths(raw), map[string]bool{}) })
	for path, want := range attack.survivors {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Errorf("%s = (%q, %v), want %q unchanged", path, got, err, want)
		}
	}
	for _, e := range attack.entries {
		if !strings.Contains(stderr, `"`+e.MaterializedPath+`"`) {
			t.Errorf("stderr must name the skipped entry %q; stderr = %q", e.MaterializedPath, stderr)
		}
	}
	if _, err := os.Lstat(filepath.Join(checkout, "stale", "a")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the valid stale path must still be removed; lstat err = %v", err)
	}
}
