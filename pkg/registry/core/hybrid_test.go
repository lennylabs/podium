package core_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/podium/pkg/embedding"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

// fakeEmbedder produces deterministic vectors based on the input
// text length and prefix. Tests use it to drive vector ranking
// without touching real APIs.
type fakeEmbedder struct {
	dim int
	err error
}

func (fakeEmbedder) ID() string        { return "fake" }
func (e fakeEmbedder) Model() string   { return "fake-model" }
func (e fakeEmbedder) Dimensions() int { return e.dim }
func (e fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	if e.err != nil {
		return nil, e.err
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, e.dim)
		for j := range v {
			if j < len(t) {
				v[j] = float32(t[j]) / 256.0
			}
		}
		out[i] = v
	}
	return out, nil
}

// Spec: §4.7 — when both vector store and embedder are configured,
// SearchArtifacts blends BM25 with vector cosine via RRF. The
// SearchResult.Degraded flag is false for the hybrid path.
func TestSearchArtifacts_HybridFusionEngages(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	dim := 16
	for _, m := range []store.ManifestRecord{
		{TenantID: "t", ArtifactID: "alpha", Version: "1.0.0", ContentHash: "sha256:1", Type: "skill", Description: "alpha skill", Layer: "L"},
		{TenantID: "t", ArtifactID: "beta", Version: "1.0.0", ContentHash: "sha256:2", Type: "skill", Description: "beta skill", Layer: "L"},
		{TenantID: "t", ArtifactID: "gamma", Version: "1.0.0", ContentHash: "sha256:3", Type: "skill", Description: "gamma skill", Layer: "L"},
	} {
		_ = st.PutManifest(context.Background(), m)
	}
	v := vector.NewMemory(dim)
	e := fakeEmbedder{dim: dim}
	for _, m := range []struct{ id, body string }{
		{"alpha", "alpha skill"}, {"beta", "beta skill"}, {"gamma", "gamma skill"},
	} {
		vecs, _ := e.Embed(context.Background(), []string{m.body})
		_ = v.Put(context.Background(), "t", m.id, "1.0.0", vecs[0])
	}
	reg := core.New(st, "t", []layer.Layer{{ID: "L", Visibility: layer.Visibility{Public: true}}}).
		WithVectorSearch(v, e)
	res, err := reg.SearchArtifacts(context.Background(), publicID, core.SearchArtifactsOptions{
		Query: "alpha skill", TopK: 5,
	})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	if res.Degraded {
		t.Errorf("Degraded = true; expected hybrid path")
	}
	if len(res.Results) == 0 || res.Results[0].ID != "alpha" {
		t.Errorf("top1 = %v, want alpha", res.Results)
	}
}

// Spec: §4.7 — when no vector store is configured, search degrades
// to BM25-only and SearchResult.Degraded surfaces the reduced
// fidelity.
func TestSearchArtifacts_DegradesWithoutVectorStore(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	_ = st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: "t", ArtifactID: "x", Version: "1.0.0",
		ContentHash: "sha256:x", Type: "skill", Description: "x", Layer: "L",
	})
	reg := core.New(st, "t", []layer.Layer{{ID: "L", Visibility: layer.Visibility{Public: true}}})
	res, err := reg.SearchArtifacts(context.Background(), publicID, core.SearchArtifactsOptions{
		Query: "x", TopK: 5,
	})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	if !res.Degraded {
		t.Errorf("Degraded = false; expected true (no vector store)")
	}
}

// Spec: §4.7 — when the embedder fails, search degrades to BM25 and
// records Degraded=true rather than erroring out. The artifact data
// stays accessible.
func TestSearchArtifacts_DegradesOnEmbedderFailure(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	_ = st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: "t", ArtifactID: "x", Version: "1.0.0",
		ContentHash: "sha256:x", Type: "skill", Description: "x", Layer: "L",
	})
	reg := core.New(st, "t", []layer.Layer{{ID: "L", Visibility: layer.Visibility{Public: true}}}).
		WithVectorSearch(vector.NewMemory(16), fakeEmbedder{dim: 16, err: errors.New("offline")})
	res, err := reg.SearchArtifacts(context.Background(), publicID, core.SearchArtifactsOptions{
		Query: "x", TopK: 5,
	})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	if !res.Degraded {
		t.Errorf("Degraded = false; expected true (embedder offline)")
	}
	if len(res.Results) == 0 {
		t.Errorf("BM25 fallback returned no results")
	}
}

// Spec: §4.7 — Reembed walks every visible manifest, embeds, and
// upserts. Returns counts so admin CLI can render a summary.
func TestReembed_AllManifests(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	for _, id := range []string{"a", "b", "c"} {
		_ = st.PutManifest(context.Background(), store.ManifestRecord{
			TenantID: "t", ArtifactID: id, Version: "1.0.0",
			ContentHash: "sha256:" + id, Type: "skill", Description: id, Layer: "L",
		})
	}
	v := vector.NewMemory(16)
	e := fakeEmbedder{dim: 16}
	reg := core.New(st, "t", []layer.Layer{{ID: "L", Visibility: layer.Visibility{Public: true}}}).
		WithVectorSearch(v, e)
	r, err := reg.Reembed(context.Background(), false)
	if err != nil {
		t.Fatalf("Reembed: %v", err)
	}
	if r.Succeeded != 3 || r.Total != 3 {
		t.Errorf("got %+v, want all 3 succeeded", r)
	}
	if len(r.Failed) != 0 {
		t.Errorf("Failed = %v, want empty", r.Failed)
	}
}

var _ = embedding.Provider(fakeEmbedder{})
