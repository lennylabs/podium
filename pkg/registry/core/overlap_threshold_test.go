package core

import (
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

// writeOwnedModel stores one computation of a at 1.0.0 under model with a
// record for each peer at 1.0.0 and the given similarity.
func (f *overlapFixture) writeOwnedModel(model string, peers map[string]float64) {
	f.t.Helper()
	recs := make([]store.OverlapRecord, 0, len(peers))
	for peer, sim := range peers {
		recs = append(recs, store.OverlapRecord{Owner: "a", OwnerVersion: "1.0.0", Peer: peer, PeerVersion: "1.0.0", Similarity: sim, ModelID: model, ComputedAt: overlapNow})
	}
	comp := store.OverlapComputation{ArtifactID: "a", Version: "1.0.0", ModelID: model, ComputedAt: overlapNow}
	if err := f.st.ReplaceOverlapRecords(f.ctx, "t", comp, recs); err != nil {
		f.t.Fatalf("ReplaceOverlapRecords: %v", err)
	}
}

// Spec: §7.3.1, §13.12 — with PODIUM_LINT_SEMANTIC_SIMILARITY unset, the
// report applies the default of the configured model identifier the records
// carry: the embedder's model, an Ollama tag of it included, or the hosted
// model of a self-embedding backend. A peer at the default is reported and
// one just below it is not, and a configured threshold wins over the model's
// default.
func TestCatalogOverlap_ModelAwareDefaultThreshold(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		model    string
		idx      func(*overlapFixture) vector.Provider
		embedder bool
		set      float64
		want     float64
	}{
		{name: "openai", model: "text-embedding-3-small", embedder: true, want: 0.80},
		{name: "ollama tag", model: "nomic-embed-text:latest", embedder: true, want: 0.86},
		{name: "unknown model", model: "voyage-3", embedder: true, want: 0.85},
		{name: "hosted e5", model: "multilingual-e5-large", idx: func(*overlapFixture) vector.Provider { return readablePassageIndex{newPassageIndex()} }, want: 0.90},
		{name: "explicit override", model: "text-embedding-3-small", embedder: true, set: 0.95, want: 0.95},
	}
	for _, tc := range cases {
		f := newOverlapFixture(t, []layer.Layer{publicLayer("L")})
		f.put("a", "1.0.0", "L", "alpha", "", false)
		f.put("at", "1.0.0", "L", "bravo", "", false)
		f.put("below", "1.0.0", "L", "charlie", "", false)
		if tc.embedder {
			f.reg.WithVectorSearch(f.idx, overlapEmbedder{table: overlapVectors, model: tc.model})
		} else {
			f.reg.WithVectorSearch(tc.idx(f), nil)
		}
		f.reg.WithOverlapChecks(lint.Thresholds{}, lint.SemanticConfig{Enabled: true, Similarity: tc.set})
		f.writeOwnedModel(tc.model, map[string]float64{"at": tc.want, "below": tc.want - 0.01})
		r, err := f.reg.CatalogOverlap(f.ctx, layer.Identity{IsPublic: true}, OverlapQuery{})
		if err != nil {
			t.Fatalf("%s: CatalogOverlap: %v", tc.name, err)
		}
		if got := *r.SemanticOverlap.Threshold; got != tc.want {
			t.Errorf("%s: threshold = %v, want %v", tc.name, got, tc.want)
		}
		if peers := peerIDs(semanticOf(r, "a")); peers != "at" {
			t.Errorf("%s: a's peers = %s, want at only", tc.name, peers)
		}
	}
}
