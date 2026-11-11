package ingest_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// ingestOne ingests a single ARTIFACT.md at id under layerID and returns
// the result.
func ingestOne(t *testing.T, st store.Store, layerID, id, src string) *ingest.Result {
	t.Helper()
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "tenant-1", LayerID: layerID, Files: fstest.MapFS{
			id + "/ARTIFACT.md": &fstest.MapFile{Data: []byte(src)},
		},
	})
	if err != nil {
		t.Fatalf("ingest %s: %v", id, err)
	}
	return res
}

// Spec: §4.6 — two layers contributing the same canonical ID is a collision,
// and ingest rejects the second contribution without storing it.
func TestIngest_CrossLayerCollisionRejected(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	ingestOne(t, st, "org-defaults", "finance/pay", contextArtifact("base"))
	res := ingestOne(t, st, "team-foo", "finance/pay", "---\ntype: context\nversion: 2.0.0\ndescription: shadow\nsensitivity: low\n---\n\nbody\n")
	if len(res.Rejected) != 1 {
		t.Fatalf("got %d rejections, want 1: %+v", len(res.Rejected), res.Rejected)
	}
	if res.Rejected[0].Code != "ingest.collision" {
		t.Errorf("code = %q, want ingest.collision", res.Rejected[0].Code)
	}
	// The base record is untouched; the shadow is not stored.
	if _, err := st.GetManifest(context.Background(), "tenant-1", "finance/pay", "2.0.0"); err == nil {
		t.Errorf("silent-shadow record must not be stored")
	}
}

// Spec: §4.7.6 — a version bump within the same layer is not a cross-layer
// collision; the new version is accepted alongside the old.
func TestIngest_SameLayerVersionBumpNotCollision(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	ingestOne(t, st, "L1", "finance/pay", contextArtifact("v1"))
	res := ingestOne(t, st, "L1", "finance/pay", "---\ntype: context\nversion: 2.0.0\ndescription: v2\nsensitivity: low\n---\n\nbody\n")
	if res.Accepted != 1 || len(res.Rejected) != 0 {
		t.Fatalf("same-layer version bump: accepted=%d rejected=%+v, want clean accept", res.Accepted, res.Rejected)
	}
}
