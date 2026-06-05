package core_test

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

// selfEmbedVec is a fake §13.12 self-embedding backend. It stores
// raw text rather than vectors and answers QueryText, so a Registry wired with
// a nil embedder still runs the vector path. The precomputed-vector Put/Query
// methods panic so a test catches the registry taking the wrong path.
type selfEmbedVec struct {
	rows    map[string]string // "id@ver" -> text
	queries []string
}

type seRow struct{ id, ver, text string }

func newSelfEmbedVec() *selfEmbedVec { return &selfEmbedVec{rows: map[string]string{}} }

func (*selfEmbedVec) ID() string       { return "pinecone" }
func (*selfEmbedVec) Dimensions() int  { return 0 }
func (*selfEmbedVec) SelfEmbeds() bool { return true }

func (s *selfEmbedVec) PutText(_ context.Context, _ /*tenant*/, id, ver, text string) error {
	s.rows[id+"@"+ver] = text
	return nil
}

func (s *selfEmbedVec) QueryText(_ context.Context, _ /*tenant*/, text string, topK int) ([]vector.Match, error) {
	s.queries = append(s.queries, text)
	out := make([]vector.Match, 0, len(s.rows))
	for _, r := range s.list() {
		out = append(out, vector.Match{ArtifactID: r.id, Version: r.ver, Distance: 0.01})
		if len(out) >= topK {
			break
		}
	}
	return out, nil
}

func (s *selfEmbedVec) list() []seRow {
	out := make([]seRow, 0, len(s.rows))
	for k, text := range s.rows {
		at := len(k) - 1
		for at >= 0 && k[at] != '@' {
			at--
		}
		out = append(out, seRow{id: k[:at], ver: k[at+1:], text: text})
	}
	return out
}

func (*selfEmbedVec) Put(context.Context, string, string, string, []float32) error {
	panic("Put called on a self-embedding backend; expected PutText")
}
func (*selfEmbedVec) Query(context.Context, string, []float32, int) ([]vector.Match, error) {
	panic("Query called on a self-embedding backend; expected QueryText")
}
func (*selfEmbedVec) Delete(context.Context, string, string, string) error { return nil }
func (*selfEmbedVec) Close() error                                         { return nil }

// compile-time assertion that the fake satisfies both contracts.
var (
	_ vector.Provider       = (*selfEmbedVec)(nil)
	_ vector.TextVectorizer = (*selfEmbedVec)(nil)
)

func seManifestStore(t *testing.T, ids ...string) store.Store {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	for _, id := range ids {
		if err := st.PutManifest(context.Background(), store.ManifestRecord{
			TenantID: "t", ArtifactID: id, Version: "1.0.0",
			ContentHash: "sha256:" + id, Type: "skill", Name: id, Description: id + " skill", Layer: "L",
		}); err != nil {
			t.Fatalf("PutManifest: %v", err)
		}
	}
	return st
}

// Spec: §13.12 — a self-embedding vector backend with no separate
// embedding provider runs the full vector path: Reembed upserts raw text via
// PutText and SearchArtifacts queries via QueryText, so search is NOT degraded.
func TestSelfEmbedding_ReembedAndSearch(t *testing.T) {
	t.Parallel()
	st := seManifestStore(t, "alpha", "beta", "gamma")
	v := newSelfEmbedVec()
	reg := core.New(st, "t", []layer.Layer{{ID: "L", Visibility: layer.Visibility{Public: true}}}).
		WithVectorSearch(v, nil) // nil embedder: the backend self-embeds

	r, err := reg.Reembed(context.Background(), core.ReembedOptions{})
	if err != nil {
		t.Fatalf("Reembed: %v", err)
	}
	if r.Succeeded != 3 || len(r.Failed) != 0 {
		t.Fatalf("Reembed = %+v, want 3 succeeded via PutText", r)
	}
	// The composed text (name/description) reached PutText, not a vector.
	if got := v.rows["alpha@1.0.0"]; got == "" {
		t.Errorf("alpha not upserted via PutText; rows=%v", v.rows)
	}

	res, err := reg.SearchArtifacts(context.Background(), publicID, core.SearchArtifactsOptions{
		Query: "alpha skill", TopK: 5,
	})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	if res.Degraded {
		t.Errorf("Degraded = true; a self-embedding backend with a nil embedder must not degrade")
	}
	if len(v.queries) == 0 || v.queries[len(v.queries)-1] != "alpha skill" {
		t.Errorf("QueryText did not receive the raw query text; got %v", v.queries)
	}
	if len(res.Results) == 0 {
		t.Errorf("self-embedding search returned no results")
	}
}

// Spec: §13.12 — the nil-embedder concession is scoped to
// self-embedding backends. A nil embedder against a backend that cannot
// self-embed still degrades to BM25-only.
func TestSelfEmbedding_NilEmbedderNonSelfEmbeddingDegrades(t *testing.T) {
	t.Parallel()
	st := seManifestStore(t, "x")
	reg := core.New(st, "t", []layer.Layer{{ID: "L", Visibility: layer.Visibility{Public: true}}}).
		WithVectorSearch(vector.NewMemory(8), nil) // memory cannot self-embed
	res, err := reg.SearchArtifacts(context.Background(), publicID, core.SearchArtifactsOptions{
		Query: "x", TopK: 5,
	})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	if !res.Degraded {
		t.Errorf("Degraded = false; nil embedder + non-self-embedding backend must degrade")
	}
}

// Spec: §13.12 — Reembed refuses when neither an embedder nor a
// self-embedding backend is configured.
func TestSelfEmbedding_ReembedRequiresVectorPath(t *testing.T) {
	t.Parallel()
	st := seManifestStore(t, "x")
	reg := core.New(st, "t", []layer.Layer{{ID: "L", Visibility: layer.Visibility{Public: true}}}).
		WithVectorSearch(vector.NewMemory(8), nil)
	if _, err := reg.Reembed(context.Background(), core.ReembedOptions{}); err == nil {
		t.Error("Reembed = nil error; want 'vector search not configured' without an embedder or self-embedding backend")
	}
}
