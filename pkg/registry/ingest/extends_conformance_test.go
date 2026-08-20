package ingest_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lennylabs/podium/internal/clock"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/registry/projection"
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

// ingestOneAt is ingestOne with an injected clock, so a caller can give each
// ingested version a distinct controlled IngestedAt.
func ingestOneAt(t *testing.T, st store.Store, clk clock.Clock, layerID, id, src string) *ingest.Result {
	t.Helper()
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "tenant-1", LayerID: layerID, Clock: clk, Files: fstest.MapFS{
			id + "/ARTIFACT.md": &fstest.MapFile{Data: []byte(src)},
		},
	})
	if err != nil {
		t.Fatalf("ingest %s: %v", id, err)
	}
	return res
}

// agentVersion is a type:agent artifact at an arbitrary version, suitable as
// an extends target.
func agentVersion(ver, desc string) string {
	return "---\ntype: agent\nversion: " + ver + "\ndescription: " + desc + "\nsensitivity: low\n---\n\nagent body\n"
}

// Spec: §4.7.6 — an unpinned extends reference resolves to `latest`, which is
// the most recently ingested version rather than the highest semver. A
// backported 1.1.0 published after 2.0.0 is therefore the parent a bare
// `extends: shared/parent` pins to.
func TestIngest_ExtendsUnpinnedPinsMostRecentlyIngestedParent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStore(t)
	// A shared frozen clock advanced between calls separates the two parent
	// versions in ingest time, so the assertion cannot be satisfied by
	// ResolveLatest's higher-semver tiebreak on an exact timestamp tie.
	clk := clock.NewFrozen(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	// The newer major line lands first.
	if res := ingestOneAt(t, st, clk, "L1", "shared/parent", agentVersion("2.0.0", "next line")); res.Accepted != 1 {
		t.Fatalf("parent 2.0.0 not accepted: %+v", res)
	}
	clk.Advance(time.Hour)
	// The backport onto the older line is published afterwards. A version
	// bump within the same layer is not a collision (§4.7.6).
	if res := ingestOneAt(t, st, clk, "L1", "shared/parent", agentVersion("1.1.0", "backport")); res.Accepted != 1 {
		t.Fatalf("parent 1.1.0 not accepted: %+v", res)
	}
	clk.Advance(time.Hour)

	child := "---\ntype: agent\nversion: 3.0.0\ndescription: child\nsensitivity: low\nextends: shared/parent\n---\n\nchild body\n"
	if res := ingestOneAt(t, st, clk, "L2", "finance/child", child); res.Accepted != 1 || len(res.Rejected) != 0 {
		t.Fatalf("accepted=%d rejected=%+v, want a clean accept", res.Accepted, res.Rejected)
	}

	rec, err := st.GetManifest(ctx, "tenant-1", "finance/child", "3.0.0")
	if err != nil {
		t.Fatalf("GetManifest child: %v", err)
	}
	if rec.ExtendsPin != "shared/parent@1.1.0" {
		t.Errorf("ExtendsPin = %q, want shared/parent@1.1.0 (most recently ingested)", rec.ExtendsPin)
	}
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

// foldParentSrc is a type:agent parent that populates every field the
// §4.6 fold reads, plus three fields (name, when_to_use, replaced_by) the
// fold must leave alone because no SQL store persists them.
const foldParentSrc = "---\n" +
	"type: agent\n" +
	"version: 1.0.0\n" +
	"name: parent-agent\n" +
	"description: parent desc\n" +
	"replaced_by: finance/successor\n" +
	"when_to_use:\n  - parent case\n" +
	"tags: [a, shared]\n" +
	"sensitivity: high\n" +
	"search_visibility: direct-only\n" +
	"---\n\nparent body\n"

// foldChildSrc extends foldParentSrc and omits description, name, and
// replaced_by so the §4.6 inheritance rule has something to resolve.
const foldChildSrc = "---\n" +
	"type: agent\n" +
	"version: 2.0.0\n" +
	"extends: shared/parent@1.x\n" +
	"when_to_use:\n  - child case\n" +
	"tags: [b]\n" +
	"sensitivity: low\n" +
	"---\n\nchild body\n"

// ingestFoldFixture ingests the parent and the extends child and returns
// the child's stored record.
func ingestFoldFixture(t *testing.T, st store.Store) store.ManifestRecord {
	t.Helper()
	if res := ingestOne(t, st, "L1", "shared/parent", foldParentSrc); res.Accepted != 1 {
		t.Fatalf("parent not accepted: %+v", res)
	}
	if res := ingestOne(t, st, "L2", "finance/child", foldChildSrc); res.Accepted != 1 {
		t.Fatalf("child not accepted: %+v", res)
	}
	rec, err := st.GetManifest(context.Background(), "tenant-1", "finance/child", "2.0.0")
	if err != nil {
		t.Fatalf("GetManifest child: %v", err)
	}
	return rec
}

// Spec: §4.6 "Omitted fields" — an omitted child scalar
// inherits the parent's value, and ingest writes the resolved values into
// the columns the registry filters, ranks, and embeds on.
// Spec: §4.7 "Artifact embeddings" — the projection reads the §4.6-resolved
// frontmatter, so the child is indexed under the inherited description.
func TestIngest_ExtendsChildStoresMergedIndexColumns(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	rec := ingestFoldFixture(t, st)

	if rec.Description != "parent desc" {
		t.Errorf("Description = %q, want %q", rec.Description, "parent desc")
	}
	if got, want := strings.Join(rec.Tags, ","), "a,shared,b"; got != want {
		t.Errorf("Tags = %q, want %q", got, want)
	}
	if rec.Sensitivity != "high" {
		t.Errorf("Sensitivity = %q, want high (most restrictive wins)", rec.Sensitivity)
	}
	if rec.SearchVisibility != "direct-only" {
		t.Errorf("SearchVisibility = %q, want direct-only", rec.SearchVisibility)
	}
	// The fold's field set stops at the four persisted columns.
	if got := strings.Join(rec.WhenToUse, ","); got != "child case" {
		t.Errorf("WhenToUse = %q, want the child's own entry only", got)
	}
	if rec.Name != "" || rec.ReplacedBy != "" {
		t.Errorf("Name = %q, ReplacedBy = %q, want both unfolded", rec.Name, rec.ReplacedBy)
	}
	if txt := projection.Artifact(rec); !strings.Contains(txt, "parent desc") {
		t.Errorf("§4.7 projection = %q, want it to carry the inherited description", txt)
	}
}

// Spec: §4.7 Version immutability — re-ingesting unchanged bytes is
// classified as idempotent before the write, so a version already stored
// with unfolded columns keeps them. The repair is a new child version, and
// ingest adds no backfill path.
func TestIngest_ExtendsChildReingestIsIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	// A first store yields the record the pipeline computes, including the
	// content hash the re-ingest below compares against.
	rec := ingestFoldFixture(t, newStore(t))

	// The store under test holds the pre-fold state a prior release wrote:
	// the same content hash with the child's authored (empty) description.
	st := newStore(t)
	if res := ingestOne(t, st, "L1", "shared/parent", foldParentSrc); res.Accepted != 1 {
		t.Fatalf("parent not accepted: %+v", res)
	}
	unfolded := rec
	unfolded.Description = ""
	if err := st.PutManifest(ctx, unfolded); err != nil {
		t.Fatalf("PutManifest unfolded child: %v", err)
	}

	res := ingestOne(t, st, "L2", "finance/child", foldChildSrc)
	if res.Idempotent != 1 || res.Accepted != 0 {
		t.Fatalf("accepted=%d idempotent=%d, want the re-ingest to be idempotent", res.Accepted, res.Idempotent)
	}
	got, err := st.GetManifest(ctx, "tenant-1", "finance/child", "2.0.0")
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	if got.Description != "" {
		t.Errorf("Description = %q, want the unfolded value to persist", got.Description)
	}
}

// Spec: §13.10 / §4.6 — public mode rejects ingest at or above the
// sensitivity floor, and the §4.6 fold writes the most-restrictive value
// across the chain into the child's indexed column. A child that authored no
// sensitivity and inherits `high` is refused rather than stored and served at
// the level the floor exists to keep out. The parent itself is ingested with
// no floor, which is §13.10's carve-out for content stored before public mode
// was enabled.
// Matrix: §6.10 (ingest.public_mode_rejects_sensitive)
func TestIngest_ExtendsFoldReappliesSensitivityFloor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStore(t)
	if res := ingestOne(t, st, "L1", "shared/parent", foldParentSrc); res.Accepted != 1 {
		t.Fatalf("parent not accepted: %+v", res)
	}
	child := "---\ntype: agent\nversion: 2.0.0\nextends: shared/parent@1.x\ntags: [b]\n---\n\nchild body\n"

	res, err := ingest.Ingest(ctx, st, ingest.Request{
		TenantID: "tenant-1", LayerID: "L2",
		Files: fstest.MapFS{
			"finance/child/ARTIFACT.md": &fstest.MapFile{Data: []byte(child)},
		},
		RejectAtOrAbove: manifest.SensitivityMedium,
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if res.Accepted != 0 || len(res.Rejected) != 1 {
		t.Fatalf("accepted=%d rejected=%+v, want the inheriting child rejected", res.Accepted, res.Rejected)
	}
	if res.Rejected[0].Code != "ingest.public_mode_rejects_sensitive" {
		t.Errorf("code = %q, want ingest.public_mode_rejects_sensitive", res.Rejected[0].Code)
	}
	if !strings.Contains(res.Rejected[0].Reason, `"high"`) {
		t.Errorf("reason = %q, want it to name the inherited level", res.Rejected[0].Reason)
	}
	if _, gerr := st.GetManifest(ctx, "tenant-1", "finance/child", "2.0.0"); gerr == nil {
		t.Errorf("child rejected at the floor must not be stored")
	}
}

// Spec: §13.10 / §4.6 — the re-applied floor reads the merged value, so a
// child whose chain stays below the floor is still accepted.
func TestIngest_ExtendsFoldBelowFloorAccepted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStore(t)
	if res := ingestOne(t, st, "L1", "shared/parent", agentParent("parent desc")); res.Accepted != 1 {
		t.Fatalf("parent not accepted: %+v", res)
	}
	child := "---\ntype: agent\nversion: 2.0.0\nextends: shared/parent@1.x\ntags: [b]\n---\n\nchild body\n"

	res, err := ingest.Ingest(ctx, st, ingest.Request{
		TenantID: "tenant-1", LayerID: "L2",
		Files: fstest.MapFS{
			"finance/child/ARTIFACT.md": &fstest.MapFile{Data: []byte(child)},
		},
		RejectAtOrAbove: manifest.SensitivityMedium,
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if res.Accepted != 1 || len(res.Rejected) != 0 {
		t.Fatalf("accepted=%d rejected=%+v, want the low-sensitivity child accepted", res.Accepted, res.Rejected)
	}
	rec, err := st.GetManifest(ctx, "tenant-1", "finance/child", "2.0.0")
	if err != nil {
		t.Fatalf("GetManifest child: %v", err)
	}
	if rec.Sensitivity != "low" {
		t.Errorf("Sensitivity = %q, want the inherited low", rec.Sensitivity)
	}
}

// parentLoadStore fails the extends fold's parent load. GetManifest returns
// err for the pinned parent record and delegates every other call, so the
// stub reproduces a parent that becomes unreadable between
// resolveExtendsPin's listing and the fold's load.
type parentLoadStore struct {
	store.Store
	parentID string
	err      error
}

func (s parentLoadStore) GetManifest(ctx context.Context, tenantID, artifactID, version string) (store.ManifestRecord, error) {
	if artifactID == s.parentID {
		return store.ManifestRecord{}, s.err
	}
	return s.Store.GetManifest(ctx, tenantID, artifactID, version)
}

// Spec: §4.6 — the fold reads the pinned parent's stored record. A store
// failure is an infrastructure fault rather than an authoring defect, so
// Ingest aborts and reports it instead of accepting the child under its
// unresolved columns.
func TestIngest_ExtendsParentLoadFailureAbortsIngest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStore(t)
	if res := ingestOne(t, st, "L1", "shared/parent", foldParentSrc); res.Accepted != 1 {
		t.Fatalf("parent not accepted: %+v", res)
	}
	boom := errors.New("connection reset")
	failing := parentLoadStore{Store: st, parentID: "shared/parent", err: boom}

	_, err := ingest.Ingest(ctx, failing, ingest.Request{
		TenantID: "tenant-1", LayerID: "L2", Files: fstest.MapFS{
			"finance/child/ARTIFACT.md": &fstest.MapFile{Data: []byte(foldChildSrc)},
		},
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Ingest error = %v, want it to wrap the store failure", err)
	}
	if !strings.Contains(err.Error(), "load extends parent shared/parent@1.0.0") {
		t.Errorf("error = %q, want it to name the pinned parent", err)
	}
	if _, gerr := st.GetManifest(ctx, "tenant-1", "finance/child", "2.0.0"); gerr == nil {
		t.Errorf("child must not be stored when the parent load fails")
	}
}

// Spec: §4.6 — the pin resolves against the tenant's manifests, so a parent
// that is gone by the time the fold loads it leaves the child unresolvable.
// Ingest rejects the child rather than indexing it under its authored subset.
func TestIngest_ExtendsParentMissingAtFoldRejectsChild(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := newStore(t)
	if res := ingestOne(t, st, "L1", "shared/parent", foldParentSrc); res.Accepted != 1 {
		t.Fatalf("parent not accepted: %+v", res)
	}
	vanished := parentLoadStore{Store: st, parentID: "shared/parent", err: store.ErrNotFound}

	res, err := ingest.Ingest(ctx, vanished, ingest.Request{
		TenantID: "tenant-1", LayerID: "L2", Files: fstest.MapFS{
			"finance/child/ARTIFACT.md": &fstest.MapFile{Data: []byte(foldChildSrc)},
		},
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(res.Rejected) != 1 || res.Accepted != 0 {
		t.Fatalf("accepted=%d rejected=%+v, want the child rejected", res.Accepted, res.Rejected)
	}
	if res.Rejected[0].Code != "ingest.invalid_artifact" {
		t.Errorf("code = %q, want ingest.invalid_artifact", res.Rejected[0].Code)
	}
	if want := "extends: parent shared/parent@1.0.0 not found"; res.Rejected[0].Reason != want {
		t.Errorf("reason = %q, want %q", res.Rejected[0].Reason, want)
	}
	if _, gerr := st.GetManifest(ctx, "tenant-1", "finance/child", "2.0.0"); gerr == nil {
		t.Errorf("rejected child must not be stored")
	}
}
