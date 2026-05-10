package main

import (
	"net/http"
	"strings"
	"testing"
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
	resolutions.Put("team/x", "", hash)

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
