package ingest_test

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// agentParent is a type:agent parent suitable as an extends target.
func agentParent(desc string) string {
	return "---\ntype: agent\nversion: 1.0.0\ndescription: " + desc + "\nsensitivity: low\n---\n\nagent body\n"
}

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

// Spec: §4.6 — "The child's type: must match the parent's; ingest rejects
// an extends: chain that crosses types."
func TestExtends_CrossTypeRejected(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	ingestOne(t, st, "L1", "shared/parent", agentParent("parent"))
	// Child declares type: context but extends a type: agent parent.
	child := "---\ntype: context\nversion: 2.0.0\ndescription: child\nextends: shared/parent@1.x\n---\n\nbody\n"
	res := ingestOne(t, st, "L2", "finance/child", child)
	if len(res.Rejected) != 1 {
		t.Fatalf("got %d rejections, want 1: %+v", len(res.Rejected), res.Rejected)
	}
	if res.Rejected[0].Code != "ingest.invalid_artifact" {
		t.Errorf("code = %q, want ingest.invalid_artifact", res.Rejected[0].Code)
	}
	if !strings.Contains(res.Rejected[0].Reason, "type") {
		t.Errorf("reason should cite the type mismatch: %q", res.Rejected[0].Reason)
	}
	if _, err := st.GetManifest(context.Background(), "tenant-1", "finance/child", "2.0.0"); err == nil {
		t.Errorf("cross-type child must not be stored")
	}
}

// Spec: §4.6 — a same-type extends chain is accepted.
func TestExtends_SameTypeAccepted(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	ingestOne(t, st, "L1", "shared/parent", agentParent("parent"))
	child := "---\ntype: agent\nversion: 2.0.0\ndescription: child\nextends: shared/parent@1.x\n---\n\nbody\n"
	res := ingestOne(t, st, "L2", "finance/child", child)
	if res.Accepted != 1 || len(res.Rejected) != 0 {
		t.Fatalf("accepted=%d rejected=%+v, want a clean accept", res.Accepted, res.Rejected)
	}
}

// Spec: §4.6 — "A collision is rejected at ingest unless the
// higher-precedence artifact declares extends:." Two layers contributing
// the same canonical ID with no extends is a forbidden silent shadow.
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

// Spec: §4.6 — the cross-layer collision is permitted when the
// higher-precedence record declares extends: pointing at the colliding id.
func TestIngest_CrossLayerExtendsOverlayAllowed(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	ingestOne(t, st, "org-defaults", "finance/pay", contextArtifact("base"))
	overlay := "---\ntype: context\nversion: 2.0.0\ndescription: overlay\nsensitivity: low\nextends: finance/pay@1.x\n---\n\noverlay body\n"
	res := ingestOne(t, st, "team-foo", "finance/pay", overlay)
	if res.Accepted != 1 || len(res.Rejected) != 0 {
		t.Fatalf("accepted=%d rejected=%+v, want a clean accept for the extends overlay", res.Accepted, res.Rejected)
	}
	rec, err := st.GetManifest(context.Background(), "tenant-1", "finance/pay", "2.0.0")
	if err != nil {
		t.Fatalf("overlay not stored: %v", err)
	}
	if rec.ExtendsPin != "finance/pay@1.0.0" {
		t.Errorf("ExtendsPin = %q, want finance/pay@1.0.0", rec.ExtendsPin)
	}
}

// Spec: §4.7.6 — a version bump within the SAME layer is not a cross-layer
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
