package integration

import (
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/internal/testharness/registryharness"
)

// searchIDsOver runs one search_artifacts call through a fresh MCP bridge and
// returns the result IDs in ranked order.
func searchIDsOver(t *testing.T, registry, cacheDir, query string) []string {
	t.Helper()
	res := callToolOver(t, registry, cacheDir, "search_artifacts", map[string]any{"query": query})
	raw, _ := res["results"].([]any)
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		if m, ok := r.(map[string]any); ok {
			if id, ok := m["id"].(string); ok {
				out = append(out, id)
			}
		}
	}
	return out
}

// Spec: §12 — "learn-from-usage reranking surfaces signal-based
// ordering." Two artifacts with identical descriptions tie on BM25 and order
// alphabetically. After repeated load_artifact calls record an access signal
// for the second one, a fresh search surfaces it first. The registry server
// keeps the usage store across requests, so the signal accumulates end to end.
func TestPodiumMCP_SearchRerankedByUsage(t *testing.T) {
	t.Parallel()

	const desc = "shared helper utility"
	h := registryharness.New(t,
		testharness.WriteTreeOption{
			Path:    "aaa/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 1.0.0\ndescription: " + desc + "\n---\n\nbody\n",
		},
		testharness.WriteTreeOption{
			Path:    "bbb/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 1.0.0\ndescription: " + desc + "\n---\n\nbody\n",
		},
	)
	cache := t.TempDir()

	// Baseline: BM25 tie breaks alphabetically, so aaa leads.
	if got := searchIDsOver(t, h.URL, cache, "helper"); len(got) < 2 || got[0] != "aaa" {
		t.Fatalf("baseline order = %v, want aaa first", got)
	}

	// Record usage for bbb via repeated loads against the same server.
	for i := 0; i < 5; i++ {
		res := callToolOver(t, h.URL, t.TempDir(), "load_artifact", map[string]any{"id": "bbb"})
		if res["id"] != "bbb" {
			t.Fatalf("load bbb failed: %v", res)
		}
	}

	// A fresh search now surfaces the more-accessed bbb first.
	if got := searchIDsOver(t, h.URL, cache, "helper"); len(got) < 2 || got[0] != "bbb" {
		t.Errorf("reranked order = %v, want bbb first", got)
	}
}
