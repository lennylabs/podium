// Package vectortest is the conformance suite every vector.Provider
// implementation must pass. Backends call Suite from their own
// package tests so Memory, PgVector, SQLiteVec, Pinecone, Weaviate,
// and Qdrant share one contract.
package vectortest

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/podium/pkg/vector"
)

// Factory returns a fresh, empty Provider for one sub-test.
type Factory func(t *testing.T) vector.Provider

// Suite runs the full conformance set against the backend the
// factory produces. Each sub-test calls factory(t) to get a clean
// store.
func Suite(t *testing.T, dim int, f Factory) {
	t.Helper()
	t.Run("PutQueryRoundTrip", func(t *testing.T) { putQueryRoundTrip(t, f(t), dim) })
	t.Run("QueryRespectsTenantBoundary", func(t *testing.T) { queryRespectsTenantBoundary(t, f(t), dim) })
	t.Run("UpsertReplacesPriorVector", func(t *testing.T) { upsertReplacesPriorVector(t, f(t), dim) })
	t.Run("DeleteRemovesVector", func(t *testing.T) { deleteRemovesVector(t, f(t), dim) })
	t.Run("DimensionMismatchRejected", func(t *testing.T) { dimensionMismatchRejected(t, f(t), dim) })
	t.Run("EmptyTenantRejected", func(t *testing.T) { emptyTenantRejected(t, f(t), dim) })
	t.Run("TopKBoundedByStoreSize", func(t *testing.T) { topKBoundedByStoreSize(t, f(t), dim) })
}

func putQueryRoundTrip(t *testing.T, p vector.Provider, dim int) {
	t.Helper()
	ctx := context.Background()
	v1 := unit(dim, 0)
	if err := p.Put(ctx, "t1", "a", "1.0.0", v1); err != nil {
		t.Fatalf("Put: %v", err)
	}
	matches, err := p.Query(ctx, "t1", v1, 5)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(matches) != 1 || matches[0].ArtifactID != "a" {
		t.Errorf("got %v, want one match for a", matches)
	}
	if matches[0].Distance > 0.001 {
		t.Errorf("self-cosine distance = %v, want ~0", matches[0].Distance)
	}
}

func queryRespectsTenantBoundary(t *testing.T, p vector.Provider, dim int) {
	t.Helper()
	ctx := context.Background()
	v := unit(dim, 0)
	_ = p.Put(ctx, "t1", "a", "1.0.0", v)
	_ = p.Put(ctx, "t2", "b", "1.0.0", v)
	got, err := p.Query(ctx, "t1", v, 5)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	for _, m := range got {
		if m.ArtifactID != "a" {
			t.Errorf("tenant leak: %v", m)
		}
	}
}

func upsertReplacesPriorVector(t *testing.T, p vector.Provider, dim int) {
	t.Helper()
	ctx := context.Background()
	v1, v2 := unit(dim, 0), unit(dim, 1)
	if err := p.Put(ctx, "t1", "a", "1.0.0", v1); err != nil {
		t.Fatalf("Put #1: %v", err)
	}
	if err := p.Put(ctx, "t1", "a", "1.0.0", v2); err != nil {
		t.Fatalf("Put #2 (upsert): %v", err)
	}
	matches, err := p.Query(ctx, "t1", v2, 5)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	// After upsert, querying with v2 should return the same artifact
	// at near-zero distance.
	if len(matches) == 0 || matches[0].ArtifactID != "a" {
		t.Fatalf("upsert not visible: %v", matches)
	}
	if matches[0].Distance > 0.001 {
		t.Errorf("post-upsert distance = %v, want ~0", matches[0].Distance)
	}
}

func deleteRemovesVector(t *testing.T, p vector.Provider, dim int) {
	t.Helper()
	ctx := context.Background()
	v := unit(dim, 0)
	_ = p.Put(ctx, "t1", "a", "1.0.0", v)
	if err := p.Delete(ctx, "t1", "a", "1.0.0"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	matches, _ := p.Query(ctx, "t1", v, 5)
	for _, m := range matches {
		if m.ArtifactID == "a" && m.Version == "1.0.0" {
			t.Errorf("Delete did not remove vector: %v", m)
		}
	}
}

func dimensionMismatchRejected(t *testing.T, p vector.Provider, dim int) {
	t.Helper()
	wrong := make([]float32, dim+1)
	err := p.Put(context.Background(), "t1", "a", "1.0.0", wrong)
	if !errors.Is(err, vector.ErrDimensionMismatch) {
		t.Errorf("Put got %v, want ErrDimensionMismatch", err)
	}
	_, err = p.Query(context.Background(), "t1", wrong, 5)
	if !errors.Is(err, vector.ErrDimensionMismatch) {
		t.Errorf("Query got %v, want ErrDimensionMismatch", err)
	}
}

func emptyTenantRejected(t *testing.T, p vector.Provider, dim int) {
	t.Helper()
	v := unit(dim, 0)
	if err := p.Put(context.Background(), "", "a", "1.0.0", v); !errors.Is(err, vector.ErrInvalidArgument) {
		t.Errorf("Put got %v, want ErrInvalidArgument", err)
	}
}

func topKBoundedByStoreSize(t *testing.T, p vector.Provider, dim int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_ = p.Put(ctx, "t1", artifactID(i), "1.0.0", unit(dim, i))
	}
	matches, err := p.Query(ctx, "t1", unit(dim, 0), 100)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(matches) != 3 {
		t.Errorf("got %d matches, want 3", len(matches))
	}
}

// unit returns a unit vector aligned with axis i.
func unit(dim, axis int) []float32 {
	v := make([]float32, dim)
	if axis < dim {
		v[axis] = 1
	} else {
		v[0] = 1
	}
	return v
}

func artifactID(i int) string {
	return string(rune('a' + i))
}
