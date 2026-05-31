package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/version"
)

// spec: §12 — "ETag caching of immutable artifact versions." The MCP client
// sends the cached content-hash ETag as If-None-Match on load_artifact; a 304
// is served from the content-addressed cache without re-downloading the
// manifest body (F-12.0.8). When HEAD revalidation (§6.5) is unavailable, the
// conditional GET is the revalidation round-trip.
func TestLoadArtifact_SendsIfNoneMatchAndServes304FromCache(t *testing.T) {
	t.Parallel()
	const fm = "---\ntype: context\n---\n"
	hash := "sha256:" + version.ContentHash([]byte(fm), nil)

	var sawIfNoneMatch atomic.Value
	sawIfNoneMatch.Store("")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			// HEAD revalidation unavailable, so the bridge falls through to
			// the conditional GET below.
			w.WriteHeader(http.StatusMethodNotAllowed)
		case http.MethodGet:
			sawIfNoneMatch.Store(r.Header.Get("If-None-Match"))
			w.Header().Set("ETag", `"`+hash+`"`)
			w.WriteHeader(http.StatusNotModified)
		}
	}))
	t.Cleanup(ts.Close)

	dir := t.TempDir()
	cache, _ := newContentCache(dir)
	if err := cache.put(hash, fm, "cached-body", nil); err != nil {
		t.Fatalf("put: %v", err)
	}
	resolutions := newResolutionCache(dir)
	t.Cleanup(func() { resolutions.Close() })
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
	if _, isErr := m["error"]; isErr {
		t.Fatalf("unexpected error result: %v", m)
	}
	if got := sawIfNoneMatch.Load().(string); got != `"`+hash+`"` {
		t.Errorf("If-None-Match = %q, want %q", got, `"`+hash+`"`)
	}
	if m["manifest_body"] != "cached-body" {
		t.Errorf("manifest_body = %v, want cached-body (served from cache): %v", m["manifest_body"], m)
	}
}

// On a 200 (changed artifact), the bridge fetches the fresh body rather than
// serving the stale cache entry.
func TestLoadArtifact_ConditionalGET200ServesFreshBody(t *testing.T) {
	t.Parallel()
	const cachedFM = "---\ntype: context\nversion: 1.0.0\n---\n"
	cachedHash := "sha256:" + version.ContentHash([]byte(cachedFM), nil)
	const freshFM = "---\ntype: context\nversion: 2.0.0\n---\n"
	freshHash := "sha256:" + version.ContentHash([]byte(freshFM), nil)
	respBody := loadArtifactJSON(t, map[string]any{
		"id": "team/x", "type": "context", "version": "2.0.0",
		"content_hash": freshHash, "manifest_body": "fresh-body", "frontmatter": freshFM,
	})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodHead:
			// HEAD reports a changed hash so the §6.5 path does not serve the
			// stale cache and falls through to the conditional GET.
			w.Header().Set("X-Podium-Content-Hash", freshHash)
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			w.Header().Set("ETag", `"`+freshHash+`"`)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(respBody))
		}
	}))
	t.Cleanup(ts.Close)

	dir := t.TempDir()
	cache, _ := newContentCache(dir)
	if err := cache.put(cachedHash, cachedFM, "stale-body", nil); err != nil {
		t.Fatalf("put: %v", err)
	}
	resolutions := newResolutionCache(dir)
	t.Cleanup(func() { resolutions.Close() })
	resolutions.PutVersion("team/x", "1.0.0", cachedHash, time.Now())

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
	if _, isErr := m["error"]; isErr {
		t.Fatalf("unexpected error result: %v", m)
	}
	if m["manifest_body"] != "fresh-body" {
		t.Errorf("manifest_body = %v, want fresh-body: %v", m["manifest_body"], m)
	}
}
