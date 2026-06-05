package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/vector"
)

// stubEmbed is a deterministic embedding.Provider for the overlay
// semantic tests: each text maps to a fixed unit vector keyed by a
// substring, so a query embeds near the matching record.
type stubEmbed struct {
	dim    int
	failOn string // when non-empty, Embed fails for any text containing it
	calls  int
	vecFor func(string) []float32
}

func (s *stubEmbed) ID() string      { return "stub" }
func (s *stubEmbed) Model() string   { return "stub-model" }
func (s *stubEmbed) Dimensions() int { return s.dim }
func (s *stubEmbed) Embed(_ context.Context, texts []string) ([][]float32, error) {
	s.calls++
	out := make([][]float32, len(texts))
	for i, t := range texts {
		if s.failOn != "" && strings.Contains(t, s.failOn) {
			return nil, errors.New("stub embed failure")
		}
		out[i] = s.vecFor(t)
	}
	return out, nil
}

// orthoVec returns a 3-dim basis vector chosen by which keyword the text
// contains, so "variance" and "restart" land on different axes.
func orthoVec(text string) []float32 {
	switch {
	case strings.Contains(text, "variance"):
		return []float32{1, 0, 0}
	case strings.Contains(text, "restart"):
		return []float32{0, 1, 0}
	default:
		return []float32{0, 0, 1}
	}
}

func semanticRecords() []filesystem.ArtifactRecord {
	return []filesystem.ArtifactRecord{
		{ID: "team/finance/variance-analysis", Artifact: &manifest.Artifact{
			Type: manifest.TypeSkill, Name: "variance-analysis",
			Description: "compute variance against last quarter", Version: "1.0.0"}},
		{ID: "team/ops/restart-runner", Artifact: &manifest.Artifact{
			Type: manifest.TypeCommand, Name: "restart-runner",
			Description: "restart the build runner", Version: "1.0.0"}},
	}
}

// Spec: §9.1 LocalSearchProvider — the semantic stream embeds the query
// and returns the nearest overlay record from the vector backend.
func TestLocalSemantic_NearestMatch(t *testing.T) {
	emb := &stubEmbed{dim: 3, vecFor: orthoVec}
	idx := newLocalSemanticIndex(emb, vector.NewMemory(3))
	got := idx.search(context.Background(), semanticRecords(), "quarterly variance report", "", "", nil, 10)
	if len(got) == 0 {
		t.Fatalf("no semantic results")
	}
	if got[0].ID != "team/finance/variance-analysis" {
		t.Errorf("top semantic hit = %q, want variance-analysis", got[0].ID)
	}
}

// Spec: §9.1 — the index builds once and is reused across queries.
func TestLocalSemantic_BuildsOnce(t *testing.T) {
	emb := &stubEmbed{dim: 3, vecFor: orthoVec}
	idx := newLocalSemanticIndex(emb, vector.NewMemory(3))
	recs := semanticRecords()
	idx.search(context.Background(), recs, "variance", "", "", nil, 10)
	afterFirst := emb.calls
	idx.search(context.Background(), recs, "restart", "", "", nil, 10)
	// One build call (batch) plus one query call per search; build must
	// not run again on the second search.
	if emb.calls != afterFirst+1 {
		t.Errorf("embed calls = %d, want %d (build memoized, only the query re-embeds)", emb.calls, afterFirst+1)
	}
}

// Spec: §9.1 — the type / scope filters apply to the semantic stream too.
func TestLocalSemantic_HonorsFilters(t *testing.T) {
	emb := &stubEmbed{dim: 3, vecFor: orthoVec}
	idx := newLocalSemanticIndex(emb, vector.NewMemory(3))
	got := idx.search(context.Background(), semanticRecords(), "restart the runner", string(manifest.TypeSkill), "", nil, 10)
	for _, r := range got {
		if r.Type != string(manifest.TypeSkill) {
			t.Errorf("type filter leaked %q (%s)", r.ID, r.Type)
		}
	}
}

// Spec: §9.1 — a backend error degrades the stream to empty so the caller
// keeps the BM25 results (the row's default).
func TestLocalSemantic_DegradesOnEmbedError(t *testing.T) {
	emb := &stubEmbed{dim: 3, failOn: "variance", vecFor: orthoVec}
	idx := newLocalSemanticIndex(emb, vector.NewMemory(3))
	if got := idx.search(context.Background(), semanticRecords(), "variance", "", "", nil, 10); got != nil {
		t.Errorf("expected nil (degraded) on embed error, got %+v", got)
	}
}

// Spec: §9.1 — a nil index (no overlay backend configured) and an empty
// query both yield no semantic results.
func TestLocalSemantic_NilAndEmptyQuery(t *testing.T) {
	var idx *localSemanticIndex
	if got := idx.search(context.Background(), semanticRecords(), "variance", "", "", nil, 10); got != nil {
		t.Errorf("nil index → %+v, want nil", got)
	}
	live := newLocalSemanticIndex(&stubEmbed{dim: 3, vecFor: orthoVec}, vector.NewMemory(3))
	if got := live.search(context.Background(), semanticRecords(), "  ", "", "", nil, 10); got != nil {
		t.Errorf("empty query → %+v, want nil", got)
	}
}

// Spec: §9.1 — buildLocalSemantic returns no index when no overlay backend
// or embedding provider is configured, leaving the overlay BM25-only.
// spec: §6.4.1 — the overlay vector backend reaches the same set
// the registry bootstrap does: sqlite-vec, a local pgvector, or a managed
// service (Pinecone, Weaviate Cloud, Qdrant Cloud). Before the fix the
// overlay switch handled only memory and sqlite-vec and returned
// "unsupported overlay vector backend" for the others.
func TestMCPOverlayVectorBackend_ReachesAllDocumentedBackends(t *testing.T) {
	cases := []struct {
		name       string
		id         string
		env        map[string]string
		wantID     string
		wantErrSub string
	}{
		{name: "memory", id: "memory", wantID: "memory"},
		{name: "sqlite-vec", id: "sqlite-vec", wantID: "sqlite-vec"},
		{
			name: "pgvector-missing-dsn", id: "pgvector",
			// Reachable: the error names the DSN env vars rather than
			// "unsupported overlay vector backend".
			wantErrSub: "DSN",
		},
		{
			name:   "pinecone",
			id:     "pinecone",
			env:    map[string]string{"PODIUM_PINECONE_API_KEY": "k", "PODIUM_PINECONE_HOST": "https://h"},
			wantID: "pinecone",
		},
		{
			name:   "weaviate-cloud",
			id:     "weaviate-cloud",
			env:    map[string]string{"PODIUM_WEAVIATE_URL": "https://w", "PODIUM_WEAVIATE_COLLECTION": "Podium"},
			wantID: "weaviate-cloud",
		},
		{
			name:   "qdrant-cloud",
			id:     "qdrant-cloud",
			env:    map[string]string{"PODIUM_QDRANT_URL": "https://q", "PODIUM_QDRANT_COLLECTION": "podium"},
			wantID: "qdrant-cloud",
		},
		{name: "unknown", id: "bogus", wantErrSub: "unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Scrub ambient pgvector DSNs (set by `make test-live` / the live
			// harness) so the missing-dsn case is hermetic; a case that needs a
			// backend supplies it via c.env, applied after this clear.
			for _, k := range []string{"PODIUM_POSTGRES_DSN", "PODIUM_POSTGRES_DSN_VECTOR", "PODIUM_PGVECTOR_DSN"} {
				t.Setenv(k, "")
			}
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			got, err := mcpOverlayVectorBackend(c.id, 8)
			if err != nil && strings.Contains(err.Error(), "unsupported overlay vector backend") {
				t.Fatalf("backend %q still unsupported: %v", c.id, err)
			}
			if c.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErrSub) {
					t.Fatalf("err = %v, want substring %q", err, c.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err = %v", err)
			}
			if got == nil || got.ID() != c.wantID {
				t.Fatalf("provider = %v, want id %q", got, c.wantID)
			}
			// Release the backend's resources. The sqlite-vec provider opens a
			// *sql.DB whose connectionOpener goroutine runs until Close, so a
			// test that constructs it must close it or leak that goroutine.
			t.Cleanup(func() { _ = got.Close() })
		})
	}
}

func TestBuildLocalSemantic_DisabledByDefault(t *testing.T) {
	for _, c := range []config{
		{},
		{localVectorBackend: "memory"},
		{localEmbeddingProvider: "ollama"},
		{localVectorBackend: "none", localEmbeddingProvider: "ollama"},
	} {
		idx, err := buildLocalSemantic(&c)
		if err != nil {
			t.Errorf("buildLocalSemantic(%+v) err = %v", c, err)
		}
		if idx != nil {
			t.Errorf("buildLocalSemantic(%+v) = non-nil, want disabled", c)
		}
	}
}
