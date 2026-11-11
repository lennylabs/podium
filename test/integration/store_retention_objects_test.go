package integration

// Store retention with object bytes.
//
// internal/serverboot/store_retention_test.go backdates a deprecated version
// and asserts its manifest record is purged while a live version survives, but
// it does not exercise the object store. This test drives the §8.4
// deprecated-version purge against a SQLite metadata store plus a filesystem
// object store with bundled resources, and asserts the correctness boundaries
// the purge must respect:
//
//   - A deprecated version past PODIUM_DEPRECATED_RETENTION_DAYS is purged: its
//     manifest record is gone.
//   - A non-deprecated successor of the same id survives untouched, and the
//     object bytes a surviving artifact references stay retrievable (the purge
//     does not delete content-addressed bytes a live artifact still uses).
//
// Spec: §8.4 (deprecated artifact versions retained 90 days after the
// deprecation flag is set), §4.4 (bundled resources are content-addressed and
// deduplicated across versions).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/store/storetest"
)

func TestStoreRetention_PurgesDeprecatedVersionKeepsSharedBytes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	const tenant = "default"
	if err := st.CreateTenant(ctx, store.Tenant{ID: tenant, Name: tenant}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	objStore, err := objectstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("objectstore.Open: %v", err)
	}

	// Two object payloads, content-addressed by sha256. Each is uploaded under
	// its hash (the object-store key is the hash hex without the "sha256:"
	// prefix), the same keying ingest uses.
	orphanBytes := []byte("orphan resource only the purged version references\n")
	sharedBytes := []byte("shared resource the successor still references\n")
	orphanHash := putObject(t, objStore, orphanBytes)
	sharedHash := putObject(t, objStore, sharedBytes)

	old := time.Now().UTC().Add(-100 * 24 * time.Hour) // past the 90-day window
	fresh := time.Now().UTC()

	// 1) A deprecated version of id A, backdated past the window, with
	//    a resource only it references (orphanHash) plus the shared resource.
	const idA = "finance/close/run-variance"
	putManifest(t, st, objStore, store.ManifestRecord{
		TenantID: tenant, ArtifactID: idA, Version: "1.0.0", ContentHash: "sha256:a-old",
		Type: "context", Layer: "team", Deprecated: true, DeprecatedAt: ptrTime(old),
		IngestedAt: old,
		Resources: []store.ResourceRef{
			{Path: "data/orphan.txt", ContentHash: "sha256:" + orphanHash, Size: int64(len(orphanBytes))},
			{Path: "data/shared.txt", ContentHash: "sha256:" + sharedHash, Size: int64(len(sharedBytes))},
		},
	})
	// 2) A NON-DEPRECATED successor of id A that still references the shared bytes.
	putManifest(t, st, objStore, store.ManifestRecord{
		TenantID: tenant, ArtifactID: idA, Version: "2.0.0", ContentHash: "sha256:a-new",
		Type: "context", Layer: "team", IngestedAt: fresh,
		Resources: []store.ResourceRef{
			{Path: "data/shared.txt", ContentHash: "sha256:" + sharedHash, Size: int64(len(sharedBytes))},
		},
	})

	// Run the §8.4 deprecated-version purge with a 90-day window.
	purged, err := st.PurgeDeprecatedManifests(ctx, time.Now().UTC().Add(-90*24*time.Hour))
	if err != nil {
		t.Fatalf("PurgeDeprecatedManifests: %v", err)
	}

	// The deprecated version A@1.0.0 is purged.
	if _, err := st.GetManifest(ctx, tenant, idA, "1.0.0"); err == nil {
		t.Errorf("deprecated version %s@1.0.0 survived the purge", idA)
	}
	// Exactly one version was purged (A@1.0.0).
	if purged != 1 {
		t.Errorf("purged %d versions, want 1 (only the deprecated version)", purged)
	}

	// The non-deprecated successor A@2.0.0 survives.
	if _, err := st.GetManifest(ctx, tenant, idA, "2.0.0"); err != nil {
		t.Errorf("non-deprecated successor %s@2.0.0 was wrongly purged: %v", idA, err)
	}

	// Object bytes a surviving artifact still references stay retrievable: the
	// shared resource, referenced by the live successor, is intact. The purge
	// removes manifest records, never content-addressed bytes a live record
	// still points at.
	if got, err := objStore.Get(ctx, sharedHash); err != nil || string(got) != string(sharedBytes) {
		t.Errorf("shared resource bytes lost after purge (err=%v); a live successor still references them", err)
	}
}

// putObject uploads body under its sha256 hex key and returns the key.
func putObject(t *testing.T, os objectstore.Provider, body []byte) string {
	t.Helper()
	key := sha256Hex(body)
	if err := os.Put(context.Background(), key, body, "text/plain"); err != nil {
		t.Fatalf("object Put: %v", err)
	}
	return key
}

// putManifest persists rec, failing the test on error.
// putManifest seals rec against objects, which holds its object-held bodies,
// so the §13.4 admission check serves it.
func putManifest(t *testing.T, st store.Store, objects objectstore.Provider, rec store.ManifestRecord) {
	t.Helper()
	if err := st.PutManifest(context.Background(), storetest.Seal(t, rec, objects, nil)); err != nil {
		t.Fatalf("PutManifest %s@%s: %v", rec.ArtifactID, rec.Version, err)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

// sha256Hex returns the lowercase hex sha256 of b, the object-store key form.
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
