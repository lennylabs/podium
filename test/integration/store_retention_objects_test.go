package integration

// Store retention with object bytes and extends-pin protection
// (gap G-LIFECYCLE-3).
//
// internal/serverboot/store_retention_test.go backdates a deprecated version
// and asserts its manifest record is purged while a live version survives, but
// it does not exercise the object store or the extends-pin interaction. This
// test drives the §8.4 deprecated-version purge against a SQLite metadata store
// plus a filesystem object store with bundled resources, and asserts the
// correctness boundaries the purge must respect:
//
//   - An unpinned deprecated version past PODIUM_DEPRECATED_RETENTION_DAYS is
//     purged: its manifest record is gone.
//   - A non-deprecated successor of the same id survives untouched, and the
//     object bytes a surviving artifact references stay retrievable (the purge
//     does not delete content-addressed bytes a live artifact still uses).
//   - A deprecated version still pinned as an extends parent by a live child is
//     PROTECTED from purge: purging it would orphan the child's load_artifact,
//     which resolves the parent through the hard pin (§4.6/§4.7.6). The child
//     still loads after the sweep.
//
// Spec: §8.4 (deprecated artifact versions retained 90 days after the
// deprecation flag is set), §4.4 (bundled resources are content-addressed and
// deduplicated across versions), §4.6 / §4.7.6 (an extends child pins its parent
// to an exact version at ingest time; load resolves the parent through that pin).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

func TestStoreRetention_PurgesDeprecatedBytesProtectsPinnedParent(t *testing.T) {
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

	// Three object payloads, content-addressed by sha256. Each is uploaded under
	// its hash (the object-store key is the hash hex without the "sha256:"
	// prefix), the same keying ingest uses.
	orphanBytes := []byte("orphan resource only the purged version references\n")
	sharedBytes := []byte("shared resource the successor still references\n")
	parentBytes := []byte("pinned-parent resource the child depends on\n")
	orphanHash := putObject(t, objStore, orphanBytes)
	sharedHash := putObject(t, objStore, sharedBytes)
	parentHash := putObject(t, objStore, parentBytes)

	old := time.Now().UTC().Add(-100 * 24 * time.Hour) // past the 90-day window
	fresh := time.Now().UTC()

	// 1) An UNPINNED deprecated version of id A, backdated past the window, with
	//    a resource only it references (orphanHash) plus the shared resource.
	const idA = "finance/close/run-variance"
	putManifest(t, st, store.ManifestRecord{
		TenantID: tenant, ArtifactID: idA, Version: "1.0.0", ContentHash: "sha256:a-old",
		Type: "context", Layer: "team", Deprecated: true, DeprecatedAt: ptrTime(old),
		IngestedAt: old,
		Resources: []store.ResourceRef{
			{Path: "data/orphan.txt", ContentHash: "sha256:" + orphanHash, Size: int64(len(orphanBytes))},
			{Path: "data/shared.txt", ContentHash: "sha256:" + sharedHash, Size: int64(len(sharedBytes))},
		},
	})
	// 2) A NON-DEPRECATED successor of id A that still references the shared bytes.
	putManifest(t, st, store.ManifestRecord{
		TenantID: tenant, ArtifactID: idA, Version: "2.0.0", ContentHash: "sha256:a-new",
		Type: "context", Layer: "team", IngestedAt: fresh,
		Resources: []store.ResourceRef{
			{Path: "data/shared.txt", ContentHash: "sha256:" + sharedHash, Size: int64(len(sharedBytes))},
		},
	})

	// 3) A DEPRECATED parent version (id P) backdated past the window, carrying its
	//    own resource, that a live child pins via extends.
	const idP = "platform/base/policy"
	putManifest(t, st, store.ManifestRecord{
		TenantID: tenant, ArtifactID: idP, Version: "1.0.0", ContentHash: "sha256:p-old",
		Type: "context", Layer: "platform", Deprecated: true, DeprecatedAt: ptrTime(old),
		IngestedAt: old,
		Resources: []store.ResourceRef{
			{Path: "data/parent.txt", ContentHash: "sha256:" + parentHash, Size: int64(len(parentBytes))},
		},
	})
	// 4) A live CHILD (id C) pinning the deprecated parent at its exact version.
	const idC = "team/checkout/policy"
	putManifest(t, st, store.ManifestRecord{
		TenantID: tenant, ArtifactID: idC, Version: "1.0.0", ContentHash: "sha256:c",
		Type: "context", Layer: "team", IngestedAt: fresh,
		Frontmatter: []byte("---\ntype: context\nversion: 1.0.0\ndescription: checkout policy\nextends: " + idP + "@1.0.0\n---\n\nbody\n"),
		ExtendsPin:  idP + "@1.0.0",
	})

	// Sanity: the child loads before the sweep (it resolves the pinned parent).
	reg := core.New(st, tenant, []layer.Layer{
		{ID: "team", Precedence: 2, Visibility: layer.Visibility{Public: true}},
		{ID: "platform", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	if _, err := reg.LoadArtifact(ctx, layer.Identity{IsPublic: true}, idC, core.LoadArtifactOptions{}); err != nil {
		t.Fatalf("pre-sweep child load failed: %v", err)
	}

	// Run the §8.4 deprecated-version purge with a 90-day window.
	purged, err := st.PurgeDeprecatedManifests(ctx, time.Now().UTC().Add(-90*24*time.Hour))
	if err != nil {
		t.Fatalf("PurgeDeprecatedManifests: %v", err)
	}

	// The unpinned deprecated version A@1.0.0 is purged.
	if _, err := st.GetManifest(ctx, tenant, idA, "1.0.0"); err == nil {
		t.Errorf("unpinned deprecated version %s@1.0.0 survived the purge", idA)
	}
	// Exactly one version was purged (A@1.0.0); the pinned parent is protected.
	if purged != 1 {
		t.Errorf("purged %d versions, want 1 (only the unpinned deprecated version)", purged)
	}

	// The non-deprecated successor A@2.0.0 survives.
	if _, err := st.GetManifest(ctx, tenant, idA, "2.0.0"); err != nil {
		t.Errorf("non-deprecated successor %s@2.0.0 was wrongly purged: %v", idA, err)
	}

	// The deprecated parent P@1.0.0 is PROTECTED because the live child pins it.
	if _, err := st.GetManifest(ctx, tenant, idP, "1.0.0"); err != nil {
		t.Errorf("pinned deprecated parent %s@1.0.0 was purged, orphaning its child: %v", idP, err)
	}

	// The child still loads after the sweep: its pinned parent survived, so the
	// extends chain resolves rather than failing with not_found.
	if _, err := reg.LoadArtifact(ctx, layer.Identity{IsPublic: true}, idC, core.LoadArtifactOptions{}); err != nil {
		t.Errorf("post-sweep child load failed (pinned parent purged out from under it): %v", err)
	}

	// Object bytes a surviving artifact still references stay retrievable: the
	// shared resource (referenced by the live successor) and the parent resource
	// (referenced by the protected parent) are intact. The purge removes manifest
	// records, never content-addressed bytes a live record still points at.
	if got, err := objStore.Get(ctx, sharedHash); err != nil || string(got) != string(sharedBytes) {
		t.Errorf("shared resource bytes lost after purge (err=%v); a live successor still references them", err)
	}
	if got, err := objStore.Get(ctx, parentHash); err != nil || string(got) != string(parentBytes) {
		t.Errorf("pinned-parent resource bytes lost after purge (err=%v); the protected parent still references them", err)
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
func putManifest(t *testing.T, st store.Store, rec store.ManifestRecord) {
	t.Helper()
	if err := st.PutManifest(context.Background(), rec); err != nil {
		t.Fatalf("PutManifest %s@%s: %v", rec.ArtifactID, rec.Version, err)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

// sha256Hex returns the lowercase hex sha256 of b, the object-store key form.
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
