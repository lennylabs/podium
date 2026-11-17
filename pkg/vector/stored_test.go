package vector_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/vector"
)

// exactCosine is the reference cosine similarity the backends are checked
// against.
func exactCosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// collocatedScorer is a collocated backend under test: it stores vectors and
// scores them by exact cosine similarity.
type collocatedScorer interface {
	vector.ModelVersioned
	vector.VectorReader
	vector.CosineScorer
}

// checkStoredAndCosine stores two rows and asserts StoredVector returns the
// stored vector and model, a missing row is vector.ErrNotFound, and
// CosineSimilarities reports the exact cosine similarity, skipping a row the
// tenant does not hold.
func checkStoredAndCosine(t *testing.T, b collocatedScorer) {
	t.Helper()
	ctx := context.Background()
	a := []float32{1, 0, 0, 0}
	// far is close to a by cosine and far by L2: a long vector at a small
	// angle. near is close by L2 and far by cosine.
	far := []float32{10, 3, 0, 0}
	near := []float32{0.6, 0.6, 0, 0}
	for id, v := range map[string][]float32{"a": a, "far": far, "near": near} {
		if err := b.PutModel(ctx, "t", id, "1", v, "m1"); err != nil {
			t.Fatalf("PutModel %s: %v", id, err)
		}
	}
	got, model, err := b.StoredVector(ctx, "t", "far", "1")
	if err != nil || model != "m1" || len(got) != 4 || got[0] != 10 || got[1] != 3 {
		t.Fatalf("StoredVector = %v, %q, %v", got, model, err)
	}
	if _, _, err := b.StoredVector(ctx, "t", "missing", "1"); !errors.Is(err, vector.ErrNotFound) {
		t.Errorf("StoredVector of a missing row = %v, want ErrNotFound", err)
	}
	sims, err := b.CosineSimilarities(ctx, "t", a, []vector.RowKey{
		{ArtifactID: "far", Version: "1"}, {ArtifactID: "near", Version: "1"}, {ArtifactID: "missing", Version: "1"},
	})
	if err != nil {
		t.Fatalf("CosineSimilarities: %v", err)
	}
	if len(sims) != 2 {
		t.Fatalf("CosineSimilarities = %v, want two rows (the missing row is absent)", sims)
	}
	for id, v := range map[string][]float32{"far": far, "near": near} {
		if got, want := sims[vector.RowKey{ArtifactID: id, Version: "1"}], exactCosine(a, v); math.Abs(got-want) > 1e-5 {
			t.Errorf("cosine(a, %s) = %v, want %v", id, got, want)
		}
	}
}

// Spec: §7.3.1 — the memory backend returns its stored vector and scores
// rows by exact cosine similarity.
func TestMemory_StoredVectorAndCosine(t *testing.T) {
	t.Parallel()
	checkStoredAndCosine(t, vector.NewMemory(4))
}

// Spec: §7.3.1 — sqlite-vec's vec0 table orders and reports L2 distance, so
// one minus its reported distance is not a cosine similarity; the backend
// computes the exact cosine similarity of the candidate rows instead.
func TestSQLiteVec_CosineDiffersFromL2Distance(t *testing.T) {
	t.Parallel()
	v, err := vector.OpenSQLiteVec(vector.SQLiteVecConfig{Dimensions: 4})
	if err != nil {
		t.Fatalf("OpenSQLiteVec: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })
	checkStoredAndCosine(t, v)

	ctx := context.Background()
	matches, err := v.QueryModel(ctx, "t", []float32{1, 0, 0, 0}, 3, "m1")
	if err != nil {
		t.Fatalf("QueryModel: %v", err)
	}
	var farDistance float32 = -1
	for _, m := range matches {
		if m.ArtifactID == "far" {
			farDistance = m.Distance
		}
	}
	// The L2 distance of far from a is about 9.5, so 1 - distance is far
	// below zero while the exact cosine similarity is about 0.96.
	if farDistance < 2 {
		t.Fatalf("far distance = %v; the index is expected to report L2 distance", farDistance)
	}
	if _, err := v.CosineSimilarities(ctx, "t", []float32{1, 0}, nil); !errors.Is(err, vector.ErrDimensionMismatch) {
		t.Errorf("CosineSimilarities with a short vector = %v, want ErrDimensionMismatch", err)
	}
}

// Spec: §7.3.1 — pgvector's <=> operator is cosine distance, so the backend
// reports one minus it as the cosine similarity of the stored vectors.
func TestPgVector_StoredVectorAndCosine(t *testing.T) {
	scope := openPgSchema(t, 4)
	t.Cleanup(scope.close)
	checkStoredAndCosine(t, scope.pg)
	if _, err := scope.pg.CosineSimilarities(context.Background(), "t", []float32{1}, nil); !errors.Is(err, vector.ErrDimensionMismatch) {
		t.Errorf("CosineSimilarities with a short vector = %v, want ErrDimensionMismatch", err)
	}
}

// managedFake serves the one stored-vector read of a managed backend. found
// selects whether the row exists.
func managedFake(t *testing.T, found bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vec := []float32{0.1, 0.2, 0.3, 0.4}
		switch {
		case r.URL.Path == "/vectors/fetch":
			id := r.URL.Query().Get("ids")
			vectors := map[string]any{}
			if found {
				vectors[id] = map[string]any{"values": vec}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"vectors": vectors})
		case strings.HasSuffix(r.URL.Path, "/points"):
			result := []any{}
			if found {
				result = append(result, map[string]any{"vector": vec})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"result": result})
		case strings.HasPrefix(r.URL.Path, "/v1/objects/"):
			if !found {
				http.Error(w, "missing", http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"vector": vec})
		default:
			http.Error(w, "unhandled path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Spec: §7.3.1 — a managed backend returns the vector it stores for a row,
// records no model per row, and reports a missing row as ErrNotFound; a
// self-embedding backend names its hosted model.
func TestManaged_StoredVectorAndHostedModel(t *testing.T) {
	t.Parallel()
	for _, found := range []bool{true, false} {
		srv := managedFake(t, found)
		pc, err := vector.NewPinecone(vector.PineconeConfig{APIKey: "k", Host: srv.URL, Dimensions: 4})
		if err != nil {
			t.Fatalf("NewPinecone: %v", err)
		}
		pc.Client = srv.Client()
		qd, err := vector.NewQdrant(vector.QdrantConfig{URL: srv.URL, Collection: "c", Dimensions: 4})
		if err != nil {
			t.Fatalf("NewQdrant: %v", err)
		}
		qd.Client = srv.Client()
		wv, err := vector.NewWeaviate(vector.WeaviateConfig{URL: srv.URL, Collection: "C", Dimensions: 4})
		if err != nil {
			t.Fatalf("NewWeaviate: %v", err)
		}
		wv.Client = srv.Client()
		for name, r := range map[string]vector.VectorReader{"pinecone": pc, "qdrant": qd, "weaviate": wv} {
			vec, model, err := r.StoredVector(context.Background(), "t", "a", "1.0.0")
			switch {
			case found && (err != nil || len(vec) != 4 || model != ""):
				t.Errorf("%s StoredVector = %v, %q, %v; want the vector and no model", name, vec, model, err)
			case !found && !errors.Is(err, vector.ErrNotFound):
				t.Errorf("%s StoredVector of a missing row = %v, want ErrNotFound", name, err)
			}
		}
	}
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/objects/") {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("not json"))
	}))
	t.Cleanup(broken.Close)
	bpc, _ := vector.NewPinecone(vector.PineconeConfig{APIKey: "k", Host: broken.URL, Dimensions: 4})
	bqd, _ := vector.NewQdrant(vector.QdrantConfig{URL: broken.URL, Collection: "c", Dimensions: 4})
	bwv, _ := vector.NewWeaviate(vector.WeaviateConfig{URL: broken.URL, Collection: "C", Dimensions: 4})
	for name, r := range map[string]vector.VectorReader{"pinecone": bpc, "qdrant": bqd, "weaviate": bwv} {
		if _, _, err := r.StoredVector(context.Background(), "t", "a", "1.0.0"); err == nil || errors.Is(err, vector.ErrNotFound) {
			t.Errorf("%s StoredVector from a broken backend = %v, want a read error", name, err)
		}
	}
	pc, _ := vector.NewPinecone(vector.PineconeConfig{APIKey: "k", Host: "http://x", InferenceModel: "llama-text-embed-v2"})
	qd, _ := vector.NewQdrant(vector.QdrantConfig{URL: "http://x", Collection: "c", InferenceModel: "bm25"})
	wv, _ := vector.NewWeaviate(vector.WeaviateConfig{URL: "http://x", Collection: "C", Vectorizer: "text2vec-weaviate"})
	got := []string{pc.HostedModel(), qd.HostedModel(), wv.HostedModel()}
	if strings.Join(got, ",") != "llama-text-embed-v2,bm25,text2vec-weaviate" {
		t.Errorf("hosted models = %v", got)
	}
}
