package ingest_test

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
)

// fakeEmbed produces a deterministic vector from text length;
// adequate for verifying the orchestration without hitting an API.
func fakeEmbed(_ context.Context, text string) ([]float32, error) {
	v := make([]float32, 8)
	v[0] = float32(len(text))
	return v, nil
}

func failingEmbed(_ context.Context, _ string) ([]float32, error) {
	return nil, errors.New("provider offline")
}

// Spec: §4.7 — successful ingest writes manifest + populates vector.
func TestIngest_PopulatesVectorOnAccept(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	v := vector.NewMemory(8)
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t",
		LayerID:  "L",
		Files: fstest.MapFS{
			"x/ARTIFACT.md": &fstest.MapFile{
				Data: []byte("---\ntype: context\nversion: 1.0.0\ndescription: x\nsensitivity: low\n---\n\nbody\n"),
			},
		},
		Embedder: fakeEmbed,
		VectorPut: func(ctx context.Context, tenant, id, ver string, vec []float32) error {
			return v.Put(ctx, tenant, id, ver, vec)
		},
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if res.Accepted != 1 {
		t.Fatalf("Accepted = %d, want 1", res.Accepted)
	}
	if len(res.EmbeddingFailures) != 0 {
		t.Errorf("EmbeddingFailures = %v, want empty", res.EmbeddingFailures)
	}
	matches, _ := v.Query(context.Background(), "t", make([]float32, 8), 5)
	found := false
	for _, m := range matches {
		if m.ArtifactID == "x" {
			found = true
		}
	}
	if !found {
		t.Errorf("vector not stored: %v", matches)
	}
}

// Spec: §4.7 — embedding-provider failure does not reject the
// ingest; the artifact lands in the manifest store and an
// EmbeddingFailure entry surfaces so admin reembed can retry.
func TestIngest_EmbedderFailureDoesNotRejectIngest(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	v := vector.NewMemory(8)
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t",
		LayerID:  "L",
		Files: fstest.MapFS{
			"x/ARTIFACT.md": &fstest.MapFile{
				Data: []byte("---\ntype: context\nversion: 1.0.0\ndescription: x\nsensitivity: low\n---\n\nbody\n"),
			},
		},
		Embedder: failingEmbed,
		VectorPut: func(ctx context.Context, tenant, id, ver string, vec []float32) error {
			return v.Put(ctx, tenant, id, ver, vec)
		},
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if res.Accepted != 1 {
		t.Errorf("Accepted = %d, want 1", res.Accepted)
	}
	if len(res.EmbeddingFailures) != 1 || res.EmbeddingFailures[0].ArtifactID != "x" {
		t.Errorf("EmbeddingFailures = %v, want one entry for x", res.EmbeddingFailures)
	}
	// Manifest persisted; vector did not.
	if _, err := st.GetManifest(context.Background(), "t", "x", "1.0.0"); err != nil {
		t.Errorf("manifest missing: %v", err)
	}
}

// Spec: §4.7 — re-ingesting an unchanged manifest is idempotent and
// does not re-embed (saves cost; embeddings are deterministic per
// content_hash anyway).
func TestIngest_IdempotentSkipsEmbedding(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	embedCalls := 0
	embedder := func(ctx context.Context, text string) ([]float32, error) {
		embedCalls++
		return fakeEmbed(ctx, text)
	}
	v := vector.NewMemory(8)
	files := fstest.MapFS{
		"x/ARTIFACT.md": &fstest.MapFile{
			Data: []byte("---\ntype: context\nversion: 1.0.0\ndescription: x\nsensitivity: low\n---\n\nbody\n"),
		},
	}
	for i := 0; i < 3; i++ {
		_, err := ingest.Ingest(context.Background(), st, ingest.Request{
			TenantID: "t", LayerID: "L", Files: files,
			Embedder: embedder,
			VectorPut: func(ctx context.Context, tenant, id, ver string, vec []float32) error {
				return v.Put(ctx, tenant, id, ver, vec)
			},
		})
		if err != nil {
			t.Fatalf("Ingest #%d: %v", i, err)
		}
	}
	if embedCalls != 1 {
		t.Errorf("embedCalls = %d, want 1 (idempotent re-ingests should not re-embed)", embedCalls)
	}
}
