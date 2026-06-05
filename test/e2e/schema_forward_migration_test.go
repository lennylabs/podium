package e2e

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §13.4 Migrations — "a binary upgrade migrates an
// existing database forward in place, without a separate migration step."
// This exercises the contract end-to-end through the real podium binary:
// `admin migrate-to-standard` opens the SQLite source via the same
// store.OpenSQLite path the server uses, so pointing it at a database
// created by an earlier binary (whose tenants and manifests tables predate
// the post-initial columns) makes the command open, migrate forward, read
// the manifest, and pump it to the target. Without the additive migration
// the open or the manifest read fails on a missing column and the command
// exits non-zero.
//
// The sqlite3 driver is registered transitively by the store package, which
// other e2e files already import, so the legacy database is crafted with a
// plain database/sql connection.
func TestForwardMigration_MigrateToStandardReadsLegacySQLite(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	srcDB := filepath.Join(home, "src.db")
	srcObjects := filepath.Join(home, "src-objects")
	dstDB := filepath.Join(home, "dst.db")
	dstObjects := filepath.Join(home, "dst-objects")

	if err := os.MkdirAll(srcObjects, 0o755); err != nil {
		t.Fatalf("mkdir srcObjects: %v", err)
	}

	// An earlier binary: reduced tenants and manifests tables holding the
	// standalone "default" tenant and one already-ingested artifact. The
	// post-initial columns (deleted_at, layer, skill_raw, and the rest) do
	// not exist yet.
	legacy, err := sql.Open("sqlite3", srcDB)
	if err != nil {
		t.Fatalf("open legacy sqlite: %v", err)
	}
	for _, stmt := range []string{
		`CREATE TABLE tenants (id TEXT PRIMARY KEY, name TEXT NOT NULL)`,
		`INSERT INTO tenants (id, name) VALUES ('default', 'acme')`,
		`CREATE TABLE manifests (
			tenant_id TEXT NOT NULL,
			artifact_id TEXT NOT NULL,
			version TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			type TEXT NOT NULL,
			ingested_at TEXT NOT NULL,
			PRIMARY KEY (tenant_id, artifact_id, version)
		)`,
		`INSERT INTO manifests (tenant_id, artifact_id, version, content_hash, type, ingested_at)
			VALUES ('default', 'legacy/skill', '1.0.0', 'sha256:legacy', 'skill', '2024-01-01T00:00:00Z')`,
	} {
		if _, err := legacy.Exec(stmt); err != nil {
			t.Fatalf("legacy exec %q: %v", stmt, err)
		}
	}
	_ = legacy.Close()

	res := runPodium(t, "", []string{"HOME=" + home},
		"admin", "migrate-to-standard",
		"--source-sqlite", srcDB,
		"--source-objects", srcObjects,
		"--target-store", "sqlite",
		"--target-sqlite", dstDB,
		"--target-objects-type", "filesystem",
		"--target-objects", dstObjects)
	if res.Exit != 0 {
		t.Fatalf("migrate-to-standard exit=%d (forward migration of the legacy source failed)\nstderr=%s\nstdout=%s",
			res.Exit, res.Stderr, res.Stdout)
	}

	// The target carries the manifest read out of the migrated legacy
	// source, proving the forward migration made the legacy row readable.
	dst, err := store.OpenSQLite(dstDB)
	if err != nil {
		t.Fatalf("open target SQLite: %v", err)
	}
	t.Cleanup(func() { _ = dst.Close() })
	manifests, err := dst.ListManifests(context.Background(), "default")
	if err != nil {
		t.Fatalf("ListManifests on target: %v", err)
	}
	if len(manifests) != 1 {
		t.Fatalf("target has %d manifests, want 1 (the migrated legacy artifact)", len(manifests))
	}
	if manifests[0].ArtifactID != "legacy/skill" || manifests[0].ContentHash != "sha256:legacy" {
		t.Errorf("migrated manifest = %q/%q, want legacy/skill/sha256:legacy",
			manifests[0].ArtifactID, manifests[0].ContentHash)
	}
}
