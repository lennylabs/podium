package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// spec: §6.5 — "Maps (id, "latest") to semver. TTL 30s by default." A latest
// resolution older than the TTL is treated as a miss (F-6.5.1); a fresh one is
// served. allowStale (offline-only / degraded fallback) serves a stale entry.
func TestResolutionCache_LatestTTL(t *testing.T) {
	t.Parallel()
	r := newResolutionCache(t.TempDir())
	defer r.Close()
	base := time.Unix(1_700_000_000, 0)
	r.PutLatest("team/x", "1.2.3", "sha256:hash", base)

	// Within the TTL window: a hit.
	if got, ok := r.Resolve("team/x", "", base.Add(10*time.Second), 30*time.Second, false); !ok || got != "sha256:hash" {
		t.Fatalf("fresh Resolve(latest) = %q ok=%v, want sha256:hash, true", got, ok)
	}
	// Past the TTL window: a miss, so the bridge falls through to the registry.
	if _, ok := r.Resolve("team/x", "", base.Add(45*time.Second), 30*time.Second, false); ok {
		t.Errorf("stale Resolve(latest) returned a hit; want miss")
	}
	// allowStale serves the stale entry (offline-only can never refresh).
	if got, ok := r.Resolve("team/x", "", base.Add(45*time.Second), 30*time.Second, true); !ok || got != "sha256:hash" {
		t.Errorf("allowStale Resolve(latest) = %q ok=%v, want sha256:hash, true", got, ok)
	}
	// ttl=0 disables expiry.
	if _, ok := r.Resolve("team/x", "", base.Add(99*time.Hour), 0, false); !ok {
		t.Errorf("ttl=0 should never expire")
	}
}

// spec: §6.5 — a pinned (id, version) is immutable and never expires, so the
// TTL applies only to `latest`.
func TestResolutionCache_PinnedNeverExpires(t *testing.T) {
	t.Parallel()
	r := newResolutionCache(t.TempDir())
	defer r.Close()
	base := time.Unix(1_700_000_000, 0)
	r.PutVersion("team/x", "1.2.3", "sha256:pinned", base)
	if got, ok := r.Resolve("team/x", "1.2.3", base.Add(72*time.Hour), 30*time.Second, false); !ok || got != "sha256:pinned" {
		t.Errorf("pinned Resolve = %q ok=%v, want sha256:pinned, true", got, ok)
	}
}

// spec: §6.5 — the (id, "latest") key maps to the resolved semver (F-6.5.4);
// the content hash is recovered by chaining through the (id, semver) entry.
func TestResolutionCache_LatestMapsToSemver(t *testing.T) {
	t.Parallel()
	r := newResolutionCache(t.TempDir())
	defer r.Close()
	now := time.Now()
	r.PutLatest("team/x", "2.0.0", "sha256:abc", now)

	// The latest key stores the semver, not the hash, directly.
	e, ok := r.getEntry(resolutionKey("team/x", ""))
	if !ok {
		t.Fatal("missing latest entry")
	}
	if e.ResolvedVersion != "2.0.0" {
		t.Errorf("latest ResolvedVersion = %q, want 2.0.0", e.ResolvedVersion)
	}
	if e.ContentHash != "" {
		t.Errorf("latest entry should not carry a content hash directly, got %q", e.ContentHash)
	}
	// Resolution still recovers the content hash via the version chain.
	if got, ok := r.Resolve("team/x", "", now, 30*time.Second, false); !ok || got != "sha256:abc" {
		t.Errorf("Resolve(latest) = %q ok=%v, want sha256:abc, true", got, ok)
	}
	// The pinned version entry is also addressable directly.
	if got, ok := r.Resolve("team/x", "2.0.0", now, 30*time.Second, false); !ok || got != "sha256:abc" {
		t.Errorf("Resolve(2.0.0) = %q ok=%v, want sha256:abc, true", got, ok)
	}
}

// spec: §6.5 — RefreshLatest restarts the TTL window after a HEAD revalidation
// without losing the resolved-version chain.
func TestResolutionCache_RefreshLatest(t *testing.T) {
	t.Parallel()
	r := newResolutionCache(t.TempDir())
	defer r.Close()
	base := time.Unix(1_700_000_000, 0)
	r.PutLatest("team/x", "1.0.0", "sha256:abc", base)

	// Stale just before refresh.
	if _, ok := r.Resolve("team/x", "", base.Add(45*time.Second), 30*time.Second, false); ok {
		t.Fatal("entry should be stale before refresh")
	}
	r.RefreshLatest("team/x", base.Add(45*time.Second))
	// Fresh again, and the semver chain still resolves.
	if got, ok := r.Resolve("team/x", "", base.Add(50*time.Second), 30*time.Second, false); !ok || got != "sha256:abc" {
		t.Errorf("after refresh Resolve(latest) = %q ok=%v, want sha256:abc, true", got, ok)
	}
}

// spec: §6.5 — "Index DB: BoltDB or SQLite" (F-6.5.5). The index persists to a
// BoltDB file that survives a reopen.
func TestResolutionCache_PersistsToBoltDB(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Now()
	r1 := newResolutionCache(dir)
	r1.PutVersion("team/x", "1.0.0", "sha256:abc", now)
	if r1.db == nil {
		t.Fatal("resolution cache has no BoltDB handle")
	}
	if err := r1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r2 := newResolutionCache(dir)
	defer r2.Close()
	if got, ok := r2.Resolve("team/x", "1.0.0", now, 30*time.Second, false); !ok || got != "sha256:abc" {
		t.Errorf("after reopen Resolve = %q ok=%v, want sha256:abc, true", got, ok)
	}
	if n := r2.Len(); n != 1 {
		t.Errorf("Len = %d, want 1", n)
	}
}

// spec: §6.5 (F-6.5.6) — a cache hit touches the bucket so a read counts as
// last access; otherwise `podium cache prune` (mtime-based) would evict a
// frequently-read but never-rewritten bucket.
func TestLoadArtifactFromCache_TouchesBucketOnRead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cache, err := newContentCache(dir)
	if err != nil {
		t.Fatalf("newContentCache: %v", err)
	}
	const hash = "sha256:abc"
	if err := cache.put(hash, "fm", "body", nil); err != nil {
		t.Fatalf("put: %v", err)
	}
	bucket := filepath.Join(dir, sanitizeHash(hash))
	past := time.Now().Add(-60 * 24 * time.Hour)
	_ = os.Chtimes(filepath.Join(bucket, "frontmatter"), past, past)
	_ = os.Chtimes(filepath.Join(bucket, "body"), past, past)

	srv := &mcpServer{cfg: &config{cacheDir: dir}}
	if _, err := srv.loadArtifactFromCache(hash, "team/x"); err != nil {
		t.Fatalf("loadArtifactFromCache: %v", err)
	}
	fi, err := os.Stat(filepath.Join(bucket, "frontmatter"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.ModTime().Before(time.Now().Add(-time.Minute)) {
		t.Errorf("read did not refresh bucket mtime; got %s", fi.ModTime())
	}
}
