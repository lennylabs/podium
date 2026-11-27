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
			expose_scope_preview INTEGER,
			active INTEGER NOT NULL DEFAULT 1
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
			signature TEXT NOT NULL DEFAULT '',
			search_visibility TEXT NOT NULL DEFAULT '',
			resources BLOB,
			deprecated_at TEXT,
			deleted_at TEXT,
			package_digest TEXT NOT NULL DEFAULT '',
			attestation_outcome TEXT NOT NULL DEFAULT 'not_evaluated',
			attestation_failure TEXT NOT NULL DEFAULT '',
			attestation_evaluated_at TEXT,
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
		`CREATE TABLE IF NOT EXISTS operator_grants (
			identity TEXT PRIMARY KEY,
			granted_at TEXT NOT NULL
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
			repo_userinfo TEXT,
			ingest_claim TEXT,
			trust_mode TEXT NOT NULL DEFAULT '',
			trust_requirements TEXT NOT NULL DEFAULT '',
			trust_layer_root TEXT NOT NULL DEFAULT '',
			attestation_mark TEXT,
			attested_commit TEXT NOT NULL DEFAULT '',
			attestation_status TEXT NOT NULL DEFAULT '',
			attestation_details TEXT NOT NULL DEFAULT '',
			attestation_dropped INTEGER NOT NULL DEFAULT 0,
			attestation_checked_at TEXT,
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
		`CREATE TABLE IF NOT EXISTS data_migrations (
			name TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL
		)`,
		// §7.3.1 semantic overlap records, keyed by owner and peer, and the
		// per-artifact computation entries.
		`CREATE TABLE IF NOT EXISTS semantic_overlap_records (
			tenant_id TEXT NOT NULL,
			owner_id TEXT NOT NULL,
			owner_version TEXT NOT NULL,
			peer_id TEXT NOT NULL,
			peer_version TEXT NOT NULL,
			similarity REAL NOT NULL,
			model_id TEXT NOT NULL DEFAULT '',
			computed_at TEXT NOT NULL,
			PRIMARY KEY (tenant_id, owner_id, peer_id)
		)`,
		`CREATE TABLE IF NOT EXISTS semantic_overlap_computations (
			tenant_id TEXT NOT NULL,
			artifact_id TEXT NOT NULL,
			version TEXT NOT NULL,
			model_id TEXT NOT NULL DEFAULT '',
			computed_at TEXT NOT NULL,
			PRIMARY KEY (tenant_id, artifact_id)
		)`,
		// §8.7 daily usage totals, one row per day, layer, artifact, client, and
		// harness. day is the UTC date as YYYY-MM-DD. The table is clustered on
		// its key (WITHOUT ROWID), so the report's sums over a range of days
		// read the rows in place rather than looking each one up from the key
		// index, which cuts their time by more than half and stores the key once.
		`CREATE TABLE IF NOT EXISTS usage_daily (
			tenant_id TEXT NOT NULL,
			day TEXT NOT NULL,
			layer TEXT NOT NULL,
			artifact_id TEXT NOT NULL,
			client_kind TEXT NOT NULL,
			harness TEXT NOT NULL,
			loads INTEGER NOT NULL DEFAULT 0,
			syncs INTEGER NOT NULL DEFAULT 0,
			search_hits INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (tenant_id, day, layer, artifact_id, client_kind, harness)
		) WITHOUT ROWID`,
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
	// Spec: §4.7.11 (Attestation storage, Root record).
	tableStmts = append(tableStmts, attestationTableStmts("BLOB", "TEXT")...)
	tableStmts = append(tableStmts, rootRecordTableStmt("attestation_roots", "TEXT"))
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
		SELECT id, name, storage_quota, search_qps_quota, materialize_rate_quota, audit_volume_quota, max_user_layers, expose_scope_preview, active
		FROM tenants WHERE id = ?`, id)
	var t Tenant
	var exposeScopePreview sql.NullBool
	err := row.Scan(&t.ID, &t.Name,
		&t.Quota.StorageBytes, &t.Quota.SearchQPS,
		&t.Quota.MaterializeRate, &t.Quota.AuditVolumePerDay, &t.Quota.MaxUserLayers,
		&exposeScopePreview, &t.Active)
	if errors.Is(err, sql.ErrNoRows) {
		return Tenant{}, ErrTenantNotFound
	}
	t.ExposeScopePreview = ptrFromNullBool(exposeScopePreview)
	return t, err
}

// ListTenants returns every provisioned tenant, ordered by ID.
func (s *SQLite) ListTenants(ctx context.Context) ([]Tenant, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, storage_quota, search_qps_quota, materialize_rate_quota, audit_volume_quota, max_user_layers, expose_scope_preview, active
		FROM tenants ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Tenant
	for rows.Next() {
		var t Tenant
		var exposeScopePreview sql.NullBool
		if err := rows.Scan(&t.ID, &t.Name,
			&t.Quota.StorageBytes, &t.Quota.SearchQPS,
			&t.Quota.MaterializeRate, &t.Quota.AuditVolumePerDay, &t.Quota.MaxUserLayers,
			&exposeScopePreview, &t.Active); err != nil {
			return nil, err
		}
		t.ExposeScopePreview = ptrFromNullBool(exposeScopePreview)
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateTenant writes a tenant's mutable configuration (Quota,
// ExposeScopePreview, active), preserving the ID and name. An unknown ID
// returns ErrTenantNotFound.
func (s *SQLite) UpdateTenant(ctx context.Context, t Tenant) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE tenants SET
			storage_quota = ?, search_qps_quota = ?, materialize_rate_quota = ?,
			audit_volume_quota = ?, max_user_layers = ?, expose_scope_preview = ?, active = ?
		WHERE id = ?`,
		t.Quota.StorageBytes, t.Quota.SearchQPS, t.Quota.MaterializeRate,
		t.Quota.AuditVolumePerDay, t.Quota.MaxUserLayers, nullBoolFromPtr(t.ExposeScopePreview), t.Active,
		t.ID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrTenantNotFound
	}
	return nil
}

// DeactivateTenant sets a tenant's active flag false without deleting its
// data. An unknown ID returns ErrTenantNotFound.
func (s *SQLite) DeactivateTenant(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE tenants SET active = 0 WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrTenantNotFound
	}
	return nil
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
	failure, err := failureColumn(rec.AttestationFailure)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO manifests
			(tenant_id, artifact_id, version, content_hash, type, description,
			 tags, sensitivity, layer, deprecated, ingested_at, frontmatter, body, skill_raw,
			 signature, search_visibility, resources, deprecated_at, deleted_at, package_digest,
			 attestation_outcome, attestation_failure, attestation_evaluated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (tenant_id, artifact_id, version) DO NOTHING`,
		rec.TenantID, rec.ArtifactID, rec.Version, rec.ContentHash,
		rec.Type, rec.Description,
		strings.Join(rec.Tags, "\n"),
		rec.Sensitivity, rec.Layer,
		boolToInt(rec.Deprecated),
		ingestedAt.UTC().Format(time.RFC3339Nano),
		rec.Frontmatter, rec.Body, rec.SkillRaw,
		rec.Signature, rec.SearchVisibility, resources,
		nullTimeText(rec.DeprecatedAt), nullTimeText(rec.DeletedAt), rec.PackageDigest,
		rec.AttestationOutcome, failure, nullTimeText(rec.AttestationEvaluatedAt))
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
	if revived, err := sqliteRevive(ctx, s.db, rec, ingestedAt, resources, failure); err != nil || revived {
		return err
	}
	// The row already existed (a prior ingest, or the winner of a
	// concurrent race). Read the stored hash back to classify; immutable
	// rows are never deleted, so the SELECT always finds it.
	var existing, layer string
	var deleted bool
	err = s.db.QueryRowContext(ctx, `
		SELECT content_hash, layer, deleted_at IS NOT NULL FROM manifests
		WHERE tenant_id = ? AND artifact_id = ? AND version = ?`,
		rec.TenantID, rec.ArtifactID, rec.Version).Scan(&existing, &layer, &deleted)
	if err != nil {
		return err
	}
	return classifyExistingManifest(rec, existing, layer, deleted)
}

// sqliteRevive restores a tombstoned row of rec's (artifact_id, version) that
// carries rec's content hash and was contributed by rec's layer, rewriting it
// as rec states it, and reports whether it did. A live row or a different hash is left for the caller to
// classify.
//
// Spec: §7.3.1 (Removed artifacts)
func sqliteRevive(ctx context.Context, ex execContexter, rec ManifestRecord, ingestedAt time.Time, resources []byte, failure string) (bool, error) {
	res, err := ex.ExecContext(ctx, `
		UPDATE manifests SET
			type = ?, description = ?, tags = ?, sensitivity = ?, layer = ?, deprecated = ?,
			ingested_at = ?, frontmatter = ?, body = ?, skill_raw = ?, signature = ?,
			search_visibility = ?, resources = ?, deprecated_at = ?, package_digest = ?,
			attestation_outcome = ?, attestation_failure = ?, attestation_evaluated_at = ?, deleted_at = NULL
		WHERE tenant_id = ? AND artifact_id = ? AND version = ?
			AND content_hash = ? AND layer = ? AND deleted_at IS NOT NULL`,
		rec.Type, rec.Description, strings.Join(rec.Tags, "\n"), rec.Sensitivity, rec.Layer,
		boolToInt(rec.Deprecated), ingestedAt.UTC().Format(time.RFC3339Nano),
		rec.Frontmatter, rec.Body, rec.SkillRaw, rec.Signature, rec.SearchVisibility, resources,
		nullTimeText(rec.DeprecatedAt), rec.PackageDigest,
		rec.AttestationOutcome, failure, nullTimeText(rec.AttestationEvaluatedAt),
		rec.TenantID, rec.ArtifactID, rec.Version, rec.ContentHash, rec.Layer)
	return rowsChanged(res, err)
}

// rowsChanged reports whether a statement changed any row, passing its error
// through. Both SQL stores' restore updates read it.
func rowsChanged(res sql.Result, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// SoftDeleteArtifacts tombstones the live rows layerID contributed for each
// of artifactIDs (§7.3.1 removed artifacts). It binds the IDs in batches of
// softDeleteBatchSize inside one transaction. With a claim, the tombstoning
// statements run first and the claim is read after them: the first write
// takes the database's write lock, so no other connection can commit a later
// claim between the read and the commit, and a read made first could instead
// fail the upgrade to a writer with SQLITE_BUSY once another connection
// committed. A claim the layer no longer holds rolls back.
func (s *SQLite) SoftDeleteArtifacts(ctx context.Context, tenantID, layerID string, artifactIDs []string, at time.Time, claim string) error {
	if len(artifactIDs) == 0 {
		if claim == "" {
			return nil
		}
		return sqliteHoldsClaim(ctx, s.db, tenantID, layerID, claim)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := softDeleteBatches(ctx, tx, at.UTC().Format(time.RFC3339Nano), tenantID, layerID, artifactIDs); err != nil {
		return err
	}
	if claim != "" {
		if err := sqliteHoldsClaim(ctx, tx, tenantID, layerID, claim); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// sqliteHoldsClaim returns ErrIngestSuperseded unless the live layer holds
// claim.
//
// Spec: §7.3.1 (Removed artifacts)
func sqliteHoldsClaim(ctx context.Context, q queryRower, tenantID, layerID, claim string) error {
	var one int
	err := q.QueryRowContext(ctx, `
		SELECT 1 FROM layer_configs
		WHERE tenant_id = ? AND id = ? AND deleted_at IS NULL AND ingest_claim = ?`,
		tenantID, layerID, claim).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrIngestSuperseded
	}
	return err
}

// ClaimLayerIngest makes claim the live layer's ingest claim (§7.3.1) and
// returns the layer as stored and the claim it replaced. A layer that is
// absent or unregistered claims nothing.
func (s *SQLite) ClaimLayerIngest(ctx context.Context, tenantID, layerID, claim string) (LayerConfig, string, error) {
	cfg, previous, _, err := s.claimLayer(ctx, tenantID, layerID, claim, nil)
	return cfg, previous, err
}

// ReleaseLayerIngest puts previous back as the layer's ingest claim when the
// live layer still holds claim (§7.3.1). An empty previous stores NULL.
func (s *SQLite) ReleaseLayerIngest(ctx context.Context, tenantID, layerID, claim, previous string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE layer_configs SET ingest_claim = ?
		WHERE tenant_id = ? AND id = ? AND deleted_at IS NULL AND ingest_claim = ?`,
		sql.NullString{String: previous, Valid: previous != ""}, tenantID, layerID, claim)
	return err
}

// RecordLayerIngest sets the ingest reference and time of the live layer that
// holds claim (§7.3.1) with one update of those two columns.
func (s *SQLite) RecordLayerIngest(ctx context.Context, tenantID, layerID, claim, ref string, at *time.Time) error {
	recorded, err := rowsChanged(s.db.ExecContext(ctx, `
		UPDATE layer_configs SET last_ingested_ref = ?, last_ingested_at = ?
		WHERE tenant_id = ? AND id = ? AND deleted_at IS NULL AND ingest_claim = ?`,
		ref, nullTimeText(at), tenantID, layerID, claim))
	if err != nil {
		return err
	}
	if !recorded {
		return ErrIngestSuperseded
	}
	return nil
}

// softDeleteBatches runs the SQLite tombstoning statements in tx.
func softDeleteBatches(ctx context.Context, tx *sql.Tx, stamp, tenantID, layerID string, artifactIDs []string) error {
	for _, batch := range idBatches(artifactIDs, softDeleteBatchSize) {
		args := []any{stamp, tenantID, layerID}
		marks := make([]string, len(batch))
		for i, id := range batch {
			marks[i] = "?"
			args = append(args, id)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE manifests SET deleted_at = ?
			WHERE tenant_id = ? AND layer = ? AND deleted_at IS NULL
				AND artifact_id IN (`+strings.Join(marks, ", ")+`)`, args...); err != nil {
			return err
		}
	}
	return nil
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
	failure, err := failureColumn(rec.AttestationFailure)
	if err != nil {
		return err
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
			 signature, search_visibility, resources, deprecated_at, deleted_at, package_digest,
			 attestation_outcome, attestation_failure, attestation_evaluated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (tenant_id, artifact_id, version) DO NOTHING`,
		rec.TenantID, rec.ArtifactID, rec.Version, rec.ContentHash,
		rec.Type, rec.Description,
		strings.Join(rec.Tags, "\n"),
		rec.Sensitivity, rec.Layer,
		boolToInt(rec.Deprecated),
		ingestedAt.UTC().Format(time.RFC3339Nano),
		rec.Frontmatter, rec.Body, rec.SkillRaw,
		rec.Signature, rec.SearchVisibility, resources,
		nullTimeText(rec.DeprecatedAt), nullTimeText(rec.DeletedAt), rec.PackageDigest,
		rec.AttestationOutcome, failure, nullTimeText(rec.AttestationEvaluatedAt))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	revived := false
	if n == 0 {
		if revived, err = sqliteRevive(ctx, tx, rec, ingestedAt, resources, failure); err != nil {
			return err
		}
	}
	if n == 0 && !revived {
		var existing, layer string
		var deleted bool
		err = tx.QueryRowContext(ctx, `
			SELECT content_hash, layer, deleted_at IS NOT NULL FROM manifests
			WHERE tenant_id = ? AND artifact_id = ? AND version = ?`,
			rec.TenantID, rec.ArtifactID, rec.Version).Scan(&existing, &layer, &deleted)
		if err != nil {
			return err
		}
		if err := classifyExistingManifest(rec, existing, layer, deleted); err != nil {
			return err
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
	defer func() { _ = rows.Close() }()
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

// ListVectorPendingKeys returns the key of every outbox row of tenantID.
func (s *SQLite) ListVectorPendingKeys(ctx context.Context, tenantID string) ([]VectorPendingKey, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT artifact_id, version FROM vector_pending WHERE tenant_id = ?`, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanVectorPendingKeys(rows)
}

// scanVectorPendingKeys reads (artifact_id, version) rows, which the SQLite
// and Postgres outbox key listings both select.
func scanVectorPendingKeys(rows *sql.Rows) ([]VectorPendingKey, error) {
	var out []VectorPendingKey
	for rows.Next() {
		var k VectorPendingKey
		if err := rows.Scan(&k.ArtifactID, &k.Version); err != nil {
			return nil, err
		}
		out = append(out, k)
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
		       signature, search_visibility, resources, deprecated_at, deleted_at, package_digest,
		       attestation_outcome, attestation_failure, attestation_evaluated_at
		FROM manifests
		WHERE tenant_id = ? AND artifact_id = ? AND version = ? AND deleted_at IS NULL`,
		tenantID, artifactID, version)
	rec, err := scanManifest(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ManifestRecord{}, ErrNotFound
	}
	return rec, err
}

// RehashManifest rewrites one row's content hash and signature under the
// compare-and-swap the Store interface documents. It carries no deleted_at
// condition, because the §13.4 migration rewrites a soft-deleted row a
// restore would bring back.
func (s *SQLite) RehashManifest(ctx context.Context, tenantID, artifactID, version, oldHash, oldSignature, newHash, signature string) error {
	stmt := `
		UPDATE manifests SET content_hash = ?, signature = ?
		WHERE tenant_id = ? AND artifact_id = ? AND version = ?
		  AND content_hash = ? AND signature = ?`
	res, err := s.db.ExecContext(ctx, stmt,
		newHash, signature, tenantID, artifactID, version, oldHash, oldSignature)
	if err != nil {
		return fmt.Errorf("store: rehash %s/%s@%s: %w", tenantID, artifactID, version, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: rehash %s/%s@%s: %w", tenantID, artifactID, version, err)
	}
	if n > 0 {
		return nil
	}
	return s.classifyConditionalMiss(ctx, "rehash", tenantID, artifactID, version)
}

// SetPackageDigest stores one row's package digest under the content-hash
// condition the Store interface documents. It carries no deleted_at
// condition, because the §13.4 fill covers a soft-deleted row a restore would
// bring back.
func (s *SQLite) SetPackageDigest(ctx context.Context, tenantID, artifactID, version, contentHash, digest string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE manifests SET package_digest = ?
		WHERE tenant_id = ? AND artifact_id = ? AND version = ? AND content_hash = ?`,
		digest, tenantID, artifactID, version, contentHash)
	changed, err := rowsChanged(res, err)
	if err != nil {
		return fmt.Errorf("store: set package digest %s/%s@%s: %w", tenantID, artifactID, version, err)
	}
	if changed {
		return nil
	}
	return s.classifyConditionalMiss(ctx, "set package digest", tenantID, artifactID, version)
}

// classifyConditionalMiss reads the row back after a conditional update of
// operation op changed nothing, to tell an absent key (ErrNotFound)
// from a stored value that no longer matches what the caller read
// (ErrImmutableViolation).
func (s *SQLite) classifyConditionalMiss(ctx context.Context, op, tenantID, artifactID, version string) error {
	var existing string
	err := s.db.QueryRowContext(ctx, `
		SELECT content_hash FROM manifests
		WHERE tenant_id = ? AND artifact_id = ? AND version = ?`,
		tenantID, artifactID, version).Scan(&existing)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("store: %s %s/%s@%s: %w", op, tenantID, artifactID, version, err)
	}
	return ErrImmutableViolation
}

// DataMigrationApplied reports whether the named §13.4 stored-value rewrite
// has completed against this database.
func (s *SQLite) DataMigrationApplied(ctx context.Context, name string) (bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT 1 FROM data_migrations WHERE name = ?`, name)
	var dummy int
	err := row.Scan(&dummy)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("store: data migration %s: %w", name, err)
	}
	return true, nil
}

// SetDataMigrationApplied records or removes the named marker. Both
// directions are idempotent.
func (s *SQLite) SetDataMigrationApplied(ctx context.Context, name string, applied bool) error {
	var err error
	if applied {
		_, err = s.db.ExecContext(ctx, `
			INSERT OR IGNORE INTO data_migrations (name, applied_at) VALUES (?, ?)`,
			name, time.Now().UTC().Format(time.RFC3339Nano))
	} else {
		_, err = s.db.ExecContext(ctx, `DELETE FROM data_migrations WHERE name = ?`, name)
	}
	if err != nil {
		return fmt.Errorf("store: data migration %s: %w", name, err)
	}
	return nil
}

// ListManifests returns every manifest for the tenant, ordered by
// artifact ID then version (matches Memory).
func (s *SQLite) ListManifests(ctx context.Context, tenantID string) ([]ManifestRecord, error) {
	return s.listManifests(ctx, tenantID, false)
}

// ListManifestsIncludingDeleted returns the tenant's manifests without the
// §8.4 tombstone filter, so the §13.4 stored-value migration reaches the rows
// a restored layer would serve.
func (s *SQLite) ListManifestsIncludingDeleted(ctx context.Context, tenantID string) ([]ManifestRecord, error) {
	return s.listManifests(ctx, tenantID, true)
}

func (s *SQLite) listManifests(ctx context.Context, tenantID string, includeDeleted bool) ([]ManifestRecord, error) {
	tombstone := "AND deleted_at IS NULL"
	if includeDeleted {
		tombstone = ""
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT tenant_id, artifact_id, version, content_hash, type, description,
		       tags, sensitivity, layer, deprecated, ingested_at, frontmatter, body, skill_raw,
		       signature, search_visibility, resources, deprecated_at, deleted_at, package_digest,
		       attestation_outcome, attestation_failure, attestation_evaluated_at
		FROM manifests
		WHERE tenant_id = ? `+tombstone+`
		ORDER BY artifact_id ASC, version ASC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
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
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM manifests
		WHERE deprecated = 1 AND deprecated_at IS NOT NULL AND deprecated_at < ?`,
		before.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), sweepAttestations(ctx, s.db)
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
	defer func() { _ = rows.Close() }()
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

// DependentsOf returns every §4.7.3 edge whose To matches artifactID.
func (s *SQLite) DependentsOf(ctx context.Context, tenantID, artifactID string) ([]DependencyEdge, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT from_artifact, to_artifact, kind
		FROM dependencies
		WHERE tenant_id = ? AND to_artifact = ? AND `+indexedEdgeKindsSQL+`
		ORDER BY from_artifact, kind`,
		tenantID, artifactID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
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
		WHERE tenant_id = ? AND `+indexedEdgeKindsSQL+`
		GROUP BY to_artifact`,
		tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
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
	defer func() { _ = rows.Close() }()
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

// GrantOperator records an instance-level operator grant (§4.7.1 Operator
// role). Inserting an existing identity is a no-op.
func (s *SQLite) GrantOperator(ctx context.Context, identity string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO operator_grants (identity, granted_at)
		VALUES (?, ?)`,
		identity, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// IsOperator reports whether the identity holds an operator grant.
func (s *SQLite) IsOperator(ctx context.Context, identity string) (bool, error) {
	row := s.db.QueryRowContext(ctx, `SELECT 1 FROM operator_grants WHERE identity = ?`, identity)
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
func (s *SQLite) PutLayerConfig(ctx context.Context, cfg LayerConfig) error {
	return sqlitePutLayer(ctx, s.db, cfg)
}

// sqlitePutLayer writes cfg through ex, a database or a transaction.
func sqlitePutLayer(ctx context.Context, ex execContexter, cfg LayerConfig) error {
	// Spec: §7.3.1 (Repository credentials). The write rule runs before any
	// statement, so a config it refuses writes nothing.
	repo, userinfo, err := LayerRepoColumns(cfg)
	if err != nil {
		return err
	}
	groups, users, err := layerListColumns(cfg)
	if err != nil {
		return err
	}
	trustReqs, err := trustRequirementsColumn(cfg.TrustRequirements)
	if err != nil {
		return err
	}
	createdAt := cfg.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	// The upsert names the columns a layer write carries, so the §7.3.1 ingest
	// claim and the §4.7.11 attestation record stay as stored, on a live row
	// and on a soft-deleted one; dropping the claim would supersede a cycle in
	// flight on every layer update.
	_, err = ex.ExecContext(ctx, `
		INSERT INTO layer_configs
			(tenant_id, id, source_type, repo, ref, root, local_path, ord,
			 user_defined, owner, public, organization, groups, users,
			 webhook_secret, last_ingested_ref, force_push_policy, git_provider, created_at, deleted_at, last_ingested_at,
			 repo_userinfo, trust_mode, trust_requirements, trust_layer_root)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (tenant_id, id) DO UPDATE SET `+layerWriteAssignments,
		cfg.TenantID, cfg.ID, cfg.SourceType, repo, cfg.Ref, cfg.Root, cfg.LocalPath,
		cfg.Order, boolToInt(cfg.UserDefined), cfg.Owner,
		boolToInt(cfg.Public), boolToInt(cfg.Organization),
		groups, users,
		cfg.WebhookSecret, cfg.LastIngestedRef, cfg.ForcePushPolicy, cfg.GitProvider,
		createdAt.UTC().Format(time.RFC3339Nano), nullTimeText(cfg.DeletedAt), nullTimeText(cfg.LastIngestedAt),
		userinfo, cfg.TrustMode, trustReqs, cfg.TrustLayerRoot)
	return err
}

// GetLayerConfig returns one layer config or ErrNotFound.
func (s *SQLite) GetLayerConfig(ctx context.Context, tenantID, id string) (LayerConfig, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT tenant_id, id, source_type, repo, ref, root, local_path, ord,
		       user_defined, owner, public, organization, groups, users,
		       webhook_secret, last_ingested_ref, force_push_policy, git_provider, created_at, deleted_at, last_ingested_at,
		       repo_userinfo, trust_mode, trust_requirements, trust_layer_root,
		       attestation_mark, attested_commit, attestation_status, attestation_details, attestation_dropped, attestation_checked_at
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
		       webhook_secret, last_ingested_ref, force_push_policy, git_provider, created_at, deleted_at, last_ingested_at,
		       repo_userinfo, trust_mode, trust_requirements, trust_layer_root,
		       attestation_mark, attested_commit, attestation_status, attestation_details, attestation_dropped, attestation_checked_at
		FROM layer_configs WHERE tenant_id = ? AND deleted_at IS NULL
		ORDER BY ord ASC, id ASC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
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
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		UPDATE layer_configs SET deleted_at = ?, ingest_claim = NULL
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

// RestoreLayerConfig clears the soft-delete tombstone on a layer and on the
// artifacts its unregister tombstoned (§8.4 admin recovery). DeleteLayerConfig
// stamps the layer and those artifacts with one timestamp, so a row whose
// tombstone differs was removed by an earlier ingest and stays removed. The
// artifacts are cleared first, while the layer's tombstone is still readable.
//
// Spec: §7.3.1 (Removed artifacts), §8.4
func (s *SQLite) RestoreLayerConfig(ctx context.Context, tenantID, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		UPDATE manifests SET deleted_at = NULL
		WHERE tenant_id = ? AND layer = ? AND deleted_at = (
			SELECT deleted_at FROM layer_configs WHERE tenant_id = ? AND id = ?)`,
		tenantID, id, tenantID, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE layer_configs SET deleted_at = NULL
		WHERE tenant_id = ? AND id = ? AND deleted_at IS NOT NULL`, tenantID, id)
	changed, err := rowsChanged(res, err)
	if err != nil {
		return err
	}
	if !changed {
		return ErrNotFound
	}
	return tx.Commit()
}

// ListDeletedLayerConfigs returns the tenant's soft-deleted layers.
func (s *SQLite) ListDeletedLayerConfigs(ctx context.Context, tenantID string) ([]LayerConfig, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT tenant_id, id, source_type, repo, ref, root, local_path, ord,
		       user_defined, owner, public, organization, groups, users,
		       webhook_secret, last_ingested_ref, force_push_policy, git_provider, created_at, deleted_at, last_ingested_at,
		       repo_userinfo, trust_mode, trust_requirements, trust_layer_root,
		       attestation_mark, attested_commit, attestation_status, attestation_details, attestation_dropped, attestation_checked_at
		FROM layer_configs WHERE tenant_id = ? AND deleted_at IS NOT NULL
		ORDER BY id ASC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
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

// PurgeExpiredLayerDeletions hard-deletes soft-deleted layers, their
// artifacts, and their §8.7 usage counts whose deleted_at predates `before`
// (§8.4 30-day window end).
func (s *SQLite) PurgeExpiredLayerDeletions(ctx context.Context, before time.Time) (int, error) {
	cutoff := before.UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM manifests
		WHERE deleted_at IS NOT NULL AND deleted_at < ?`, cutoff); err != nil {
		return 0, err
	}
	// Spec: §8.7 — the counts are keyed by layer identifier, so they go with
	// the layer; left behind, they would report to a later layer that
	// reuses the identifier.
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM usage_daily
		WHERE (tenant_id, layer) IN (
			SELECT tenant_id, id FROM layer_configs
			WHERE deleted_at IS NOT NULL AND deleted_at < ?)`, cutoff); err != nil {
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
	if err := sweepAttestations(ctx, tx); err != nil {
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
	var repo, groups, users, createdAt, trustReqs string
	var deletedAt, lastIngestedAt, repoUserinfo, mark, checkedAt sql.NullString
	var details string
	err := scanner.Scan(
		&cfg.TenantID, &cfg.ID, &cfg.SourceType,
		&repo, &cfg.Ref, &cfg.Root, &cfg.LocalPath,
		&cfg.Order, &userDefined, &cfg.Owner,
		&public, &org, &groups, &users,
		&cfg.WebhookSecret, &cfg.LastIngestedRef, &cfg.ForcePushPolicy, &cfg.GitProvider,
		&createdAt, &deletedAt, &lastIngestedAt, &repoUserinfo,
		&cfg.TrustMode, &trustReqs, &cfg.TrustLayerRoot,
		&mark, &cfg.Commit, &cfg.Status, &details,
		&cfg.Dropped, &checkedAt)
	if err != nil {
		return LayerConfig{}, err
	}
	if cfg.TrustRequirements, err = parseTrustRequirements(trustReqs); err != nil {
		return LayerConfig{}, err
	}
	if cfg.Details, err = parseAttestationDetails(details); err != nil {
		return LayerConfig{}, err
	}
	cfg.Mark = parseNullTimeText(mark)
	cfg.CheckedAt = parseNullTimeText(checkedAt)
	cfg.Repo, cfg.RegisteredRepo = SplitLayerRepo(repo, repoUserinfo)
	cfg.UserDefined = userDefined != 0
	cfg.Public = public != 0
	cfg.Organization = org != 0
	if groups != "" {
		cfg.Groups = strings.Split(groups, layerListSeparator)
	}
	if users != "" {
		cfg.Users = strings.Split(users, layerListSeparator)
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
	var deprecatedAt, deletedAt, evaluatedAt sql.NullString
	var failure string
	err := scanner.Scan(
		&rec.TenantID, &rec.ArtifactID, &rec.Version, &rec.ContentHash,
		&rec.Type, &rec.Description, &tags, &rec.Sensitivity, &rec.Layer,
		&deprecated, &ingestedAt, &rec.Frontmatter, &rec.Body, &rec.SkillRaw,
		&rec.Signature, &rec.SearchVisibility, &resources,
		&deprecatedAt, &deletedAt, &rec.PackageDigest,
		&rec.AttestationOutcome, &failure, &evaluatedAt)
	if err != nil {
		return ManifestRecord{}, err
	}
	if rec.AttestationFailure, err = parseFailureColumn(failure); err != nil {
		return ManifestRecord{}, err
	}
	rec.AttestationEvaluatedAt = parseNullTimeText(evaluatedAt)
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

// ReplaceOverlapRecords removes the records the computed artifact owns,
// stores recs, and upserts the computation entry in one transaction. It
// returns ErrOverlapSuperseded, changing nothing, when the stored entry
// supersedes comp. The transaction's first statement is the delete, a write,
// so SQLite grants it the database's single write lock before it reads the
// entry: a concurrent call for the same artifact waits under the busy timeout
// and then replaces this call's records whole, and the entry read is the
// latest committed one.
func (s *SQLite) ReplaceOverlapRecords(ctx context.Context, tenantID string, comp OverlapComputation, recs []OverlapRecord) error {
	comp, norm, err := normalizeOverlapComputation(tenantID, comp, recs)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM semantic_overlap_records WHERE tenant_id = ? AND owner_id = ?`,
		tenantID, comp.ArtifactID); err != nil {
		return err
	}
	if superseded, err := s.overlapSupersededTx(ctx, tx, comp); err != nil || superseded {
		if err == nil {
			err = ErrOverlapSuperseded
		}
		return err
	}
	for _, r := range norm {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO semantic_overlap_records
				(tenant_id, owner_id, owner_version, peer_id, peer_version, similarity, model_id, computed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			r.TenantID, r.Owner, r.OwnerVersion, r.Peer, r.PeerVersion, r.Similarity, r.ModelID,
			r.ComputedAt.Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT OR REPLACE INTO semantic_overlap_computations
			(tenant_id, artifact_id, version, model_id, computed_at)
		VALUES (?, ?, ?, ?, ?)`,
		comp.TenantID, comp.ArtifactID, comp.Version, comp.ModelID, comp.ComputedAt.Format(time.RFC3339Nano)); err != nil {
		return err
	}
	return tx.Commit()
}

// overlapSupersededTx reports whether the computation entry stored for
// comp's artifact supersedes comp, reading it inside tx.
func (s *SQLite) overlapSupersededTx(ctx context.Context, tx *sql.Tx, comp OverlapComputation) (bool, error) {
	var prev OverlapComputation
	var at string
	err := tx.QueryRowContext(ctx, `
		SELECT version, computed_at FROM semantic_overlap_computations
		WHERE tenant_id = ? AND artifact_id = ?`, comp.TenantID, comp.ArtifactID).Scan(&prev.Version, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if prev.ComputedAt, err = time.Parse(time.RFC3339Nano, at); err != nil {
		return false, fmt.Errorf("store: parse overlap computed_at %q: %w", at, err)
	}
	return supersededBy(prev, comp), nil
}

// ListOverlapRecords returns the tenant's records ordered by owner and peer.
func (s *SQLite) ListOverlapRecords(ctx context.Context, tenantID string) ([]OverlapRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT tenant_id, owner_id, owner_version, peer_id, peer_version, similarity, model_id, computed_at
		FROM semantic_overlap_records
		WHERE tenant_id = ?
		ORDER BY owner_id, peer_id`, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []OverlapRecord{}
	for rows.Next() {
		var r OverlapRecord
		var at string
		if err := rows.Scan(&r.TenantID, &r.Owner, &r.OwnerVersion, &r.Peer, &r.PeerVersion, &r.Similarity, &r.ModelID, &at); err != nil {
			return nil, err
		}
		if r.ComputedAt, err = time.Parse(time.RFC3339Nano, at); err != nil {
			return nil, fmt.Errorf("store: parse overlap computed_at %q: %w", at, err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListOverlapComputations returns the tenant's computation entries ordered by
// artifact.
func (s *SQLite) ListOverlapComputations(ctx context.Context, tenantID string) ([]OverlapComputation, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT tenant_id, artifact_id, version, model_id, computed_at
		FROM semantic_overlap_computations
		WHERE tenant_id = ?
		ORDER BY artifact_id`, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []OverlapComputation{}
	for rows.Next() {
		var c OverlapComputation
		var at string
		if err := rows.Scan(&c.TenantID, &c.ArtifactID, &c.Version, &c.ModelID, &at); err != nil {
			return nil, err
		}
		if c.ComputedAt, err = time.Parse(time.RFC3339Nano, at); err != nil {
			return nil, fmt.Errorf("store: parse overlap computation computed_at %q: %w", at, err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteOverlapArtifacts removes the records owned by and the computation
// entries of each named artifact in one transaction.
func (s *SQLite) DeleteOverlapArtifacts(ctx context.Context, tenantID string, artifactIDs []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, id := range artifactIDs {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM semantic_overlap_records WHERE tenant_id = ? AND owner_id = ?`, tenantID, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM semantic_overlap_computations WHERE tenant_id = ? AND artifact_id = ?`, tenantID, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
