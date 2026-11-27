package sync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

// staleRenderPath is a skill the render fixture writes for finance-pack and
// that narrowFinancePack takes out of the view.
const staleRenderPath = "claude/finance-pack/skills/run-variance/SKILL.md"

// narrowFinancePack returns the render plugins narrowed to finance/ap/**, so
// the finance/close skill and the whole security-baseline plugin leave the
// view.
func narrowFinancePack() []PluginFilter {
	return []PluginFilter{{Name: "finance-pack", Include: []string{"finance/ap/**"}}}
}

// Spec: §7.5.3, §7.8 — a marketplace render records the file hash of each
// file it writes whole and deletes a stale file that still holds those bytes.
func TestRender_StaleCleanupRemovesMatchingFile(t *testing.T) {
	t.Parallel()
	workdir := t.TempDir()
	opts := renderOpts(t, renderFixtureRegistry(t), workdir, []string{"claude-code"})
	if _, err := Render(context.Background(), opts); err != nil {
		t.Fatalf("first Render: %v", err)
	}
	wantFileHashes(t, workdir)
	stale := filepath.Join(workdir, filepath.FromSlash(staleRenderPath))
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("fixture must render %s: %v", staleRenderPath, err)
	}

	opts.Plugins = narrowFinancePack()
	res, err := Render(context.Background(), opts)
	if err != nil {
		t.Fatalf("narrowed Render: %v", err)
	}
	if !res.Changed {
		t.Error("a render that removed stale files must report Changed true")
	}
	if _, serr := os.Stat(stale); !os.IsNotExist(serr) {
		t.Errorf("the unchanged stale file must be removed; stat err = %v", serr)
	}
	if lockRecords(t, workdir, staleRenderPath) {
		t.Error("the lock must not record the removed file")
	}
	wantFileHashes(t, workdir)
}

// Spec: §7.5.3, §7.8 — a marketplace render whose cleanup leaves a stale
// recorded file in place fails with ErrStaleFilesKept, naming each kept file
// and its reason, and does not rewrite the lock. Otherwise the render would
// report no change for a removal it could not make, the publish phase would
// skip the commit, and every later render would keep the file again. After the
// publisher removes the file by hand, the next render succeeds.
func TestRender_KeptStaleFileFailsRender(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(t *testing.T, workdir string)
		reason string
	}{
		{
			name: "file edited after the render",
			mutate: func(t *testing.T, workdir string) {
				writeFile(t, filepath.Join(workdir, filepath.FromSlash(staleRenderPath)), "line one\r\nline two\r\n")
			},
			reason: "does not match the file_hash",
		},
		{
			name: "lock written before file_hash",
			mutate: func(t *testing.T, workdir string) {
				lock, err := ReadLock(workdir)
				if err != nil || lock == nil {
					t.Fatalf("ReadLock = (%v, %v)", lock, err)
				}
				for i := range lock.Artifacts {
					lock.Artifacts[i].FileHash = ""
				}
				if err := WriteLock(workdir, lock); err != nil {
					t.Fatalf("WriteLock: %v", err)
				}
			},
			reason: "records no file_hash",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			workdir := t.TempDir()
			opts := renderOpts(t, renderFixtureRegistry(t), workdir, []string{"claude-code"})
			if _, err := Render(context.Background(), opts); err != nil {
				t.Fatalf("first Render: %v", err)
			}
			tc.mutate(t, workdir)
			stale := filepath.Join(workdir, filepath.FromSlash(staleRenderPath))
			before := readTargetFile(t, workdir, staleRenderPath)

			opts.Plugins = narrowFinancePack()
			_, err := Render(context.Background(), opts)
			if !errors.Is(err, ErrStaleFilesKept) {
				t.Fatalf("narrowed Render = %v, want ErrStaleFilesKept", err)
			}
			for _, want := range []string{staleRenderPath, tc.reason, "by hand"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error must contain %q: %v", want, err)
				}
			}
			wantContentEq(t, stale, string(before))
			if !lockRecords(t, workdir, staleRenderPath) {
				t.Error("a failed render must leave the prior lock in place")
			}

			// The publisher removes the file by hand; the next render reconciles.
			if err := os.Remove(stale); err != nil {
				t.Fatalf("Remove: %v", err)
			}
			if _, err := Render(context.Background(), opts); err != nil && strings.Contains(err.Error(), staleRenderPath) {
				t.Fatalf("Render after the manual removal still names the file: %v", err)
			}
		})
	}
}

// Spec: §7.5.3, §7.8 — a render that fails on a kept stale file fails before
// it writes, so a reused checkout keeps its tree and its lock. After the
// publisher removes the named file, the next render makes the whole catalog
// change and reports it, so the publish phase commits it.
func TestRender_KeptStaleFileLeavesReusedCheckoutUntouched(t *testing.T) {
	t.Parallel()
	workdir := t.TempDir()
	opts := renderOpts(t, renderFixtureRegistry(t), workdir, []string{"claude-code"})
	if _, err := Render(context.Background(), opts); err != nil {
		t.Fatalf("first Render: %v", err)
	}
	edited := filepath.Join(workdir, filepath.FromSlash(staleRenderPath))
	writeFile(t, edited, "edited in the checkout\n")
	const goneLater = "claude/finance-pack/skills/pay-invoice/SKILL.md"
	manifest := readTargetFile(t, workdir, ".claude-plugin/marketplace.json")
	lockBefore := readTargetFile(t, workdir, ".podium/sync.lock")

	opts.Plugins = []PluginFilter{{Name: "security-baseline", Include: []string{"security/baseline/**"}}}
	if _, err := Render(context.Background(), opts); !errors.Is(err, ErrStaleFilesKept) {
		t.Fatalf("Render = %v, want ErrStaleFilesKept", err)
	}
	if _, err := os.Stat(filepath.Join(workdir, filepath.FromSlash(goneLater))); err != nil {
		t.Errorf("a failed render must not remove %s: %v", goneLater, err)
	}
	if got := readTargetFile(t, workdir, ".claude-plugin/marketplace.json"); string(got) != string(manifest) {
		t.Error("a failed render must not rewrite the marketplace manifest")
	}
	if got := readTargetFile(t, workdir, ".podium/sync.lock"); string(got) != string(lockBefore) {
		t.Error("a failed render must not rewrite the lock")
	}

	if err := os.Remove(edited); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	res, err := Render(context.Background(), opts)
	if err != nil {
		t.Fatalf("Render after the manual removal: %v", err)
	}
	if !res.Changed || len(res.ChangedArtifacts) == 0 {
		t.Errorf("Render = (Changed %v, %v), want the catalog change reported", res.Changed, res.ChangedArtifacts)
	}
	if _, serr := os.Stat(filepath.Join(workdir, filepath.FromSlash(goneLater))); !os.IsNotExist(serr) {
		t.Errorf("%s must be removed by the render after the remedy; stat err = %v", goneLater, serr)
	}
}

// Spec: §7.5.3, §7.8 — a forged lock entry naming a file the render did not
// write fails the render rather than deleting the file, and the error names
// the lock-entry remedy.
func TestRender_ForgedEntryFailsWithLockRemedy(t *testing.T) {
	t.Parallel()
	workdir := t.TempDir()
	opts := renderOpts(t, renderFixtureRegistry(t), workdir, []string{"claude-code"})
	if _, err := Render(context.Background(), opts); err != nil {
		t.Fatalf("first Render: %v", err)
	}
	writeFile(t, filepath.Join(workdir, "README.md"), "# acme marketplace\n")
	lock, err := ReadLock(workdir)
	if err != nil || lock == nil {
		t.Fatalf("ReadLock = (%v, %v)", lock, err)
	}
	lock.Artifacts = append(lock.Artifacts, LockArtifact{MaterializedPath: "README.md", FileHash: materialize.FileHash([]byte("other"))})
	if err := WriteLock(workdir, lock); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}

	_, err = Render(context.Background(), opts)
	if !errors.Is(err, ErrStaleFilesKept) {
		t.Fatalf("Render = %v, want ErrStaleFilesKept", err)
	}
	for _, want := range []string{"README.md", "remove its entry from .podium/sync.lock"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must contain %q: %v", want, err)
		}
	}
	wantContentEq(t, filepath.Join(workdir, "README.md"), "# acme marketplace\n")
}

// Spec: §7.5.3, §7.8 — a stale file that passes the hash check but cannot be
// removed, here because its directory is read-only, fails the render and
// leaves its entry in the lock.
func TestRender_RemoveFailureFailsRender(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("directory permission bits do not deny removal on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root removes a file whatever its directory's permission bits")
	}
	workdir := t.TempDir()
	opts := renderOpts(t, renderFixtureRegistry(t), workdir, []string{"claude-code"})
	if _, err := Render(context.Background(), opts); err != nil {
		t.Fatalf("first Render: %v", err)
	}
	dir := filepath.Dir(filepath.Join(workdir, filepath.FromSlash(staleRenderPath)))
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	opts.Plugins = narrowFinancePack()
	_, err := Render(context.Background(), opts)
	if !errors.Is(err, ErrStaleFilesKept) || !strings.Contains(err.Error(), "cannot be removed") {
		t.Fatalf("Render = %v, want ErrStaleFilesKept naming the failed removal", err)
	}
	if !lockRecords(t, workdir, staleRenderPath) {
		t.Error("the lock must keep the entry of the file the render could not remove")
	}
}

// Spec: §7.5.3 — keptReason maps each hash-check refusal to its reason and
// leaves every other cleanup error, such as a recorded-path refusal, out of
// the kept list.
func TestKeptReason(t *testing.T) {
	t.Parallel()
	cases := map[error]string{
		materialize.ErrStaleHashMissing:  "records no file_hash",
		materialize.ErrStaleHashMismatch: "does not match",
		materialize.ErrStaleUnreadable:   "cannot be read",
		materialize.ErrStaleNotRemoved:   "cannot be removed",
		materialize.ErrUnconfinedPath:    "",
	}
	for err, want := range cases {
		got := keptReason(fmt.Errorf("wrapped: %w", err))
		if (want == "" && got != "") || !strings.Contains(got, want) {
			t.Errorf("keptReason(%v) = %q, want it to contain %q", err, got, want)
		}
	}
}

// wantContentEq fails the test unless the file at path holds want.
func wantContentEq(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Errorf("%s = (%q, %v), want %q", path, got, err, want)
	}
}
