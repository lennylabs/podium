package core_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/registry/core"
)

// spec: §12 — "learn-from-usage reranking surfaces signal-based ordering."
// The usage-signal store records meta-tool access frequency and feeds
// search_artifacts and load_domain ordering (F-12.0.9).

func TestMemoryUsageSignals_RecordRanking(t *testing.T) {
	t.Parallel()
	sig := core.NewMemoryUsageSignals()
	ctx := context.Background()
	sig.Record(ctx, tenant, "alpha", "s1")
	sig.Record(ctx, tenant, "beta", "s1")
	sig.Record(ctx, tenant, "beta", "s2")
	sig.Record(ctx, tenant, "beta", "s3")
	sig.Record(ctx, tenant, "", "s1") // empty id ignored

	got := sig.Ranking(ctx, tenant)
	want := []string{"beta", "alpha"} // beta accessed 3x, alpha 1x
	if len(got) != len(want) {
		t.Fatalf("ranking = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ranking[%d] = %q, want %q (full %v)", i, got[i], want[i], got)
		}
	}
	// A tenant with no signal yields no ranking.
	if r := sig.Ranking(ctx, "other"); len(r) != 0 {
		t.Errorf("empty-tenant ranking = %v, want none", r)
	}
}

// A resolved load_artifact records the §3.3 access signal.
func TestLoadArtifact_RecordsUsageSignal(t *testing.T) {
	t.Parallel()
	reg := setupRegistry(t, fstest.MapFS{
		"tools/calc/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextManifest("calc"))},
	}, "L")
	sig := core.NewMemoryUsageSignals()
	reg.WithUsageSignals(sig)

	if _, err := reg.LoadArtifact(context.Background(), publicID, "tools/calc", core.LoadArtifactOptions{}); err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	got := sig.Ranking(context.Background(), tenant)
	if len(got) != 1 || got[0] != "tools/calc" {
		t.Errorf("ranking after load = %v, want [tools/calc]", got)
	}

	// A failed (not-found) load must not record a usage signal.
	if _, err := reg.LoadArtifact(context.Background(), publicID, "tools/missing", core.LoadArtifactOptions{}); err == nil {
		t.Fatalf("expected not-found error")
	}
	if got := sig.Ranking(context.Background(), tenant); len(got) != 1 {
		t.Errorf("ranking = %v, want only the successful load recorded", got)
	}
}

// Two artifacts with identical descriptions tie on BM25; the usage signal
// breaks the tie so the more-accessed artifact ranks first.
func TestSearchArtifacts_UsageRerank(t *testing.T) {
	t.Parallel()
	fs := fstest.MapFS{
		"tools/aaa/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextManifest("shared helper"))},
		"tools/bbb/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextManifest("shared helper"))},
	}

	// Control: no usage signal, so the BM25 tie breaks alphabetically.
	ctrl := setupRegistry(t, fs, "L")
	res, err := ctrl.SearchArtifacts(context.Background(), publicID, core.SearchArtifactsOptions{Query: "helper"})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	if len(res.Results) != 2 || res.Results[0].ID != "tools/aaa" {
		t.Fatalf("control order = %v, want tools/aaa first", ids(res.Results))
	}

	// With usage favoring tools/bbb, it overtakes the alphabetical winner.
	reg := setupRegistry(t, fs, "L")
	sig := core.NewMemoryUsageSignals()
	for i := 0; i < 5; i++ {
		sig.Record(context.Background(), tenant, "tools/bbb", "s1")
	}
	reg.WithUsageSignals(sig)
	res, err = reg.SearchArtifacts(context.Background(), publicID, core.SearchArtifactsOptions{Query: "helper"})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	if len(res.Results) != 2 || res.Results[0].ID != "tools/bbb" {
		t.Errorf("usage-reranked order = %v, want tools/bbb first", ids(res.Results))
	}
}

// The load_domain notable pool reorders the "signal" tier by usage frequency.
func TestLoadDomain_NotableUsageRerank(t *testing.T) {
	t.Parallel()
	fs := fstest.MapFS{
		"g1/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextManifest("one"))},
		"g2/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextManifest("two"))},
		"g3/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextManifest("three"))},
	}

	// Control: alphabetical signal order.
	ctrl := setupRegistry(t, fs, "L")
	got, err := ctrl.LoadDomain(context.Background(), publicID, "", core.LoadDomainOptions{})
	if err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	if len(got.Notable) == 0 || got.Notable[0].ID != "g1" {
		t.Fatalf("control notable order = %v, want g1 first", notableIDs(got.Notable))
	}

	// With usage favoring g3, it leads the notable pool.
	reg := setupRegistry(t, fs, "L")
	sig := core.NewMemoryUsageSignals()
	for i := 0; i < 4; i++ {
		sig.Record(context.Background(), tenant, "g3", "s1")
	}
	reg.WithUsageSignals(sig)
	got, err = reg.LoadDomain(context.Background(), publicID, "", core.LoadDomainOptions{})
	if err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	if len(got.Notable) == 0 || got.Notable[0].ID != "g3" {
		t.Errorf("usage-reranked notable order = %v, want g3 first", notableIDs(got.Notable))
	}
}

func ids(ds []core.ArtifactDescriptor) []string {
	out := make([]string, len(ds))
	for i, d := range ds {
		out[i] = d.ID
	}
	return out
}

func notableIDs(ds []core.ArtifactDescriptor) []string { return ids(ds) }
