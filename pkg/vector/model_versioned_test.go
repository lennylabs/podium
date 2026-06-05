package vector_test

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/pkg/vector"
)

// mvBackends returns the §4.7 model-versioned backends to run the suite
// against. Pinecone/Weaviate/Qdrant are managed and not model-versioned.
func mvBackends(t *testing.T) map[string]vector.ModelVersioned {
	t.Helper()
	sv, err := vector.OpenSQLiteVec(vector.SQLiteVecConfig{Dimensions: 4})
	if err != nil {
		t.Fatalf("OpenSQLiteVec: %v", err)
	}
	t.Cleanup(func() { _ = sv.Close() })
	return map[string]vector.ModelVersioned{
		"memory":     vector.NewMemory(4),
		"sqlite-vec": sv,
	}
}

func vec4(a, b, c, d float32) []float32 { return []float32{a, b, c, d} }

func ids(ms []vector.Match) map[string]bool {
	out := map[string]bool{}
	for _, m := range ms {
		out[m.ArtifactID] = true
	}
	return out
}

// PutModel tags rows; QueryModel restricts to the current model so a stale
// model's rows never score; PurgeModelExcept drops them.
func TestModelVersioned_RestrictAndPurge(t *testing.T) {
	for name, mv := range mvBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			if err := mv.PutModel(ctx, "default", "a/1", "1.0.0", vec4(1, 0, 0, 0), "model-A"); err != nil {
				t.Fatalf("put A: %v", err)
			}
			if err := mv.PutModel(ctx, "default", "a/2", "1.0.0", vec4(0, 1, 0, 0), "model-A"); err != nil {
				t.Fatalf("put A2: %v", err)
			}
			// A query restricted to model-A sees both.
			got, err := mv.QueryModel(ctx, "default", vec4(1, 0, 0, 0), 10, "model-A")
			if err != nil {
				t.Fatalf("query A: %v", err)
			}
			if g := ids(got); !g["a/1"] || !g["a/2"] {
				t.Fatalf("model-A query = %v, want a/1 and a/2", g)
			}

			// Re-tag a/1 under a new model. A query restricted to model-B must
			// not return the still-model-A a/2.
			if err := mv.PutModel(ctx, "default", "a/1", "1.0.0", vec4(1, 0, 0, 0), "model-B"); err != nil {
				t.Fatalf("put B: %v", err)
			}
			got, _ = mv.QueryModel(ctx, "default", vec4(1, 0, 0, 0), 10, "model-B")
			if g := ids(got); !g["a/1"] || g["a/2"] {
				t.Fatalf("model-B query = %v, want a/1 only (a/2 is stale model-A)", g)
			}

			// Purge everything not on model-B; a/2 (model-A) is removed.
			n, err := mv.PurgeModelExcept(ctx, "default", "model-B")
			if err != nil {
				t.Fatalf("purge: %v", err)
			}
			if n != 1 {
				t.Errorf("purged %d rows, want 1", n)
			}
			got, _ = mv.QueryModel(ctx, "default", vec4(0, 1, 0, 0), 10, "model-B")
			if ids(got)["a/2"] {
				t.Error("a/2 survived the purge")
			}
		})
	}
}

// A row written with no model (legacy / plain Put) still surfaces under a
// model-restricted query, so an upgrade with no model change keeps serving.
func TestModelVersioned_LegacyUntaggedRowsServed(t *testing.T) {
	for name, mv := range mvBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			// Plain Put (via the Provider) leaves model_id empty.
			p := mv.(vector.Provider)
			if err := p.Put(ctx, "default", "legacy/1", "1.0.0", vec4(1, 0, 0, 0)); err != nil {
				t.Fatalf("plain put: %v", err)
			}
			got, err := mv.QueryModel(ctx, "default", vec4(1, 0, 0, 0), 10, "model-A")
			if err != nil {
				t.Fatalf("query: %v", err)
			}
			if !ids(got)["legacy/1"] {
				t.Error("legacy untagged row not served under a model-restricted query")
			}
		})
	}
}
