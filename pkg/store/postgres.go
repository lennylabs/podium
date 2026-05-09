package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

// Postgres is a Store implementation backed by Postgres via lib/pq.
// Used by the standard deployment per spec §13.10. The conformance
// suite (pkg/store/storetest) covers the same contract that Memory
// and SQLite satisfy; Postgres-specific tests gate on the
// PODIUM_POSTGRES_DSN env var so CI can run with or without a
// database.
type Postgres struct {
	db *sql.DB
}

// OpenPostgres opens a connection to the database at dsn. The DSN
// follows the lib/pq form, e.g.
// "postgres://user:pass@host:5432/db?sslmode=disable". The schema is
// applied on every open; existing schemas are unchanged thanks to
// IF NOT EXISTS.
func OpenPostgres(dsn string) (*Postgres, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	p := &Postgres{db: db}
	if err := p.applySchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return p, nil
}

// Close releases the underlying database connection pool.
func (p *Postgres) Close() error { return p.db.Close() }

// DB returns the underlying *sql.DB. Test fixtures use it to manage
// per-suite schemas (CREATE/DROP SCHEMA, TRUNCATE between runs);
// production code should not need it.
func (p *Postgres) DB() *sql.DB { return p.db }

// applySchema runs the schema migrations idempotently. The Postgres
// schema mirrors the SQLite layout but uses native types: BYTEA for
// blobs, BOOLEAN for bool fields, TIMESTAMPTZ for timestamps.
func (p *Postgres) applySchema() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS tenants (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			storage_quota BIGINT NOT NULL DEFAULT 0,
			search_qps_quota BIGINT NOT NULL DEFAULT 0,
			materialize_rate_quota BIGINT NOT NULL DEFAULT 0,
			audit_volume_quota BIGINT NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS manifests (
			tenant_id TEXT NOT NULL,
			artifact_id TEXT NOT NULL,
			version TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			type TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			tags TEXT NOT NULL DEFAULT '',
			sensitivity TEXT NOT NULL DEFAULT '',
			layer TEXT NOT NULL DEFAULT '',
			deprecated BOOLEAN NOT NULL DEFAULT FALSE,
			ingested_at TIMESTAMPTZ NOT NULL,
			frontmatter BYTEA,
			body BYTEA,
			extends_pin TEXT NOT NULL DEFAULT '',
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
			granted_at TIMESTAMPTZ NOT NULL,
			PRIMARY KEY (user_id, org_id)
		)`,
		`CREATE TABLE IF NOT EXISTS layer_configs (
			tenant_id TEXT NOT NULL,
			id TEXT NOT NULL,
			source_type TEXT NOT NULL,
			repo TEXT NOT NULL DEFAULT '',
			ref TEXT NOT NULL DEFAULT '',
			root TEXT NOT NULL DEFAULT '',
			local_path TEXT NOT NULL DEFAULT '',
			ord BIGINT NOT NULL DEFAULT 0,
			user_defined BOOLEAN NOT NULL DEFAULT FALSE,
			owner TEXT NOT NULL DEFAULT '',
			public BOOLEAN NOT NULL DEFAULT FALSE,
			organization BOOLEAN NOT NULL DEFAULT FALSE,
			groups TEXT NOT NULL DEFAULT '',
			users TEXT NOT NULL DEFAULT '',
			webhook_secret TEXT NOT NULL DEFAULT '',
			last_ingested_ref TEXT NOT NULL DEFAULT '',
			force_push_policy TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL,
			PRIMARY KEY (tenant_id, id)
		)`,
	}
	for _, sql := range stmts {
		if _, err := p.db.Exec(sql); err != nil {
			return fmt.Errorf("schema: %w (statement: %s)", err, sql)
		}
	}
	return nil
}

// CreateTenant inserts a new tenant. Inserting an existing tenant ID
// is a no-op (matches Memory and SQLite).
func (p *Postgres) CreateTenant(ctx context.Context, t Tenant) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO tenants
			(id, name, storage_quota, search_qps_quota, materialize_rate_quota, audit_volume_quota)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO NOTHING`,
		t.ID, t.Name,
		t.Quota.StorageBytes, t.Quota.SearchQPS,
		t.Quota.MaterializeRate, t.Quota.AuditVolumePerDay)
	return err
}

// GetTenant returns the tenant or ErrTenantNotFound.
func (p *Postgres) GetTenant(ctx context.Context, id string) (Tenant, error) {
	row := p.db.QueryRowContext(ctx, `
		SELECT id, name, storage_quota, search_qps_quota, materialize_rate_quota, audit_volume_quota
		FROM tenants WHERE id = $1`, id)
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
// (tenant, id, version) with a different content hash is rejected.
// Same content hash is idempotent.
func (p *Postgres) PutManifest(ctx context.Context, rec ManifestRecord) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(ctx, `
		SELECT content_hash FROM manifests
		WHERE tenant_id = $1 AND artifact_id = $2 AND version = $3`,
		rec.TenantID, rec.ArtifactID, rec.Version)
	var existing string
	err = row.Scan(&existing)
	switch {
	case err == nil:
		if existing != rec.ContentHash {
			return ErrImmutableViolation
		}
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
			 tags, sensitivity, layer, deprecated, ingested_at, frontmatter, body,
			 extends_pin)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
		rec.TenantID, rec.ArtifactID, rec.Version, rec.ContentHash,
		rec.Type, rec.Description,
		strings.Join(rec.Tags, "\n"),
		rec.Sensitivity, rec.Layer,
		rec.Deprecated, ingestedAt.UTC(),
		rec.Frontmatter, rec.Body,
		rec.ExtendsPin)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// GetManifest returns the manifest or ErrNotFound.
func (p *Postgres) GetManifest(ctx context.Context, tenantID, artifactID, version string) (ManifestRecord, error) {
	row := p.db.QueryRowContext(ctx, `
		SELECT tenant_id, artifact_id, version, content_hash, type, description,
		       tags, sensitivity, layer, deprecated, ingested_at, frontmatter, body,
		       extends_pin
		FROM manifests
		WHERE tenant_id = $1 AND artifact_id = $2 AND version = $3`,
		tenantID, artifactID, version)
	rec, err := scanManifestPG(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ManifestRecord{}, ErrNotFound
	}
	return rec, err
}

// ListManifests returns every manifest for the tenant, ordered by
// artifact ID then version.
func (p *Postgres) ListManifests(ctx context.Context, tenantID string) ([]ManifestRecord, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT tenant_id, artifact_id, version, content_hash, type, description,
		       tags, sensitivity, layer, deprecated, ingested_at, frontmatter, body,
		       extends_pin
		FROM manifests
		WHERE tenant_id = $1
		ORDER BY artifact_id ASC, version ASC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ManifestRecord{}
	for rows.Next() {
		rec, err := scanManifestPG(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// PutDependency records a dependency edge.
func (p *Postgres) PutDependency(ctx context.Context, tenantID string, edge DependencyEdge) error {
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO dependencies (tenant_id, from_artifact, to_artifact, kind)
		VALUES ($1, $2, $3, $4)`,
		tenantID, edge.From, edge.To, edge.Kind)
	return err
}

// DependentsOf returns every edge whose To matches artifactID.
func (p *Postgres) DependentsOf(ctx context.Context, tenantID, artifactID string) ([]DependencyEdge, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT from_artifact, to_artifact, kind
		FROM dependencies
		WHERE tenant_id = $1 AND to_artifact = $2
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
func (p *Postgres) GrantAdmin(ctx context.Context, g AdminGrant) error {
	granted := g.Granted
	if granted.IsZero() {
		granted = time.Now().UTC()
	}
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO admin_grants (user_id, org_id, granted_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, org_id) DO UPDATE SET granted_at = EXCLUDED.granted_at`,
		g.UserID, g.OrgID, granted.UTC())
	return err
}

// IsAdmin checks the admin grant table.
func (p *Postgres) IsAdmin(ctx context.Context, userID, orgID string) (bool, error) {
	row := p.db.QueryRowContext(ctx, `
		SELECT 1 FROM admin_grants WHERE user_id = $1 AND org_id = $2`,
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

// PutLayerConfig inserts or replaces a layer config.
func (p *Postgres) PutLayerConfig(ctx context.Context, cfg LayerConfig) error {
	createdAt := cfg.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO layer_configs
			(tenant_id, id, source_type, repo, ref, root, local_path, ord,
			 user_defined, owner, public, organization, groups, users,
			 webhook_secret, last_ingested_ref, force_push_policy, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
		ON CONFLICT (tenant_id, id) DO UPDATE SET
			source_type = EXCLUDED.source_type,
			repo = EXCLUDED.repo,
			ref = EXCLUDED.ref,
			root = EXCLUDED.root,
			local_path = EXCLUDED.local_path,
			ord = EXCLUDED.ord,
			user_defined = EXCLUDED.user_defined,
			owner = EXCLUDED.owner,
			public = EXCLUDED.public,
			organization = EXCLUDED.organization,
			groups = EXCLUDED.groups,
			users = EXCLUDED.users,
			webhook_secret = EXCLUDED.webhook_secret,
			last_ingested_ref = EXCLUDED.last_ingested_ref,
			force_push_policy = EXCLUDED.force_push_policy,
			created_at = EXCLUDED.created_at`,
		cfg.TenantID, cfg.ID, cfg.SourceType, cfg.Repo, cfg.Ref, cfg.Root, cfg.LocalPath,
		cfg.Order, cfg.UserDefined, cfg.Owner,
		cfg.Public, cfg.Organization,
		strings.Join(cfg.Groups, "\n"), strings.Join(cfg.Users, "\n"),
		cfg.WebhookSecret, cfg.LastIngestedRef, cfg.ForcePushPolicy,
		createdAt.UTC())
	return err
}

// GetLayerConfig returns one layer config or ErrNotFound.
func (p *Postgres) GetLayerConfig(ctx context.Context, tenantID, id string) (LayerConfig, error) {
	row := p.db.QueryRowContext(ctx, `
		SELECT tenant_id, id, source_type, repo, ref, root, local_path, ord,
		       user_defined, owner, public, organization, groups, users,
		       webhook_secret, last_ingested_ref, force_push_policy, created_at
		FROM layer_configs
		WHERE tenant_id = $1 AND id = $2`, tenantID, id)
	cfg, err := scanLayerConfigPG(row)
	if errors.Is(err, sql.ErrNoRows) {
		return LayerConfig{}, ErrNotFound
	}
	return cfg, err
}

// ListLayerConfigs returns every layer for the tenant in Order ascending.
func (p *Postgres) ListLayerConfigs(ctx context.Context, tenantID string) ([]LayerConfig, error) {
	rows, err := p.db.QueryContext(ctx, `
		SELECT tenant_id, id, source_type, repo, ref, root, local_path, ord,
		       user_defined, owner, public, organization, groups, users,
		       webhook_secret, last_ingested_ref, force_push_policy, created_at
		FROM layer_configs WHERE tenant_id = $1
		ORDER BY ord ASC, id ASC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LayerConfig{}
	for rows.Next() {
		cfg, err := scanLayerConfigPG(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, cfg)
	}
	return out, rows.Err()
}

// DeleteLayerConfig removes a layer.
func (p *Postgres) DeleteLayerConfig(ctx context.Context, tenantID, id string) error {
	_, err := p.db.ExecContext(ctx, `
		DELETE FROM layer_configs WHERE tenant_id = $1 AND id = $2`,
		tenantID, id)
	return err
}

// scanManifestPG scans a manifest row from Postgres. Differs from
// the SQLite scanner in two ways: deprecated is BOOLEAN (scanned
// directly into bool) and ingested_at is TIMESTAMPTZ (scanned into
// time.Time). Frontmatter and Body come back as []byte directly.
func scanManifestPG(scanner rowScanner) (ManifestRecord, error) {
	var rec ManifestRecord
	var tags string
	err := scanner.Scan(
		&rec.TenantID, &rec.ArtifactID, &rec.Version, &rec.ContentHash,
		&rec.Type, &rec.Description, &tags, &rec.Sensitivity, &rec.Layer,
		&rec.Deprecated, &rec.IngestedAt, &rec.Frontmatter, &rec.Body,
		&rec.ExtendsPin)
	if err != nil {
		return ManifestRecord{}, err
	}
	if tags != "" {
		rec.Tags = strings.Split(tags, "\n")
	}
	return rec, nil
}

// scanLayerConfigPG scans a layer config row from Postgres. Bool
// columns are scanned directly; timestamps come back as time.Time.
func scanLayerConfigPG(scanner rowScanner) (LayerConfig, error) {
	var cfg LayerConfig
	var groups, users string
	err := scanner.Scan(
		&cfg.TenantID, &cfg.ID, &cfg.SourceType,
		&cfg.Repo, &cfg.Ref, &cfg.Root, &cfg.LocalPath,
		&cfg.Order, &cfg.UserDefined, &cfg.Owner,
		&cfg.Public, &cfg.Organization, &groups, &users,
		&cfg.WebhookSecret, &cfg.LastIngestedRef, &cfg.ForcePushPolicy,
		&cfg.CreatedAt)
	if err != nil {
		return LayerConfig{}, err
	}
	if groups != "" {
		cfg.Groups = strings.Split(groups, "\n")
	}
	if users != "" {
		cfg.Users = strings.Split(users, "\n")
	}
	return cfg, nil
}
