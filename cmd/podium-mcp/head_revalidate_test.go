package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/version"
)

// spec: §6.5 — in always-revalidate mode a cached resolution is
// revalidated via HEAD. When the registry confirms the content hash is
// unchanged, the bridge serves the cached content and issues no full GET.
func TestLoadArtifact_AlwaysRevalidate_HeadMatchServesCache(t *testing.T) {
	t.Parallel()
	const fm = "---\ntype: context\nversion: 1.0.0\n---\n"
	hash := "sha256:" + version.CanonicalContentHash([]byte(fm), nil, nil)
	var gets, heads int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			atomic.AddInt32(&heads, 1)
			w.Header().Set("X-Podium-Content-Hash", hash)
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			atomic.AddInt32(&gets, 1)
			w.WriteHeader(http.StatusInternalServerError) // a full GET here is a bug
		}
	}))
	defer ts.Close()

	dir := t.TempDir()
	cache, _ := newContentCache(dir)
	primeCachedRecord(t, cache, "team/x", fm, "cached-body")
	resolutions := newResolutionCache(dir)
	defer func() { _ = resolutions.Close() }()
	resolutions.PutVersion("team/x", "1.0.0", hash, time.Now())

	srv := &mcpServer{
		cfg:         &config{cacheDir: dir, cacheMode: "always-revalidate", registry: ts.URL, harness: "none", verifyPolicy: sign.PolicyNever},
		cache:       cache,
		resolutions: resolutions,
		adapters:    adapter.DefaultRegistry(),
		http:        &http.Client{},
	}
	out := srv.loadArtifact(map[string]any{"id": "team/x", "version": "1.0.0"})
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("loadArtifact returned %T (%v), want map", out, out)
	}
	if m["manifest_body"] != "cached-body" {
		t.Errorf("manifest_body = %v, want cached-body (served from cache): %v", m["manifest_body"], m)
	}
	if got := atomic.LoadInt32(&heads); got != 1 {
		t.Errorf("HEAD count = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&gets); got != 0 {
		t.Errorf("GET count = %d, want 0 (HEAD match must not full-fetch)", got)
	}
}

// spec: §6.5 — when HEAD reports a different content hash, the cached
// content is stale and the bridge performs a full GET.
func TestLoadArtifact_AlwaysRevalidate_HeadMismatchFullFetches(t *testing.T) {
	t.Parallel()
	const cachedFM = "---\ntype: context\nversion: 1.0.0\n---\n"
	cachedHash := "sha256:" + version.CanonicalContentHash([]byte(cachedFM), nil, nil)
	respBody := loadArtifactJSON(t, map[string]any{
		"id": "team/x", "type": "context", "version": "2.0.0",
		"manifest_body": "fresh-body", "frontmatter": "---\ntype: context\nversion: 2.0.0\n---\n",
	})
	var gets, heads int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			atomic.AddInt32(&heads, 1)
			// A content hash different from the cached resolution → stale.
			w.Header().Set("X-Podium-Content-Hash", "sha256:changed")
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			atomic.AddInt32(&gets, 1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(respBody))
		}
	}))
	defer ts.Close()

	dir := t.TempDir()
	cache, _ := newContentCache(dir)
	_ = cache.put(cachedHash, cachedFM, "cached-body", nil)
	resolutions := newResolutionCache(dir)
	defer func() { _ = resolutions.Close() }()
	resolutions.PutVersion("team/x", "2.0.0", cachedHash, time.Now())

	srv := &mcpServer{
		cfg:         &config{cacheDir: dir, cacheMode: "always-revalidate", registry: ts.URL, harness: "none", verifyPolicy: sign.PolicyNever},
		cache:       cache,
		resolutions: resolutions,
		adapters:    adapter.DefaultRegistry(),
		http:        &http.Client{},
	}
	out := srv.loadArtifact(map[string]any{"id": "team/x", "version": "2.0.0"})
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("loadArtifact returned %T (%v), want map", out, out)
	}
	if m["manifest_body"] != "fresh-body" {
		t.Errorf("manifest_body = %v, want fresh-body (HEAD mismatch must full-fetch): %v", m["manifest_body"], m)
	}
	if got := atomic.LoadInt32(&heads); got != 1 {
		t.Errorf("HEAD count = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&gets); got != 1 {
		t.Errorf("GET count = %d, want 1", got)
	}
}

// Spec: §4.7.9, §6.5 — in always-revalidate, a HEAD that matches the cached
// content hash does not replay a cached delivery pair whose signature fails
// under the current key set. The record is a cache miss: one unconditional GET
// replaces it, and a second load serves the replaced record with no GET.
func TestLoadArtifact_HeadMatchWithRetiredKeySignatureRefetches(t *testing.T) {
	t.Parallel()
	f, stub, _ := rotationSetup(t, "always-revalidate", http.StatusOK)

	wantServed(t, f.srv.loadArtifact(rotationArgs), "cached-body")
	if got := stub.uncondGets.Load(); got != 1 {
		t.Errorf("unconditional GETs after the first load = %d, want 1", got)
	}
	wantServed(t, f.srv.loadArtifact(rotationArgs), "cached-body")
	if got := stub.uncondGets.Load() + stub.condGets.Load(); got != 1 {
		t.Errorf("GETs after the second load = %d, want 1 (the replaced record verifies)", got)
	}
	if got := stub.heads.Load(); got != 2 {
		t.Errorf("HEAD count = %d, want 2", got)
	}
}

// Spec: §4.7.9, §6.5 — the refetch a failing cached signature triggers is
// verified like any fresh response: one signed under a key outside the set is
// refused, and the bridge makes no second refetch.
// Matrix: §6.10 (materialize.signature_invalid)
func TestLoadArtifact_HeadMatchRefetchUnderUntrustedKeyRefused(t *testing.T) {
	t.Parallel()
	f, stub, _ := rotationSetup(t, "always-revalidate", http.StatusOK)
	stub.fresh = f.underC

	out := f.srv.loadArtifact(rotationArgs)
	wantRefused(t, out, "materialize.signature_invalid")
	wantNotDelivered(t, out)
	if got := stub.uncondGets.Load(); got != 1 {
		t.Errorf("unconditional GETs = %d, want 1", got)
	}
	if got := stub.condGets.Load(); got != 0 {
		t.Errorf("conditional GETs = %d, want 0", got)
	}
}

// Spec: §4.7.9, §6.5, §7.4 — when the registry answers the HEAD and then
// cannot be reached for the refetch, the load returns the always-revalidate
// cache-miss outcome and never delivers the failing cached record.
// Matrix: §6.10 (network.registry_unreachable)
func TestLoadArtifact_HeadMatchRefetchUnreachableReturnsRegistryUnreachable(t *testing.T) {
	t.Parallel()
	f, stub, _ := rotationSetup(t, "always-revalidate", http.StatusOK)
	stub.abortGET = true

	out := f.srv.loadArtifact(rotationArgs)
	wantRefused(t, out, "network.registry_unreachable")
	wantNotDelivered(t, out)
	if got := stub.heads.Load(); got != 1 {
		t.Errorf("HEAD count = %d, want 1", got)
	}
}

// Spec: §6.5, §6.9 — a refetch the registry answers and refuses passes the
// registry's §6.10 envelope through rather than relabeling it unreachable,
// and the failing cached record is not delivered.
func TestLoadArtifact_HeadMatchRefetchRejectedPassesThrough(t *testing.T) {
	t.Parallel()
	f, stub, _ := rotationSetup(t, "always-revalidate", http.StatusOK)
	stub.rejectGET = true

	out := f.srv.loadArtifact(rotationArgs)
	wantRefused(t, out, "auth.untrusted_runtime")
	wantNotDelivered(t, out)
}

// Spec: §6.5, §6.6 step 2 — only a signature failure makes a cached record a
// cache miss. A cached record whose bytes no longer reproduce its delivery
// hash is refused with materialize.content_hash_mismatch and not refetched.
func TestLoadArtifact_HeadMatchEditedCacheRecordRefusedWithoutRefetch(t *testing.T) {
	t.Parallel()
	f, stub, _ := rotationSetup(t, "always-revalidate", http.StatusOK)
	edited := filepath.Join(deliveryDir(f.srv.cfg.cacheDir, f.cached), "frontmatter")
	if err := os.WriteFile(edited, []byte(f.cached.Frontmatter+"injected: true\n"), 0o644); err != nil {
		t.Fatalf("edit: %v", err)
	}

	out := f.srv.loadArtifact(rotationArgs)
	wantRefused(t, out, "materialize.content_hash_mismatch")
	if got := stub.uncondGets.Load() + stub.condGets.Load(); got != 0 {
		t.Errorf("GETs = %d, want 0", got)
	}
}
