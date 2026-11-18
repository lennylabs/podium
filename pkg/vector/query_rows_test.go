package vector_test

import (
	"testing"

	"github.com/lennylabs/podium/pkg/vector"
)

// Spec: §7.3.1 — each backend reports the most rows it accepts in one query,
// so the overlap computation's widening never asks more. The collocated
// memory and pgvector backends accept any topK, sqlite-vec returns at most
// its KNN candidate bound, and the managed backends report 1,000.
func TestMaxQueryRowsOf_PerBackend(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		p    vector.Provider
		want int
	}{
		"memory":     {vector.NewMemory(4), 0},
		"pgvector":   {&vector.PgVector{}, 0},
		"sqlite-vec": {&vector.SQLiteVec{}, 1000},
		"pinecone":   {&vector.Pinecone{}, 1000},
		"qdrant":     {&vector.Qdrant{}, 1000},
		"weaviate":   {&vector.Weaviate{}, 1000},
		"nil":        {nil, 0},
	} {
		if got := vector.MaxQueryRowsOf(tc.p); got != tc.want {
			t.Errorf("%s: MaxQueryRowsOf = %d, want %d", name, got, tc.want)
		}
	}
}
