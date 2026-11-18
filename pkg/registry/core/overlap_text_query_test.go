package core

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

// plainIndex is a vector backend with none of the optional capabilities: the
// embedded interface promotes only the vector.Provider methods, so it is
// neither a vector.VectorReader nor a vector.TextVectorizer.
type plainIndex struct{ vector.Provider }

// Spec: §7.3.1 — with no local embedder, a computation queries a backend that
// cannot return the stored vector with the projection text. A backend that
// can do neither fails the computation with an error naming both, rather than
// panicking on the text-query assertion.
func TestNeighborSourceFor_BackendWithNeitherCapability(t *testing.T) {
	t.Parallel()
	r := New(store.NewMemory(), "", nil).WithVectorSearch(plainIndex{vector.NewMemory(4)}, nil)
	rec := store.ManifestRecord{ArtifactID: "fin/invoices", Version: "1.0.0", Type: "agent", Description: "reconciles invoices"}
	_, err := r.neighborSourceFor(context.Background(), rec, nil)
	if err == nil || !strings.Contains(err.Error(), "cannot return stored vectors and cannot query with text") {
		t.Errorf("neighborSourceFor = %v, want the error naming both missing capabilities", err)
	}
}
