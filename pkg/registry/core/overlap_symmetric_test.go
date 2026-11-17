package core

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/vector"
)

// passageIndex is a self-embedding managed backend fake. PutText embeds a
// projection with a word table and stores the passage vector; Query answers a
// vector query with the cosine distance to each stored row, which is what a
// managed index created with the cosine metric reports. QueryText embeds its
// input as a query, which an asymmetric model such as multilingual-e5-large
// places away from the passage of the same text, so a text query neither
// scores identical texts at 1 nor scores a pair the same in both directions.
type passageIndex struct {
	mem       *vector.Memory
	words     map[string][]float32
	textCalls int
	vecCalls  int
}

func newPassageIndex() *passageIndex {
	return &passageIndex{mem: vector.NewMemory(4), words: map[string][]float32{
		"alpha":   {1, 0, 0, 0},
		"bravo":   {0.99, 0.12, 0, 0},
		"charlie": {0.45, 0.89, 0, 0},
	}}
}

func (p *passageIndex) passage(text string) []float32 {
	for w, v := range p.words {
		if strings.Contains(text, w) {
			return v
		}
	}
	return []float32{0, 0, 0, 1}
}

// queryEmbedding tilts the passage vector toward an axis the passages do not
// use, by an amount that depends on the vector, so the query of a text and
// its passage differ and the tilt differs per text.
func (p *passageIndex) queryEmbedding(text string) []float32 {
	v := append([]float32(nil), p.passage(text)...)
	v[3] += 0.3 + 0.2*v[1]
	return v
}

func (*passageIndex) ID() string          { return "pinecone" }
func (*passageIndex) Dimensions() int     { return 0 }
func (*passageIndex) Close() error        { return nil }
func (*passageIndex) SelfEmbeds() bool    { return true }
func (*passageIndex) HostedModel() string { return "multilingual-e5-large" }
func (p *passageIndex) Put(ctx context.Context, t, id, ver string, v []float32) error {
	return p.mem.Put(ctx, t, id, ver, v)
}
func (p *passageIndex) Delete(ctx context.Context, t, id, ver string) error {
	return p.mem.Delete(ctx, t, id, ver)
}
func (p *passageIndex) PutText(ctx context.Context, t, id, ver, text string) error {
	return p.mem.Put(ctx, t, id, ver, p.passage(text))
}
func (p *passageIndex) Query(ctx context.Context, t string, v []float32, k int) ([]vector.Match, error) {
	p.vecCalls++
	return p.mem.Query(ctx, t, v, k)
}
func (p *passageIndex) QueryText(ctx context.Context, t, text string, k int) ([]vector.Match, error) {
	p.textCalls++
	return p.mem.Query(ctx, t, p.queryEmbedding(text), k)
}

// readablePassageIndex is passageIndex with the stored-vector read, which
// Pinecone, Qdrant, and Weaviate implement for a self-embedding index.
type readablePassageIndex struct{ *passageIndex }

func (p readablePassageIndex) StoredVector(ctx context.Context, t, id, ver string) ([]float32, string, error) {
	return p.mem.StoredVector(ctx, t, id, ver)
}

// selfEmbedFixture stores a, a2 (the same text as a), and b on idx through
// the registry's self-embedding write path and computes all three.
func selfEmbedFixture(t *testing.T, idx vector.Provider) map[string]float64 {
	t.Helper()
	f := newOverlapFixture(t, []layer.Layer{publicLayer("L")})
	f.reg.WithVectorSearch(idx, nil)
	for id, desc := range map[string]string{"a": "alpha", "a2": "alpha", "b": "bravo"} {
		f.put(id, "1.0.0", "L", desc, "", true)
	}
	keys := []vector.RowKey{{ArtifactID: "a", Version: "1.0.0"}, {ArtifactID: "a2", Version: "1.0.0"}, {ArtifactID: "b", Version: "1.0.0"}}
	if res, err := f.reg.ComputeOverlap(f.ctx, keys); err != nil || res.Computed != 3 {
		t.Fatalf("ComputeOverlap = %+v, %v; want 3 computed", res, err)
	}
	sims := map[string]float64{}
	for _, r := range f.records() {
		sims[r.Owner+">"+r.Peer] = r.Similarity
		if r.ModelID != "multilingual-e5-large" {
			t.Errorf("record %s>%s carries model %q, want the hosted model", r.Owner, r.Peer, r.ModelID)
		}
	}
	return sims
}

// Spec: §7.3.1 — a self-embedding backend that returns stored vectors is
// queried with A's stored vector, so a pair scores the same from either side
// and two identical texts score 1. A self-embedding backend that cannot
// return a stored vector is queried with the projection text.
func TestComputeOverlap_SelfEmbeddingStoredVectorIsSymmetric(t *testing.T) {
	t.Parallel()
	readable := readablePassageIndex{newPassageIndex()}
	sims := selfEmbedFixture(t, readable)
	if readable.textCalls != 0 || readable.vecCalls == 0 {
		t.Errorf("readable backend: %d text queries and %d vector queries, want vector queries only", readable.textCalls, readable.vecCalls)
	}
	if math.Abs(sims["a>b"]-sims["b>a"]) > 1e-6 {
		t.Errorf("a>b = %v and b>a = %v, want equal", sims["a>b"], sims["b>a"])
	}
	if sims["a>a2"] != 1 || sims["a2>a"] != 1 {
		t.Errorf("identical texts scored a>a2 = %v and a2>a = %v, want 1", sims["a>a2"], sims["a2>a"])
	}
	if want := cosine(overlapVectors["alpha"], overlapVectors["bravo"]); math.Abs(sims["a>b"]-want) > 1e-6 {
		t.Errorf("a>b = %v, want the cosine of the stored vectors %v", sims["a>b"], want)
	}

	textOnly := newPassageIndex()
	textSims := selfEmbedFixture(t, textOnly)
	if textOnly.textCalls == 0 || textOnly.vecCalls != 0 {
		t.Errorf("text-only backend: %d text queries and %d vector queries, want text queries only", textOnly.textCalls, textOnly.vecCalls)
	}
	if textSims["a>a2"] >= 1 {
		t.Errorf("the text fallback scored identical texts %v; the fake's query embedding should keep it below 1", textSims["a>a2"])
	}
}
