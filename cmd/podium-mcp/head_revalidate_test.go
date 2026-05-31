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

// spec: §6.5 (F-6.5.2) — in always-revalidate mode a cached resolution is
// revalidated via HEAD. When the registry confirms the content hash is
// unchanged, the bridge serves the cached content and issues no full GET.
func TestLoadArtifact_AlwaysRevalidate_HeadMatchServesCache(t *testing.T) {
	t.Parallel()
	const fm = "---\ntype: context\n---\n"
	hash := "sha256:" + version.ContentHash([]byte(fm), nil)
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
	if err := cache.put(hash, fm, "cached-body", nil); err != nil {
		t.Fatalf("put: %v", err)
	}
	resolutions := newResolutionCache(dir)
	defer resolutions.Close()
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

// spec: §6.5 (F-6.5.2) — when HEAD reports a different content hash, the cached
// content is stale and the bridge performs a full GET.
func TestLoadArtifact_AlwaysRevalidate_HeadMismatchFullFetches(t *testing.T) {
	t.Parallel()
	const cachedFM = "---\ntype: context\nversion: 1.0.0\n---\n"
	cachedHash := "sha256:" + version.ContentHash([]byte(cachedFM), nil)
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
	defer resolutions.Close()
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
