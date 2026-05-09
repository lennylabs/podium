package core_test

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// nestedAnalyzeRegistry creates a registry with a sparse close-
// reporting subdomain (1 artifact), a regular cap-markets (3
// artifacts), and a passthrough chain a/b/c/d/x.
func nestedAnalyzeRegistry(t *testing.T) *core.Registry {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	put := func(id string, tags []string) {
		if err := st.PutManifest(context.Background(), store.ManifestRecord{
			TenantID: "t", ArtifactID: id, Version: "1.0.0",
			ContentHash: "sha256:" + id, Type: "skill", Layer: "L",
			Tags: tags,
		}); err != nil {
			t.Fatalf("PutManifest %s: %v", id, err)
		}
	}
	put("finance/close-reporting/run", []string{"finance"})
	put("finance/cap-markets/alpha", []string{"finance", "trading"})
	put("finance/cap-markets/beta", []string{"finance", "risk"})
	put("finance/cap-markets/gamma", []string{"finance", "ops"})
	put("a/b/c/d/leaf", []string{"deep"})
	return core.New(st, "t", []layer.Layer{
		{ID: "L", Visibility: layer.Visibility{Public: true}},
	})
}

// Spec: §4.5.5 — AnalyzeDomain reports recursive_count and flags
// sparse subdomains as fold candidates.
// Phase: 8
func TestAnalyzeDomain_FoldCandidates(t *testing.T) {
	testharness.RequirePhase(t, 8)
	t.Parallel()
	reg := nestedAnalyzeRegistry(t)
	r, err := reg.AnalyzeDomain(context.Background(), publicID, "finance")
	if err != nil {
		t.Fatalf("AnalyzeDomain: %v", err)
	}
	foldPaths := map[string]bool{}
	for _, c := range r.FoldCandidates {
		foldPaths[c.Path] = true
	}
	if !foldPaths["finance/close-reporting"] {
		t.Errorf("close-reporting should be a fold candidate; got %+v", r.FoldCandidates)
	}
	if foldPaths["finance/cap-markets"] {
		t.Errorf("cap-markets should NOT be a fold candidate (3 artifacts)")
	}
}

// Spec: §4.5.5 — passthrough chain depth correctly counts the
// single-child intermediates above an artifact-bearing domain.
// Phase: 8
func TestAnalyzeDomain_PassthroughChainDepth(t *testing.T) {
	testharness.RequirePhase(t, 8)
	t.Parallel()
	reg := nestedAnalyzeRegistry(t)
	r, err := reg.AnalyzeDomain(context.Background(), publicID, "a")
	if err != nil {
		t.Fatalf("AnalyzeDomain: %v", err)
	}
	if r.PassthroughChainLength < 3 {
		t.Errorf("PassthroughChainLength = %d, want >= 3 (b → c → d)", r.PassthroughChainLength)
	}
}

// Spec: §4.5.5 — root analysis produces the expected
// recursive_count for the whole tenant view.
// Phase: 8
func TestAnalyzeDomain_RootCounts(t *testing.T) {
	testharness.RequirePhase(t, 8)
	t.Parallel()
	reg := nestedAnalyzeRegistry(t)
	r, err := reg.AnalyzeDomain(context.Background(), publicID, "")
	if err != nil {
		t.Fatalf("AnalyzeDomain: %v", err)
	}
	if r.RecursiveCount != 5 {
		t.Errorf("RecursiveCount = %d, want 5", r.RecursiveCount)
	}
	if r.ChildCount < 2 {
		t.Errorf("ChildCount = %d, want >= 2 (finance, a)", r.ChildCount)
	}
}
