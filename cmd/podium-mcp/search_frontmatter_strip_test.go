package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// spec: §3.2 / §5 — the MCP meta-tool surface returns lean descriptors. The
// registry carries the artifact frontmatter on the /v1/search_artifacts wire
// for the §7.6.1 read-CLI/SDK schema, so the bridge must drop it before
// handing results to the agent.
func TestSearchArtifacts_StripsFrontmatterNoOverlay(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"query":         "x",
			"total_matched": 1,
			"results": []map[string]any{
				{"id": "team/a", "type": "skill", "frontmatter": "---\ntype: skill\n---\n"},
			},
		})
	}))
	t.Cleanup(ts.Close)

	srv := &mcpServer{cfg: &config{registry: ts.URL}, http: &http.Client{}}
	out := srv.searchArtifacts(map[string]any{"query": "x"})
	body, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("got %T, want map", out)
	}
	results, ok := body["results"].([]any)
	if !ok {
		t.Fatalf("results type = %T, want []any", body["results"])
	}
	for _, r := range results {
		m := r.(map[string]any)
		if _, present := m["frontmatter"]; present {
			t.Errorf("frontmatter leaked to agent in no-overlay path: %+v", m)
		}
	}
}

// spec: §3.2 / §5 — the fused (overlay-present) path also strips the registry
// descriptor's frontmatter, keeping registry hits uniform with overlay
// descriptors (which carry none).
func TestSearchArtifacts_StripsFrontmatterFused(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"query":         "variance",
			"total_matched": 1,
			"results": []map[string]any{
				{"id": "registry/a", "type": "skill", "version": "1.0.0", "description": "variance helper", "frontmatter": "---\ntype: skill\n---\n"},
			},
		})
	}))
	t.Cleanup(ts.Close)

	srv := &mcpServer{
		cfg:  &config{registry: ts.URL},
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
	body := out.(map[string]any)
	results, _ := body["results"].([]map[string]any)
	if len(results) == 0 {
		t.Fatalf("expected fused results")
	}
	for _, r := range results {
		if _, present := r["frontmatter"]; present {
			t.Errorf("frontmatter leaked to agent in fused path: %+v", r)
		}
	}
}
