package core

import (
	"errors"
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

// Spec: §7.3.1 — the report gives total, the number of artifacts the caller
// can see, and computed, the number of those whose last successful
// computation is for their current served version and the configured model.
// A hidden artifact counts toward neither, a computation of an earlier
// version or of another model is not counted, and both are null when the
// check is off.
func TestCatalogOverlap_ComputedAndTotal(t *testing.T) {
	t.Parallel()
	f := reportFixture(t)
	f.writeOwned("b", "1.0.0", overlapNow, nil)
	f.writeOwned("h", "1.0.0", overlapNow, nil)
	f.writeOwned("s", "0.9.0", overlapNow, nil)
	other := store.OverlapComputation{ArtifactID: "old", Version: "2.0.0", ModelID: "m2", ComputedAt: overlapNow}
	if err := f.st.ReplaceOverlapRecords(f.ctx, "t", other, nil); err != nil {
		t.Fatalf("ReplaceOverlapRecords: %v", err)
	}
	alice := layer.Identity{Sub: "alice", IsAuthenticated: true}
	bob := layer.Identity{Sub: "bob", IsAuthenticated: true, Groups: []string{"eng"}}
	for name, tc := range map[string]struct {
		id              layer.Identity
		computed, total int
	}{
		"alice": {id: alice, computed: 2, total: 4},
		"bob":   {id: bob, computed: 3, total: 5},
	} {
		r, err := f.reg.CatalogOverlap(f.ctx, tc.id, OverlapQuery{ArtifactID: "a"})
		if err != nil {
			t.Fatalf("%s: CatalogOverlap: %v", name, err)
		}
		st := r.SemanticOverlap
		if st.Computed == nil || st.Total == nil || *st.Computed != tc.computed || *st.Total != tc.total {
			t.Errorf("%s: computed/total = %v/%v, want %d/%d", name, st.Computed, st.Total, tc.computed, tc.total)
		}
	}
	f.reg.WithOverlapChecks(lint.Thresholds{}, lint.SemanticConfig{})
	r, err := f.reg.CatalogOverlap(f.ctx, alice, OverlapQuery{})
	if err != nil || r.SemanticOverlap.Computed != nil || r.SemanticOverlap.Total != nil {
		t.Errorf("off: computed/total = %v/%v (%v), want null", r.SemanticOverlap.Computed, r.SemanticOverlap.Total, err)
	}
	failing := &faultyOverlapStore{Memory: f.st, failRuns: true}
	broken := New(failing, "t", []layer.Layer{publicLayer("L")}).WithVectorSearch(f.idx, overlapEmbedder{model: "m1"}).
		WithOverlapChecks(lint.Thresholds{}, lint.SemanticConfig{Enabled: true})
	if _, err := broken.CatalogOverlap(f.ctx, alice, OverlapQuery{}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("CatalogOverlap with a failing computation listing = %v, want ErrUnavailable", err)
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
