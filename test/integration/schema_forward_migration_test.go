package integration

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §13.4 Migrations — "a binary upgrade migrates an
// existing database forward in place, without a separate migration step."
// This drives the real ingest pipeline and the §8.4 soft-delete path
// against a SQLite database created by an earlier binary whose manifests
// table predates the post-initial columns (deleted_at, layer, skill_raw,
// resources, search_visibility, and the rest). Opening the database with
// the current binary must backfill those columns so ingest writes and the
// deleted_at-referencing reads succeed instead of failing with a
// missing-column error.
//
// The sqlite3 driver is registered transitively by the store package, so
// the legacy table is crafted with a plain database/sql connection.
func TestForwardMigration_IngestAndSoftDeleteOnLegacySQLite(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "legacy.db")

	// Simulate the earlier binary: a manifests table with only the
	// primary-key and NOT-NULL-without-default columns it shipped with,
	// holding one already-ingested artifact.
	legacy, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open legacy sqlite: %v", err)
	}
	for _, stmt := range []string{
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
			VALUES ('t', 'legacy/skill', '1.0.0', 'sha256:legacy', 'skill', '2024-01-01T00:00:00Z')`,
	} {
		if _, err := legacy.Exec(stmt); err != nil {
			t.Fatalf("legacy exec %q: %v", stmt, err)
		}
	}
	_ = legacy.Close()

	// The current binary opens and migrates the database forward in place.
	st, err := store.OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite on legacy database (forward migration failed): %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t", Name: "acme"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	// Ingest a new artifact through the real pipeline. The write path sets
	// every manifests column, so a database still missing them would fail
	// here rather than silently dropping data.
	body := []byte("---\ntype: context\nversion: 1.0.0\ndescription: x\nsensitivity: low\n---\n\nfresh body\n")
	res, err := ingest.Ingest(ctx, st, ingest.Request{
		TenantID: "t",
		LayerID:  "team-shared",
		Files:    fstest.MapFS{"fresh/ARTIFACT.md": &fstest.MapFile{Data: body}},
	})
	if err != nil {
		t.Fatalf("Ingest into migrated legacy database: %v", err)
	}
	if res.Accepted != 1 {
		t.Fatalf("Accepted = %d, want 1", res.Accepted)
	}

	// The read path (deleted_at IS NULL) returns the legacy row alongside
	// the freshly ingested one.
	list, err := st.ListManifests(ctx, "t")
	if err != nil {
		t.Fatalf("ListManifests on migrated database: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ListManifests returned %d rows, want 2 (legacy + ingested)", len(list))
	}

	// The §8.4 soft-delete writes deleted_at on the layer's manifests; on a
	// database missing deleted_at this is the missing-column failure the
	// finding names. Register the layer config so the delete has a target.
	if err := st.PutLayerConfig(ctx, store.LayerConfig{
		TenantID: "t", ID: "team-shared", SourceType: "git",
	}); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}
	if err := st.DeleteLayerConfig(ctx, "t", "team-shared"); err != nil {
		t.Fatalf("DeleteLayerConfig (soft-delete) on migrated database: %v", err)
	}

	// The ingested artifact is now tombstoned and hidden from normal reads;
	// the legacy row (in no layer) remains visible.
	after, err := st.ListManifests(ctx, "t")
	if err != nil {
		t.Fatalf("ListManifests after soft-delete: %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("ListManifests after soft-delete returned %d rows, want 1 (legacy only)", len(after))
	}
	if after[0].ArtifactID != "legacy/skill" {
		t.Errorf("remaining artifact = %q, want legacy/skill", after[0].ArtifactID)
	}
}
