package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/materialize"
)

// hookArtifactSrc is a hook the claude-code adapter merges into the shared
// .claude/settings.json rather than writing as a file of its own.
const hookArtifactSrc = "---\ntype: hook\nname: stop\nversion: 1.0.0\ndescription: Stop hook.\nhook_event: stop\nhook_action: |\n  echo done\n---\n\nbody\n"

// wantFileHashes asserts that every lock entry at target records the file
// hash of the bytes at its path when it is a standalone path, and records none
// when it is a config-merge path.
func wantFileHashes(t *testing.T, target string) *LockFile {
	t.Helper()
	lock, err := ReadLock(target)
	if err != nil || lock == nil {
		t.Fatalf("ReadLock = (%v, %v)", lock, err)
	}
	for _, e := range lock.Artifacts {
		if e.Merge != "" {
			if e.FileHash != "" {
				t.Errorf("config-merge entry %q records file_hash %q, want none", e.MaterializedPath, e.FileHash)
			}
			continue
		}
		want := materialize.FileHash(readTargetFile(t, target, e.MaterializedPath))
		if e.FileHash != want {
			t.Errorf("entry %q file_hash = %q, want %q", e.MaterializedPath, e.FileHash, want)
		}
	}
	return lock
}

// lockRecords reports whether the lock at target records rel.
func lockRecords(t *testing.T, target, rel string) bool {
	t.Helper()
	lock, err := ReadLock(target)
	if err != nil || lock == nil {
		t.Fatalf("ReadLock = (%v, %v)", lock, err)
	}
	for _, e := range lock.Artifacts {
		if e.MaterializedPath == rel {
			return true
		}
	}
	return false
}

// Spec: §7.5.3 — the lock round-trips a per-file file_hash under that key.
func TestLockFile_FileHashRoundTrip(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	hash := materialize.FileHash([]byte("body"))
	lock := &LockFile{Artifacts: []LockArtifact{
		{ID: "a", MaterializedPath: "a/ARTIFACT.md", FileHash: hash},
		{ID: "b", MaterializedPath: ".mcp.json", Merge: "json"},
	}}
	if err := WriteLock(target, lock); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}
	raw := string(readTargetFile(t, target, ".podium/sync.lock"))
	if !strings.Contains(raw, "file_hash: "+hash) {
		t.Errorf("lock must record file_hash: %s; got:\n%s", hash, raw)
	}
	if strings.Count(raw, "file_hash:") != 1 {
		t.Errorf("an entry without a file hash must omit the key; got:\n%s", raw)
	}
	got, err := ReadLock(target)
	if err != nil || got == nil {
		t.Fatalf("ReadLock = (%v, %v)", got, err)
	}
	if got.Artifacts[0].FileHash != hash || got.Artifacts[1].FileHash != "" {
		t.Errorf("ReadLock file hashes = %q, %q; want %q, empty", got.Artifacts[0].FileHash, got.Artifacts[1].FileHash, hash)
	}
}

// Spec: §7.5.3 — sync records the file hash of each file it writes whole and
// none for a config-merge path, which the cleanup never deletes.
func TestRun_RecordsFileHashForWholeFiles(t *testing.T) {
	t.Parallel()
	registry := agentRegistry(t, "team/glossary")
	testharness.WriteTree(t, registry, testharness.WriteTreeOption{Path: "audit/stop/ARTIFACT.md", Content: hookArtifactSrc})
	target := t.TempDir()
	if _, err := Run(Options{RegistryPath: registry, Target: target, AdapterID: "claude-code"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	lock := wantFileHashes(t, target)
	var merged, whole int
	for _, e := range lock.Artifacts {
		if e.Merge != "" {
			merged++
		} else {
			whole++
		}
	}
	if merged == 0 || whole == 0 {
		t.Fatalf("fixture must produce a config-merge and a standalone entry; lock = %+v", lock.Artifacts)
	}
}

// Spec: §7.5.3 — a narrowed scope removes an out-of-scope file that still
// holds the bytes sync wrote, and the lock then records only what is in scope.
func TestRun_NarrowedScopeRemovesUnchangedFile(t *testing.T) {
	t.Parallel()
	target := t.TempDir()
	opts := Options{RegistryPath: agentRegistry(t, "team/a", "team/b"), Target: target, AdapterID: "none"}
	syncChanged(t, opts)
	opts.Scope = ScopeFilter{Include: []string{"team/a"}}
	syncChanged(t, opts)
	if _, err := os.Stat(filepath.Join(target, "team", "b")); !os.IsNotExist(err) {
		t.Errorf("the out-of-scope file and its empty directory must be removed; stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "team", "a", "ARTIFACT.md")); err != nil {
		t.Errorf("the in-scope file must stay: %v", err)
	}
	wantFileHashes(t, target)
}

// Spec: §7.5.3 — an out-of-scope file the operator edited after sync wrote it
// stays in place, is named on standard error, and leaves the lock, so the next
// sync neither deletes it nor names it again.
//
// Not parallel: the test swaps the process-wide os.Stderr.
func TestRun_EditedStaleFileKeptAndReported(t *testing.T) {
	target := t.TempDir()
	opts := Options{RegistryPath: agentRegistry(t, "team/a", "team/b"), Target: target, AdapterID: "none"}
	syncChanged(t, opts)
	edited := filepath.Join(target, "team", "b", "ARTIFACT.md")
	writeFile(t, edited, "alice's notes\n")

	opts.Scope = ScopeFilter{Include: []string{"team/a"}}
	stderr := captureStderr(t, func() { syncChanged(t, opts) })
	wantContentEq(t, edited, "alice's notes\n")
	if !strings.Contains(stderr, `"team/b/ARTIFACT.md"`) || !strings.Contains(stderr, "file_hash") {
		t.Errorf("stderr must name the kept file and the hash mismatch; stderr = %q", stderr)
	}
	if lockRecords(t, target, "team/b/ARTIFACT.md") {
		t.Error("the lock must not record the kept file")
	}

	stderr = captureStderr(t, func() { syncChanged(t, opts) })
	if strings.Contains(stderr, "team/b/ARTIFACT.md") {
		t.Errorf("a later sync must not name the dropped entry again; stderr = %q", stderr)
	}
	wantContentEq(t, edited, "alice's notes\n")
}

// Spec: §7.5.3 — a forged lock entry that names a file the operator wrote,
// with no file hash or with a hash of other bytes, does not delete it.
//
// Not parallel: the test swaps the process-wide os.Stderr.
func TestRun_ForgedLockEntryKeepsUserFile(t *testing.T) {
	target := t.TempDir()
	opts := Options{RegistryPath: agentRegistry(t, "team/a"), Target: target, AdapterID: "none"}
	syncChanged(t, opts)
	writeFile(t, filepath.Join(target, "src", "main.go"), "package main\n")
	writeFile(t, filepath.Join(target, "README.md"), "# acme\n")
	lock, err := ReadLock(target)
	if err != nil || lock == nil {
		t.Fatalf("ReadLock = (%v, %v)", lock, err)
	}
	lock.Artifacts = append(lock.Artifacts,
		LockArtifact{ID: "evil/no-hash", MaterializedPath: "src/main.go"},
		LockArtifact{ID: "evil/forged", MaterializedPath: "README.md", FileHash: materialize.FileHash([]byte("other"))},
	)
	if err := WriteLock(target, lock); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}

	stderr := captureStderr(t, func() { syncChanged(t, opts) })
	wantContentEq(t, filepath.Join(target, "src", "main.go"), "package main\n")
	wantContentEq(t, filepath.Join(target, "README.md"), "# acme\n")
	for _, rel := range []string{"src/main.go", "README.md"} {
		if !strings.Contains(stderr, `"`+rel+`"`) {
			t.Errorf("stderr must name %q; stderr = %q", rel, stderr)
		}
		if lockRecords(t, target, rel) {
			t.Errorf("the lock must not carry the forged entry for %q", rel)
		}
	}
}

// Spec: §7.5.5, §7.5.3 — `podium sync override --remove` deletes the removed
// artifact's files through the guarded cleanup: an unchanged file goes, and
// an edited one stays.
//
// Not parallel: the test swaps the process-wide os.Stderr.
func TestOverride_RemoveUsesGuardedCleanup(t *testing.T) {
	target := t.TempDir()
	registry := agentRegistry(t, "team/a", "team/b")
	syncChanged(t, Options{RegistryPath: registry, Target: target, AdapterID: "none"})
	edited := filepath.Join(target, "team", "b", "ARTIFACT.md")
	writeFile(t, edited, "alice's notes\n")

	var oerr error
	stderr := captureStderr(t, func() {
		_, oerr = Override(OverrideOptions{Target: target, RegistryPath: registry, AdapterID: "none", Remove: []string{"team/a", "team/b"}})
	})
	if oerr != nil {
		t.Fatalf("Override: %v", oerr)
	}
	if _, err := os.Stat(filepath.Join(target, "team", "a")); !os.IsNotExist(err) {
		t.Errorf("the unchanged removed file must be deleted; stat err = %v", err)
	}
	wantContentEq(t, edited, "alice's notes\n")
	if !strings.Contains(stderr, `"team/b/ARTIFACT.md"`) {
		t.Errorf("stderr must name the kept file; stderr = %q", stderr)
	}
}

// Spec: §7.5.3, §7.8 — a marketplace render records the file hash of each
// file it writes whole, deletes a stale file that still matches, and keeps a
// stale file that changed after the render wrote it.
//
// Not parallel: the test swaps the process-wide os.Stderr.
func TestRender_StaleCleanupChecksFileHash(t *testing.T) {
	workdir := t.TempDir()
	opts := renderOpts(t, renderFixtureRegistry(t), workdir, []string{"claude-code"})
	if _, err := Render(context.Background(), opts); err != nil {
		t.Fatalf("first Render: %v", err)
	}
	wantFileHashes(t, workdir)
	edited := filepath.Join(workdir, "claude", "security-baseline", "skills", "lockdown", "SKILL.md")
	writeFile(t, edited, "edited in the checkout\n")
	removed := filepath.Join(workdir, "claude", "finance-pack", "skills", "run-variance", "SKILL.md")
	if _, err := os.Stat(removed); err != nil {
		t.Fatalf("fixture must render %s: %v", removed, err)
	}

	opts.Plugins = []PluginFilter{{Name: "finance-pack", Include: []string{"finance/ap/**"}}}
	var err error
	stderr := captureStderr(t, func() { _, err = Render(context.Background(), opts) })
	if err != nil {
		t.Fatalf("second Render: %v", err)
	}
	wantContentEq(t, edited, "edited in the checkout\n")
	if !strings.Contains(stderr, "claude/security-baseline/skills/lockdown/SKILL.md") {
		t.Errorf("stderr must name the kept file; stderr = %q", stderr)
	}
	if _, serr := os.Stat(removed); !os.IsNotExist(serr) {
		t.Errorf("the unchanged stale file must be removed; stat err = %v", serr)
	}
	if lockRecords(t, workdir, "claude/security-baseline/skills/lockdown/SKILL.md") {
		t.Error("the lock must not record the kept file")
	}
	wantFileHashes(t, workdir)
}

// wantContentEq fails the test unless the file at path holds want.
func wantContentEq(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Errorf("%s = (%q, %v), want %q", path, got, err, want)
	}
}
