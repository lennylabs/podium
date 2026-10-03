package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/lennylabs/podium/internal/revmark"
)

// Spec: §6.5 — `podium cache prune --days N` removes content
// buckets whose newest file mtime is older than N days. Younger
// buckets stay.
func TestCachePrune_RemovesOldBuckets(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "sha256-old")
	young := filepath.Join(dir, "sha256-young")
	for _, b := range []string{old, young} {
		if err := os.MkdirAll(b, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(filepath.Join(b, "frontmatter"), []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	// Backdate "old" by 60 days.
	past := time.Now().Add(-60 * 24 * time.Hour)
	_ = os.Chtimes(filepath.Join(old, "frontmatter"), past, past)
	_ = os.Chtimes(old, past, past)

	rc := cachePrune([]string{
		"--dir", dir,
		"--days", "30",
	})
	if rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("old bucket survived: %v", err)
	}
	if _, err := os.Stat(young); err != nil {
		t.Errorf("young bucket removed: %v", err)
	}
}

// Spec: §6.5 — --dry-run reports without removing.
func TestCachePrune_DryRun(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "sha256-old")
	_ = os.MkdirAll(old, 0o755)
	_ = os.WriteFile(filepath.Join(old, "x"), []byte("data"), 0o644)
	past := time.Now().Add(-60 * 24 * time.Hour)
	_ = os.Chtimes(filepath.Join(old, "x"), past, past)
	_ = os.Chtimes(old, past, past)

	rc := cachePrune([]string{"--dir", dir, "--days", "30", "--dry-run"})
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if _, err := os.Stat(old); err != nil {
		t.Errorf("dry-run removed bucket: %v", err)
	}
}

// Spec: §6.5 — `--days 0` is the boundary "older than now": a freshly-written
// bucket is prunable because its last access is already in the past. `--dry-run`
// lists it without removing it.
func TestCachePrune_DaysZeroListsFreshBucketDryRun(t *testing.T) {
	dir := t.TempDir()
	bucket := filepath.Join(dir, "sha256-fresh")
	if err := os.MkdirAll(bucket, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// Written just now, with no backdating: --days 30 would keep it, --days 0
	// must select it.
	if err := os.WriteFile(filepath.Join(bucket, "frontmatter"), []byte("fm"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if rc := cachePrune([]string{"--dir", dir, "--days", "0", "--dry-run"}); rc != 0 {
		t.Fatalf("rc = %d, want 0 (--days 0 is valid)", rc)
	}
	if _, err := os.Stat(bucket); err != nil {
		t.Errorf("dry-run removed the fresh bucket: %v", err)
	}
}

// Spec: §6.5 — a negative `--days` is rejected; it would push the cutoff into
// the future and evict buckets newer than now.
func TestCachePrune_NegativeDaysRejected(t *testing.T) {
	if rc := cachePrune([]string{"--dir", t.TempDir(), "--days", "-1"}); rc != 2 {
		t.Errorf("rc = %d, want 2 for negative --days", rc)
	}
}

// Spec: §6.5 — pruning a missing cache dir is a no-op success.
func TestCachePrune_MissingDirIsNoop(t *testing.T) {
	rc := cachePrune([]string{"--dir", filepath.Join(t.TempDir(), "absent"), "--days", "30"})
	if rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
}

// Spec: §6.5 — prune never deletes the `.resolutions` index nor any
// directory that is not a content bucket, even when their mtimes are stale.
func TestCachePrune_PreservesResolutionIndexAndNonBuckets(t *testing.T) {
	dir := t.TempDir()
	past := time.Now().Add(-60 * 24 * time.Hour)

	// A stale resolution index (dot-prefixed, no frontmatter).
	res := filepath.Join(dir, ".resolutions")
	if err := os.MkdirAll(res, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	idx := filepath.Join(res, "index.db")
	_ = os.WriteFile(idx, []byte("db"), 0o644)
	_ = os.Chtimes(idx, past, past)
	_ = os.Chtimes(res, past, past)

	// A stale non-bucket directory (no frontmatter file).
	stray := filepath.Join(dir, "not-a-bucket")
	_ = os.MkdirAll(stray, 0o755)
	_ = os.WriteFile(filepath.Join(stray, "junk"), []byte("x"), 0o644)
	_ = os.Chtimes(filepath.Join(stray, "junk"), past, past)
	_ = os.Chtimes(stray, past, past)

	// A genuine stale content bucket that should be removed.
	bucket := filepath.Join(dir, "deadbeef")
	_ = os.MkdirAll(bucket, 0o755)
	_ = os.WriteFile(filepath.Join(bucket, "frontmatter"), []byte("fm"), 0o644)
	_ = os.Chtimes(filepath.Join(bucket, "frontmatter"), past, past)
	_ = os.Chtimes(bucket, past, past)

	if rc := cachePrune([]string{"--dir", dir, "--days", "30"}); rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
	if _, err := os.Stat(res); err != nil {
		t.Errorf(".resolutions index was pruned: %v", err)
	}
	if _, err := os.Stat(stray); err != nil {
		t.Errorf("non-bucket directory was pruned: %v", err)
	}
	if _, err := os.Stat(bucket); !os.IsNotExist(err) {
		t.Errorf("stale content bucket survived: %v", err)
	}
}

// seedRevisionMarks writes an index DB under dir whose revisions bucket holds
// one mark per artifact ID under the given registry.
func seedRevisionMarks(t *testing.T, dir, registry string, ids ...string) {
	t.Helper()
	path := revmark.IndexPath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := bolt.Open(path, 0o644, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(revmark.Bucket())
		if err != nil {
			return err
		}
		for _, id := range ids {
			key := revmark.Key{Registry: registry, ArtifactID: id}.Encode()
			if err := b.Put(key, []byte("100")); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// Spec: §6.5 — `podium cache reset-revisions` deletes the marks for the named
// artifact IDs across every registry and reports the count, including zero
// when the bucket exists but no key matches.
func TestCacheResetRevisions_DeletesNamedMarks(t *testing.T) {
	dir := t.TempDir()
	seedRevisionMarks(t, dir, "https://r1", "team/a", "team/b")
	seedRevisionMarks(t, dir, "https://r2", "team/a")

	var rc int
	out := captureStdout(t, func() { rc = cacheResetRevisions([]string{"team/a", "--dir", dir}) })
	if rc != 0 || !strings.Contains(out, "cache: reset 2 revision mark(s)") {
		t.Fatalf("rc = %d, stdout = %q; want 0 and a reset count of 2", rc, out)
	}
	out = captureStdout(t, func() { rc = cacheResetRevisions([]string{"--dir", dir, "team/zzz"}) })
	if rc != 0 || !strings.Contains(out, "cache: reset 0 revision mark(s)") {
		t.Fatalf("rc = %d, stdout = %q; want 0 and a reset count of 0", rc, out)
	}
	out = captureStdout(t, func() { rc = cacheResetRevisions([]string{"--dir", dir}) })
	if rc != 0 || !strings.Contains(out, "cache: reset 1 revision mark(s)") {
		t.Fatalf("rc = %d, stdout = %q; want 0 and a reset count of 1", rc, out)
	}
}

// Spec: §6.5 — an absent index reports no marks and creates nothing, and the
// cache directory defaults to PODIUM_CACHE_DIR.
func TestCacheResetRevisions_NoMarks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	t.Setenv("PODIUM_CACHE_DIR", dir)
	var rc int
	out := captureStdout(t, func() { rc = cacheCmd([]string{"reset-revisions"}) })
	if rc != 0 || !strings.Contains(out, "cache: no revision marks under "+dir) {
		t.Fatalf("rc = %d, stdout = %q; want 0 and the no-marks line", rc, out)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("reset-revisions created %s: %v", dir, err)
	}
}

// Spec: §6.5 — a running podium-mcp holds the index lock, so the reset exits 1
// with the stop-and-retry message and deletes nothing.
func TestCacheResetRevisions_LockedIndex(t *testing.T) {
	dir := t.TempDir()
	seedRevisionMarks(t, dir, "https://r1", "team/a")
	db, err := bolt.Open(revmark.IndexPath(dir), 0o644, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	var rc int
	errOut := captureStderr(t, func() { rc = cacheResetRevisions([]string{"--dir", dir}) })
	want := "error: " + revmark.IndexPath(dir) + " is in use by a running podium-mcp; stop every MCP server that uses this cache directory and retry."
	if rc != 1 || !strings.Contains(errOut, want) {
		t.Fatalf("rc = %d, stderr = %q; want 1 and %q", rc, errOut, want)
	}
}

// Spec: §6.5 — an index path that is not a BoltDB file is reported as an
// error, and a flag parse error exits 2 while --help exits 0.
func TestCacheResetRevisions_ErrorsAndFlags(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(revmark.IndexPath(dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(revmark.IndexPath(dir), []byte("not a bolt file"), 0o644); err != nil {
		t.Fatal(err)
	}
	var rc int
	errOut := captureStderr(t, func() { rc = cacheResetRevisions([]string{"--dir", dir}) })
	if rc != 1 || !strings.HasPrefix(errOut, "error: ") {
		t.Fatalf("rc = %d, stderr = %q; want 1 and an error line", rc, errOut)
	}
	_ = captureStderr(t, func() { rc = cacheResetRevisions([]string{"--bogus"}) })
	if rc != 2 {
		t.Errorf("unknown flag rc = %d, want 2", rc)
	}
	_ = captureStderr(t, func() { rc = cacheResetRevisions([]string{"--help"}) })
	if rc != 0 {
		t.Errorf("--help rc = %d, want 0", rc)
	}
}

// Spec: §6.5 — resolveCacheDir keeps an explicit directory and otherwise
// falls back to ~/.podium/cache.
func TestResolveCacheDir(t *testing.T) {
	if got, err := resolveCacheDir("/x/y"); err != nil || got != "/x/y" {
		t.Errorf("resolveCacheDir(/x/y) = %q, %v", got, err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	got, err := resolveCacheDir("")
	if err != nil || got != filepath.Join(home, ".podium", "cache") {
		t.Errorf("resolveCacheDir(\"\") = %q, %v", got, err)
	}
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	if _, err := resolveCacheDir(""); err == nil && runtime.GOOS != "windows" && runtime.GOOS != "plan9" {
		t.Errorf("resolveCacheDir with no home succeeded")
	}
}
