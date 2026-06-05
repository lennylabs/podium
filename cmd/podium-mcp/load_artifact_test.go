package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/version"
)

// loadArtifactJSON builds a /v1/load_artifact response body whose content_hash
// is the canonical hash of frontmatter plus resources, so the §6.6 step 2
// consumer-side check (verifyContentHash) accepts it. Compute it in
// the test goroutine and write the returned string from the stub handler.
func loadArtifactJSON(t *testing.T, fields map[string]any) string {
	t.Helper()
	fm, _ := fields["frontmatter"].(string)
	parts := [][]byte{[]byte(fm), nil}
	if res, ok := fields["resources"].(map[string]string); ok {
		keys := make([]string, 0, len(res))
		for k := range res {
			keys = append(keys, k)
		}
		sortStrings(keys)
		for _, k := range keys {
			parts = append(parts, []byte(k), []byte(res[k]))
		}
	}
	if _, set := fields["content_hash"]; !set {
		fields["content_hash"] = "sha256:" + version.ContentHash(parts...)
	}
	b, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal stub response: %v", err)
	}
	return string(b)
}

// sortStrings is a tiny dependency-free sort for the test helper.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

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
	respBody := loadArtifactJSON(t, map[string]any{
		"id": "x", "type": "context", "version": "1.0.0",
		"manifest_body": "body", "frontmatter": "---\ntype: context\n---\n",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/load_artifact" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(respBody))
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
	respBody := loadArtifactJSON(t, map[string]any{
		"id": "x", "type": "context", "version": "1.0.0",
		"frontmatter": "---\ntype: context\n---\n",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(respBody))
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
