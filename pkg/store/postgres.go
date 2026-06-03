package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "github.com/lib/pq"
)

// Postgres is a Store implementation backed by Postgres via lib/pq.
// Used by the standard deployment per spec §13.10. The conformance
// suite (pkg/store/storetest) covers the same contract that Memory
// and SQLite satisfy; Postgres-specific tests gate on the
// PODIUM_POSTGRES_DSN env var so CI can run with or without a
// database.
//
// spec: §4.7.1 — tenancy isolation is schema-per-org. Each org's tables
// (manifests, domains, dependencies, admin_grants, layer_configs) live in a
// dedicated "org_<id>" schema, so a query for one org cannot reach another
// org's rows even with a forged WHERE clause, and dropping an org is a single
// DROP SCHEMA CASCADE. The cross-org "shared infrastructure metadata" tables
// (the tenants registry and the vector outbox) stay in public and carry
// row-level security policies keyed on the org id.
type Postgres struct {
	db *sql.DB

	// mu guards provisioned. ensureOrg holds it across the per-org schema
	// provisioning so a fresh org's tables are created exactly once.
	mu sync.Mutex
	// provisioned records org ids whose schema and tables this process has
	// already created, so the per-call routing skips the idempotent DDL after
	// the first write for an org.
	provisioned map[string]bool
}

// orgIDPattern is the allowlist an org id must match before it is interpolated
// into a schema identifier, where bind parameters are not allowed. Org ids are
// UUIDs (§4.7.1) and tenant ids in the conformance suite are short slugs; both
// fit [A-Za-z0-9._-]. Rejecting anything else (notably a double quote that
// could break out of the quoted identifier) keeps the schema name injection-safe.
var orgIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,54}$`)

// quoteIdent renders name as a double-quoted Postgres identifier, doubling any
// embedded quote.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// orgSchemaIdent maps an org id to the quoted identifier of the schema that
// holds that org's tables (§4.7.1 schema-per-org). It validates the id against
// orgIDPattern first because the name lands in a DDL identifier position.
func orgSchemaIdent(orgID string) (string, error) {
	if !orgIDPattern.MatchString(orgID) {
		return "", fmt.Errorf("store: invalid org id %q for schema isolation", orgID)
	}
	return quoteIdent("org_" + orgID), nil
}

// orgTableStmts create the per-org tables inside the current search_path
// (the org schema). They carry the tenant_id column and tenant-scoped WHERE
// clauses unchanged from the flat layout, so the schema boundary is an
// additional physical isolation on top of the existing key scoping rather than
// a rewrite of every query.
var orgTableStmts = []string{
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
		skill_raw BYTEA,
		extends_pin TEXT NOT NULL DEFAULT '',
		signature TEXT NOT NULL DEFAULT '',
		search_visibility TEXT NOT NULL DEFAULT '',
		resources BYTEA,
		deprecated_at TIMESTAMPTZ,
		deleted_at TIMESTAMPTZ,
		PRIMARY KEY (tenant_id, artifact_id, version)
	)`,
	`CREATE TABLE IF NOT EXISTS domains (
		tenant_id TEXT NOT NULL,
		layer TEXT NOT NULL,
		path TEXT NOT NULL,
		raw BYTEA,
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
		git_provider TEXT NOT NULL DEFAULT '',
		created_at TIMESTAMPTZ NOT NULL,
		deleted_at TIMESTAMPTZ,
		last_ingested_at TIMESTAMPTZ,
		PRIMARY KEY (tenant_id, id)
	)`,
}

// orgIndexStmts create the per-org indexes after the org tables and the
// additive column migration (idx_manifests_tenant_layer references the layer
// column, which the migration backfills on an org schema initialized by an
// earlier binary).
var orgIndexStmts = []string{
	`CREATE INDEX IF NOT EXISTS idx_manifests_tenant_type
		ON manifests(tenant_id, type)`,
	`CREATE INDEX IF NOT EXISTS idx_manifests_tenant_layer
		ON manifests(tenant_id, layer)`,
	`CREATE INDEX IF NOT EXISTS idx_domains_tenant_path
		ON domains(tenant_id, path)`,
	`CREATE INDEX IF NOT EXISTS idx_deps_to
		ON dependencies(tenant_id, to_artifact)`,
}

// sharedTableStmts create the cross-org tables in public: the tenants registry
// (§4.7.1 "shared infrastructure metadata") and the vector outbox the §4.7.2
// drain worker reconciles across orgs. They are schema-qualified so they always
// land in public regardless of the connection's search_path.
var sharedTableStmts = []string{
	`CREATE TABLE IF NOT EXISTS public.tenants (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		storage_quota BIGINT NOT NULL DEFAULT 0,
		search_qps_quota BIGINT NOT NULL DEFAULT 0,
		materialize_rate_quota BIGINT NOT NULL DEFAULT 0,
		audit_volume_quota BIGINT NOT NULL DEFAULT 0,
		max_user_layers BIGINT NOT NULL DEFAULT 0,
		expose_scope_preview BOOLEAN
	)`,
	`CREATE TABLE IF NOT EXISTS public.vector_pending (
		tenant_id TEXT NOT NULL,
		artifact_id TEXT NOT NULL,
		version TEXT NOT NULL,
		text BYTEA NOT NULL,
		enqueued_at TIMESTAMPTZ NOT NULL,
		attempts BIGINT NOT NULL DEFAULT 0,
		next_retry_at TIMESTAMPTZ NOT NULL,
		last_error TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (tenant_id, artifact_id, version)
	)`,
}

// sharedIndexStmts index the cross-org tables in public.
var sharedIndexStmts = []string{
	`CREATE INDEX IF NOT EXISTS idx_vector_pending_next_retry
		ON public.vector_pending(next_retry_at)`,
}

// rlsStmts enable §4.7.1 row-level security on the cross-org tables, keyed on
// the org id carried in the per-request podium.org_id setting.
//
// tenants is the org registry: it is FORCE'd so even the table owner is subject
// to the policy, and the store sets podium.org_id to the target org id around
// every tenants read and write (set_config(..., is_local := true)), so a
// session scoped to one org cannot read or write another org's registry row.
//
// vector_pending is the cross-org outbox: the policy is the same org_id check,
// but it is not FORCE'd so the trusted registry process (the table owner) drains
// every org's queued embeddings in one scan, while any non-owner role is still
// confined to its own org's rows.
var rlsStmts = []string{
	`ALTER TABLE public.tenants ENABLE ROW LEVEL SECURITY`,
	`ALTER TABLE public.tenants FORCE ROW LEVEL SECURITY`,
	`DO $$ BEGIN
		IF NOT EXISTS (
			SELECT 1 FROM pg_policies
			WHERE schemaname = 'public' AND tablename = 'tenants'
			  AND policyname = 'tenants_org_isolation'
		) THEN
			CREATE POLICY tenants_org_isolation ON public.tenants
				USING (id = current_setting('podium.org_id', true))
				WITH CHECK (id = current_setting('podium.org_id', true));
		END IF;
	END $$`,
	`ALTER TABLE public.vector_pending ENABLE ROW LEVEL SECURITY`,
	`DO $$ BEGIN
		IF NOT EXISTS (
			SELECT 1 FROM pg_policies
			WHERE schemaname = 'public' AND tablename = 'vector_pending'
			  AND policyname = 'vector_pending_org_isolation'
		) THEN
			CREATE POLICY vector_pending_org_isolation ON public.vector_pending
				USING (tenant_id = current_setting('podium.org_id', true))
				WITH CHECK (tenant_id = current_setting('podium.org_id', true));
		END IF;
	END $$`,
}

// OpenPostgres opens a connection to the database at dsn. The DSN
// follows the lib/pq form, e.g.
// "postgres://user:pass@host:5432/db?sslmode=disable". The shared schema is
// applied on every open; existing schemas are unchanged thanks to
// IF NOT EXISTS. Per-org schemas are provisioned lazily on first use.
func OpenPostgres(dsn string) (*Postgres, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	p := &Postgres{db: db, provisioned: map[string]bool{}}
	if err := p.applySchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return p, nil
}

// Close releases the underlying database connection pool.
func (p *Postgres) Close() error { return p.db.Close() }

// DB returns the underlying *sql.DB. Test fixtures use it to inspect the
// per-org schemas; production code should not need it.
func (p *Postgres) DB() *sql.DB { return p.db }

// ReplicationLagSeconds returns the §13.2.1 observed replication lag in
// seconds. On a read replica it measures how far the replica trails the
// primary via pg_last_xact_replay_timestamp(); on a primary (no replay
// in progress) that function is NULL and the COALESCE reports 0, the
// genuine no-replica value. The /readyz body and the
// X-Podium-Read-Only-Lag-Seconds header surface this value.
func (p *Postgres) ReplicationLagSeconds(ctx context.Context) (int, error) {
	var secs int
	err := p.db.QueryRowContext(ctx,
		`SELECT COALESCE(EXTRACT(EPOCH FROM (now() - pg_last_xact_replay_timestamp()))::int, 0)`).Scan(&secs)
	if err != nil {
		return 0, err
	}
	if secs < 0 {
		secs = 0
	}
	return secs, nil
}

// applySchema creates the cross-org tables and their row-level-security
// policies. The Postgres schema mirrors the SQLite layout but uses native
// types: BYTEA for blobs, BOOLEAN for bool fields, TIMESTAMPTZ for timestamps.
// Per spec §13.4 the setup is additive: tables are created when absent and
// columns added after a table first shipped are backfilled by
// applyAdditivePostgres, so a binary upgrade migrates an existing database
// forward in place without a separate migration step. Per-org tables are
// created on demand by ensureOrg.
func (p *Postgres) applySchema() error {
	for _, stmt := range sharedTableStmts {
		if _, err := p.db.Exec(stmt); err != nil {
			return fmt.Errorf("schema: %w (statement: %s)", err, stmt)
		}
	}
	// §13.4 additive migration for the shared tables. applyAdditivePostgres
	// also names the org tables, which do not exist in public; those ALTERs are
	// skipped as undefined_table.
	if err := applyAdditivePostgres(p.db); err != nil {
		return err
	}
	for _, stmt := range sharedIndexStmts {
		if _, err := p.db.Exec(stmt); err != nil {
			return fmt.Errorf("schema: %w (statement: %s)", err, stmt)
		}
	}
	for _, stmt := range rlsStmts {
		if _, err := p.db.Exec(stmt); err != nil {
			return fmt.Errorf("schema: %w (statement: %s)", err, stmt)
		}
	}
	return nil
}

// ensureOrg provisions the per-org schema and its tables, idempotently, and
// caches success so subsequent writes for the org skip the DDL. It returns the
// quoted schema identifier for callers that route a connection at it.
func (p *Postgres) ensureOrg(ctx context.Context, orgID string) (string, error) {
	ident, err := orgSchemaIdent(orgID)
	if err != nil {
		return "", err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.provisioned[orgID] {
		return ident, nil
	}
	conn, err := p.db.Conn(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS `+ident); err != nil {
		return "", fmt.Errorf("provision org schema %s: %w", ident, err)
	}
	// Org tables resolve to the org schema (first on the path); shared tables
	// referenced by the additive migration resolve to public.
	if _, err := conn.ExecContext(ctx, `SET search_path TO `+ident+`, public`); err != nil {
		return "", err
	}
	for _, stmt := range orgTableStmts {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			return "", fmt.Errorf("provision org table: %w (statement: %s)", err, stmt)
		}
	}
	if err := applyAdditivePostgresCtx(ctx, conn); err != nil {
		return "", err
	}
	for _, stmt := range orgIndexStmts {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			return "", fmt.Errorf("provision org index: %w (statement: %s)", err, stmt)
		}
	}
	p.provisioned[orgID] = true
	return ident, nil
}

// org returns a pooled connection pinned to the org's schema for the duration
// of one store call, plus a release func the caller defers. The org schema is
// first on the search_path so the unqualified org-table queries resolve to it;
// public is second so a shared table (vector_pending) still resolves. The
// schema and tables are provisioned on first use.
func (p *Postgres) org(ctx context.Context, orgID string) (*sql.Conn, func(), error) {
	ident, err := p.ensureOrg(ctx, orgID)
	if err != nil {
		return nil, nil, err
	}
	conn, err := p.db.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	if _, err := conn.ExecContext(ctx, `SET search_path TO `+ident+`, public`); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	return conn, func() { _ = conn.Close() }, nil
}

// listOrgSchemas returns the quoted identifiers of every provisioned org schema,
// for the cross-org maintenance scans (deprecated-version purge, layer-deletion
// purge) that operate over all orgs.
func (p *Postgres) listOrgSchemas(ctx context.Context) ([]string, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT nspname FROM pg_namespace WHERE nspname LIKE 'org\_%' ESCAPE '\'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var idents []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		idents = append(idents, quoteIdent(name))
	}
	return idents, rows.Err()
}

// DropOrg removes an org's schema (and every table in it) and its registry row,
// giving the §4.7.1 clean drop-org semantics ("schema-per-org gives clean
// drop-org semantics"). The DROP SCHEMA CASCADE removes exactly one org's data
// and leaves every other org untouched.
func (p *Postgres) DropOrg(ctx context.Context, orgID string) error {
	ident, err := orgSchemaIdent(orgID)
	if err != nil {
		return err
	}
	if _, err := p.db.ExecContext(ctx, `DROP SCHEMA IF EXISTS `+ident+` CASCADE`); err != nil {
		return fmt.Errorf("drop org schema %s: %w", ident, err)
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT set_config('podium.org_id', $1, true)`, orgID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM public.tenants WHERE id = $1`, orgID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM public.vector_pending WHERE tenant_id = $1`, orgID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	p.mu.Lock()
	delete(p.provisioned, orgID)
	p.mu.Unlock()
	return nil
}

// ResetForTest drops every org schema, truncates the shared tables, and clears
// the provisioning cache so a test fixture can start from an empty database
// while reusing the connection pool. Test-support only.
func (p *Postgres) ResetForTest(ctx context.Context) error {
	idents, err := p.listOrgSchemas(ctx)
	if err != nil {
		return err
	}
	for _, ident := range idents {
		if _, err := p.db.ExecContext(ctx, `DROP SCHEMA IF EXISTS `+ident+` CASCADE`); err != nil {
			return err
		}
	}
	if _, err := p.db.ExecContext(ctx,
		`TRUNCATE public.tenants, public.vector_pending RESTART IDENTITY`); err != nil {
		return err
	}
	p.mu.Lock()
	p.provisioned = map[string]bool{}
	p.mu.Unlock()
	return nil
}

// CreateTenant inserts a new tenant. Inserting an existing tenant ID
// is a no-op (matches Memory and SQLite). It provisions the org's schema first
// (§4.7.1) and writes the registry row under the per-request podium.org_id so
// the row passes the row-level-security WITH CHECK.
func (p *Postgres) CreateTenant(ctx context.Context, t Tenant) error {
	if _, err := p.ensureOrg(ctx, t.ID); err != nil {
		return err
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT set_config('podium.org_id', $1, true)`, t.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO public.tenants
			(id, name, storage_quota, search_qps_quota, materialize_rate_quota, audit_volume_quota, max_user_layers, expose_scope_preview)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO NOTHING`,
		t.ID, t.Name,
		t.Quota.StorageBytes, t.Quota.SearchQPS,
		t.Quota.MaterializeRate, t.Quota.AuditVolumePerDay, t.Quota.MaxUserLayers,
		nullBoolFromPtr(t.ExposeScopePreview)); err != nil {
		return err
	}
	return tx.Commit()
}

// GetTenant returns the tenant or ErrTenantNotFound. The read runs under the
// per-request podium.org_id so the row-level-security policy on the tenants
// registry returns only this org's row (§4.7.1).
func (p *Postgres) GetTenant(ctx context.Context, id string) (Tenant, error) {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return Tenant{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT set_config('podium.org_id', $1, true)`, id); err != nil {
		return Tenant{}, err
	}
	row := tx.QueryRowContext(ctx, `
		SELECT id, name, storage_quota, search_qps_quota, materialize_rate_quota, audit_volume_quota, max_user_layers, expose_scope_preview
		FROM public.tenants WHERE id = $1`, id)
	var t Tenant
	var exposeScopePreview sql.NullBool
	err = row.Scan(&t.ID, &t.Name,
		&t.Quota.StorageBytes, &t.Quota.SearchQPS,
		&t.Quota.MaterializeRate, &t.Quota.AuditVolumePerDay, &t.Quota.MaxUserLayers,
		&exposeScopePreview)
	if errors.Is(err, sql.ErrNoRows) {
		return Tenant{}, ErrTenantNotFound
	}
	if err != nil {
		return Tenant{}, err
	}
	t.ExposeScopePreview = ptrFromNullBool(exposeScopePreview)
	if err := tx.Commit(); err != nil {
		return Tenant{}, err
	}
	return t, nil
}

// PutManifest enforces the §4.7 immutability invariant: the same
// (tenant, id, version) with a different content hash is rejected with
// ErrImmutableViolation; the same content hash is an idempotent no-op.
//
// spec: §4.7 — the invariant must hold under concurrent ingest. A
// SELECT-then-INSERT pair lets two writers both observe no row under
// READ COMMITTED and then collide on the primary key, leaking the raw
// unique-violation error (F-1.3.1). A single atomic
// INSERT ... ON CONFLICT DO NOTHING is the immutability anchor instead:
// at most one writer inserts the row, and the loser reads the stored
// hash back to classify the outcome deterministically — a differing hash
// is the conflict (ErrImmutableViolation), an identical hash is the
// idempotent retry.
func (p *Postgres) PutManifest(ctx context.Context, rec ManifestRecord) error {
	conn, release, err := p.org(ctx, rec.TenantID)
	if err != nil {
		return err
	}
	defer release()
	ingestedAt := rec.IngestedAt
	if ingestedAt.IsZero() {
		ingestedAt = time.Now().UTC()
	}
	stampDeprecation(&rec)
	resources, err := MarshalResources(rec.Resources)
	if err != nil {
		return fmt.Errorf("marshal resources: %w", err)
	}
	res, err := conn.ExecContext(ctx, `
		INSERT INTO manifests
			(tenant_id, artifact_id, version, content_hash, type, description,
			 tags, sensitivity, layer, deprecated, ingested_at, frontmatter, body,
			 extends_pin, signature, search_visibility, resources, deprecated_at, deleted_at, skill_raw)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
		ON CONFLICT (tenant_id, artifact_id, version) DO NOTHING`,
		rec.TenantID, rec.ArtifactID, rec.Version, rec.ContentHash,
		rec.Type, rec.Description,
		strings.Join(rec.Tags, "\n"),
		rec.Sensitivity, rec.Layer,
		rec.Deprecated, ingestedAt.UTC(),
		rec.Frontmatter, rec.Body,
		rec.ExtendsPin, rec.Signature, rec.SearchVisibility, resources,
		nullTimePG(rec.DeprecatedAt), nullTimePG(rec.DeletedAt), rec.SkillRaw)
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
	err = conn.QueryRowContext(ctx, `
		SELECT content_hash FROM manifests
		WHERE tenant_id = $1 AND artifact_id = $2 AND version = $3`,
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
// outbox row in one transaction, so a crash never leaves a stored artifact
// without a queued embedding. The pending row is written only when the
// manifest is newly inserted; an idempotent re-ingest commits the no-op
// without re-queuing, and a differing hash rolls back with
// ErrImmutableViolation. The manifest lands in the org schema; the outbox row
// in the cross-org public.vector_pending, both within the one transaction.
func (p *Postgres) PutManifestWithVectorPending(ctx context.Context, rec ManifestRecord, pending VectorPending) error {
	ingestedAt := rec.IngestedAt
	if ingestedAt.IsZero() {
		ingestedAt = time.Now().UTC()
	}
	stampDeprecation(&rec)
	resources, err := MarshalResources(rec.Resources)
	if err != nil {
		return fmt.Errorf("marshal resources: %w", err)
	}
	conn, release, err := p.org(ctx, rec.TenantID)
	if err != nil {
		return err
	}
	defer release()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO manifests
			(tenant_id, artifact_id, version, content_hash, type, description,
			 tags, sensitivity, layer, deprecated, ingested_at, frontmatter, body,
			 extends_pin, signature, search_visibility, resources, deprecated_at, deleted_at, skill_raw)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)
		ON CONFLICT (tenant_id, artifact_id, version) DO NOTHING`,
		rec.TenantID, rec.ArtifactID, rec.Version, rec.ContentHash,
		rec.Type, rec.Description,
		strings.Join(rec.Tags, "\n"),
		rec.Sensitivity, rec.Layer,
		rec.Deprecated, ingestedAt.UTC(),
		rec.Frontmatter, rec.Body,
		rec.ExtendsPin, rec.Signature, rec.SearchVisibility, resources,
		nullTimePG(rec.DeprecatedAt), nullTimePG(rec.DeletedAt), rec.SkillRaw)
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
			WHERE tenant_id = $1 AND artifact_id = $2 AND version = $3`,
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
		INSERT INTO public.vector_pending
			(tenant_id, artifact_id, version, text, enqueued_at, attempts, next_retry_at, last_error)
		VALUES ($1, $2, $3, $4, $5, $6, $7, '')
		ON CONFLICT (tenant_id, artifact_id, version) DO UPDATE SET
			text = EXCLUDED.text, enqueued_at = EXCLUDED.enqueued_at,
			attempts = 0, next_retry_at = EXCLUDED.next_retry_at, last_error = ''`,
		rec.TenantID, rec.ArtifactID, rec.Version, []byte(pending.Text),
		pending.EnqueuedAt.UTC(), pending.Attempts, pending.NextRetryAt.UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

// ListVectorPending returns up to limit eligible outbox rows, oldest first.
// The outbox is the cross-org public.vector_pending, scanned by the trusted
// drain worker, so it is not routed through a single org's schema.
func (p *Postgres) ListVectorPending(ctx context.Context, limit int, now time.Time) ([]VectorPending, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := p.db.QueryContext(ctx, `
		SELECT tenant_id, artifact_id, version, text, enqueued_at, attempts, next_retry_at, last_error
		FROM public.vector_pending
		WHERE next_retry_at <= $1
		ORDER BY enqueued_at ASC
		LIMIT $2`, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VectorPending
	for rows.Next() {
		var pend VectorPending
		var text []byte
		if err := rows.Scan(&pend.TenantID, &pend.ArtifactID, &pend.Version, &text,
			&pend.EnqueuedAt, &pend.Attempts, &pend.NextRetryAt, &pend.LastError); err != nil {
			return nil, err
		}
		pend.Text = string(text)
		out = append(out, pend)
	}
	return out, rows.Err()
}

// MarkVectorPendingDone removes a drained outbox row.
func (p *Postgres) MarkVectorPendingDone(ctx context.Context, tenantID, artifactID, version string) error {
	_, err := p.db.ExecContext(ctx, `
		DELETE FROM public.vector_pending
		WHERE tenant_id = $1 AND artifact_id = $2 AND version = $3`,
		tenantID, artifactID, version)
	return err
}

// MarkVectorPendingRetry records a failed drain attempt with backoff.
func (p *Postgres) MarkVectorPendingRetry(ctx context.Context, tenantID, artifactID, version string, nextRetryAt time.Time, errMsg string) error {
	_, err := p.db.ExecContext(ctx, `
		UPDATE public.vector_pending
		SET attempts = attempts + 1, next_retry_at = $1, last_error = $2
		WHERE tenant_id = $3 AND artifact_id = $4 AND version = $5`,
		nextRetryAt.UTC(), errMsg, tenantID, artifactID, version)
	return err
}

// VectorOutboxStats returns the outbox depth and oldest enqueue time.
func (p *Postgres) VectorOutboxStats(ctx context.Context) (int, time.Time, error) {
	var depth int
	var oldest sql.NullTime
	err := p.db.QueryRowContext(ctx, `
		SELECT COUNT(*), MIN(enqueued_at) FROM public.vector_pending`).Scan(&depth, &oldest)
	if err != nil {
		return 0, time.Time{}, err
	}
	var t time.Time
	if oldest.Valid {
		t = oldest.Time
	}
	return depth, t, nil
}

// GetManifest returns the manifest or ErrNotFound.
func (p *Postgres) GetManifest(ctx context.Context, tenantID, artifactID, version string) (ManifestRecord, error) {
	conn, release, err := p.org(ctx, tenantID)
	if err != nil {
		return ManifestRecord{}, err
	}
	defer release()
	row := conn.QueryRowContext(ctx, `
		SELECT tenant_id, artifact_id, version, content_hash, type, description,
		       tags, sensitivity, layer, deprecated, ingested_at, frontmatter, body,
		       extends_pin, signature, search_visibility, resources, deprecated_at, deleted_at, skill_raw
		FROM manifests
		WHERE tenant_id = $1 AND artifact_id = $2 AND version = $3 AND deleted_at IS NULL`,
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
	conn, release, err := p.org(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer release()
	rows, err := conn.QueryContext(ctx, `
		SELECT tenant_id, artifact_id, version, content_hash, type, description,
		       tags, sensitivity, layer, deprecated, ingested_at, frontmatter, body,
		       extends_pin, signature, search_visibility, resources, deprecated_at, deleted_at, skill_raw
		FROM manifests
		WHERE tenant_id = $1 AND deleted_at IS NULL
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

// PurgeDeprecatedManifests removes deprecated versions whose
// deprecated_at predates `before` (§8.4 90-day window). It is a cross-org
// maintenance scan, so it iterates every provisioned org schema.
func (p *Postgres) PurgeDeprecatedManifests(ctx context.Context, before time.Time) (int, error) {
	idents, err := p.listOrgSchemas(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, ident := range idents {
		n, err := p.purgeDeprecatedInSchema(ctx, ident, before)
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

func (p *Postgres) purgeDeprecatedInSchema(ctx context.Context, ident string, before time.Time) (int, error) {
	conn, err := p.db.Conn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SET search_path TO `+ident+`, public`); err != nil {
		return 0, err
	}
	res, err := conn.ExecContext(ctx, `
		DELETE FROM manifests
		WHERE deprecated = TRUE AND deprecated_at IS NOT NULL AND deprecated_at < $1`,
		before.UTC())
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// PutDomain upserts the DOMAIN.md record for a (tenant, layer, path).
func (p *Postgres) PutDomain(ctx context.Context, rec DomainRecord) error {
	conn, release, err := p.org(ctx, rec.TenantID)
	if err != nil {
		return err
	}
	defer release()
	_, err = conn.ExecContext(ctx, `
		INSERT INTO domains (tenant_id, layer, path, raw)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (tenant_id, layer, path) DO UPDATE SET raw = EXCLUDED.raw`,
		rec.TenantID, rec.Layer, rec.Path, rec.Raw)
	return err
}

// ListDomains returns every domain record for the tenant, ordered by
// path then layer (matches Memory and SQLite).
func (p *Postgres) ListDomains(ctx context.Context, tenantID string) ([]DomainRecord, error) {
	conn, release, err := p.org(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer release()
	rows, err := conn.QueryContext(ctx, `
		SELECT tenant_id, layer, path, raw
		FROM domains
		WHERE tenant_id = $1
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
func (p *Postgres) PutDependency(ctx context.Context, tenantID string, edge DependencyEdge) error {
	conn, release, err := p.org(ctx, tenantID)
	if err != nil {
		return err
	}
	defer release()
	_, err = conn.ExecContext(ctx, `
		INSERT INTO dependencies (tenant_id, from_artifact, to_artifact, kind)
		VALUES ($1, $2, $3, $4)`,
		tenantID, edge.From, edge.To, edge.Kind)
	return err
}

// DependentsOf returns every edge whose To matches artifactID.
func (p *Postgres) DependentsOf(ctx context.Context, tenantID, artifactID string) ([]DependencyEdge, error) {
	conn, release, err := p.org(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer release()
	rows, err := conn.QueryContext(ctx, `
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

// DependencyInDegree counts distinct dependents per target artifact (§4.7.3).
func (p *Postgres) DependencyInDegree(ctx context.Context, tenantID string) (map[string]int, error) {
	conn, release, err := p.org(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer release()
	rows, err := conn.QueryContext(ctx, `
		SELECT to_artifact, COUNT(DISTINCT from_artifact)
		FROM dependencies
		WHERE tenant_id = $1
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
func (p *Postgres) GrantAdmin(ctx context.Context, g AdminGrant) error {
	granted := g.Granted
	if granted.IsZero() {
		granted = time.Now().UTC()
	}
	conn, release, err := p.org(ctx, g.OrgID)
	if err != nil {
		return err
	}
	defer release()
	_, err = conn.ExecContext(ctx, `
		INSERT INTO admin_grants (user_id, org_id, granted_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, org_id) DO UPDATE SET granted_at = EXCLUDED.granted_at`,
		g.UserID, g.OrgID, granted.UTC())
	return err
}

// IsAdmin checks the admin grant table.
func (p *Postgres) IsAdmin(ctx context.Context, userID, orgID string) (bool, error) {
	conn, release, err := p.org(ctx, orgID)
	if err != nil {
		return false, err
	}
	defer release()
	row := conn.QueryRowContext(ctx, `
		SELECT 1 FROM admin_grants WHERE user_id = $1 AND org_id = $2`,
		userID, orgID)
	var dummy int
	err = row.Scan(&dummy)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// ListAdminGrants returns every grant for orgID, ordered by user_id.
func (p *Postgres) ListAdminGrants(ctx context.Context, orgID string) ([]AdminGrant, error) {
	conn, release, err := p.org(ctx, orgID)
	if err != nil {
		return nil, err
	}
	defer release()
	rows, err := conn.QueryContext(ctx, `
		SELECT user_id, org_id, granted_at FROM admin_grants
		WHERE org_id = $1 ORDER BY user_id`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdminGrant
	for rows.Next() {
		var g AdminGrant
		if err := rows.Scan(&g.UserID, &g.OrgID, &g.Granted); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// RevokeAdmin removes the admin grant; missing rows are a no-op.
func (p *Postgres) RevokeAdmin(ctx context.Context, userID, orgID string) error {
	conn, release, err := p.org(ctx, orgID)
	if err != nil {
		return err
	}
	defer release()
	_, err = conn.ExecContext(ctx, `
		DELETE FROM admin_grants WHERE user_id = $1 AND org_id = $2`,
		userID, orgID)
	return err
}

// PutLayerConfig inserts or replaces a layer config.
func (p *Postgres) PutLayerConfig(ctx context.Context, cfg LayerConfig) error {
	createdAt := cfg.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	conn, release, err := p.org(ctx, cfg.TenantID)
	if err != nil {
		return err
	}
	defer release()
	_, err = conn.ExecContext(ctx, `
		INSERT INTO layer_configs
			(tenant_id, id, source_type, repo, ref, root, local_path, ord,
			 user_defined, owner, public, organization, groups, users,
			 webhook_secret, last_ingested_ref, force_push_policy, git_provider, created_at, deleted_at, last_ingested_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
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
			git_provider = EXCLUDED.git_provider,
			created_at = EXCLUDED.created_at,
			deleted_at = EXCLUDED.deleted_at,
			last_ingested_at = EXCLUDED.last_ingested_at`,
		cfg.TenantID, cfg.ID, cfg.SourceType, cfg.Repo, cfg.Ref, cfg.Root, cfg.LocalPath,
		cfg.Order, cfg.UserDefined, cfg.Owner,
		cfg.Public, cfg.Organization,
		strings.Join(cfg.Groups, "\n"), strings.Join(cfg.Users, "\n"),
		cfg.WebhookSecret, cfg.LastIngestedRef, cfg.ForcePushPolicy, cfg.GitProvider,
		createdAt.UTC(), nullTimePG(cfg.DeletedAt), nullTimePG(cfg.LastIngestedAt))
	return err
}

// GetLayerConfig returns one layer config or ErrNotFound.
func (p *Postgres) GetLayerConfig(ctx context.Context, tenantID, id string) (LayerConfig, error) {
	conn, release, err := p.org(ctx, tenantID)
	if err != nil {
		return LayerConfig{}, err
	}
	defer release()
	row := conn.QueryRowContext(ctx, `
		SELECT tenant_id, id, source_type, repo, ref, root, local_path, ord,
		       user_defined, owner, public, organization, groups, users,
		       webhook_secret, last_ingested_ref, force_push_policy, git_provider, created_at, deleted_at, last_ingested_at
		FROM layer_configs
		WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NULL`, tenantID, id)
	cfg, err := scanLayerConfigPG(row)
	if errors.Is(err, sql.ErrNoRows) {
		return LayerConfig{}, ErrNotFound
	}
	return cfg, err
}

// ListLayerConfigs returns every layer for the tenant in Order ascending.
func (p *Postgres) ListLayerConfigs(ctx context.Context, tenantID string) ([]LayerConfig, error) {
	conn, release, err := p.org(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer release()
	rows, err := conn.QueryContext(ctx, `
		SELECT tenant_id, id, source_type, repo, ref, root, local_path, ord,
		       user_defined, owner, public, organization, groups, users,
		       webhook_secret, last_ingested_ref, force_push_policy, git_provider, created_at, deleted_at, last_ingested_at
		FROM layer_configs WHERE tenant_id = $1 AND deleted_at IS NULL
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

// DeleteLayerConfig soft-deletes a layer and the artifacts ingested from
// it (§8.4): both rows get a deleted_at tombstone, hiding them from
// normal reads while keeping them recoverable for 30 days.
func (p *Postgres) DeleteLayerConfig(ctx context.Context, tenantID, id string) error {
	conn, release, err := p.org(ctx, tenantID)
	if err != nil {
		return err
	}
	defer release()
	now := time.Now().UTC()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		UPDATE layer_configs SET deleted_at = $1
		WHERE tenant_id = $2 AND id = $3 AND deleted_at IS NULL`, now, tenantID, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE manifests SET deleted_at = $1
		WHERE tenant_id = $2 AND layer = $3 AND deleted_at IS NULL`, now, tenantID, id); err != nil {
		return err
	}
	return tx.Commit()
}

// RestoreLayerConfig clears the soft-delete tombstone on a layer and its
// artifacts (§8.4 admin recovery).
func (p *Postgres) RestoreLayerConfig(ctx context.Context, tenantID, id string) error {
	conn, release, err := p.org(ctx, tenantID)
	if err != nil {
		return err
	}
	defer release()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `
		UPDATE layer_configs SET deleted_at = NULL
		WHERE tenant_id = $1 AND id = $2 AND deleted_at IS NOT NULL`, tenantID, id)
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
		WHERE tenant_id = $1 AND layer = $2 AND deleted_at IS NOT NULL`, tenantID, id); err != nil {
		return err
	}
	return tx.Commit()
}

// ListDeletedLayerConfigs returns the tenant's soft-deleted layers.
func (p *Postgres) ListDeletedLayerConfigs(ctx context.Context, tenantID string) ([]LayerConfig, error) {
	conn, release, err := p.org(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer release()
	rows, err := conn.QueryContext(ctx, `
		SELECT tenant_id, id, source_type, repo, ref, root, local_path, ord,
		       user_defined, owner, public, organization, groups, users,
		       webhook_secret, last_ingested_ref, force_push_policy, git_provider, created_at, deleted_at, last_ingested_at
		FROM layer_configs WHERE tenant_id = $1 AND deleted_at IS NOT NULL
		ORDER BY id ASC`, tenantID)
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

// PurgeExpiredLayerDeletions hard-deletes soft-deleted layers and their
// artifacts whose deleted_at predates `before` (§8.4 30-day window end). It is
// a cross-org maintenance scan, so it iterates every provisioned org schema.
func (p *Postgres) PurgeExpiredLayerDeletions(ctx context.Context, before time.Time) (int, error) {
	idents, err := p.listOrgSchemas(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, ident := range idents {
		n, err := p.purgeExpiredLayersInSchema(ctx, ident, before)
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

func (p *Postgres) purgeExpiredLayersInSchema(ctx context.Context, ident string, before time.Time) (int, error) {
	cutoff := before.UTC()
	conn, err := p.db.Conn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SET search_path TO `+ident+`, public`); err != nil {
		return 0, err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM manifests
		WHERE deleted_at IS NOT NULL AND deleted_at < $1`, cutoff); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `
		DELETE FROM layer_configs
		WHERE deleted_at IS NOT NULL AND deleted_at < $1`, cutoff)
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

// scanManifestPG scans a manifest row from Postgres. Differs from
// the SQLite scanner in two ways: deprecated is BOOLEAN (scanned
// directly into bool) and ingested_at is TIMESTAMPTZ (scanned into
// time.Time). Frontmatter and Body come back as []byte directly.
func scanManifestPG(scanner rowScanner) (ManifestRecord, error) {
	var rec ManifestRecord
	var tags string
	var resources []byte
	var deprecatedAt, deletedAt sql.NullTime
	err := scanner.Scan(
		&rec.TenantID, &rec.ArtifactID, &rec.Version, &rec.ContentHash,
		&rec.Type, &rec.Description, &tags, &rec.Sensitivity, &rec.Layer,
		&rec.Deprecated, &rec.IngestedAt, &rec.Frontmatter, &rec.Body,
		&rec.ExtendsPin, &rec.Signature, &rec.SearchVisibility, &resources,
		&deprecatedAt, &deletedAt, &rec.SkillRaw)
	if err != nil {
		return ManifestRecord{}, err
	}
	if tags != "" {
		rec.Tags = strings.Split(tags, "\n")
	}
	rec.DeprecatedAt = ptrFromNullTime(deprecatedAt)
	rec.DeletedAt = ptrFromNullTime(deletedAt)
	if rec.Resources, err = UnmarshalResources(resources); err != nil {
		return ManifestRecord{}, err
	}
	return rec, nil
}

// nullTimePG renders an optional timestamp for a Postgres TIMESTAMPTZ
// param: nil persists as NULL, a value as UTC.
func nullTimePG(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}

// ptrFromNullTime converts a scanned NULL-able timestamp to a pointer.
func ptrFromNullTime(nt sql.NullTime) *time.Time {
	if !nt.Valid {
		return nil
	}
	v := nt.Time.UTC()
	return &v
}

// scanLayerConfigPG scans a layer config row from Postgres. Bool
// columns are scanned directly; timestamps come back as time.Time.
func scanLayerConfigPG(scanner rowScanner) (LayerConfig, error) {
	var cfg LayerConfig
	var groups, users string
	var deletedAt, lastIngestedAt sql.NullTime
	err := scanner.Scan(
		&cfg.TenantID, &cfg.ID, &cfg.SourceType,
		&cfg.Repo, &cfg.Ref, &cfg.Root, &cfg.LocalPath,
		&cfg.Order, &cfg.UserDefined, &cfg.Owner,
		&cfg.Public, &cfg.Organization, &groups, &users,
		&cfg.WebhookSecret, &cfg.LastIngestedRef, &cfg.ForcePushPolicy, &cfg.GitProvider,
		&cfg.CreatedAt, &deletedAt, &lastIngestedAt)
	if err != nil {
		return LayerConfig{}, err
	}
	if groups != "" {
		cfg.Groups = strings.Split(groups, "\n")
	}
	if users != "" {
		cfg.Users = strings.Split(users, "\n")
	}
	cfg.DeletedAt = ptrFromNullTime(deletedAt)
	cfg.LastIngestedAt = ptrFromNullTime(lastIngestedAt)
	return cfg, nil
}
