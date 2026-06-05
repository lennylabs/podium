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
	memory := path == ":memory:"
	if memory {
		dsn = "file::memory:?cache=shared&_busy_timeout=5000"
	}
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// A shared-cache in-memory database is a single database with no WAL,
	// so concurrent writers contend on a table lock that the busy handler
	// cannot resolve (SQLITE_LOCKED, not SQLITE_BUSY). Serialize access
	// through one connection so concurrent PutManifest calls queue instead
	// of leaking "database table is locked". MaxIdleConns stays at its
	// default so the single connection is retained and the in-memory cache
	// is not torn down between calls. File-backed databases keep the
	// default pool: WAL plus _busy_timeout lets readers run concurrently
	// with a single writer.
	if memory {
		db.SetMaxOpenConns(1)
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

// applySchema runs the schema migrations idempotently. Per spec §13.4 the
// setup is additive: tables are created when absent (CREATE TABLE IF NOT
// EXISTS) and columns added after a table first shipped are backfilled by
// applyAdditiveSQLite, so a binary upgrade migrates an existing database
// forward in place without a separate migration step.
func (s *SQLite) applySchema() error {
	tableStmts := []string{
		`CREATE TABLE IF NOT EXISTS tenants (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			storage_quota INTEGER NOT NULL DEFAULT 0,
			search_qps_quota INTEGER NOT NULL DEFAULT 0,
			materialize_rate_quota INTEGER NOT NULL DEFAULT 0,
			audit_volume_quota INTEGER NOT NULL DEFAULT 0,
			max_user_layers INTEGER NOT NULL DEFAULT 0,
			expose_scope_preview INTEGER
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
			skill_raw BLOB,
			extends_pin TEXT NOT NULL DEFAULT '',
			signature TEXT NOT NULL DEFAULT '',
			search_visibility TEXT NOT NULL DEFAULT '',
			resources BLOB,
			deprecated_at TEXT,
			deleted_at TEXT,
			PRIMARY KEY (tenant_id, artifact_id, version)
		)`,
		`CREATE TABLE IF NOT EXISTS domains (
			tenant_id TEXT NOT NULL,
			layer TEXT NOT NULL,
			path TEXT NOT NULL,
			raw BLOB,
			PRIMARY KEY (tenant_id, layer, path)
		)`,
		`CREATE TABLE IF NOT EXISTS dependencies (
			tenant_id TEXT NOT NULL,
			from_artifact TEXT NOT NULL,
			to_artifact TEXT NOT NULL,
			kind TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS admin_grants (
			user_id TEXT NOT NULL,
			org_id TEXT NOT NULL,
			granted_at TEXT NOT NULL,
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
			ord INTEGER NOT NULL DEFAULT 0,
			user_defined INTEGER NOT NULL DEFAULT 0,
			owner TEXT NOT NULL DEFAULT '',
			public INTEGER NOT NULL DEFAULT 0,
			organization INTEGER NOT NULL DEFAULT 0,
			groups TEXT NOT NULL DEFAULT '',
			users TEXT NOT NULL DEFAULT '',
			webhook_secret TEXT NOT NULL DEFAULT '',
			last_ingested_ref TEXT NOT NULL DEFAULT '',
			force_push_policy TEXT NOT NULL DEFAULT '',
			git_provider TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			deleted_at TEXT,
			last_ingested_at TEXT,
			PRIMARY KEY (tenant_id, id)
		)`,
		`CREATE TABLE IF NOT EXISTS vector_pending (
			tenant_id TEXT NOT NULL,
			artifact_id TEXT NOT NULL,
			version TEXT NOT NULL,
			text BLOB NOT NULL,
			enqueued_at TEXT NOT NULL,
			attempts INTEGER NOT NULL DEFAULT 0,
			next_retry_at TEXT NOT NULL,
			last_error TEXT NOT NULL DEFAULT '',
			PRIMARY KEY (tenant_id, artifact_id, version)
		)`,
	}
	// Indexes are created after the additive column migration because
	// idx_manifests_tenant_layer references the `layer` column, which the
	// migration backfills on a database initialized by an earlier binary.
	indexStmts := []string{
		`CREATE INDEX IF NOT EXISTS idx_manifests_tenant_type
			ON manifests(tenant_id, type)`,
		`CREATE INDEX IF NOT EXISTS idx_manifests_tenant_layer
			ON manifests(tenant_id, layer)`,
		`CREATE INDEX IF NOT EXISTS idx_domains_tenant_path
			ON domains(tenant_id, path)`,
		`CREATE INDEX IF NOT EXISTS idx_deps_to
			ON dependencies(tenant_id, to_artifact)`,
		`CREATE INDEX IF NOT EXISTS idx_vector_pending_next_retry
			ON vector_pending(next_retry_at)`,
	}
	for _, sql := range tableStmts {
		if _, err := s.db.Exec(sql); err != nil {
			return fmt.Errorf("schema: %w (statement: %s)", err, sql)
		}
	}
	// §13.4 additive migration: CREATE TABLE IF NOT EXISTS is a no-op on a
	// table that already exists, so columns added after that table first
	// shipped are added here against a database initialized by an earlier
	// binary.
	if err := applyAdditiveSQLite(s.db); err != nil {
		return err
	}
	for _, sql := range indexStmts {
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
			(id, name, storage_quota, search_qps_quota, materialize_rate_quota, audit_volume_quota, max_user_layers, expose_scope_preview)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Name,
		t.Quota.StorageBytes, t.Quota.SearchQPS,
		t.Quota.MaterializeRate, t.Quota.AuditVolumePerDay, t.Quota.MaxUserLayers,
		nullBoolFromPtr(t.ExposeScopePreview))
	return err
}

// GetTenant returns the tenant or ErrTenantNotFound.
func (s *SQLite) GetTenant(ctx context.Context, id string) (Tenant, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, storage_quota, search_qps_quota, materialize_rate_quota, audit_volume_quota, max_user_layers, expose_scope_preview
		FROM tenants WHERE id = ?`, id)
	var t Tenant
	var exposeScopePreview sql.NullBool
	err := row.Scan(&t.ID, &t.Name,
		&t.Quota.StorageBytes, &t.Quota.SearchQPS,
		&t.Quota.MaterializeRate, &t.Quota.AuditVolumePerDay, &t.Quota.MaxUserLayers,
		&exposeScopePreview)
	if errors.Is(err, sql.ErrNoRows) {
		return Tenant{}, ErrTenantNotFound
	}
	t.ExposeScopePreview = ptrFromNullBool(exposeScopePreview)
	return t, err
}

// PutManifest enforces the §4.7 immutability invariant: the same
// (tenant, id, version) with a different content hash is rejected with
// ErrImmutableViolation; the same content hash is an idempotent no-op.
//
// spec: §4.7 — the invariant must hold under concurrent ingest. A
// SELECT-then-INSERT pair can let two writers both observe no row and
// then collide on the primary key, leaking a raw constraint error.
// A single atomic INSERT ... ON CONFLICT DO NOTHING is the
// immutability anchor instead: at most one writer inserts the row, and
// the loser reads the stored hash back to classify the outcome — a
// differing hash is the conflict (ErrImmutableViolation), an identical
// hash is the idempotent retry.
func (s *SQLite) PutManifest(ctx context.Context, rec ManifestRecord) error {
	ingestedAt := rec.IngestedAt
	if ingestedAt.IsZero() {
		ingestedAt = time.Now().UTC()
	}
	stampDeprecation(&rec)
	resources, err := MarshalResources(rec.Resources)
	if err != nil {
		return fmt.Errorf("marshal resources: %w", err)
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO manifests
			(tenant_id, artifact_id, version, content_hash, type, description,
			 tags, sensitivity, layer, deprecated, ingested_at, frontmatter, body, skill_raw,
			 extends_pin, signature, search_visibility, resources, deprecated_at, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (tenant_id, artifact_id, version) DO NOTHING`,
		rec.TenantID, rec.ArtifactID, rec.Version, rec.ContentHash,
		rec.Type, rec.Description,
		strings.Join(rec.Tags, "\n"),
		rec.Sensitivity, rec.Layer,
		boolToInt(rec.Deprecated),
		ingestedAt.UTC().Format(time.RFC3339Nano),
		rec.Frontmatter, rec.Body, rec.SkillRaw,
		rec.ExtendsPin, rec.Signature, rec.SearchVisibility, resources,
		nullTimeText(rec.DeprecatedAt), nullTimeText(rec.DeletedAt))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil // inserted these bytes
	}
	// The row already existed (a prior ingest, or the winner of a
	// concurrent race). Read the stored hash back to classify; immutable
	// rows are never deleted, so the SELECT always finds it.
	var existing string
	err = s.db.QueryRowContext(ctx, `
		SELECT content_hash FROM manifests
		WHERE tenant_id = ? AND artifact_id = ? AND version = ?`,
		rec.TenantID, rec.ArtifactID, rec.Version).Scan(&existing)
	if err != nil {
		return err
	}
	if existing != rec.ContentHash {
		return ErrImmutableViolation
	}
	return nil // idempotent — same hash
}

// PutManifestWithVectorPending commits the manifest and the §4.7.2 vector
// outbox row in a single transaction, so a crash never leaves a stored
// artifact without a queued embedding. The pending row is written only when
// the manifest is newly inserted; an idempotent re-ingest (same hash) commits
// the manifest no-op without re-queuing, and a differing hash rolls back with
// ErrImmutableViolation.
func (s *SQLite) PutManifestWithVectorPending(ctx context.Context, rec ManifestRecord, pending VectorPending) error {
	ingestedAt := rec.IngestedAt
	if ingestedAt.IsZero() {
		ingestedAt = time.Now().UTC()
	}
	stampDeprecation(&rec)
	resources, err := MarshalResources(rec.Resources)
	if err != nil {
		return fmt.Errorf("marshal resources: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO manifests
			(tenant_id, artifact_id, version, content_hash, type, description,
			 tags, sensitivity, layer, deprecated, ingested_at, frontmatter, body, skill_raw,
			 extends_pin, signature, search_visibility, resources, deprecated_at, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (tenant_id, artifact_id, version) DO NOTHING`,
		rec.TenantID, rec.ArtifactID, rec.Version, rec.ContentHash,
		rec.Type, rec.Description,
		strings.Join(rec.Tags, "\n"),
		rec.Sensitivity, rec.Layer,
		boolToInt(rec.Deprecated),
		ingestedAt.UTC().Format(time.RFC3339Nano),
		rec.Frontmatter, rec.Body, rec.SkillRaw,
		rec.ExtendsPin, rec.Signature, rec.SearchVisibility, resources,
		nullTimeText(rec.DeprecatedAt), nullTimeText(rec.DeletedAt))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var existing string
		err = tx.QueryRowContext(ctx, `
			SELECT content_hash FROM manifests
			WHERE tenant_id = ? AND artifact_id = ? AND version = ?`,
			rec.TenantID, rec.ArtifactID, rec.Version).Scan(&existing)
		if err != nil {
			return err
		}
		if existing != rec.ContentHash {
			return ErrImmutableViolation
		}
		return tx.Commit() // idempotent — same hash, no re-queue
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO vector_pending
			(tenant_id, artifact_id, version, text, enqueued_at, attempts, next_retry_at, last_error)
		VALUES (?, ?, ?, ?, ?, ?, ?, '')
		ON CONFLICT (tenant_id, artifact_id, version) DO UPDATE SET
			text = excluded.text, enqueued_at = excluded.enqueued_at,
			attempts = 0, next_retry_at = excluded.next_retry_at, last_error = ''`,
		rec.TenantID, rec.ArtifactID, rec.Version, []byte(pending.Text),
		pending.EnqueuedAt.UTC().Format(time.RFC3339Nano), pending.Attempts,
		pending.NextRetryAt.UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return tx.Commit()
}

// ListVectorPending returns up to limit eligible outbox rows, oldest first.
func (s *SQLite) ListVectorPending(ctx context.Context, limit int, now time.Time) ([]VectorPending, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT tenant_id, artifact_id, version, text, enqueued_at, attempts, next_retry_at, last_error
		FROM vector_pending
		WHERE next_retry_at <= ?
		ORDER BY enqueued_at ASC
		LIMIT ?`, now.UTC().Format(time.RFC3339Nano), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VectorPending
	for rows.Next() {
		var p VectorPending
		var text []byte
		var enqueued, nextRetry string
		if err := rows.Scan(&p.TenantID, &p.ArtifactID, &p.Version, &text,
			&enqueued, &p.Attempts, &nextRetry, &p.LastError); err != nil {
			return nil, err
		}
		p.Text = string(text)
		p.EnqueuedAt, _ = time.Parse(time.RFC3339Nano, enqueued)
		p.NextRetryAt, _ = time.Parse(time.RFC3339Nano, nextRetry)
		out = append(out, p)
	}
	return out, rows.Err()
}

// MarkVectorPendingDone removes a drained outbox row.
func (s *SQLite) MarkVectorPendingDone(ctx context.Context, tenantID, artifactID, version string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM vector_pending
		WHERE tenant_id = ? AND artifact_id = ? AND version = ?`,
		tenantID, artifactID, version)
	return err
}

// MarkVectorPendingRetry records a failed drain attempt with backoff.
func (s *SQLite) MarkVectorPendingRetry(ctx context.Context, tenantID, artifactID, version string, nextRetryAt time.Time, errMsg string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE vector_pending
		SET attempts = attempts + 1, next_retry_at = ?, last_error = ?
		WHERE tenant_id = ? AND artifact_id = ? AND version = ?`,
		nextRetryAt.UTC().Format(time.RFC3339Nano), errMsg, tenantID, artifactID, version)
	return err
}

// VectorOutboxStats returns the outbox depth and oldest enqueue time.
func (s *SQLite) VectorOutboxStats(ctx context.Context) (int, time.Time, error) {
	var depth int
	var oldest sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), MIN(enqueued_at) FROM vector_pending`).Scan(&depth, &oldest)
	if err != nil {
		return 0, time.Time{}, err
	}
	var t time.Time
	if oldest.Valid {
		t, _ = time.Parse(time.RFC3339Nano, oldest.String)
	}
	return depth, t, nil
}

// GetManifest returns the manifest or ErrNotFound.
func (s *SQLite) GetManifest(ctx context.Context, tenantID, artifactID, version string) (ManifestRecord, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT tenant_id, artifact_id, version, content_hash, type, description,
		       tags, sensitivity, layer, deprecated, ingested_at, frontmatter, body, skill_raw,
		       extends_pin, signature, search_visibility, resources, deprecated_at, deleted_at
		FROM manifests
		WHERE tenant_id = ? AND artifact_id = ? AND version = ? AND deleted_at IS NULL`,
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
		       tags, sensitivity, layer, deprecated, ingested_at, frontmatter, body, skill_raw,
		       extends_pin, signature, search_visibility, resources, deprecated_at, deleted_at
		FROM manifests
		WHERE tenant_id = ? AND deleted_at IS NULL
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

// PurgeDeprecatedManifests removes deprecated versions whose
// DeprecatedAt predates `before` (§8.4 90-day window). Timestamps are
// stored as RFC3339Nano UTC text, which sorts lexicographically.
func (s *SQLite) PurgeDeprecatedManifests(ctx context.Context, before time.Time) (int, error) {
	// §4.6/§4.7.6 extends-pin protection: a deprecated version that is still
	// pinned as an extends parent by another manifest must not be purged. The
	// child's pin is a hard reference resolved to this exact version at the
	// child's ingest time, and load_artifact resolves the parent through it
	// (resolveExtendsChain). Purging the pinned parent would orphan the child
	// and make it unloadable, so the version is retained until no manifest pins
	// it (the child is itself reingested onto a newer parent or removed). The
	// NOT EXISTS clause keys on the canonical "<artifact_id>@<version>" pin form
	// ExtendsPin stores.
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM manifests
		WHERE deprecated = 1 AND deprecated_at IS NOT NULL AND deprecated_at < ?
		  AND NOT EXISTS (
			SELECT 1 FROM manifests AS child
			WHERE child.tenant_id = manifests.tenant_id
			  AND child.extends_pin = manifests.artifact_id || '@' || manifests.version
		  )`,
		before.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// PutDomain upserts the DOMAIN.md record for a (tenant, layer, path).
func (s *SQLite) PutDomain(ctx context.Context, rec DomainRecord) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO domains (tenant_id, layer, path, raw)
		VALUES (?, ?, ?, ?)`,
		rec.TenantID, rec.Layer, rec.Path, rec.Raw)
	return err
}

// ListDomains returns every domain record for the tenant, ordered by
// path then layer (matches Memory).
func (s *SQLite) ListDomains(ctx context.Context, tenantID string) ([]DomainRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT tenant_id, layer, path, raw
		FROM domains
		WHERE tenant_id = ?
		ORDER BY path ASC, layer ASC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DomainRecord{}
	for rows.Next() {
		var rec DomainRecord
		if err := rows.Scan(&rec.TenantID, &rec.Layer, &rec.Path, &rec.Raw); err != nil {
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

// DependencyInDegree counts distinct dependents per target artifact (§4.7.3).
func (s *SQLite) DependencyInDegree(ctx context.Context, tenantID string) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT to_artifact, COUNT(DISTINCT from_artifact)
		FROM dependencies
		WHERE tenant_id = ?
		GROUP BY to_artifact`,
		tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var to string
		var n int
		if err := rows.Scan(&to, &n); err != nil {
			return nil, err
		}
		out[to] = n
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

// ListAdminGrants returns every grant for orgID, ordered by user_id.
func (s *SQLite) ListAdminGrants(ctx context.Context, orgID string) ([]AdminGrant, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT user_id, org_id, granted_at FROM admin_grants
		WHERE org_id = ? ORDER BY user_id`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdminGrant
	for rows.Next() {
		var g AdminGrant
		var granted string
		if err := rows.Scan(&g.UserID, &g.OrgID, &granted); err != nil {
			return nil, err
		}
		g.Granted, _ = time.Parse(time.RFC3339Nano, granted)
		out = append(out, g)
	}
	return out, rows.Err()
}

// RevokeAdmin removes the admin grant; missing rows are a no-op.
func (s *SQLite) RevokeAdmin(ctx context.Context, userID, orgID string) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM admin_grants WHERE user_id = ? AND org_id = ?`,
		userID, orgID)
	return err
}

// PutLayerConfig inserts or replaces a layer config.
func (s *SQLite) PutLayerConfig(ctx context.Context, cfg LayerConfig) error {
	createdAt := cfg.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO layer_configs
			(tenant_id, id, source_type, repo, ref, root, local_path, ord,
			 user_defined, owner, public, organization, groups, users,
			 webhook_secret, last_ingested_ref, force_push_policy, git_provider, created_at, deleted_at, last_ingested_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cfg.TenantID, cfg.ID, cfg.SourceType, cfg.Repo, cfg.Ref, cfg.Root, cfg.LocalPath,
		cfg.Order, boolToInt(cfg.UserDefined), cfg.Owner,
		boolToInt(cfg.Public), boolToInt(cfg.Organization),
		strings.Join(cfg.Groups, "\n"), strings.Join(cfg.Users, "\n"),
		cfg.WebhookSecret, cfg.LastIngestedRef, cfg.ForcePushPolicy, cfg.GitProvider,
		createdAt.UTC().Format(time.RFC3339Nano), nullTimeText(cfg.DeletedAt), nullTimeText(cfg.LastIngestedAt))
	return err
}

// GetLayerConfig returns one layer config or ErrNotFound.
func (s *SQLite) GetLayerConfig(ctx context.Context, tenantID, id string) (LayerConfig, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT tenant_id, id, source_type, repo, ref, root, local_path, ord,
		       user_defined, owner, public, organization, groups, users,
		       webhook_secret, last_ingested_ref, force_push_policy, git_provider, created_at, deleted_at, last_ingested_at
		FROM layer_configs
		WHERE tenant_id = ? AND id = ? AND deleted_at IS NULL`, tenantID, id)
	cfg, err := scanLayerConfig(row)
	if errors.Is(err, sql.ErrNoRows) {
		return LayerConfig{}, ErrNotFound
	}
	return cfg, err
}

// ListLayerConfigs returns every layer for the tenant in Order ascending.
func (s *SQLite) ListLayerConfigs(ctx context.Context, tenantID string) ([]LayerConfig, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT tenant_id, id, source_type, repo, ref, root, local_path, ord,
		       user_defined, owner, public, organization, groups, users,
		       webhook_secret, last_ingested_ref, force_push_policy, git_provider, created_at, deleted_at, last_ingested_at
		FROM layer_configs WHERE tenant_id = ? AND deleted_at IS NULL
		ORDER BY ord ASC, id ASC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LayerConfig{}
	for rows.Next() {
		cfg, err := scanLayerConfig(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, cfg)
	}
	return out, rows.Err()
}

// DeleteLayerConfig soft-deletes a layer and the artifacts ingested from
// it (§8.4): both rows get a deleted_at tombstone, hiding them from
// normal reads while keeping them recoverable for 30 days.
func (s *SQLite) DeleteLayerConfig(ctx context.Context, tenantID, id string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE layer_configs SET deleted_at = ?
		WHERE tenant_id = ? AND id = ? AND deleted_at IS NULL`, now, tenantID, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE manifests SET deleted_at = ?
		WHERE tenant_id = ? AND layer = ? AND deleted_at IS NULL`, now, tenantID, id); err != nil {
		return err
	}
	return tx.Commit()
}

// RestoreLayerConfig clears the soft-delete tombstone on a layer and its
// artifacts (§8.4 admin recovery).
func (s *SQLite) RestoreLayerConfig(ctx context.Context, tenantID, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `
		UPDATE layer_configs SET deleted_at = NULL
		WHERE tenant_id = ? AND id = ? AND deleted_at IS NOT NULL`, tenantID, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE manifests SET deleted_at = NULL
		WHERE tenant_id = ? AND layer = ? AND deleted_at IS NOT NULL`, tenantID, id); err != nil {
		return err
	}
	return tx.Commit()
}

// ListDeletedLayerConfigs returns the tenant's soft-deleted layers.
func (s *SQLite) ListDeletedLayerConfigs(ctx context.Context, tenantID string) ([]LayerConfig, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT tenant_id, id, source_type, repo, ref, root, local_path, ord,
		       user_defined, owner, public, organization, groups, users,
		       webhook_secret, last_ingested_ref, force_push_policy, git_provider, created_at, deleted_at, last_ingested_at
		FROM layer_configs WHERE tenant_id = ? AND deleted_at IS NOT NULL
		ORDER BY id ASC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LayerConfig{}
	for rows.Next() {
		cfg, err := scanLayerConfig(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, cfg)
	}
	return out, rows.Err()
}

// PurgeExpiredLayerDeletions hard-deletes soft-deleted layers and their
// artifacts whose deleted_at predates `before` (§8.4 30-day window end).
func (s *SQLite) PurgeExpiredLayerDeletions(ctx context.Context, before time.Time) (int, error) {
	cutoff := before.UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM manifests
		WHERE deleted_at IS NOT NULL AND deleted_at < ?`, cutoff); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `
		DELETE FROM layer_configs
		WHERE deleted_at IS NOT NULL AND deleted_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(n), nil
}

func scanLayerConfig(scanner rowScanner) (LayerConfig, error) {
	var cfg LayerConfig
	var userDefined, public, org int
	var groups, users, createdAt string
	var deletedAt, lastIngestedAt sql.NullString
	err := scanner.Scan(
		&cfg.TenantID, &cfg.ID, &cfg.SourceType,
		&cfg.Repo, &cfg.Ref, &cfg.Root, &cfg.LocalPath,
		&cfg.Order, &userDefined, &cfg.Owner,
		&public, &org, &groups, &users,
		&cfg.WebhookSecret, &cfg.LastIngestedRef, &cfg.ForcePushPolicy, &cfg.GitProvider,
		&createdAt, &deletedAt, &lastIngestedAt)
	if err != nil {
		return LayerConfig{}, err
	}
	cfg.UserDefined = userDefined != 0
	cfg.Public = public != 0
	cfg.Organization = org != 0
	if groups != "" {
		cfg.Groups = strings.Split(groups, "\n")
	}
	if users != "" {
		cfg.Users = strings.Split(users, "\n")
	}
	if createdAt != "" {
		cfg.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	}
	cfg.DeletedAt = parseNullTimeText(deletedAt)
	cfg.LastIngestedAt = parseNullTimeText(lastIngestedAt)
	return cfg, nil
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
	var resources []byte
	var deprecatedAt, deletedAt sql.NullString
	err := scanner.Scan(
		&rec.TenantID, &rec.ArtifactID, &rec.Version, &rec.ContentHash,
		&rec.Type, &rec.Description, &tags, &rec.Sensitivity, &rec.Layer,
		&deprecated, &ingestedAt, &rec.Frontmatter, &rec.Body, &rec.SkillRaw,
		&rec.ExtendsPin, &rec.Signature, &rec.SearchVisibility, &resources,
		&deprecatedAt, &deletedAt)
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
	rec.DeprecatedAt = parseNullTimeText(deprecatedAt)
	rec.DeletedAt = parseNullTimeText(deletedAt)
	if rec.Resources, err = UnmarshalResources(resources); err != nil {
		return ManifestRecord{}, err
	}
	return rec, nil
}

// nullTimeText renders an optional timestamp for a SQLite TEXT column:
// nil persists as NULL, a value as RFC3339Nano UTC.
func nullTimeText(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// parseNullTimeText is the inverse of nullTimeText.
func parseNullTimeText(ns sql.NullString) *time.Time {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, ns.String)
	if err != nil {
		return nil
	}
	t = t.UTC()
	return &t
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
