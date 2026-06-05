package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// Spec: §6.4.1 — when the workspace overlay holds a record that
// also matches the search query, the MCP server fuses it into the
// registry's response via RRF. Items unique to either stream
// still appear in the output.
func TestSearchArtifacts_FanoutRRF(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"query":         "variance",
			"total_matched": 2,
			"results": []map[string]any{
				{"id": "registry/a", "type": "skill", "version": "1.0.0", "description": "a"},
				{"id": "registry/b", "type": "skill", "version": "1.0.0", "description": "b"},
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
	body, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("got %T, want map", out)
	}
	results, _ := body["results"].([]map[string]any)
	if len(results) != 3 {
		t.Fatalf("len = %d, want 3 (2 registry + 1 overlay)", len(results))
	}
	ids := map[string]bool{}
	for _, r := range results {
		id, _ := r["id"].(string)
		ids[id] = true
	}
	for _, want := range []string{"registry/a", "registry/b", "overlay/local-variance"} {
		if !ids[want] {
			t.Errorf("missing %q in fused results: %+v", want, ids)
		}
	}
	// total_matched bumps by the local hit count so callers can
	// see overlay artifacts existed.
	if body["total_matched"].(int) != 3 {
		t.Errorf("total_matched = %v, want 3", body["total_matched"])
	}
}

// Spec: §6.4.1 — empty overlay leaves the registry response
// untouched (no fan-out, no rewrite) so deployments without an
// overlay see the same shape they did before.
func TestSearchArtifacts_NoOverlayPassthrough(t *testing.T) {
	t.Parallel()
	registryPayload := map[string]any{
		"query":         "x",
		"total_matched": 1,
		"results": []map[string]any{
			{"id": "team/a", "type": "skill"},
		},
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(registryPayload)
	}))
	t.Cleanup(ts.Close)

	srv := &mcpServer{cfg: &config{registry: ts.URL}, http: &http.Client{}}
	out := srv.searchArtifacts(map[string]any{"query": "x"})

	// The output is whatever jsonAny decoded; assert a top-level
	// total_matched of 1.
	body, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("got %T, want map", out)
	}
	if got, _ := body["total_matched"].(float64); got != 1 {
		t.Errorf("total_matched = %v, want 1 (no fanout)", body["total_matched"])
	}
}

// Spec: §6.4.1 — when a draft on the overlay is also returned by
// the registry, the fused total_matched counts it once. The pre-fix code
// summed registry.TotalMatched + len(local), so an overlapping hit inflated
// the total above the deduplicated result count.
func TestSearchArtifacts_OverlapTotalNotDoubleCounted(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// total_matched (5) exceeds the two returned results: the registry's
		// count is pre-top_k-truncation. The overlay draft shared/variance is
		// also returned by the registry.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"query":         "variance",
			"total_matched": 5,
			"results": []map[string]any{
				{"id": "shared/variance", "type": "skill", "version": "1.0.0", "description": "variance helper"},
				{"id": "registry/b", "type": "skill", "version": "1.0.0", "description": "b"},
			},
		})
	}))
	t.Cleanup(ts.Close)

	srv := &mcpServer{
		cfg:  &config{registry: ts.URL},
		http: &http.Client{},
		overlay: []filesystem.ArtifactRecord{
			{
				ID: "shared/variance",
				Artifact: &manifest.Artifact{
					Type:        manifest.TypeSkill,
					Name:        "variance",
					Description: "local variance helper for the workspace",
					Version:     "0.1.0",
				},
			},
		},
	}
	out := srv.searchArtifacts(map[string]any{"query": "variance", "top_k": float64(10)})
	body, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("got %T, want map", out)
	}
	results, _ := body["results"].([]map[string]any)
	// The overlapping artifact is merged into a single descriptor.
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2 (shared merged)", len(results))
	}
	// total_matched is the registry's distinct count; the overlapping
	// overlay hit adds nothing because it is already in that count.
	if got := body["total_matched"].(int); got != 5 {
		t.Errorf("total_matched = %d, want 5 (no double count); pre-fix value was 6", got)
	}
	// The merged descriptor is annotated as also living on the overlay.
	for _, r := range results {
		if r["id"] == "shared/variance" && r["overlay"] != true {
			t.Errorf("overlapping descriptor not annotated overlay=true: %+v", r)
		}
	}
}

// Spec: §6.4.1 — an overlay-only draft (absent from the registry's
// returned results) adds exactly one to the registry's pre-truncation total.
func TestSearchArtifacts_OverlayOnlyAddsOne(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"query":         "variance",
			"total_matched": 5,
			"results": []map[string]any{
				{"id": "registry/a", "type": "skill", "version": "1.0.0", "description": "a variance note"},
			},
		})
	}))
	t.Cleanup(ts.Close)

	srv := &mcpServer{
		cfg:  &config{registry: ts.URL},
		http: &http.Client{},
		overlay: []filesystem.ArtifactRecord{
			{
				ID: "overlay/variance-draft",
				Artifact: &manifest.Artifact{
					Type:        manifest.TypeSkill,
					Name:        "variance-draft",
					Description: "draft variance helper on the workspace overlay",
					Version:     "0.1.0",
				},
			},
		},
	}
	out := srv.searchArtifacts(map[string]any{"query": "variance", "top_k": float64(10)})
	body := out.(map[string]any)
	if got := body["total_matched"].(int); got != 6 {
		t.Errorf("total_matched = %d, want 6 (registry 5 + 1 overlay-only)", got)
	}
}

// Spec: §6.4.1 — direct coverage of the count helper: registry
// total plus distinct overlay-only IDs, deduped across the local and
// semantic streams, ignoring overlap and empty IDs.
func TestFusedTotalMatched(t *testing.T) {
	t.Parallel()
	reg := []map[string]any{{"id": "r/a"}, {"id": "r/b"}}
	cases := []struct {
		name     string
		total    int
		results  []map[string]any
		locals   [][]localSearchResult
		expected int
	}{
		{"no overlay", 5, reg, nil, 5},
		{"overlay-only", 5, reg, [][]localSearchResult{{{ID: "o/x"}}}, 6},
		{"overlap not counted", 5, reg, [][]localSearchResult{{{ID: "r/a"}}}, 5},
		{"dedup across streams", 5, reg, [][]localSearchResult{{{ID: "o/x"}}, {{ID: "o/x"}}}, 6},
		{"empty id ignored", 5, reg, [][]localSearchResult{{{ID: ""}}}, 5},
		{"offline no registry", 0, nil, [][]localSearchResult{{{ID: "o/x"}, {ID: "o/y"}}}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := fusedTotalMatched(tc.total, tc.results, tc.locals...)
			if got != tc.expected {
				t.Errorf("fusedTotalMatched = %d, want %d", got, tc.expected)
			}
		})
	}
}

// Spec: §6.4.1 — overlay records that don't match the query are
// dropped from the fused output (BM25 scores zero filter out).
func TestSearchArtifacts_OverlayMissDropped(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"query":         "variance",
			"total_matched": 1,
			"results": []map[string]any{
				{"id": "registry/a", "type": "skill"},
			},
		})
	}))
	t.Cleanup(ts.Close)

	srv := &mcpServer{
		cfg:  &config{registry: ts.URL},
		http: &http.Client{},
		overlay: []filesystem.ArtifactRecord{
			{
				ID: "overlay/unrelated",
				Artifact: &manifest.Artifact{
					Type:        manifest.TypeSkill,
					Name:        "unrelated",
					Description: "completely different topic",
				},
			},
		},
	}
	out := srv.searchArtifacts(map[string]any{"query": "variance", "top_k": float64(10)})
	body, _ := out.(map[string]any)
	results, _ := body["results"].([]map[string]any)
	for _, r := range results {
		id, _ := r["id"].(string)
		if id == "overlay/unrelated" {
			t.Errorf("overlay miss leaked into results")
		}
	}
}
