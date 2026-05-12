package ingest_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// ArtifactCountQuota: when CurrentArtifactCount already equals the
// quota, ingest rejects the next new (distinct) artifact.
func TestIngest_ArtifactCountQuotaEnforced(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	// Pre-seed two artifacts so tenantHasArtifact reports them.
	for _, id := range []string{"a", "b"} {
		_ = st.PutManifest(context.Background(), store.ManifestRecord{
			TenantID: "t", ArtifactID: id, Version: "1.0.0",
			ContentHash: "sha256:seed-" + id, Type: "context",
			Description: id, Sensitivity: "low", Layer: "L",
		})
	}
	// Ingest a third artifact "c" with CurrentArtifactCount=2 and
	// ArtifactCountQuota=2 — the projected count is 3, which exceeds
	// the cap, so it must be rejected.
	files := fstest.MapFS{
		"c/ARTIFACT.md": &fstest.MapFile{
			Data: []byte("---\ntype: context\nversion: 1.0.0\ndescription: c\nsensitivity: low\n---\n\nbody\n"),
		},
	}
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID:             "t",
		LayerID:              "L",
		Files:                files,
		ArtifactCountQuota:   2,
		CurrentArtifactCount: 2,
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(res.Rejected) == 0 {
		t.Errorf("expected at least one quota rejection, got %+v", res)
	}
	// The rejection code should be quota.artifact_count_exceeded.
	for _, r := range res.Rejected {
		if r.Code != "quota.artifact_count_exceeded" {
			t.Errorf("rejection code = %q, want quota.artifact_count_exceeded", r.Code)
		}
	}
}

// Re-ingesting an existing artifact id does not count against the quota.
func TestIngest_ArtifactCountQuotaIgnoresExisting(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	// Pre-seed "a" so tenantHasArtifact reports it.
	_ = st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: "t", ArtifactID: "a", Version: "1.0.0",
		ContentHash: "sha256:seed-a", Type: "context",
		Description: "a", Sensitivity: "low", Layer: "L",
	})
	files := fstest.MapFS{
		"a/ARTIFACT.md": &fstest.MapFile{
			Data: []byte("---\ntype: context\nversion: 2.0.0\ndescription: a-v2\nsensitivity: low\n---\n\nbody\n"),
		},
	}
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID:             "t",
		LayerID:              "L",
		Files:                files,
		ArtifactCountQuota:   1,
		CurrentArtifactCount: 1,
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	// New version of an existing artifact id; quota allows.
	if len(res.Rejected) != 0 {
		t.Errorf("expected no rejections, got %+v", res.Rejected)
	}
}

// Quota of 0 disables the check; all artifacts pass.
func TestIngest_ArtifactCountQuotaDisabled(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	files := fstest.MapFS{}
	for _, name := range []string{"a", "b", "c", "d"} {
		files[name+"/ARTIFACT.md"] = &fstest.MapFile{
			Data: []byte("---\ntype: context\nversion: 1.0.0\ndescription: x\nsensitivity: low\n---\n\nbody\n"),
		}
	}
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID:           "t",
		LayerID:            "L",
		Files:              files,
		ArtifactCountQuota: 0, // disabled
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if res.Accepted != 4 {
		t.Errorf("Accepted = %d, want 4", res.Accepted)
	}
}
