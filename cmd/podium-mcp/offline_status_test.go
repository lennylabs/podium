package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// spec: §12 — "The cache and offline-first mode let cached artifacts continue
// to work during transient outages. Fresh load_domain / search_domains /
// search_artifacts returns an explicit 'offline' status that hosts can
// surface." When the registry is unreachable (transport-level failure), each
// discovery/search meta-tool returns status "offline" rather than an error
// envelope.

const unreachableRegistry = "http://127.0.0.1:1" // unbound port → connect refused

func TestLoadDomain_OfflineStatusOnUnreachable(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{registry: unreachableRegistry}, http: &http.Client{}}
	out := srv.proxyGet("/v1/load_domain", map[string]any{"path": "finance"}, nil)
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("got %T, want map", out)
	}
	if m["status"] != "offline" {
		t.Errorf("status = %v, want offline", m["status"])
	}
	if _, has := m["error"]; has {
		t.Errorf("offline result must not carry an error key: %v", m)
	}
}

func TestSearchDomains_OfflineStatusOnUnreachable(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{registry: unreachableRegistry}, http: &http.Client{}}
	out := srv.proxyGet("/v1/search_domains", map[string]any{"query": "x"}, map[string]any{"results": []any{}})
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("got %T, want map", out)
	}
	if m["status"] != "offline" {
		t.Errorf("status = %v, want offline", m["status"])
	}
	results, ok := m["results"].([]any)
	if !ok || len(results) != 0 {
		t.Errorf("results = %v, want empty list", m["results"])
	}
}

func TestSearchArtifacts_OfflineStatusNoOverlay(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{registry: unreachableRegistry}, http: &http.Client{}}
	out := srv.searchArtifacts(map[string]any{"query": "variance"})
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("got %T, want map", out)
	}
	if m["status"] != "offline" {
		t.Errorf("status = %v, want offline", m["status"])
	}
	if m["query"] != "variance" {
		t.Errorf("query = %v, want variance", m["query"])
	}
	results, ok := m["results"].([]any)
	if !ok || len(results) != 0 {
		t.Errorf("results = %v, want empty list", m["results"])
	}
}

// With a workspace overlay the registry is unreachable but the local records
// are not, so a fresh search still surfaces the overlay matches alongside the
// offline status (§12: cached/local artifacts continue to work).
func TestSearchArtifacts_OfflineStatusServesOverlay(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{
		cfg:  &config{registry: unreachableRegistry},
		http: &http.Client{},
		overlay: []filesystem.ArtifactRecord{
			{
				ID: "overlay/local-variance",
				Artifact: &manifest.Artifact{
					Type:        manifest.TypeSkill,
					Name:        "local-variance",
					Description: "local variance helper for the workspace",
					Version:     "0.1.0",
				},
			},
		},
	}
	out := srv.searchArtifacts(map[string]any{"query": "variance", "top_k": float64(10)})
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("got %T, want map", out)
	}
	if m["status"] != "offline" {
		t.Errorf("status = %v, want offline", m["status"])
	}
	results, ok := m["results"].([]map[string]any)
	if !ok || len(results) != 1 {
		t.Fatalf("results = %v, want one overlay hit", m["results"])
	}
	if results[0]["id"] != "overlay/local-variance" {
		t.Errorf("result id = %v, want overlay/local-variance", results[0]["id"])
	}
}

// Spec: §7.4 — offline-first "serve cached results silently": the
// discovery meta-tools must NOT carry an explicit "offline" status field in
// offline-first mode, distinguishing it from always-revalidate, which does.
func TestProxyGet_OfflineFirstServesSilently(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{registry: unreachableRegistry, cacheMode: "offline-first"}, http: &http.Client{}}
	out := srv.proxyGet("/v1/search_domains", map[string]any{"query": "x"}, map[string]any{"results": []any{}})
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("got %T, want map", out)
	}
	if _, has := m["status"]; has {
		t.Errorf("offline-first must serve silently (no status field): %v", m)
	}
	if _, has := m["error"]; has {
		t.Errorf("offline-first must not carry an error key: %v", m)
	}
	if results, ok := m["results"].([]any); !ok || len(results) != 0 {
		t.Errorf("results = %v, want empty list", m["results"])
	}
}

// Spec: §7.4 — search_artifacts also serves silently in
// offline-first mode: overlay matches return with no "offline" status field.
func TestSearchArtifacts_OfflineFirstServesSilently(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{registry: unreachableRegistry, cacheMode: "offline-first"}, http: &http.Client{}}
	out := srv.searchArtifacts(map[string]any{"query": "variance"})
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("got %T, want map", out)
	}
	if _, has := m["status"]; has {
		t.Errorf("offline-first search must serve silently (no status field): %v", m)
	}
	if m["query"] != "variance" {
		t.Errorf("query = %v, want variance", m["query"])
	}
}

// Spec: §7.4 — always-revalidate (the default mode) keeps the
// explicit "offline" status, the contrast offline-first drops.
func TestProxyGet_AlwaysRevalidateKeepsOfflineStatus(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{registry: unreachableRegistry, cacheMode: "always-revalidate"}, http: &http.Client{}}
	out := srv.proxyGet("/v1/load_domain", map[string]any{"path": "finance"}, nil)
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("got %T, want map", out)
	}
	if m["status"] != "offline" {
		t.Errorf("status = %v, want offline (always-revalidate surfaces it)", m["status"])
	}
}

// Spec: §7.4 — offline-only "never contact the registry; structured error if
// cache miss." The discovery meta-tools keep no content cache, so an
// offline-only load_domain / search_domains returns the structured
// network.offline_cache_miss error without dialing the registry. Pointing at
// an unbound port proves no connection is attempted: a dial would surface a
// connect/refused transport error instead of the offline-miss code.
func TestProxyGet_OfflineOnlyNeverContactsRegistry(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/v1/load_domain", "/v1/search_domains", "/v1/scope/preview"} {
		path := path
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			srv := &mcpServer{cfg: &config{registry: unreachableRegistry, cacheMode: "offline-only"}, http: &http.Client{}}
			out := srv.proxyGet(path, map[string]any{"path": "finance"}, map[string]any{"results": []any{}})
			body := errorMessageText(out)
			if !strings.Contains(body, "network.offline_cache_miss") {
				t.Errorf("error = %q, want network.offline_cache_miss", body)
			}
			if m, ok := out.(map[string]any); ok && m["status"] == "offline" {
				t.Errorf("offline-only miss must be a structured error, not an offline status: %v", m)
			}
		})
	}
}

// Spec: §7.4 — offline-only search_artifacts serves only the workspace overlay
// and never contacts the registry. With overlay matches it returns them; an
// empty overlay is a structured cache miss.
func TestSearchArtifacts_OfflineOnlyServesOverlayOnly(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{
		cfg:  &config{registry: unreachableRegistry, cacheMode: "offline-only"},
		http: &http.Client{},
		overlay: []filesystem.ArtifactRecord{
			{
				ID: "overlay/local-variance",
				Artifact: &manifest.Artifact{
					Type:        manifest.TypeSkill,
					Name:        "local-variance",
					Description: "local variance helper for the workspace",
					Version:     "0.1.0",
				},
			},
		},
	}
	out := srv.searchArtifacts(map[string]any{"query": "variance", "top_k": float64(10)})
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("got %T, want map", out)
	}
	results, ok := m["results"].([]map[string]any)
	if !ok || len(results) != 1 || results[0]["id"] != "overlay/local-variance" {
		t.Fatalf("results = %v, want one overlay hit", m["results"])
	}
}

func TestSearchArtifacts_OfflineOnlyEmptyOverlayMisses(t *testing.T) {
	t.Parallel()
	srv := &mcpServer{cfg: &config{registry: unreachableRegistry, cacheMode: "offline-only"}, http: &http.Client{}}
	out := srv.searchArtifacts(map[string]any{"query": "variance"})
	body := errorMessageText(out)
	if !strings.Contains(body, "network.offline_cache_miss") {
		t.Errorf("error = %q, want network.offline_cache_miss", body)
	}
}

// A structured >=400 registry response means the registry is reachable and
// rejected the request, so it must surface as a §6.10 error envelope, not an
// offline status. This guards the transport-failure vs request-rejection
// distinction in isRegistryUnreachable.
func TestProxyGet_RegistryErrorIsNotOffline(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":"registry.unavailable","message":"boom","retryable":true}`))
	}))
	t.Cleanup(ts.Close)
	srv := &mcpServer{cfg: &config{registry: ts.URL}, http: &http.Client{}}
	out := srv.proxyGet("/v1/load_domain", map[string]any{"path": "x"}, nil)
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("got %T, want map", out)
	}
	if m["status"] == "offline" {
		t.Errorf("reachable-but-erroring registry must not be offline: %v", m)
	}
	if m["code"] != "registry.unavailable" {
		t.Errorf("code = %v, want registry.unavailable", m["code"])
	}
}
