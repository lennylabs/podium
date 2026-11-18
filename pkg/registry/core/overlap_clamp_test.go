package core

import (
	"context"
	"math"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/vector"
)

// overshootScorer is the memory backend whose exact cosine scoring reports
// every row slightly above 1, as float32 rounding does on sqlite-vec.
type overshootScorer struct{ *vector.Memory }

func (o overshootScorer) CosineSimilarities(_ context.Context, _ string, _ []float32, rows []vector.RowKey) (map[vector.RowKey]float64, error) {
	out := make(map[vector.RowKey]float64, len(rows))
	for _, k := range rows {
		out[k] = 1.0000006
	}
	return out, nil
}

// Spec: §7.3.1 — a similarity is limited to the range 0 to 1 wherever it is
// computed or stored: a managed backend's reported distance just below 0 is
// stored as 1, one above 1 is stored as 0, and a collocated backend's cosine
// just above 1 is stored as 1.
func TestComputeOverlap_SimilarityClampedToUnitRange(t *testing.T) {
	t.Parallel()
	managed := newManagedIndex([]vector.Match{
		{ArtifactID: "b", Version: "1.0.0", Distance: -0.0000222},
		{ArtifactID: "c", Version: "1.0.0", Distance: 1.5},
	})
	f := newOverlapFixture(t, []layer.Layer{publicLayer("L")})
	f.put("a", "1.0.0", "L", "alpha", "", false)
	f.put("b", "1.0.0", "L", "bravo", "", false)
	f.put("c", "1.0.0", "L", "charlie", "", false)
	f.reg.WithVectorSearch(managed, overlapEmbedder{model: "m1"})
	if _, err := f.reg.ComputeOverlap(f.ctx, []vector.RowKey{{ArtifactID: "a", Version: "1.0.0"}}); err != nil {
		t.Fatalf("ComputeOverlap: %v", err)
	}
	got := map[string]float64{}
	for _, r := range f.records() {
		got[r.Peer] = r.Similarity
	}
	if got["b"] != 1 || got["c"] != 0 {
		t.Errorf("managed similarities = %v, want b at 1 and c at 0", got)
	}

	g := newOverlapFixture(t, []layer.Layer{publicLayer("L")})
	scorer := overshootScorer{vector.NewMemory(4)}
	g.reg.WithVectorSearch(scorer, overlapEmbedder{table: overlapVectors, model: "m1"})
	g.put("a", "1.0.0", "L", "alpha", "", true)
	g.put("a2", "1.0.0", "L", "alpha", "", true)
	if _, err := g.reg.ComputeOverlap(g.ctx, []vector.RowKey{{ArtifactID: "a", Version: "1.0.0"}}); err != nil {
		t.Fatalf("ComputeOverlap: %v", err)
	}
	if recs := g.records(); len(recs) != 1 || recs[0].Similarity != 1 {
		t.Errorf("collocated records = %+v, want a>a2 at exactly 1", recs)
	}
	for in, want := range map[float64]float64{1.0000222: 1, -0.3: 0, 0.42: 0.42, 1: 1, 0: 0} {
		if got := clampSimilarity(in); got != want {
			t.Errorf("clampSimilarity(%v) = %v, want %v", in, got, want)
		}
	}
	if got := clampSimilarity(math.NaN()); got != 0 {
		t.Errorf("clampSimilarity(NaN) = %v, want 0", got)
	}
}
