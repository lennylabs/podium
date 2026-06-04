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

// Spec: §4.6 — a higher-precedence layer overlays a same-ID artifact from a
// lower-precedence layer by declaring extends: <id> and carrying its own
// version (§4.7.6: "each artifact has its own version stored in the
// registry"). The parent reference (unpinned) must resolve against the
// lower-precedence layer's record even though that record shares the
// canonical ID, and the merged child wins per the field-semantics table.
func TestIngest_CrossLayerExtendsOverlayDistinctVersion(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	base := "---\ntype: context\nversion: 0.1.0\ndescription: base\nsensitivity: low\n---\n\nbase body\n"
	ingestOne(t, st, "base", "greet", base)
	overlay := "---\ntype: context\nversion: 0.2.0\ndescription: overlay\nsensitivity: low\nextends: greet\n---\n\noverlay body\n"
	res := ingestOne(t, st, "team", "greet", overlay)
	if res.Accepted != 1 || len(res.Rejected) != 0 {
		t.Fatalf("accepted=%d rejected=%+v, want a clean accept for the same-ID overlay", res.Accepted, res.Rejected)
	}
	rec, err := st.GetManifest(context.Background(), "tenant-1", "greet", "0.2.0")
	if err != nil {
		t.Fatalf("overlay not stored: %v", err)
	}
	// The unpinned parent reference resolves to the lower-precedence record.
	if rec.ExtendsPin != "greet@0.1.0" {
		t.Errorf("ExtendsPin = %q, want greet@0.1.0", rec.ExtendsPin)
	}
	if rec.Layer != "team" {
		t.Errorf("stored Layer = %q, want team", rec.Layer)
	}
	if rec.Description != "overlay" {
		t.Errorf("Description = %q, want overlay (child wins)", rec.Description)
	}
}

// Spec: §4.6 / §4.7.6 — when a child names its own canonical ID as the
// extends parent but no lower-precedence record carries a different version,
// the only candidate is the child's own record, which is a genuine
// self-cycle. resolveExtendsPin excludes only the child's own-layer record,
// so a same-ID record from a different layer at the SAME version is still a
// candidate parent; that path collides on the (id, version) store key and is
// reported as a version conflict rather than a false self-extends cycle.
func TestIngest_SameVersionOverlayIsConflictNotSelfCycle(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	base := "---\ntype: context\nversion: 0.1.0\ndescription: base\nsensitivity: low\n---\n\nbase body\n"
	ingestOne(t, st, "base", "greet", base)
	overlay := "---\ntype: context\nversion: 0.1.0\ndescription: overlay\nsensitivity: low\nextends: greet\n---\n\noverlay body\n"
	res := ingestOne(t, st, "team", "greet", overlay)
	// The lower-precedence parent resolves, so this is not a self-cycle
	// rejection; the same (id, version) store key makes it a version
	// conflict instead.
	for _, r := range res.Rejected {
		if strings.Contains(r.Reason, "self-extends cycle") {
			t.Fatalf("must not report a self-extends cycle for a cross-layer same-ID parent: %+v", r)
		}
	}
	if len(res.Conflicts) != 1 {
		t.Fatalf("conflicts=%+v, want exactly one version conflict", res.Conflicts)
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
