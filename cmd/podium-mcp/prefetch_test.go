package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// spec: §7.6.2 — "The MCP server uses this endpoint internally for cache
// warm-up when configured to prefetch." prefetch POSTs the configured IDs to
// /v1/artifacts:batchLoad and warms the §6.5 content + resolution caches so a
// later load_artifact serves from cache.
func TestPrefetch_WarmsContentAndResolutionCache(t *testing.T) {
	const hash = "sha256:deadbeef"
	var gotMethod, gotPath string
	var gotIDs []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		var req struct {
			IDs []string `json:"ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotIDs = req.IDs
		out := make([]map[string]any, 0, len(req.IDs))
		for _, id := range req.IDs {
			out = append(out, map[string]any{
				"id":            id,
				"status":        "ok",
				"version":       "1.0.0",
				"content_hash":  hash,
				"manifest_body": "BODY",
				"frontmatter":   "---\ntype: skill\nversion: 1.0.0\n---\n",
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer ts.Close()

	dir := t.TempDir()
	cache, err := newContentCache(dir)
	if err != nil {
		t.Fatalf("newContentCache: %v", err)
	}
	rc := newResolutionCache(dir)
	defer rc.Close()
	s := &mcpServer{
		cfg:         &config{registry: ts.URL, cacheDir: dir, identityProvider: "injected-session-token"},
		http:        ts.Client(),
		cache:       cache,
		resolutions: rc,
		sessionID:   "sess",
	}
	if err := s.prefetch([]string{"finance/run"}); err != nil {
		t.Fatalf("prefetch: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/artifacts:batchLoad" {
		t.Errorf("request = %s %s, want POST /v1/artifacts:batchLoad", gotMethod, gotPath)
	}
	if len(gotIDs) != 1 || gotIDs[0] != "finance/run" {
		t.Errorf("ids = %v, want [finance/run]", gotIDs)
	}
	if !cache.has(hash) {
		t.Errorf("content cache not warmed for %s", hash)
	}
	got, ok := rc.Resolve("finance/run", "", time.Now(), 0, true)
	if !ok || got != hash {
		t.Errorf("resolution cache: got %q ok=%v, want %s", got, ok, hash)
	}
}

// spec: §7.6.2 — error items are skipped during warm-up; a partial batch does
// not poison the cache.
func TestPrefetch_SkipsErrorItems(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		out := []map[string]any{
			{"id": "missing", "status": "error", "error": map[string]any{"code": "visibility.denied"}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer ts.Close()

	dir := t.TempDir()
	cache, _ := newContentCache(dir)
	rc := newResolutionCache(dir)
	defer rc.Close()
	s := &mcpServer{
		cfg:         &config{registry: ts.URL, cacheDir: dir, identityProvider: "injected-session-token"},
		http:        ts.Client(),
		cache:       cache,
		resolutions: rc,
		sessionID:   "sess",
	}
	if err := s.prefetch([]string{"missing"}); err != nil {
		t.Fatalf("prefetch: %v", err)
	}
	if _, ok := rc.Resolve("missing", "", time.Now(), 0, true); ok {
		t.Errorf("error item should not warm the resolution cache")
	}
}
