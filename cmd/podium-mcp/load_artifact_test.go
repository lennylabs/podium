package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/sign"
)

func newTestServer(t *testing.T, cfg *config) *mcpServer {
	t.Helper()
	cache, err := newContentCache(t.TempDir())
	if err != nil {
		t.Fatalf("newContentCache: %v", err)
	}
	s := &mcpServer{
		cfg:         cfg,
		http:        &http.Client{},
		cache:       cache,
		resolutions: newResolutionCache(t.TempDir()),
		adapters:    adapter.DefaultRegistry(),
	}
	return s
}

// loadArtifact succeeds against a stub registry response, caches the
// resolution, and returns a result map.
func TestLoadArtifact_HappyPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/load_artifact" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{
			"id": "x", "type": "context", "version": "1.0.0",
			"content_hash": "sha256:abc", "manifest_body": "body",
			"frontmatter": "---\ntype: context\n---\n"
		}`))
	}))
	defer srv.Close()
	s := newTestServer(t, &config{
		registry:     srv.URL,
		harness:      "none",
		verifyPolicy: sign.PolicyNever,
	})
	got := s.loadArtifact(map[string]any{"id": "x"})
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("type = %T", got)
	}
	if m["id"] != "x" {
		t.Errorf("id = %v", m["id"])
	}
}

// always-revalidate mode falls back to cache when the registry is unreachable.
func TestLoadArtifact_AlwaysRevalidate_NetworkFailure(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, &config{
		registry:     "http://127.0.0.1:1",
		cacheMode:    "always-revalidate",
		harness:      "none",
		verifyPolicy: sign.PolicyNever,
	})
	got := s.loadArtifact(map[string]any{"id": "x"})
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("type = %T", got)
	}
	if _, has := m["error"]; !has {
		t.Errorf("expected error, got %v", m)
	}
}

// offline-only with no cache surfaces a cache.offline_miss error.
func TestLoadArtifact_OfflineOnlyCacheMiss(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, &config{
		registry:     "http://127.0.0.1:1",
		cacheMode:    "offline-only",
		harness:      "none",
		verifyPolicy: sign.PolicyNever,
	})
	got := s.loadArtifact(map[string]any{"id": "x"})
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("type = %T", got)
	}
	if errStr, _ := m["error"].(string); !strings.Contains(errStr, "offline") {
		t.Errorf("error = %q", errStr)
	}
}

// Malformed registry response surfaces a decode error.
func TestLoadArtifact_MalformedResponse(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()
	s := newTestServer(t, &config{
		registry:     srv.URL,
		harness:      "none",
		verifyPolicy: sign.PolicyNever,
	})
	got := s.loadArtifact(map[string]any{"id": "x"})
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("type = %T", got)
	}
	if errStr, _ := m["error"].(string); !strings.Contains(errStr, "decode") {
		t.Errorf("error = %q", errStr)
	}
}

// argsIDAndVersion extracts id + version from the args map.
func TestArgsIDAndVersion(t *testing.T) {
	t.Parallel()
	id, ver := argsIDAndVersion(map[string]any{"id": "x", "version": "1.0.0"})
	if id != "x" || ver != "1.0.0" {
		t.Errorf("got id=%q ver=%q", id, ver)
	}
	id, ver = argsIDAndVersion(map[string]any{})
	if id != "" || ver != "" {
		t.Errorf("empty args: id=%q ver=%q", id, ver)
	}
	id, ver = argsIDAndVersion(map[string]any{"id": "x"})
	if id != "x" || ver != "" {
		t.Errorf("no version: id=%q ver=%q", id, ver)
	}
}

// callTool dispatches to the proper handler.
func TestCallTool_DispatchToLoadArtifact(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"x","type":"context","version":"1.0.0","content_hash":"sha256:a","frontmatter":"---\ntype: context\n---\n"}`))
	}))
	defer srv.Close()
	s := newTestServer(t, &config{
		registry:     srv.URL,
		harness:      "none",
		verifyPolicy: sign.PolicyNever,
	})
	got := s.callTool([]byte(`{"name":"load_artifact","arguments":{"id":"x"}}`))
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("type = %T", got)
	}
	if m["id"] != "x" {
		t.Errorf("id = %v", m["id"])
	}
}

// callTool dispatches to search_artifacts via the registry.
func TestCallTool_DispatchToSearchArtifacts(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"total_matched":0,"results":[]}`))
	}))
	defer srv.Close()
	s := newTestServer(t, &config{
		registry:     srv.URL,
		harness:      "none",
		verifyPolicy: sign.PolicyNever,
	})
	got := s.callTool([]byte(`{"name":"search_artifacts","arguments":{"query":"x"}}`))
	if got == nil {
		t.Errorf("nil result")
	}
}

// callTool dispatches to load_domain via proxyGet.
func TestCallTool_DispatchToLoadDomain(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/load_domain" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"path":"","subdomains":[]}`))
	}))
	defer srv.Close()
	s := newTestServer(t, &config{
		registry:     srv.URL,
		harness:      "none",
		verifyPolicy: sign.PolicyNever,
	})
	got := s.callTool([]byte(`{"name":"load_domain","arguments":{"path":""}}`))
	if got == nil {
		t.Errorf("nil result")
	}
}
