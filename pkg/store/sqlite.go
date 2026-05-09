package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// SQLite is a Store implementation backed by a SQLite file or
// in-memory database. Used by the standalone deployment per
// spec §13.10 (default backend) and by integration tests that need a
// realistic SQL backing.
type SQLite struct {
	db *sql.DB
}

// OpenSQLite opens (or creates) the SQLite database at path. Use
// ":memory:" for an in-memory database. The schema is applied on every
// open; existing schemas are unchanged thanks to IF NOT EXISTS.
func OpenSQLite(path string) (*SQLite, error) {
	dsn := path + "?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=ON"
	if path == ":memory:" {
		dsn = "file::memory:?cache=shared"
	}
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	s := &SQLite{db: db}
	if err := s.applySchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the underlying database connection.
func (s *SQLite) Close() error { return s.db.Close() }

// applySchema runs the schema migrations idempotently.
func (s *SQLite) applySchema() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS tenants (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			storage_quota INTEGER NOT NULL DEFAULT 0,
			search_qps_quota INTEGER NOT NULL DEFAULT 0,
			materialize_rate_quota INTEGER NOT NULL DEFAULT 0,
			audit_volume_quota INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS manifests (
			tenant_id TEXT NOT NULL,
			artifact_id TEXT NOT NULL,
			version TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			type TEXT NOT NULL,
			description TEXT,
			tags TEXT,
			sensitivity TEXT,
			layer TEXT,
			deprecated INTEGER NOT NULL DEFAULT 0,
			ingested_at TEXT NOT NULL,
			frontmatter BLOB,
			body BLOB,
			PRIMARY KEY (tenant_id, artifact_id, version)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_manifests_tenant_type
			ON manifests(tenant_id, type)`,
		`CREATE INDEX IF NOT EXISTS idx_manifests_tenant_layer
			ON manifests(tenant_id, layer)`,
		`CREATE TABLE IF NOT EXISTS dependencies (
			tenant_id TEXT NOT NULL,
			from_artifact TEXT NOT NULL,
			to_artifact TEXT NOT NULL,
			kind TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_deps_to
			ON dependencies(tenant_id, to_artifact)`,
		`CREATE TABLE IF NOT EXISTS admin_grants (
			user_id TEXT NOT NULL,
			org_id TEXT NOT NULL,
			granted_at TEXT NOT NULL,
			PRIMARY KEY (user_id, org_id)
		)`,
	}
	for _, sql := range stmts {
		if _, err := s.db.Exec(sql); err != nil {
			return fmt.Errorf("schema: %w (statement: %s)", err, sql)
		}
	}
	return nil
}

// CreateTenant inserts a new tenant. Inserting an existing tenant ID
// is a no-op (matches Memory).
func (s *SQLite) CreateTenant(ctx context.Context, t Tenant) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO tenants
			(id, name, storage_quota, search_qps_quota, materialize_rate_quota, audit_volume_quota)
		VALUES (?, ?, ?, ?, ?, ?)`,
		t.ID, t.Name,
		t.Quota.StorageBytes, t.Quota.SearchQPS,
		t.Quota.MaterializeRate, t.Quota.AuditVolumePerDay)
	return err
}

// GetTenant returns the tenant or ErrTenantNotFound.
func (s *SQLite) GetTenant(ctx context.Context, id string) (Tenant, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, storage_quota, search_qps_quota, materialize_rate_quota, audit_volume_quota
		FROM tenants WHERE id = ?`, id)
	var t Tenant
	err := row.Scan(&t.ID, &t.Name,
		&t.Quota.StorageBytes, &t.Quota.SearchQPS,
		&t.Quota.MaterializeRate, &t.Quota.AuditVolumePerDay)
	if errors.Is(err, sql.ErrNoRows) {
		return Tenant{}, ErrTenantNotFound
	}
	return t, err
}

// PutManifest enforces the §4.7 immutability invariant: the same
// (tenant, id, version) with a different content hash is rejected; the
// same content hash is a no-op.
func (s *SQLite) PutManifest(ctx context.Context, rec ManifestRecord) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(ctx, `
		SELECT content_hash FROM manifests
		WHERE tenant_id = ? AND artifact_id = ? AND version = ?`,
		rec.TenantID, rec.ArtifactID, rec.Version)
	var existing string
	err = row.Scan(&existing)
	switch {
	case err == nil:
		if existing != rec.ContentHash {
			return ErrImmutableViolation
		}
		// Idempotent — same hash, no-op.
		return tx.Commit()
	case errors.Is(err, sql.ErrNoRows):
		// Insert below.
	default:
		return err
	}

	ingestedAt := rec.IngestedAt
	if ingestedAt.IsZero() {
		ingestedAt = time.Now().UTC()
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO manifests
			(tenant_id, artifact_id, version, content_hash, type, description,
			 tags, sensitivity, layer, deprecated, ingested_at, frontmatter, body)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.TenantID, rec.ArtifactID, rec.Version, rec.ContentHash,
		rec.Type, rec.Description,
		strings.Join(rec.Tags, "\n"),
		rec.Sensitivity, rec.Layer,
		boolToInt(rec.Deprecated),
		ingestedAt.UTC().Format(time.RFC3339Nano),
		rec.Frontmatter, rec.Body)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// GetManifest returns the manifest or ErrNotFound.
func (s *SQLite) GetManifest(ctx context.Context, tenantID, artifactID, version string) (ManifestRecord, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT tenant_id, artifact_id, version, content_hash, type, description,
		       tags, sensitivity, layer, deprecated, ingested_at, frontmatter, body
		FROM manifests
		WHERE tenant_id = ? AND artifact_id = ? AND version = ?`,
		tenantID, artifactID, version)
	rec, err := scanManifest(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ManifestRecord{}, ErrNotFound
	}
	return rec, err
}

// ListManifests returns every manifest for the tenant, ordered by
// artifact ID then version (matches Memory).
func (s *SQLite) ListManifests(ctx context.Context, tenantID string) ([]ManifestRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT tenant_id, artifact_id, version, content_hash, type, description,
		       tags, sensitivity, layer, deprecated, ingested_at, frontmatter, body
		FROM manifests
		WHERE tenant_id = ?
		ORDER BY artifact_id ASC, version ASC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ManifestRecord{}
	for rows.Next() {
		rec, err := scanManifest(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// PutDependency records a dependency edge.
func (s *SQLite) PutDependency(ctx context.Context, tenantID string, edge DependencyEdge) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO dependencies (tenant_id, from_artifact, to_artifact, kind)
		VALUES (?, ?, ?, ?)`,
		tenantID, edge.From, edge.To, edge.Kind)
	return err
}

// DependentsOf returns every edge whose To matches artifactID.
func (s *SQLite) DependentsOf(ctx context.Context, tenantID, artifactID string) ([]DependencyEdge, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT from_artifact, to_artifact, kind
		FROM dependencies
		WHERE tenant_id = ? AND to_artifact = ?
		ORDER BY from_artifact, kind`,
		tenantID, artifactID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DependencyEdge
	for rows.Next() {
		var e DependencyEdge
		if err := rows.Scan(&e.From, &e.To, &e.Kind); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GrantAdmin records an admin grant.
func (s *SQLite) GrantAdmin(ctx context.Context, g AdminGrant) error {
	granted := g.Granted
	if granted.IsZero() {
		granted = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO admin_grants (user_id, org_id, granted_at)
		VALUES (?, ?, ?)`,
		g.UserID, g.OrgID, granted.UTC().Format(time.RFC3339Nano))
	return err
}

// IsAdmin checks the admin grant table.
func (s *SQLite) IsAdmin(ctx context.Context, userID, orgID string) (bool, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT 1 FROM admin_grants WHERE user_id = ? AND org_id = ?`,
		userID, orgID)
	var dummy int
	err := row.Scan(&dummy)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// rowScanner is satisfied by *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanManifest(scanner rowScanner) (ManifestRecord, error) {
	var rec ManifestRecord
	var deprecated int
	var tags string
	var ingestedAt string
	err := scanner.Scan(
		&rec.TenantID, &rec.ArtifactID, &rec.Version, &rec.ContentHash,
		&rec.Type, &rec.Description, &tags, &rec.Sensitivity, &rec.Layer,
		&deprecated, &ingestedAt, &rec.Frontmatter, &rec.Body)
	if err != nil {
		return ManifestRecord{}, err
	}
	rec.Deprecated = deprecated != 0
	if tags != "" {
		rec.Tags = strings.Split(tags, "\n")
	}
	if ingestedAt != "" {
		rec.IngestedAt, _ = time.Parse(time.RFC3339Nano, ingestedAt)
	}
	return rec, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
