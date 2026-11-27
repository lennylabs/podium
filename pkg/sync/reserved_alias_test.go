package sync

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
)

// aliasedLockEntries are prior-lock entries whose first segment a filesystem
// resolves to .git or .podium: an HFS+ ignorable code point, an NTFS short
// name, an NTFS trailing period, and an NTFS alternate data stream. Each names
// a file that exists under the aliased name on the test's filesystem, so a
// cleanup that took the entry as stale would remove it.
var aliasedLockEntries = []LockArtifact{
	{ID: "evil/hfs", MaterializedPath: "\u200c.git/HEAD"},
	{ID: "evil/hfs-mid", MaterializedPath: ".g\u200cit/config"},
	{ID: "evil/short", MaterializedPath: "GIT~1/config"},
	{ID: "evil/trailing", MaterializedPath: ".git./config"},
	{ID: "evil/stream", MaterializedPath: ".git::$INDEX_ALLOCATION/config"},
	{ID: "evil/state", MaterializedPath: "PODIUM~1/sync.yaml"},
	{ID: "evil/state-hfs", MaterializedPath: "\ufeff.podium/sync.yaml"},
}

// Spec: §7.5.3 — a prior-lock entry whose path names .git or the sync state
// under the aliasing rule is skipped with a warning that names it, the
// cleanup removes nothing at that path, the repository's .git is untouched,
// and the lock the run writes omits the entry.
//
// Not parallel: the test swaps the process-wide os.Stderr.
func TestRun_StaleCleanupSkipsAliasedReservedNames(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	opts := Options{RegistryPath: agentRegistry(t, "company-glossary"), Target: target, AdapterID: "none"}
	syncChanged(t, opts)

	survivors := map[string]string{filepath.Join(target, ".git", "HEAD"): "ref: refs/heads/main\n"}
	for _, e := range aliasedLockEntries {
		survivors[filepath.Join(target, filepath.FromSlash(e.MaterializedPath))] = e.ID + "\n"
	}
	for path, content := range survivors {
		writeFile(t, path, content)
	}
	lock, err := ReadLock(target)
	if err != nil || lock == nil {
		t.Fatalf("ReadLock = (%v, %v)", lock, err)
	}
	lock.Artifacts = append(lock.Artifacts, aliasedLockEntries...)
	if err := WriteLock(target, lock); err != nil {
		t.Fatalf("WriteLock: %v", err)
	}

	stderr := captureStderr(t, func() { syncChanged(t, opts) })
	for path, want := range survivors {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Errorf("%s = (%q, %v), want %q unchanged", path, got, err, want)
		}
	}
	for _, e := range aliasedLockEntries {
		if !strings.Contains(stderr, "skipping lock entry") || !strings.Contains(stderr, strconv.Quote(e.MaterializedPath)) {
			t.Errorf("stderr must name the skipped entry %q; stderr = %q", e.MaterializedPath, stderr)
		}
	}
	after, err := ReadLock(target)
	if err != nil || after == nil {
		t.Fatalf("ReadLock after the run = (%v, %v)", after, err)
	}
	for _, la := range after.Artifacts {
		if strings.HasPrefix(la.ID, "evil/") {
			t.Errorf("the lock the run wrote still records %s at %q", la.ID, la.MaterializedPath)
		}
	}
}

// Spec: §4.2, §7.5.3 — a filesystem-source sync whose registry holds a
// package under a directory that aliases .git fails before it writes, so
// neither the aliased directory nor the target's .git receives a file.
func TestRun_RefusesArtifactIDAliasingDotGit(t *testing.T) {
	t.Parallel()
	for _, dir := range []string{"GIT~1/hooks", "\u200c.git/hooks"} {
		registry := t.TempDir()
		testharness.WriteTree(t, registry,
			testharness.WriteTreeOption{Path: dir + "/ARTIFACT.md", Content: agentArtifactSrc},
			testharness.WriteTreeOption{Path: "company-glossary/ARTIFACT.md", Content: agentArtifactSrc},
		)
		target := t.TempDir()
		head := filepath.Join(target, ".git", "HEAD")
		writeFile(t, head, "ref: refs/heads/main\n")

		_, err := Run(Options{RegistryPath: registry, Target: target, AdapterID: "none"})
		if err == nil || !strings.Contains(err.Error(), "names .git or .podium") {
			t.Errorf("Run with package %q = %v, want the canonical-ID refusal", dir, err)
		}
		first, _, _ := strings.Cut(dir, "/")
		if _, err := os.Lstat(filepath.Join(target, first)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Run with package %q created %q: lstat err = %v", dir, first, err)
		}
		entries, err := os.ReadDir(filepath.Join(target, ".git"))
		if err != nil || len(entries) != 1 {
			t.Errorf("target .git after the refused run = (%v, %v), want HEAD alone", entries, err)
		}
	}
}

// Spec: §4.2, §6.4, §7.5.3 — the canonical-ID rule applies to a workspace
// overlay package, so a sync whose overlay holds a package under a directory
// that aliases .podium fails before it writes into the sync state.
func TestRun_RefusesOverlayIDAliasingDotPodium(t *testing.T) {
	t.Parallel()
	overlayDir := t.TempDir()
	testharness.WriteTree(t, overlayDir,
		testharness.WriteTreeOption{Path: "PODIUM~1/overlay/ARTIFACT.md", Content: agentArtifactSrc},
	)
	target := t.TempDir()
	_, err := Run(Options{
		RegistryPath: agentRegistry(t, "company-glossary"),
		OverlayPath:  overlayDir,
		Target:       target,
		AdapterID:    "none",
	})
	if err == nil || !strings.Contains(err.Error(), "overlay") || !strings.Contains(err.Error(), "names .git or .podium") {
		t.Errorf("Run with an aliased overlay package = %v, want the overlay canonical-ID refusal", err)
	}
	if _, err := os.Lstat(filepath.Join(target, "PODIUM~1")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Run created PODIUM~1: lstat err = %v", err)
	}
}
