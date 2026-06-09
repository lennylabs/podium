package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/lib/pq"
)

// additiveColumn describes a non-primary-key column that a binary upgrade
// may need to add to a table created by an earlier binary. Spec §13.4
// requires each backend's setup to be additive: "tables are created when
// absent and new columns are added when absent ... so a binary upgrade
// migrates an existing database forward in place." CREATE TABLE IF NOT
// EXISTS only creates the whole table; it is a no-op on a table that
// already exists, so a column added after a table first shipped is never
// backfilled by the CREATE alone. These ALTER statements perform the
// missing forward migration.
//
// Every listed column is nullable or carries a non-NULL default, so
// ALTER TABLE ADD COLUMN can run against a table that already has rows.
// Columns that already existed at a table's initial ship remain listed
// here harmlessly: the idempotent guard turns an already-present column
// into a no-op (ADD COLUMN IF NOT EXISTS on Postgres, duplicate-column
// tolerance on SQLite). Columns that the scan path reads into a
// non-nullable Go type carry a non-NULL default so a row inserted before
// the column existed backfills to a scannable value rather than NULL.
//
// When a column is added to a CREATE TABLE in sqlite.go or postgres.go,
// add it here too so an in-place upgrade backfills it on an existing
// database.
type additiveColumn struct {
	table     string
	sqliteDef string // "<name> <sqlite-type> [constraints]" for ALTER TABLE ADD COLUMN
	pgDef     string // "<name> <postgres-type> [constraints]" for ALTER TABLE ADD COLUMN
}

// additiveColumns is the single source of truth for §13.4 forward column
// migration across both SQL metadata stores. Primary-key columns and the
// NOT-NULL-without-default columns present at a table's initial creation
// are intentionally absent: they can never be missing from a table that
// exists, and a NOT-NULL-without-default column cannot be added to a
// populated table.
var additiveColumns = []additiveColumn{
	// tenants
	{"tenants", "storage_quota INTEGER NOT NULL DEFAULT 0", "storage_quota BIGINT NOT NULL DEFAULT 0"},
	{"tenants", "search_qps_quota INTEGER NOT NULL DEFAULT 0", "search_qps_quota BIGINT NOT NULL DEFAULT 0"},
	{"tenants", "materialize_rate_quota INTEGER NOT NULL DEFAULT 0", "materialize_rate_quota BIGINT NOT NULL DEFAULT 0"},
	{"tenants", "audit_volume_quota INTEGER NOT NULL DEFAULT 0", "audit_volume_quota BIGINT NOT NULL DEFAULT 0"},
	{"tenants", "max_user_layers INTEGER NOT NULL DEFAULT 0", "max_user_layers BIGINT NOT NULL DEFAULT 0"},
	{"tenants", "expose_scope_preview INTEGER", "expose_scope_preview BOOLEAN"},
	{"tenants", "active INTEGER NOT NULL DEFAULT 1", "active BOOLEAN NOT NULL DEFAULT TRUE"},

	// manifests
	{"manifests", "description TEXT NOT NULL DEFAULT ''", "description TEXT NOT NULL DEFAULT ''"},
	{"manifests", "tags TEXT NOT NULL DEFAULT ''", "tags TEXT NOT NULL DEFAULT ''"},
	{"manifests", "sensitivity TEXT NOT NULL DEFAULT ''", "sensitivity TEXT NOT NULL DEFAULT ''"},
	{"manifests", "layer TEXT NOT NULL DEFAULT ''", "layer TEXT NOT NULL DEFAULT ''"},
	{"manifests", "deprecated INTEGER NOT NULL DEFAULT 0", "deprecated BOOLEAN NOT NULL DEFAULT FALSE"},
	{"manifests", "frontmatter BLOB", "frontmatter BYTEA"},
	{"manifests", "body BLOB", "body BYTEA"},
	{"manifests", "skill_raw BLOB", "skill_raw BYTEA"},
	{"manifests", "extends_pin TEXT NOT NULL DEFAULT ''", "extends_pin TEXT NOT NULL DEFAULT ''"},
	{"manifests", "signature TEXT NOT NULL DEFAULT ''", "signature TEXT NOT NULL DEFAULT ''"},
	{"manifests", "search_visibility TEXT NOT NULL DEFAULT ''", "search_visibility TEXT NOT NULL DEFAULT ''"},
	{"manifests", "resources BLOB", "resources BYTEA"},
	{"manifests", "deprecated_at TEXT", "deprecated_at TIMESTAMPTZ"},
	{"manifests", "deleted_at TEXT", "deleted_at TIMESTAMPTZ"},

	// domains
	{"domains", "raw BLOB", "raw BYTEA"},

	// layer_configs
	{"layer_configs", "repo TEXT NOT NULL DEFAULT ''", "repo TEXT NOT NULL DEFAULT ''"},
	{"layer_configs", "ref TEXT NOT NULL DEFAULT ''", "ref TEXT NOT NULL DEFAULT ''"},
	{"layer_configs", "root TEXT NOT NULL DEFAULT ''", "root TEXT NOT NULL DEFAULT ''"},
	{"layer_configs", "local_path TEXT NOT NULL DEFAULT ''", "local_path TEXT NOT NULL DEFAULT ''"},
	{"layer_configs", "ord INTEGER NOT NULL DEFAULT 0", "ord BIGINT NOT NULL DEFAULT 0"},
	{"layer_configs", "user_defined INTEGER NOT NULL DEFAULT 0", "user_defined BOOLEAN NOT NULL DEFAULT FALSE"},
	{"layer_configs", "owner TEXT NOT NULL DEFAULT ''", "owner TEXT NOT NULL DEFAULT ''"},
	{"layer_configs", "public INTEGER NOT NULL DEFAULT 0", "public BOOLEAN NOT NULL DEFAULT FALSE"},
	{"layer_configs", "organization INTEGER NOT NULL DEFAULT 0", "organization BOOLEAN NOT NULL DEFAULT FALSE"},
	{"layer_configs", "groups TEXT NOT NULL DEFAULT ''", "groups TEXT NOT NULL DEFAULT ''"},
	{"layer_configs", "users TEXT NOT NULL DEFAULT ''", "users TEXT NOT NULL DEFAULT ''"},
	{"layer_configs", "webhook_secret TEXT NOT NULL DEFAULT ''", "webhook_secret TEXT NOT NULL DEFAULT ''"},
	{"layer_configs", "last_ingested_ref TEXT NOT NULL DEFAULT ''", "last_ingested_ref TEXT NOT NULL DEFAULT ''"},
	{"layer_configs", "force_push_policy TEXT NOT NULL DEFAULT ''", "force_push_policy TEXT NOT NULL DEFAULT ''"},
	{"layer_configs", "git_provider TEXT NOT NULL DEFAULT ''", "git_provider TEXT NOT NULL DEFAULT ''"},
	{"layer_configs", "deleted_at TEXT", "deleted_at TIMESTAMPTZ"},
	{"layer_configs", "last_ingested_at TEXT", "last_ingested_at TIMESTAMPTZ"},

	// vector_pending
	{"vector_pending", "attempts INTEGER NOT NULL DEFAULT 0", "attempts BIGINT NOT NULL DEFAULT 0"},
	{"vector_pending", "last_error TEXT NOT NULL DEFAULT ''", "last_error TEXT NOT NULL DEFAULT ''"},
}

// applyAdditiveSQLite backfills any §13.4 additive column missing from an
// existing SQLite database. SQLite has no ADD COLUMN IF NOT EXISTS, so
// each ALTER tolerates the duplicate-column error on an already-current
// database, mirroring the vector backend (pkg/vector/sqlitevec.go).
func applyAdditiveSQLite(db *sql.DB) error {
	for _, c := range additiveColumns {
		stmt := fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s`, c.table, c.sqliteDef)
		if _, err := db.Exec(stmt); err != nil && !isDuplicateColumn(err) {
			return fmt.Errorf("schema: %s: %w", stmt, err)
		}
	}
	return nil
}

// execContexter is the subset of *sql.DB / *sql.Conn the additive migration
// runs against. The §4.7.1 schema-per-org split applies the migration both on
// the pooled DB (shared tables in public) and on a per-org connection pinned to
// an org schema, so the migration must accept either handle.
type execContexter interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// applyAdditivePostgres backfills any §13.4 additive column missing from an
// existing Postgres database using the native ADD COLUMN IF NOT EXISTS
// guard, which makes a re-run against a current schema a no-op.
func applyAdditivePostgres(db *sql.DB) error {
	return applyAdditivePostgresCtx(context.Background(), db)
}

// applyAdditivePostgresCtx runs the additive migration against ex. Under the
// §4.7.1 schema-per-org layout no single schema context holds every table the
// additiveColumns list names (org tables live in per-org schemas, shared tables
// in public), so a table absent from the current search_path is skipped rather
// than treated as an error: there is nothing to migrate for a table that does
// not exist in this context.
func applyAdditivePostgresCtx(ctx context.Context, ex execContexter) error {
	for _, c := range additiveColumns {
		stmt := fmt.Sprintf(`ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s`, c.table, c.pgDef)
		if _, err := ex.ExecContext(ctx, stmt); err != nil {
			if isUndefinedTable(err) {
				continue
			}
			return fmt.Errorf("schema: %s: %w", stmt, err)
		}
	}
	return nil
}

// isUndefinedTable reports whether err is Postgres undefined_table (SQLSTATE
// 42P01), raised when an ALTER targets a table that is not visible in the
// current search_path.
func isUndefinedTable(err error) bool {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return pqErr.Code == "42P01"
	}
	return false
}

// isDuplicateColumn reports whether err is SQLite's "duplicate column
// name" error, raised when ALTER TABLE ADD COLUMN targets a column that
// already exists.
func isDuplicateColumn(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate column name")
}
