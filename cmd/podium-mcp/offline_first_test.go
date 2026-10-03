package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/sign"
)

// spec: §6.5 — offline-first is "use cached resolution and content if present;
// only call the registry on miss." A present-but-stale (id, "latest")
// resolution (older than the 30s TTL) is served from cache without any registry
// call. TTL revalidation is scoped to always-revalidate ("Revalidated via HEAD
// on hit when PODIUM_CACHE_MODE=always-revalidate"), so staleness alone does not
// force a registry call in offline-first.
func TestLoadArtifact_OfflineFirst_StaleLatestServedFromCache(t *testing.T) {
	t.Parallel()
	const fm = "---\ntype: context\nversion: 1.0.0\n---\n"
	var calls int32

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError) // any registry call here is a bug
	}))
	defer ts.Close()

	dir := t.TempDir()
	cache, _ := newContentCache(dir)
	hash := primeCachedRecord(t, cache, "team/x", fm, "cached-body").ContentHash
	resolutions := newResolutionCache(dir)
	defer func() { _ = resolutions.Close() }()
	// Prime (team/x, "latest") -> 1.0.0 -> hash, fetched an hour ago (well past
	// the 30s TTL) so the entry is present but stale.
	putLatest(resolutions, "team/x", "1.0.0", hash, time.Now().Add(-time.Hour))

	srv := &mcpServer{
		cfg:         &config{cacheDir: dir, cacheMode: "offline-first", registry: ts.URL, harness: "none", verifyPolicy: sign.PolicyNever, resolutionTTL: 30 * time.Second},
		cache:       cache,
		resolutions: resolutions,
		adapters:    adapter.DefaultRegistry(),
		http:        &http.Client{},
	}
	out := srv.loadArtifact(map[string]any{"id": "team/x"}) // latest (no version)
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("loadArtifact returned %T (%v), want map", out, out)
	}
	if m["manifest_body"] != "cached-body" {
		t.Errorf("manifest_body = %v, want cached-body (present stale latest served from cache): %v", m["manifest_body"], m)
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Errorf("registry call count = %d, want 0 (offline-first serves a present resolution without calling the registry)", got)
	}
}

// spec: §6.5 — offline-first still calls the registry on a genuine miss (no
// cached resolution at all), distinguishing "present but stale" from "absent."
func TestLoadArtifact_OfflineFirst_TrueMissCallsRegistry(t *testing.T) {
	t.Parallel()
	respBody := loadArtifactJSON(t, map[string]any{
		"id": "team/x", "type": "context", "version": "1.0.0",
		"manifest_body": "fresh-body", "frontmatter": "---\ntype: context\n---\n",
	})
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(respBody))
	}))
	defer ts.Close()

	dir := t.TempDir()
	cache, _ := newContentCache(dir)
	resolutions := newResolutionCache(dir)
	defer func() { _ = resolutions.Close() }()

	srv := &mcpServer{
		cfg:         &config{cacheDir: dir, cacheMode: "offline-first", registry: ts.URL, harness: "none", verifyPolicy: sign.PolicyNever, resolutionTTL: 30 * time.Second},
		cache:       cache,
		resolutions: resolutions,
		adapters:    adapter.DefaultRegistry(),
		http:        &http.Client{},
	}
	out := srv.loadArtifact(map[string]any{"id": "team/x"})
	m, _ := out.(map[string]any)
	if m["manifest_body"] != "fresh-body" {
		t.Errorf("manifest_body = %v, want fresh-body (a true miss fetches from the registry)", m["manifest_body"])
	}
	if got := atomic.LoadInt32(&calls); got == 0 {
		t.Error("registry call count = 0, want >=1 (a true cache miss must call the registry in offline-first)")
	}
}

// Spec: §4.7.9, §6.5 — in offline-first, a present resolution whose cached
// delivery pair fails under the current key set is a cache miss: the bridge
// fetches once without a validator and serves the verified response.
func TestLoadArtifact_OfflineFirst_RetiredKeySignatureRefetches(t *testing.T) {
	t.Parallel()
	f, stub, _ := rotationSetup(t, "offline-first", http.StatusOK)

	wantServed(t, f.srv.loadArtifact(rotationArgs), "cached-body")
	if got := stub.uncondGets.Load(); got != 1 {
		t.Errorf("unconditional GETs = %d, want 1", got)
	}
	if got := stub.heads.Load() + stub.condGets.Load(); got != 0 {
		t.Errorf("HEAD and conditional requests = %d, want 0", got)
	}
}

// Spec: §4.7.9, §6.5, §7.4 — in offline-first with the registry unreachable,
// the failing cached record is a cache miss answered with the offline status
// and no artifact.
func TestLoadArtifact_OfflineFirst_RetiredKeySignatureUnreachableIsOffline(t *testing.T) {
	t.Parallel()
	f, _, ts := rotationSetup(t, "offline-first", http.StatusOK)
	ts.Close()

	out := f.srv.loadArtifact(rotationArgs)
	wantNotDelivered(t, out)
	m := out.(map[string]any)
	if _, isErr := m["error"]; isErr {
		t.Errorf("offline-first cache miss carried an error: %v", m)
	}
	if m["status"] != "offline" {
		t.Errorf("status = %v, want offline", m["status"])
	}
}

// Spec: §4.7.9, §6.5 — offline-only never calls the registry, so a cached
// delivery pair that fails under the current key set is refused rather than
// answered as a cache miss.
// Matrix: §6.10 (materialize.signature_invalid)
func TestLoadArtifact_OfflineOnly_RetiredKeySignatureRefused(t *testing.T) {
	t.Parallel()
	f, stub, _ := rotationSetup(t, "offline-only", http.StatusOK)

	out := f.srv.loadArtifact(rotationArgs)
	wantRefused(t, out, "materialize.signature_invalid")
	wantNotDelivered(t, out)
	if got := stub.requests(); got != 0 {
		t.Errorf("registry requests = %d, want 0", got)
	}
}
