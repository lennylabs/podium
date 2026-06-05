package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// legacyExec opens a raw connection to the SQLite file at path and runs
// each statement, simulating a database initialized by an earlier binary
// whose CREATE TABLE carried fewer columns. The sqlite3 driver is already
// registered via the store package's blank import.
func legacyExec(t *testing.T, path string, stmts ...string) {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open legacy sqlite: %v", err)
	}
	defer db.Close()
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("legacy exec %q: %v", s, err)
		}
	}
}

// columnExists reports whether table has a column named col in the SQLite
// database at path.
func columnExists(t *testing.T, path, table, col string) bool {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatalf("open sqlite for pragma: %v", err)
	}
	defer db.Close()
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		t.Fatalf("pragma table_info(%s): %v", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("scan pragma row: %v", err)
		}
		if name == col {
			return true
		}
	}
	return false
}

// Spec: §13.4 Migrations — a binary upgrade must add columns
// that were introduced after a table first shipped. CREATE TABLE IF NOT
// EXISTS is a no-op on an existing table, so a database initialized by an
// earlier binary keeps its old column set. Here the legacy manifests table
// predates the deleted_at column (and every other post-initial column);
// opening it with the current binary must backfill those columns so the
// soft-delete and retention reads that reference deleted_at succeed instead
// of failing with a missing-column error.
func TestSQLite_AdditiveMigration_BackfillsManifestColumns(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "legacy.db")

	// An earlier binary created manifests with only the primary-key and
	// NOT-NULL-without-default columns it shipped with; deleted_at, layer,
	// skill_raw, resources, and the rest did not exist yet.
	legacyExec(t, path,
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
			VALUES ('t', 'a/x', '1.0.0', 'sha256:legacy', 'skill', '2024-01-01T00:00:00Z')`,
	)

	// Precondition: the legacy database genuinely lacks deleted_at.
	if columnExists(t, path, "manifests", "deleted_at") {
		t.Fatal("precondition failed: legacy manifests already has deleted_at")
	}

	// The current binary opens the database and migrates it forward in
	// place. Without the additive migration this fails outright, because the
	// idx_manifests_tenant_layer index references the absent layer column.
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite on legacy database: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// The post-initial columns are now present.
	for _, col := range []string{"deleted_at", "deprecated_at", "layer", "skill_raw", "resources", "search_visibility"} {
		if !columnExists(t, path, "manifests", col) {
			t.Errorf("column %q not backfilled by additive migration", col)
		}
	}

	ctx := context.Background()

	// The soft-delete read path (deleted_at IS NULL) that the finding names
	// now runs against the migrated row instead of erroring.
	got, err := s.GetManifest(ctx, "t", "a/x", "1.0.0")
	if err != nil {
		t.Fatalf("GetManifest on migrated legacy row: %v", err)
	}
	if got.ContentHash != "sha256:legacy" {
		t.Errorf("ContentHash = %q, want sha256:legacy", got.ContentHash)
	}

	list, err := s.ListManifests(ctx, "t")
	if err != nil {
		t.Fatalf("ListManifests on migrated database: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListManifests returned %d rows, want 1", len(list))
	}

	// The retention read (PurgeDeprecatedManifests references deprecated_at)
	// must also run without a missing-column error.
	if _, err := s.PurgeDeprecatedManifests(ctx, time.Now()); err != nil {
		t.Fatalf("PurgeDeprecatedManifests on migrated database: %v", err)
	}
}

// Spec: §13.4 Migrations — the layer_configs table gained
// webhook_secret, force_push_policy, git_provider, last_ingested_at, and
// the soft-delete deleted_at column after it first shipped. A legacy
// database missing them must be migrated forward so the layer reads that
// select those columns do not fail.
func TestSQLite_AdditiveMigration_BackfillsLayerConfigColumns(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "legacy.db")

	legacyExec(t, path,
		`CREATE TABLE layer_configs (
			tenant_id TEXT NOT NULL,
			id TEXT NOT NULL,
			source_type TEXT NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY (tenant_id, id)
		)`,
		`INSERT INTO layer_configs (tenant_id, id, source_type, created_at)
			VALUES ('t', 'team-shared', 'git', '2024-01-01T00:00:00Z')`,
	)
	if columnExists(t, path, "layer_configs", "webhook_secret") {
		t.Fatal("precondition failed: legacy layer_configs already has webhook_secret")
	}

	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite on legacy database: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	ctx := context.Background()
	cfg, err := s.GetLayerConfig(ctx, "t", "team-shared")
	if err != nil {
		t.Fatalf("GetLayerConfig on migrated legacy row: %v", err)
	}
	if cfg.SourceType != "git" {
		t.Errorf("SourceType = %q, want git", cfg.SourceType)
	}
	if cfg.WebhookSecret != "" {
		t.Errorf("WebhookSecret = %q, want empty (backfilled default)", cfg.WebhookSecret)
	}

	// The §8.4 soft-delete writes deleted_at on both layer_configs and the
	// layer's manifests; the migration must have added deleted_at so the
	// UPDATE does not hit a missing column.
	if err := s.DeleteLayerConfig(ctx, "t", "team-shared"); err != nil {
		t.Fatalf("DeleteLayerConfig (soft-delete) on migrated database: %v", err)
	}
	deleted, err := s.ListDeletedLayerConfigs(ctx, "t")
	if err != nil {
		t.Fatalf("ListDeletedLayerConfigs: %v", err)
	}
	if len(deleted) != 1 {
		t.Fatalf("ListDeletedLayerConfigs returned %d, want 1", len(deleted))
	}
}

// Spec: §13.4 Migrations — every additive column declared for forward
// migration must correspond to a real column in the current CREATE TABLE
// schema. A typo or a stale table name would silently fail to migrate the
// intended column, so this guard opens a fresh database (all columns
// created) and asserts each additive entry names an existing column.
func TestSQLite_AdditiveColumnsMatchSchema(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "fresh.db")
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	for _, c := range additiveColumns {
		col := strings.Fields(c.sqliteDef)[0]
		if !columnExists(t, path, c.table, col) {
			t.Errorf("additive column %s.%s is not present in the fresh schema (typo or stale table name?)", c.table, col)
		}
	}
}

// Spec: §13.4 Migrations — the Postgres metadata store must add
// columns to a table created by an earlier binary. The test creates legacy
// tables in an isolated schema, runs the additive migration, and asserts the
// post-initial columns appear. Gated on PODIUM_POSTGRES_DSN like the rest of
// the Postgres suite. A single pooled connection keeps the per-session
// search_path stable across statements.
func TestPostgres_AdditiveMigration(t *testing.T) {
	dsn := os.Getenv("PODIUM_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PODIUM_POSTGRES_DSN unset; skipping Postgres additive-migration check")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Skipf("open postgres: %v", err)
	}
	if err := db.Ping(); err != nil {
		t.Skipf("ping postgres: %v (database unreachable)", err)
	}
	db.SetMaxOpenConns(1) // keep search_path on one session
	t.Cleanup(func() { _ = db.Close() })

	const schema = "podium_additive_migrate_test"
	ctx := context.Background()
	exec := func(q string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schema))
	exec(fmt.Sprintf("CREATE SCHEMA %s", schema))
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schema))
	})
	exec(fmt.Sprintf("SET search_path TO %s", schema))

	// Legacy tables: only the primary-key and NOT-NULL-without-default
	// columns the earlier binary shipped with. applyAdditivePostgres alters
	// every table named in additiveColumns, so all of them must exist.
	exec(`CREATE TABLE tenants (id TEXT PRIMARY KEY, name TEXT NOT NULL)`)
	exec(`CREATE TABLE manifests (
		tenant_id TEXT NOT NULL, artifact_id TEXT NOT NULL, version TEXT NOT NULL,
		content_hash TEXT NOT NULL, type TEXT NOT NULL, ingested_at TIMESTAMPTZ NOT NULL,
		PRIMARY KEY (tenant_id, artifact_id, version))`)
	exec(`CREATE TABLE domains (tenant_id TEXT NOT NULL, layer TEXT NOT NULL, path TEXT NOT NULL,
		PRIMARY KEY (tenant_id, layer, path))`)
	exec(`CREATE TABLE layer_configs (tenant_id TEXT NOT NULL, id TEXT NOT NULL,
		source_type TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL, PRIMARY KEY (tenant_id, id))`)
	exec(`CREATE TABLE vector_pending (tenant_id TEXT NOT NULL, artifact_id TEXT NOT NULL,
		version TEXT NOT NULL, text BYTEA NOT NULL, enqueued_at TIMESTAMPTZ NOT NULL,
		next_retry_at TIMESTAMPTZ NOT NULL, PRIMARY KEY (tenant_id, artifact_id, version))`)
	exec(`INSERT INTO manifests (tenant_id, artifact_id, version, content_hash, type, ingested_at)
		VALUES ('t', 'a/x', '1.0.0', 'sha256:legacy', 'skill', now())`)

	if err := applyAdditivePostgres(db); err != nil {
		t.Fatalf("applyAdditivePostgres: %v", err)
	}
	// Re-run must be a clean no-op (ADD COLUMN IF NOT EXISTS).
	if err := applyAdditivePostgres(db); err != nil {
		t.Fatalf("applyAdditivePostgres (second run): %v", err)
	}

	pgColumnExists := func(table, col string) bool {
		var n int
		err := db.QueryRowContext(ctx,
			`SELECT count(*) FROM information_schema.columns
			 WHERE table_schema = $1 AND table_name = $2 AND column_name = $3`,
			schema, table, col).Scan(&n)
		if err != nil {
			t.Fatalf("information_schema lookup %s.%s: %v", table, col, err)
		}
		return n > 0
	}
	for _, c := range additiveColumns {
		col := strings.Fields(c.pgDef)[0]
		if !pgColumnExists(c.table, col) {
			t.Errorf("additive column %s.%s not present after Postgres migration", c.table, col)
		}
	}

	// Scan-safety: the legacy row backfilled to non-NULL defaults for the
	// columns the scan path reads into non-nullable Go types.
	var desc string
	var deletedAt sql.NullTime
	if err := db.QueryRowContext(ctx,
		`SELECT description, deleted_at FROM manifests WHERE artifact_id = 'a/x'`).Scan(&desc, &deletedAt); err != nil {
		t.Fatalf("select backfilled columns from legacy row: %v", err)
	}
	if desc != "" {
		t.Errorf("description = %q, want empty backfilled default", desc)
	}
	if deletedAt.Valid {
		t.Errorf("deleted_at = %v, want NULL on a legacy row", deletedAt)
	}
}
