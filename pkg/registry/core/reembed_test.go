package core_test

import (
	"context"
	"errors"
	"testing"

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

func (*fakeEmbedderRE) ID() string                 { return "fake" }
func (*fakeEmbedderRE) Model() string              { return "fake-model" }
func (e *fakeEmbedderRE) Dimensions() int          { return e.dim }
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
	res, err := reg.Reembed(context.Background(), false)
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
	res, err := reg.Reembed(context.Background(), true)
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

func TestReembed_EmbedFailureRecordedInFailed(t *testing.T) {
	t.Parallel()
	reg, e, _ := newReembedHarness(t)
	e.err = errors.New("embedder unavailable")
	res, err := reg.Reembed(context.Background(), false)
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
	if _, err := reg.Reembed(context.Background(), false); err == nil {
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
