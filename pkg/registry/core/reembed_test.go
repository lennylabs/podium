package core_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

func newReembedHarness(t *testing.T) (*core.Registry, *fakeEmbedderRE, *vector.Memory) {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	for _, m := range []store.ManifestRecord{
		{TenantID: "t", ArtifactID: "a", Version: "1.0.0", Description: "a desc", Body: []byte("body a"), Layer: "L"},
		{TenantID: "t", ArtifactID: "b", Version: "1.0.0", Description: "b desc", Body: []byte("body b"), Layer: "L"},
	} {
		if err := st.PutManifest(context.Background(), m); err != nil {
			t.Fatalf("PutManifest: %v", err)
		}
	}
	e := &fakeEmbedderRE{dim: 8}
	v := vector.NewMemory(8)
	reg := core.New(st, "t", nil).WithVectorSearch(v, e)
	return reg, e, v
}

type fakeEmbedderRE struct {
	dim  int
	err  error
	hits int
}

func (*fakeEmbedderRE) ID() string        { return "fake" }
func (*fakeEmbedderRE) Model() string     { return "fake-model" }
func (e *fakeEmbedderRE) Dimensions() int { return e.dim }
func (e *fakeEmbedderRE) Embed(_ context.Context, texts []string) ([][]float32, error) {
	e.hits++
	if e.err != nil {
		return nil, e.err
	}
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = make([]float32, e.dim)
		out[i][0] = 1
	}
	return out, nil
}

func TestReembed_TwoManifestsAllSucceed(t *testing.T) {
	t.Parallel()
	reg, e, _ := newReembedHarness(t)
	res, err := reg.Reembed(context.Background(), core.ReembedOptions{})
	if err != nil {
		t.Fatalf("Reembed: %v", err)
	}
	if res.Total != 2 || res.Succeeded != 2 {
		t.Errorf("got %+v", res)
	}
	if e.hits == 0 {
		t.Errorf("embedder not called")
	}
}

func TestReembed_OnlyMissingSkipsExisting(t *testing.T) {
	t.Parallel()
	reg, _, v := newReembedHarness(t)
	// Pre-seed a vector for artifact "a" so the missing check skips it.
	_ = v.Put(context.Background(), "t", "a", "1.0.0", make([]float32, 8))
	res, err := reg.Reembed(context.Background(), core.ReembedOptions{OnlyIfMissing: true})
	if err != nil {
		t.Fatalf("Reembed: %v", err)
	}
	if res.Total != 2 {
		t.Errorf("Total = %d, want 2", res.Total)
	}
	// At least one artifact (the one we pre-seeded) was skipped.
	if res.Succeeded > 1 {
		t.Errorf("Succeeded = %d, want ≤1 (one skipped)", res.Succeeded)
	}
}

// spec: §4.7 — Reembed(OnlyIfMissing) must skip every artifact that
// already has a vector, even when the tenant holds more than the old
// fixed 100-item probe window. The prior probe queried topK=100 and
// reported existing vectors as missing past that boundary (F-4.7.10).
func TestReembed_OnlyMissingScalesPast100Artifacts(t *testing.T) {
	t.Parallel()
	const n = 150
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("a%03d", i)
		if err := st.PutManifest(context.Background(), store.ManifestRecord{
			TenantID: "t", ArtifactID: id, Version: "1.0.0",
			Description: id + " desc", Layer: "L",
		}); err != nil {
			t.Fatalf("PutManifest: %v", err)
		}
	}
	e := &fakeEmbedderRE{dim: 8}
	v := vector.NewMemory(8)
	reg := core.New(st, "t", nil).WithVectorSearch(v, e)

	// First pass populates a vector for every artifact.
	if _, err := reg.Reembed(context.Background(), core.ReembedOptions{}); err != nil {
		t.Fatalf("seed Reembed: %v", err)
	}
	// Second pass with OnlyIfMissing must re-embed nothing: all n vectors
	// already exist. With the old topK=100 probe, the 50 artifacts past
	// the window were falsely re-embedded.
	res, err := reg.Reembed(context.Background(), core.ReembedOptions{OnlyIfMissing: true})
	if err != nil {
		t.Fatalf("Reembed: %v", err)
	}
	if res.Total != n {
		t.Errorf("Total = %d, want %d", res.Total, n)
	}
	if res.Succeeded != 0 {
		t.Errorf("Succeeded = %d, want 0 (all %d already embedded)", res.Succeeded, n)
	}
}

// spec: §4.7 — Reembed(Since) covers only artifacts ingested at or after
// the cutoff; the boundary is inclusive (F-4.7.8 `--since`).
func TestReembed_SinceFiltersByIngestedAt(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	cutoff := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	for _, m := range []store.ManifestRecord{
		{TenantID: "t", ArtifactID: "old", Version: "1.0.0", Description: "old", Layer: "L", IngestedAt: cutoff.Add(-time.Hour)},
		{TenantID: "t", ArtifactID: "fresh", Version: "1.0.0", Description: "fresh", Layer: "L", IngestedAt: cutoff.Add(time.Hour)},
		{TenantID: "t", ArtifactID: "edge", Version: "1.0.0", Description: "edge", Layer: "L", IngestedAt: cutoff},
	} {
		if err := st.PutManifest(context.Background(), m); err != nil {
			t.Fatalf("PutManifest: %v", err)
		}
	}
	e := &fakeEmbedderRE{dim: 8}
	v := vector.NewMemory(8)
	reg := core.New(st, "t", nil).WithVectorSearch(v, e)

	res, err := reg.Reembed(context.Background(), core.ReembedOptions{Since: cutoff})
	if err != nil {
		t.Fatalf("Reembed: %v", err)
	}
	// "fresh" and "edge" (at the inclusive cutoff) re-embed; "old" does not.
	if res.Total != 2 || res.Succeeded != 2 {
		t.Errorf("got Total=%d Succeeded=%d, want 2/2 (fresh + at-cutoff; old excluded)", res.Total, res.Succeeded)
	}
	probe := make([]float32, 8)
	probe[0] = 1
	matches, _ := v.Query(context.Background(), "t", probe, 10)
	for _, m := range matches {
		if m.ArtifactID == "old" {
			t.Errorf("artifact ingested before --since was re-embedded")
		}
	}
}

func TestReembed_EmbedFailureRecordedInFailed(t *testing.T) {
	t.Parallel()
	reg, e, _ := newReembedHarness(t)
	e.err = errors.New("embedder unavailable")
	res, err := reg.Reembed(context.Background(), core.ReembedOptions{})
	if err != nil {
		t.Fatalf("Reembed: %v", err)
	}
	if len(res.Failed) != 2 {
		t.Errorf("Failed = %d, want 2", len(res.Failed))
	}
}

func TestReembed_NoVectorSearchConfiguredErrors(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	reg := core.New(st, "t", nil)
	if _, err := reg.Reembed(context.Background(), core.ReembedOptions{}); err == nil {
		t.Errorf("expected error when vector search not configured")
	}
}

func TestReembedOne_Happy(t *testing.T) {
	t.Parallel()
	reg, _, _ := newReembedHarness(t)
	if err := reg.ReembedOne(context.Background(), "a", "1.0.0"); err != nil {
		t.Errorf("ReembedOne: %v", err)
	}
}

func TestReembedOne_NoVectorSearchConfiguredErrors(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	reg := core.New(st, "t", nil)
	if err := reg.ReembedOne(context.Background(), "a", "1.0.0"); err == nil {
		t.Errorf("expected error when vector search not configured")
	}
}

func TestReembedOne_UnknownArtifactErrors(t *testing.T) {
	t.Parallel()
	reg, _, _ := newReembedHarness(t)
	if err := reg.ReembedOne(context.Background(), "ghost", "1.0.0"); err == nil {
		t.Errorf("expected error for missing manifest")
	}
}
