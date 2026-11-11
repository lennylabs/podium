package integration

// Store retention with object bytes and a retired extends: child.
//
// internal/serverboot/store_retention_test.go backdates a deprecated version
// and asserts its manifest record is purged while a live version survives, but
// it does not exercise the object store or a stored extends: child. This test
// drives the §8.4 deprecated-version purge against the SQLite metadata store,
// and against Postgres when PODIUM_POSTGRES_DSN is set, plus a filesystem
// object store with bundled resources, and asserts the boundaries the purge
// must respect:
//
//   - A deprecated version past PODIUM_DEPRECATED_RETENTION_DAYS is purged: its
//     manifest record is gone.
//   - A deprecated version that a stored child names in extends:, through its
//     ARTIFACT.md and through the legacy extends_pin column a migrated database
//     keeps, is purged as well. The purge carries no pinned-parent protection.
//   - The child is a retired row: it stays in the store, and a registry load of
//     it answers registry.not_found after the sweep.
//   - A non-deprecated successor of the same id survives untouched, and the
//     object bytes a surviving artifact references stay retrievable (the purge
//     does not delete content-addressed bytes a live artifact still uses).
//
// Spec: §8.4 (deprecated artifact versions retained 90 days after the
// deprecation flag is set), §4.4 (bundled resources are content-addressed and
// deduplicated across versions), §4.6 (a stored row whose ARTIFACT.md declares
// extends: is retired), §13.4 (the additive schema keeps an existing
// database's unused extends_pin column).

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/store/storetest"
)

// retentionStore is one metadata store the retention test runs against, with
// the hook that writes a legacy extends_pin value into a manifest row.
type retentionStore struct {
	name string
	open func(t *testing.T) (store.Store, legacyPinFunc)
}

// legacyPinFunc gives the (tenant, id, version) row the extends_pin value pin,
// adding the column first when the schema omits it, which reproduces a
// database an earlier binary created and this binary migrated.
type legacyPinFunc func(t *testing.T, tenant, id, version, pin string)

func retentionStores() []retentionStore {
	stores := []retentionStore{{name: "sqlite", open: openRetentionSQLite}}
	if dsn := os.Getenv("PODIUM_POSTGRES_DSN"); dsn != "" {
		stores = append(stores, retentionStore{name: "postgres", open: func(t *testing.T) (store.Store, legacyPinFunc) {
			return openRetentionPostgres(t, dsn)
		}})
	}
	return stores
}

func openRetentionSQLite(t *testing.T) (store.Store, legacyPinFunc) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "registry.db")
	st, err := store.OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	pin := func(t *testing.T, tenant, id, version, pin string) {
		t.Helper()
		db, err := sql.Open("sqlite3", path)
		if err != nil {
			t.Fatalf("open legacy sqlite: %v", err)
		}
		defer func() { _ = db.Close() }()
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('manifests') WHERE name = 'extends_pin'`).Scan(&n); err != nil {
			t.Fatalf("pragma table_info(manifests): %v", err)
		}
		if n == 0 {
			if _, err := db.Exec(`ALTER TABLE manifests ADD COLUMN extends_pin TEXT NOT NULL DEFAULT ''`); err != nil {
				t.Fatalf("add legacy extends_pin column: %v", err)
			}
		}
		setLegacyPin(t, db, `UPDATE manifests SET extends_pin = ? WHERE tenant_id = ? AND artifact_id = ? AND version = ?`,
			pin, tenant, id, version)
	}
	return st, pin
}

func openRetentionPostgres(t *testing.T, dsn string) (store.Store, legacyPinFunc) {
	t.Helper()
	// ResetForTest drops every org schema on the shared database, and the
	// purge scans every org schema, so this subtest holds the whole-database
	// lock for its full run.
	lockPostgresReset(t)
	pg, err := store.OpenPostgres(dsn)
	if err != nil {
		t.Skipf("OpenPostgres %q: %v (database unreachable)", dsn, err)
	}
	t.Cleanup(func() { _ = pg.Close() })
	if err := pg.ResetForTest(context.Background()); err != nil {
		t.Fatalf("ResetForTest: %v", err)
	}
	pin := func(t *testing.T, tenant, id, version, pin string) {
		t.Helper()
		table := fmt.Sprintf(`"org_%s".manifests`, tenant)
		if _, err := pg.DB().Exec(`ALTER TABLE ` + table + ` ADD COLUMN IF NOT EXISTS extends_pin TEXT NOT NULL DEFAULT ''`); err != nil {
			t.Fatalf("add legacy extends_pin column: %v", err)
		}
		setLegacyPin(t, pg.DB(), `UPDATE `+table+` SET extends_pin = $1 WHERE tenant_id = $2 AND artifact_id = $3 AND version = $4`,
			pin, tenant, id, version)
	}
	return pg, pin
}

// setLegacyPin runs the extends_pin UPDATE and fails unless it touched one row.
func setLegacyPin(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	res, err := db.Exec(query, args...)
	if err != nil {
		t.Fatalf("set legacy extends_pin: %v", err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		t.Fatalf("set legacy extends_pin touched %d rows (err=%v), want 1", n, err)
	}
}

func TestStoreRetention_PurgesDeprecatedVersionKeepsSharedBytes(t *testing.T) {
	t.Parallel()
	for _, s := range retentionStores() {
		s := s
		t.Run(s.name, func(t *testing.T) {
			t.Parallel()
			st, legacyPin := s.open(t)
			assertDeprecatedPurge(t, st, legacyPin)
		})
	}
}

func assertDeprecatedPurge(t *testing.T, st store.Store, legacyPin legacyPinFunc) {
	t.Helper()
	ctx := context.Background()
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
	// 3) A deprecated version of id P, backdated past the window, that a stored
	//    child names as its extends: parent.
	const idP = "platform/base/policy"
	putManifest(t, st, objStore, store.ManifestRecord{
		TenantID: tenant, ArtifactID: idP, Version: "1.0.0", ContentHash: "sha256:p-old",
		Type: "context", Layer: "platform", Deprecated: true, DeprecatedAt: ptrTime(old),
		IngestedAt: old,
	})
	// 4) A live child C whose ARTIFACT.md declares extends: P@1.0.0, as a row an
	//    earlier binary stored, with the same parent in the legacy extends_pin
	//    column. The old purge protection read that column.
	const idC = "team/checkout/policy"
	putManifest(t, st, objStore, store.ManifestRecord{
		TenantID: tenant, ArtifactID: idC, Version: "1.0.0", ContentHash: "sha256:c",
		Type: "context", Layer: "team", IngestedAt: fresh,
		Frontmatter: []byte("---\ntype: context\nversion: 1.0.0\ndescription: checkout policy\nextends: " + idP + "@1.0.0\n---\n\nbody\n"),
	})
	legacyPin(t, tenant, idC, "1.0.0", idP+"@1.0.0")

	// Run the §8.4 deprecated-version purge with a 90-day window.
	purged, err := st.PurgeDeprecatedManifests(ctx, time.Now().UTC().Add(-90*24*time.Hour))
	if err != nil {
		t.Fatalf("PurgeDeprecatedManifests: %v", err)
	}

	// Both deprecated versions are purged: A@1.0.0, and P@1.0.0 although the
	// child names it in its frontmatter and in extends_pin.
	if purged != 2 {
		t.Errorf("purged %d versions, want 2 (both deprecated versions, the extends: parent included)", purged)
	}
	if _, err := st.GetManifest(ctx, tenant, idA, "1.0.0"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deprecated version %s@1.0.0 after purge: err = %v, want store.ErrNotFound", idA, err)
	}
	if _, err := st.GetManifest(ctx, tenant, idP, "1.0.0"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deprecated extends: parent %s@1.0.0 after purge: err = %v, want store.ErrNotFound", idP, err)
	}

	// The non-deprecated successor A@2.0.0 survives.
	if _, err := st.GetManifest(ctx, tenant, idA, "2.0.0"); err != nil {
		t.Errorf("non-deprecated successor %s@2.0.0 was wrongly purged: %v", idA, err)
	}

	// The child row stays in the store, because the purge removes deprecated
	// versions only, and the registry treats it as retired: a load answers
	// registry.not_found rather than resolving a parent.
	if _, err := st.GetManifest(ctx, tenant, idC, "1.0.0"); err != nil {
		t.Errorf("child row %s@1.0.0 was removed by the purge: %v", idC, err)
	}
	reg := core.New(st, tenant, []layer.Layer{
		{ID: "team", Precedence: 2, Visibility: layer.Visibility{Public: true}},
		{ID: "platform", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	}).WithAdmission(nil, objStore, objectstore.DefaultReadTimeout)
	if _, err := reg.LoadArtifact(ctx, layer.Identity{IsPublic: true}, idC, core.LoadArtifactOptions{}); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("load of retired child %s after purge: err = %v, want registry.not_found", idC, err)
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
