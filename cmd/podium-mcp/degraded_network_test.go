package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Spec: §7.4 — always-revalidate + unreachable registry +
// cache hit: serve from cache with status=offline +
// served_from_cache=true.
func TestLoadArtifact_AlwaysRevalidateFallsBackToCacheOnNetworkError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cache, _ := newContentCache(dir)
	const hash = "sha256:abc"
	if err := cache.put(hash, "fm", "body", nil); err != nil {
		t.Fatalf("put: %v", err)
	}
	resolutions := newResolutionCache(dir)
	resolutions.PutLatest("team/x", "", hash, time.Now())

	srv := &mcpServer{
		cfg: &config{
			cacheDir:  dir,
			cacheMode: "always-revalidate",
			registry:  "http://127.0.0.1:1", // unbound port → connect refused
			harness:   "none",
		},
		cache:       cache,
		resolutions: resolutions,
		http:        &http.Client{},
	}
	out := srv.loadArtifact(map[string]any{"id": "team/x"})
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("loadArtifact returned %T, want map", out)
	}
	if m["status"] != "offline" {
		t.Errorf("status = %v, want offline", m["status"])
	}
	if served, _ := m["served_from_cache"].(bool); !served {
		t.Errorf("served_from_cache = %v, want true", m["served_from_cache"])
	}
}

// Spec: §7.4 — always-revalidate + unreachable + cache miss:
// returns network.registry_unreachable.
func TestLoadArtifact_AlwaysRevalidateNetworkUnreachableErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cache, _ := newContentCache(dir)
	srv := &mcpServer{
		cfg: &config{
			cacheDir:  dir,
			cacheMode: "always-revalidate",
			registry:  "http://127.0.0.1:1",
			harness:   "none",
		},
		cache:       cache,
		resolutions: newResolutionCache(dir),
		http:        &http.Client{},
	}
	out := srv.loadArtifact(map[string]any{"id": "team/never-cached"})
	body := errorMessageText(out)
	if !strings.Contains(body, "network.registry_unreachable") {
		t.Errorf("error body = %q, want network.registry_unreachable", body)
	}
}

// Spec: §6.9 — a reachable registry that answers and refuses (here a 403
// auth.untrusted_runtime) must surface the registry's structured §6.10
// envelope unchanged. In always-revalidate mode with no cache entry the
// rejection must NOT be relabeled as the retryable network.registry_unreachable
// (F-6.9.4): that conflates a registry that refused with one that could not
// be reached.
func TestLoadArtifact_ReachableRejectionPassesThroughNotRelabeled(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"auth.untrusted_runtime","message":"runtime not registered","retryable":false,"suggested_action":"register the runtime signing key"}`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	cache, _ := newContentCache(dir)
	s := &mcpServer{
		cfg: &config{
			cacheDir:  dir,
			cacheMode: "always-revalidate",
			registry:  srv.URL,
			harness:   "none",
		},
		cache:       cache,
		resolutions: newResolutionCache(dir),
		http:        &http.Client{},
	}
	out := s.loadArtifact(map[string]any{"id": "team/x"})
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("loadArtifact returned %T, want map", out)
	}
	if m["code"] != "auth.untrusted_runtime" {
		t.Errorf("code = %v, want auth.untrusted_runtime (got %v)", m["code"], m)
	}
	if strings.Contains(errorMessageText(out), "network.registry_unreachable") {
		t.Errorf("reachable rejection relabeled as network.registry_unreachable: %v", m)
	}
	// The registry marked this non-retryable; the passthrough must preserve it.
	if r, _ := m["retryable"].(bool); r {
		t.Errorf("retryable = true, want false (registry envelope must survive): %v", m)
	}
}

// Spec: §7.4 — offline-first + unreachable + cache miss: "no error; serve
// cached results silently." With nothing cached the bridge returns a silent
// offline status rather than the network.registry_unreachable error the
// always-revalidate mode would surface (F-7.4.4).
func TestLoadArtifact_OfflineFirstCacheMissUnreachableIsSilent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cache, _ := newContentCache(dir)
	srv := &mcpServer{
		cfg: &config{
			cacheDir:  dir,
			cacheMode: "offline-first",
			registry:  "http://127.0.0.1:1", // unbound port → connect refused
			harness:   "none",
		},
		cache:       cache,
		resolutions: newResolutionCache(dir),
		http:        &http.Client{},
	}
	out := srv.loadArtifact(map[string]any{"id": "team/never-cached"})
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("loadArtifact returned %T, want map", out)
	}
	if m["status"] != "offline" {
		t.Errorf("status = %v, want offline", m["status"])
	}
	if _, hasErr := m["error"]; hasErr {
		t.Errorf("offline-first miss must not carry an error key: %v", m)
	}
	if strings.Contains(errorMessageText(out), "network.registry_unreachable") {
		t.Errorf("offline-first must not surface network.registry_unreachable: %v", m)
	}
}

// errorMessageText returns the message inside the {"error": "..."}
// envelope errorResult produces, or "" if the input doesn't match.
func errorMessageText(out any) string {
	m, ok := out.(map[string]any)
	if !ok {
		return ""
	}
	if e, ok := m["error"].(string); ok {
		return e
	}
	return ""
}
