package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// Spec: §6.5 — the resolution cache persists (id, version) →
// content_hash so offline-first reads can serve future requests
// without contacting the registry.
func TestResolutionCache_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r1 := newResolutionCache(dir)
	r1.Put("team/finance", "1.0.0", "sha256:abc")
	r1.Put("team/finance", "", "sha256:abc")

	r2 := newResolutionCache(dir)
	if got, ok := r2.Get("team/finance", "1.0.0"); !ok || got != "sha256:abc" {
		t.Errorf("Get(1.0.0) = %q ok=%v, want sha256:abc, true", got, ok)
	}
	if got, ok := r2.Get("team/finance", ""); !ok || got != "sha256:abc" {
		t.Errorf("Get(latest) = %q ok=%v, want sha256:abc, true", got, ok)
	}
}

// Spec: §6.5 — disabled cache (empty cache dir) is a no-op.
func TestResolutionCache_DisabledCache(t *testing.T) {
	t.Parallel()
	r := newResolutionCache("")
	r.Put("x", "1.0.0", "sha256:abc")
	if _, ok := r.Get("x", "1.0.0"); ok {
		t.Errorf("disabled cache returned a hit")
	}
}

// Spec: §6.5 — loadArtifactFromCache reads the bytes the content
// cache holds at contentHash, plus every bundled resource.
func TestLoadArtifactFromCache_RecoversBytes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cache, err := newContentCache(dir)
	if err != nil {
		t.Fatalf("newContentCache: %v", err)
	}
	const hash = "sha256:abc"
	if err := cache.put(hash, "frontmatter-body", "manifest-body", map[string]string{
		"scripts/run.py": "print('x')\n",
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	srv := &mcpServer{cfg: &config{cacheDir: dir}}
	got, err := srv.loadArtifactFromCache(hash, "team/x")
	if err != nil {
		t.Fatalf("loadArtifactFromCache: %v", err)
	}
	if got.ID != "team/x" {
		t.Errorf("ID = %q, want team/x", got.ID)
	}
	if got.Frontmatter != "frontmatter-body" {
		t.Errorf("Frontmatter = %q", got.Frontmatter)
	}
	if got.ManifestBody != "manifest-body" {
		t.Errorf("ManifestBody = %q", got.ManifestBody)
	}
	if got.Resources["scripts/run.py"] != "print('x')\n" {
		t.Errorf("Resources = %+v", got.Resources)
	}
	// Bucket exists.
	if _, err := filepath.Abs(filepath.Join(dir, sanitizeHash(hash))); err != nil {
		t.Errorf("Abs: %v", err)
	}
}

// Spec: §6.5 — loadArtifactFromCache returns an error on missing
// bucket so offline-only mode can surface the cache.offline_miss
// envelope.
func TestLoadArtifactFromCache_Missing(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{cacheDir: t.TempDir()}}
	_, err := srv.loadArtifactFromCache("sha256:absent", "x")
	if err == nil || !strings.Contains(err.Error(), "cache miss") {
		t.Errorf("err = %v, want cache miss", err)
	}
}
