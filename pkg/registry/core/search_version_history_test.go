package core_test

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// versionHistoryRegistry stores one artifact at four versions and returns a
// registry over it, so a search sees a stored version history behind a single
// canonical ID.
func versionHistoryRegistry(t *testing.T) *core.Registry {
	t.Helper()
	const tenant = "t"
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: tenant}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	for _, v := range []string{"0.1.0", "0.2.0", "0.3.0", "0.4.0"} {
		if err := st.PutManifest(context.Background(), store.ManifestRecord{
			TenantID: tenant, ArtifactID: "finance/ap/pay-invoice", Version: v,
			ContentHash: "sha256:" + v, Type: "skill", Layer: "L",
			Description: "pay an untrusted invoice",
		}); err != nil {
			t.Fatalf("PutManifest %s: %v", v, err)
		}
	}
	return core.New(st, tenant, []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
}

// Spec: §4.7.6 / §5 — search ranks artifacts at their latest version, so a
// stored version history contributes one result and counts once in
// total_matched. Counting versions made a caller read a remainder that no
// filter could reach, because the reranks key on the canonical ID and collapse
// the duplicates the count had already reported.
func TestSearchArtifacts_VersionHistoryCountsOnce(t *testing.T) {
	t.Parallel()
	reg := versionHistoryRegistry(t)
	for _, tc := range []struct {
		name  string
		query string
	}{
		{name: "query", query: "untrusted"},
		{name: "browse", query: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := reg.SearchArtifacts(context.Background(), layer.Identity{IsPublic: true},
				core.SearchArtifactsOptions{Query: tc.query, TopK: 10})
			if err != nil {
				t.Fatalf("SearchArtifacts: %v", err)
			}
			if len(res.Results) != 1 {
				t.Fatalf("Results = %d, want 1: %+v", len(res.Results), res.Results)
			}
			if res.TotalMatched != 1 {
				t.Errorf("TotalMatched = %d, want 1", res.TotalMatched)
			}
			if res.Results[0].Version != "0.4.0" {
				t.Errorf("Version = %q, want 0.4.0", res.Results[0].Version)
			}
		})
	}
}

// Spec: §4.7.6 / §12 — the learn-from-usage rerank keys on the canonical ID,
// so it collapses a version history to one row. total_matched stays consistent
// with the rows the caller can reach.
func TestSearchArtifacts_VersionHistoryCountsOnceUnderUsageRerank(t *testing.T) {
	t.Parallel()
	reg := versionHistoryRegistry(t)
	sig := core.NewMemoryUsageSignals()
	sig.Record(context.Background(), "t", "finance/ap/pay-invoice", "s1")
	reg = reg.WithUsageSignals(sig)

	res, err := reg.SearchArtifacts(context.Background(), layer.Identity{IsPublic: true},
		core.SearchArtifactsOptions{Query: "untrusted", TopK: 10})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	if len(res.Results) != res.TotalMatched {
		t.Errorf("Results = %d, TotalMatched = %d: the count claims a remainder no filter can reach",
			len(res.Results), res.TotalMatched)
	}
	if res.TotalMatched != 1 {
		t.Errorf("TotalMatched = %d, want 1", res.TotalMatched)
	}
}
