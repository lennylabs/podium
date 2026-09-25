package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Spec: §7.4 — always-revalidate + unreachable registry + cache hit: serve
// the verified cached record with status=offline + served_from_cache=true.
// Both markers are also stamped on an error envelope, so the success signal is
// the absence of an error and the served body.
func TestLoadArtifact_AlwaysRevalidateFallsBackToCacheOnNetworkError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rec := cachedRecord("team/x", "---\ntype: context\nversion: 1.0.0\n---\nbody\n", "body\n", nil)
	srv := cacheServer(t, dir, "http://127.0.0.1:1", "always-revalidate") // unbound port → connect refused
	if err := srv.cacheVerifiedRecord(rec); err != nil {
		t.Fatalf("cacheVerifiedRecord: %v", err)
	}
	srv.resolutions.PutLatest("team/x", "1.0.0", rec.ContentHash, time.Now())

	out := srv.loadArtifact(map[string]any{"id": "team/x"})
	wantServed(t, out, "body\n")
	m := out.(map[string]any)
	if m["status"] != "offline" {
		t.Errorf("status = %v, want offline", m["status"])
	}
	if served, _ := m["served_from_cache"].(bool); !served {
		t.Errorf("served_from_cache = %v, want true", m["served_from_cache"])
	}
}

// Spec: §7.4, §4.7.10, §6.6 — the degraded-network fallback re-verifies the
// cached record: a per-ID document edited after a valid record was written
// fails the delivery check and writes nothing, and neither offline marker
// makes the refusal a served record.
func TestLoadArtifact_AlwaysRevalidateFallbackRefusesAnEditedCacheRecord(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dest := t.TempDir()
	rec := cachedRecord("team/x", "---\ntype: context\nversion: 1.0.0\n---\nbody\n", "body\n", nil)
	srv := cacheServer(t, dir, "http://127.0.0.1:1", "always-revalidate")
	if err := srv.cacheVerifiedRecord(rec); err != nil {
		t.Fatalf("cacheVerifiedRecord: %v", err)
	}
	srv.resolutions.PutLatest("team/x", "1.0.0", rec.ContentHash, time.Now())
	edited := filepath.Join(deliveryDir(dir, rec), "frontmatter")
	if err := os.WriteFile(edited, []byte(rec.Frontmatter+"injected: true\n"), 0o644); err != nil {
		t.Fatalf("edit: %v", err)
	}

	out := srv.loadArtifact(map[string]any{"id": "team/x", "destination": dest})
	wantRefused(t, out, "materialize.content_hash_mismatch")
	if entries, _ := os.ReadDir(dest); len(entries) != 0 {
		t.Errorf("a refused fallback wrote to the destination: %v", entries)
	}
}

// Spec: §7.4 — always-revalidate + unreachable + cache miss:
// returns network.registry_unreachable.
// Matrix: §6.10 (network.registry_unreachable)
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
// rejection must NOT be relabeled as the retryable network.registry_unreachable:
// that conflates a registry that refused with one that could not
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
// always-revalidate mode would surface.
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
