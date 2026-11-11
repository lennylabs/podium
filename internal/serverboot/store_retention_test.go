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
