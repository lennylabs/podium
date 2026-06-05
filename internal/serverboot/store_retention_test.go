package serverboot

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §8.4 — runStoreRetentionOnce purges deprecated artifact versions
// past the deprecated-version window and hard-deletes soft-deleted layers
// past the recovery window, leaving fresher records intact.
func TestRunStoreRetentionOnce_PurgesExpiredRecords(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t", Name: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	// A deprecated version stamped 100 days ago (purgeable at 90d).
	oldDep := store.ManifestRecord{
		TenantID: "t", ArtifactID: "skill/x", Version: "1.0.0", ContentHash: "h1",
		Type: "skill", Deprecated: true, Layer: "team",
		IngestedAt: time.Now().UTC().Add(-100 * 24 * time.Hour),
	}
	if err := st.PutManifest(ctx, oldDep); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	// A live version that must survive.
	live := store.ManifestRecord{
		TenantID: "t", ArtifactID: "skill/x", Version: "2.0.0", ContentHash: "h2",
		Type: "skill", Layer: "team", IngestedAt: time.Now().UTC(),
	}
	if err := st.PutManifest(ctx, live); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}

	// A layer soft-deleted 40 days ago (purgeable at 30d).
	deletedAt := time.Now().UTC().Add(-40 * 24 * time.Hour)
	if err := st.PutLayerConfig(ctx, store.LayerConfig{
		TenantID: "t", ID: "gone", SourceType: "local", LocalPath: "/x", DeletedAt: &deletedAt,
	}); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}

	runStoreRetentionOnce(ctx, st, 90*24*time.Hour, 30*24*time.Hour)

	if _, err := st.GetManifest(ctx, "t", "skill/x", "1.0.0"); err == nil {
		t.Errorf("old deprecated version survived store retention")
	}
	if _, err := st.GetManifest(ctx, "t", "skill/x", "2.0.0"); err != nil {
		t.Errorf("live version wrongly purged: %v", err)
	}
	deleted, _ := st.ListDeletedLayerConfigs(ctx, "t")
	if len(deleted) != 0 {
		t.Errorf("expired soft-deleted layer survived: %+v", deleted)
	}
}

// Spec: §8.4 deprecated-version purge intersected with §4.6/§4.7.6 extends pins —
// runStoreRetentionOnce must not purge a deprecated version that a live child
// still pins as an extends parent. The child resolves the parent through the
// hard pin at load time, so purging the pinned parent would orphan the child.
// The unpinned deprecated version in the same sweep is still purged.
func TestRunStoreRetentionOnce_ProtectsExtendsPinnedParent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t", Name: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	old := time.Now().UTC().Add(-100 * 24 * time.Hour) // past the 90-day window

	// A deprecated parent past the window, pinned by a live child below.
	if err := st.PutManifest(ctx, store.ManifestRecord{
		TenantID: "t", ArtifactID: "base/policy", Version: "1.0.0", ContentHash: "p1",
		Type: "context", Layer: "platform", Deprecated: true, IngestedAt: old,
	}); err != nil {
		t.Fatalf("put parent: %v", err)
	}
	// A live child pinning the deprecated parent at its exact version.
	if err := st.PutManifest(ctx, store.ManifestRecord{
		TenantID: "t", ArtifactID: "team/policy", Version: "1.0.0", ContentHash: "c1",
		Type: "context", Layer: "team", ExtendsPin: "base/policy@1.0.0",
		IngestedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("put child: %v", err)
	}
	// An unrelated deprecated version past the window with no pin: must be purged.
	if err := st.PutManifest(ctx, store.ManifestRecord{
		TenantID: "t", ArtifactID: "other/thing", Version: "1.0.0", ContentHash: "o1",
		Type: "context", Layer: "team", Deprecated: true, IngestedAt: old,
	}); err != nil {
		t.Fatalf("put unpinned: %v", err)
	}

	runStoreRetentionOnce(ctx, st, 90*24*time.Hour, 30*24*time.Hour)

	// The pinned parent is protected even though it is deprecated and past the
	// window: purging it would orphan the live child.
	if _, err := st.GetManifest(ctx, "t", "base/policy", "1.0.0"); err != nil {
		t.Errorf("extends-pinned deprecated parent was purged, orphaning its child: %v", err)
	}
	// The unpinned deprecated version is still purged.
	if _, err := st.GetManifest(ctx, "t", "other/thing", "1.0.0"); err == nil {
		t.Errorf("unpinned deprecated version survived the purge")
	}
}
