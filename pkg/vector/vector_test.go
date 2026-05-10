package vector_test

import (
	"testing"

	"github.com/lennylabs/podium/pkg/vector"
	"github.com/lennylabs/podium/pkg/vector/vectortest"
)

// Spec: §4.7 — the in-memory backend satisfies the SPI conformance
// contract. Other backends pull this same suite into their package
// tests so Memory / PgVector / SQLiteVec / Pinecone / Weaviate /
// Qdrant share one contract.
func TestMemory_Conformance(t *testing.T) {
	t.Parallel()
	vectortest.Suite(t, 8, func(t *testing.T) vector.Provider {
		return vector.NewMemory(8)
	})
}
