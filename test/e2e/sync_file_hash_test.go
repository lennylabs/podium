package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	podsync "github.com/lennylabs/podium/pkg/sync"
)

// fileHashSync runs `podium sync` against reg into tgt with the none harness,
// passing include as the scope when it is not empty, and requires exit 0.
func fileHashSync(t *testing.T, reg, tgt, include, what string) cliResult {
	t.Helper()
	args := []string{"sync", "--registry", reg, "--target", tgt, "--harness", "none"}
	if include != "" {
		args = append(args, "--include", include)
	}
	res := runPodium(t, "", nil, args...)
	cliWantExit(t, res, 0, what)
	return res
}

// lockPaths returns the materialized paths the lock at tgt records, each
// mapped to its file_hash.
func lockPaths(t *testing.T, tgt string) map[string]string {
	t.Helper()
	lock, err := podsync.ReadLock(tgt)
	if err != nil || lock == nil {
		t.Fatalf("ReadLock = (%v, %v)", lock, err)
	}
	out := map[string]string{}
	for _, e := range lock.Artifacts {
		out[e.MaterializedPath] = e.FileHash
	}
	return out
}

// Spec: §7.5.3 — `podium sync` records a sha256 file_hash for each file it
// writes, and a narrowed scope removes an out-of-scope file that still holds
// the bytes sync wrote.
func TestSync_FileHashNarrowedScopeRemovesUnchangedFile(t *testing.T) {
	reg := cliReg(t)
	tgt := t.TempDir()
	fileHashSync(t, reg, tgt, "", "full sync")
	recorded := lockPaths(t, tgt)
	if h := recorded["personal/note/ARTIFACT.md"]; !strings.HasPrefix(h, "sha256:") || len(h) != len("sha256:")+64 {
		t.Fatalf("lock file_hash for personal/note = %q, want sha256:<64 hex>", h)
	}
	cliContains(t, readFile(t, filepath.Join(tgt, ".podium", "sync.lock")), "file_hash: sha256:", "lock records file_hash")

	fileHashSync(t, reg, tgt, "finance/**", "narrowed sync")
	files := readTreeFiltered(t, tgt)
	if _, ok := files["personal/note/ARTIFACT.md"]; ok {
		t.Fatalf("narrowed sync must remove personal/note: %v", keysOf(files))
	}
	if _, err := os.Stat(filepath.Join(tgt, "personal")); !os.IsNotExist(err) {
		t.Errorf("the emptied personal/ directory must be pruned; stat err = %v", err)
	}
	if _, ok := files["finance/invoice/ARTIFACT.md"]; !ok {
		t.Fatalf("narrowed sync must keep finance/invoice: %v", keysOf(files))
	}
}

// Spec: §7.5.3 — an out-of-scope file edited after sync wrote it stays in
// place, standard error names it, and the next lock no longer records it.
func TestSync_FileHashEditedFileKeptAndReported(t *testing.T) {
	reg := cliReg(t)
	tgt := t.TempDir()
	fileHashSync(t, reg, tgt, "", "full sync")
	edited := filepath.Join(tgt, "personal", "note", "ARTIFACT.md")
	if err := os.WriteFile(edited, []byte("alice's local notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := fileHashSync(t, reg, tgt, "finance/**", "narrowed sync")
	if got := readFile(t, edited); got != "alice's local notes\n" {
		t.Fatalf("edited file = %q, want it kept unchanged", got)
	}
	cliContains(t, res.Stderr, `"personal/note/ARTIFACT.md"`, "stderr names the kept file")
	cliContains(t, res.Stderr, "file_hash", "stderr states the hash mismatch")
	if _, ok := lockPaths(t, tgt)["personal/note/ARTIFACT.md"]; ok {
		t.Fatal("the lock must drop the kept file's entry")
	}

	res = fileHashSync(t, reg, tgt, "finance/**", "repeat sync")
	cliNotContains(t, res.Stderr, "personal/note/ARTIFACT.md", "a later sync does not name the file again")
	if got := readFile(t, edited); got != "alice's local notes\n" {
		t.Fatalf("edited file after the repeat sync = %q", got)
	}
}

// Spec: §7.5.3 — a forged lock entry naming a file the user wrote, with no
// file_hash or with a hash of other bytes, leaves the file in place.
func TestSync_FileHashForgedLockEntryKeepsUserFile(t *testing.T) {
	reg := cliReg(t)
	tgt := t.TempDir()
	fileHashSync(t, reg, tgt, "finance/**", "baseline sync")
	userFiles := map[string]string{
		"src/main.go": "package main\n",
		"notes.md":    "# acme\n",
	}
	for rel, content := range userFiles {
		path := filepath.Join(tgt, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lock, err := podsync.ReadLock(tgt)
	if err != nil || lock == nil {
		t.Fatalf("ReadLock = (%v, %v)", lock, err)
	}
	lock.Artifacts = append(lock.Artifacts,
		podsync.LockArtifact{ID: "evil/no-hash", MaterializedPath: "src/main.go"},
		podsync.LockArtifact{ID: "evil/forged", MaterializedPath: "notes.md",
			FileHash: "sha256:0000000000000000000000000000000000000000000000000000000000000000"},
	)
	if err := podsync.WriteLock(tgt, lock); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}

	res := fileHashSync(t, reg, tgt, "finance/**", "sync over the forged lock")
	for rel, content := range userFiles {
		if got := readFile(t, filepath.Join(tgt, filepath.FromSlash(rel))); got != content {
			t.Errorf("%s = %q, want %q unchanged", rel, got, content)
		}
		cliContains(t, res.Stderr, `"`+rel+`"`, "stderr names the forged entry")
		if _, ok := lockPaths(t, tgt)[rel]; ok {
			t.Errorf("the lock must drop the forged entry for %s", rel)
		}
	}
}

// pushRemoteChange clones remote, applies change to the clone, and commits and
// pushes the result to main, as a contributor to the marketplace repository
// would.
func pushRemoteChange(t *testing.T, remote, msg string, change func(dir string)) {
	t.Helper()
	dir := t.TempDir()
	if r := gitCmd(t, dir, "clone", "--branch", "main", remote, dir); r.Exit != 0 {
		t.Fatalf("clone: exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	change(dir)
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-m", msg}, {"push", "origin", "main"}} {
		if r := gitCmd(t, dir, args...); r.Exit != 0 {
			t.Fatalf("git %v: exit=%d stderr=%s", args, r.Exit, r.Stderr)
		}
	}
}

// Spec: §7.5.3, §7.8 — a `kind: marketplace` render whose cleanup would leave
// a stale recorded file in place fails the target before the publish phase,
// names the file and the reason, and pushes nothing. The case is a lock an
// older release committed, which records no file_hash, followed by a narrowed
// plugin. After the publisher removes the file from the repository by hand,
// the next run publishes.
func TestPublishing_StaleFileWithoutHashFailsRender(t *testing.T) {
	t.Parallel()
	requireGit(t)
	const stale = "claude/finance-pack/skills/run-variance/SKILL.md"
	reg := writePublishRegistry(t)
	remote := bareRemote(t)
	ws := t.TempDir()
	cfg := writeSyncConfigMarketplace(t, ws, reg, remote, "claude-code")
	if r := runPodium(t, "", gitEnv(), "sync", "--config", cfg); r.Exit != 0 {
		t.Fatalf("first sync exit=%d stderr=%s", r.Exit, r.Stderr)
	}

	pushRemoteChange(t, remote, "lock from an older release", func(dir string) {
		lockPath := filepath.Join(dir, ".podium", "sync.lock")
		lines := strings.Split(readFile(t, lockPath), "\n")
		kept := lines[:0]
		for _, l := range lines {
			if !strings.Contains(l, "file_hash:") {
				kept = append(kept, l)
			}
		}
		if err := os.WriteFile(lockPath, []byte(strings.Join(kept, "\n")), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	narrowed := strings.Replace(readFile(t, cfg), `include: ["finance/**"]`, `include: ["finance/ap/**"]`, 1)
	if err := os.WriteFile(cfg, []byte(narrowed), 0o644); err != nil {
		t.Fatal(err)
	}
	before := remoteCommitCount(t, remote)

	res := runPodium(t, "", gitEnv(), "sync", "--config", cfg)
	cliWantNonZero(t, res, "sync over a lock without file hashes")
	cliContains(t, res.Stderr, stale, "the error names the kept file")
	cliContains(t, res.Stderr, "records no file_hash", "the error states the reason")
	if n := remoteCommitCount(t, remote); n != before {
		t.Fatalf("a failed render must push nothing: commit count = %d, want %d", n, before)
	}

	pushRemoteChange(t, remote, "remove the stale skill by hand", func(dir string) {
		if err := os.Remove(filepath.Join(dir, filepath.FromSlash(stale))); err != nil {
			t.Fatal(err)
		}
	})
	res = runPodium(t, "", gitEnv(), "sync", "--config", cfg)
	cliWantExit(t, res, 0, "sync after the manual removal")
	if _, ok := remoteTree(t, remote)[stale]; ok {
		t.Fatalf("the removed skill must stay out of the published tree")
	}
}
