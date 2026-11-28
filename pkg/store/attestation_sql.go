package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// attestationTableStmts create the §4.7.11 tenant tables of the SQL stores:
// the stored bundles and the links. Both backends share them; Postgres
// creates them in each org schema, where the schema is the tenant boundary.
//
// Spec: §4.7.11 (Attestation storage)
func attestationTableStmts(blob, ts string) []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS attestations (
			tenant_id TEXT NOT NULL,
			digest TEXT NOT NULL,
			bundle ` + blob + ` NOT NULL,
			layer_id TEXT NOT NULL DEFAULT '',
			source_ref TEXT NOT NULL DEFAULT '',
			predicate_type TEXT NOT NULL DEFAULT '',
			instance TEXT NOT NULL DEFAULT '',
			source_repository TEXT NOT NULL DEFAULT '',
			source_repository_owner TEXT NOT NULL DEFAULT '',
			source_repository_ref TEXT NOT NULL DEFAULT '',
			signer_issuer TEXT NOT NULL DEFAULT '',
			signer_identity TEXT NOT NULL DEFAULT '',
			log_index BIGINT,
			attested_at ` + ts + ` NOT NULL,
			stored_at ` + ts + ` NOT NULL,
			PRIMARY KEY (tenant_id, digest)
		)`,
		`CREATE TABLE IF NOT EXISTS artifact_attestations (
			tenant_id TEXT NOT NULL,
			artifact_id TEXT NOT NULL,
			version TEXT NOT NULL,
			predicate_type TEXT NOT NULL,
			instance TEXT NOT NULL,
			signer_issuer TEXT NOT NULL,
			signer_identity TEXT NOT NULL,
			digest TEXT NOT NULL,
			verdict TEXT NOT NULL,
			verified_under TEXT NOT NULL DEFAULT '',
			linked_at ` + ts + ` NOT NULL,
			PRIMARY KEY (tenant_id, artifact_id, version, predicate_type, instance, signer_issuer, signer_identity)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_artifact_attestations_digest
			ON artifact_attestations(tenant_id, digest)`,
	}
}

// rootRecordTableStmt creates the §4.7.11 root records. An instance is
// configured for the whole deployment, so the table has no tenant column and
// Postgres keeps it in public.
func rootRecordTableStmt(table, ts string) string {
	return `CREATE TABLE IF NOT EXISTS ` + table + ` (
		instance TEXT PRIMARY KEY,
		root_digest TEXT NOT NULL,
		root_time ` + ts + ` NOT NULL
	)`
}

// attSQL holds what differs between the SQLite and the Postgres forms of the
// attestation statements: the placeholder syntax, the form of a time
// argument, and the table that holds the root records.
type attSQL struct {
	pg bool
}

// q rewrites the ? placeholders of query to $n on Postgres. No statement of
// this file holds a ? in a literal.
func (d attSQL) q(query string) string {
	if !d.pg {
		return query
	}
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// tm renders a time argument: a TIMESTAMPTZ value on Postgres and RFC 3339
// text on SQLite, at the stored precision in both.
func (d attSQL) tm(t time.Time) any {
	t = normalizeMark(t)
	if d.pg {
		return t
	}
	return t.Format(time.RFC3339Nano)
}

// rootsTable names the table of the root records.
func (d attSQL) rootsTable() string {
	if d.pg {
		return "public.attestation_roots"
	}
	return "attestation_roots"
}

// timeCol scans a time column that either backend returns: a time.Time from
// Postgres and RFC 3339 text from SQLite.
type timeCol struct{ t time.Time }

// Scan implements sql.Scanner.
func (c *timeCol) Scan(v any) error {
	switch x := v.(type) {
	case time.Time:
		c.t = x.UTC()
	case string:
		return c.parse(x)
	case []byte:
		return c.parse(string(x))
	case nil:
		c.t = time.Time{}
	default:
		return fmt.Errorf("store: scan a time from %T", v)
	}
	return nil
}

func (c *timeCol) parse(s string) error {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return fmt.Errorf("store: parse time %q: %w", s, err)
	}
	c.t = t.UTC()
	return nil
}

// sqlExec is the part of *sql.Tx, *sql.DB, and *sql.Conn the statements use.
type sqlExec interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// writeAttestations applies w inside tx. With a claim, its first statement
// is a no-op update of the layer row under the claim, and under w.Policy
// when it is set, which takes the row's write lock and refuses the write
// when the layer no longer holds the claim or that policy.
func (d attSQL) writeAttestations(ctx context.Context, tx *sql.Tx, tenantID, layerID, claim string, w AttestationWrites) (AttestationWriteResult, error) {
	if claim != "" {
		if err := d.checkClaim(ctx, tx, tenantID, layerID, claim, w.Policy); err != nil {
			return AttestationWriteResult{}, err
		}
	}
	for _, b := range w.Bundles {
		if err := d.insertBundle(ctx, tx, tenantID, normalizeStoredAttestation(b)); err != nil {
			return AttestationWriteResult{}, err
		}
	}
	var res AttestationWriteResult
	for _, v := range w.Versions {
		updated, err := d.writeVersion(ctx, tx, tenantID, v)
		if err != nil {
			return AttestationWriteResult{}, err
		}
		res.Updated = append(res.Updated, updated...)
	}
	return res, nil
}

// checkClaim locks the layer row while it holds claim, and the stored trust
// policy p when p is set, and returns ErrIngestSuperseded otherwise.
func (d attSQL) checkClaim(ctx context.Context, tx *sql.Tx, tenantID, layerID, claim string, p *TrustPolicy) error {
	q := `UPDATE layer_configs SET ingest_claim = ingest_claim
		WHERE tenant_id = ? AND id = ? AND deleted_at IS NULL AND ingest_claim = ?`
	args := []any{tenantID, layerID, claim}
	if p != nil {
		mode, reqs, root, err := policyColumns(*p)
		if err != nil {
			return err
		}
		q += ` AND trust_mode = ? AND trust_requirements = ? AND trust_layer_root = ?`
		args = append(args, mode, reqs, root)
	}
	held, err := rowsChanged(tx.ExecContext(ctx, d.q(q), args...))
	if err != nil {
		return err
	}
	if !held {
		return ErrIngestSuperseded
	}
	return nil
}

// insertBundle stores a bundle the tenant does not hold yet. A stored bundle
// takes a no-op update, which locks its row until the write commits, so a
// purge's sweep that runs meanwhile skips it (pgSweepStmts) rather than
// delete a bundle a link of this write references.
func (d attSQL) insertBundle(ctx context.Context, tx *sql.Tx, tenantID string, b StoredAttestation) error {
	var logIndex any
	if b.LogIndex != nil {
		logIndex = *b.LogIndex
	}
	_, err := tx.ExecContext(ctx, d.q(`
		INSERT INTO attestations (tenant_id, digest, bundle, layer_id, source_ref, predicate_type, instance,
			source_repository, source_repository_owner, source_repository_ref, signer_issuer, signer_identity,
			log_index, attested_at, stored_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (tenant_id, digest) DO UPDATE SET digest = excluded.digest`),
		tenantID, b.Digest, b.Bundle, b.LayerID, b.SourceRef, b.PredicateType, b.Instance,
		b.SourceRepository, b.SourceRepositoryOwner, b.SourceRepositoryRef, b.SignerIssuer, b.SignerIdentity,
		logIndex, d.tm(b.AttestedAt), d.tm(b.StoredAt))
	if err != nil {
		return fmt.Errorf("store: store attestation %s: %w", b.Digest, err)
	}
	return nil
}

// writeVersion applies the write of one version and returns the link
// updates that applied.
func (d attSQL) writeVersion(ctx context.Context, tx *sql.Tx, tenantID string, v VersionAttestations) ([]AttestationLink, error) {
	if v.ReplaceLinks {
		if _, err := tx.ExecContext(ctx, d.q(`
			DELETE FROM artifact_attestations WHERE tenant_id = ? AND artifact_id = ? AND version = ?`),
			tenantID, v.ArtifactID, v.Version); err != nil {
			return nil, err
		}
	}
	for _, l := range v.Links {
		l = normalizeLink(l)
		if _, err := tx.ExecContext(ctx, d.q(`
			INSERT INTO artifact_attestations (tenant_id, artifact_id, version, predicate_type, instance,
				signer_issuer, signer_identity, digest, verdict, verified_under, linked_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (tenant_id, artifact_id, version, predicate_type, instance, signer_issuer, signer_identity)
			DO UPDATE SET digest = excluded.digest, verdict = excluded.verdict,
				verified_under = excluded.verified_under, linked_at = excluded.linked_at`),
			tenantID, v.ArtifactID, v.Version, l.PredicateType, l.Instance, l.SignerIssuer, l.SignerIdentity,
			l.Digest, l.Verdict, l.VerifiedUnder, d.tm(l.LinkedAt)); err != nil {
			return nil, err
		}
	}
	var updated []AttestationLink
	for _, u := range v.Updates {
		l := normalizeLink(u.Link)
		l.ArtifactID, l.Version = v.ArtifactID, v.Version
		changed, err := rowsChanged(tx.ExecContext(ctx, d.q(`
			UPDATE artifact_attestations SET verdict = ?, verified_under = ?, linked_at = ?
			WHERE tenant_id = ? AND artifact_id = ? AND version = ? AND predicate_type = ? AND instance = ?
				AND signer_issuer = ? AND signer_identity = ? AND digest = ? AND verdict = ? AND verified_under = ?`),
			l.Verdict, l.VerifiedUnder, d.tm(l.LinkedAt),
			tenantID, v.ArtifactID, v.Version, l.PredicateType, l.Instance, l.SignerIssuer, l.SignerIdentity, l.Digest,
			u.ReadVerdict, u.ReadVerifiedUnder))
		if err != nil {
			return nil, err
		}
		if changed {
			updated = append(updated, l)
		}
	}
	return updated, d.writeOutcome(ctx, tx, tenantID, v)
}

// writeOutcome sets the outcome of the version's stored row, under the
// condition OutcomeIf names.
func (d attSQL) writeOutcome(ctx context.Context, tx *sql.Tx, tenantID string, v VersionAttestations) error {
	if v.Outcome == nil {
		return nil
	}
	o := normalizedOutcome(*v.Outcome)
	failure, err := failureColumn(o.Failure)
	if err != nil {
		return err
	}
	var evaluated any
	if o.EvaluatedAt != nil {
		evaluated = d.tm(*o.EvaluatedAt)
	}
	query := `UPDATE manifests SET attestation_outcome = ?, attestation_failure = ?, attestation_evaluated_at = ?
		WHERE tenant_id = ? AND artifact_id = ? AND version = ?`
	args := []any{o.Outcome, failure, evaluated, tenantID, v.ArtifactID, v.Version}
	if v.OutcomeIf != "" {
		query += ` AND attestation_outcome = ?`
		args = append(args, v.OutcomeIf)
	}
	_, err = tx.ExecContext(ctx, d.q(query), args...)
	return err
}

// bundleColumns are the columns a bundle read returns, in scanBundle's
// order.
const bundleColumns = `tenant_id, digest, bundle, layer_id, source_ref, predicate_type, instance,
	source_repository, source_repository_owner, source_repository_ref, signer_issuer, signer_identity,
	log_index, attested_at, stored_at`

// scanBundle scans one bundle row.
func scanBundle(sc rowScanner) (StoredAttestation, error) {
	var a StoredAttestation
	var logIndex sql.NullInt64
	var attested, stored timeCol
	if err := sc.Scan(&a.TenantID, &a.Digest, &a.Bundle, &a.LayerID, &a.SourceRef, &a.PredicateType, &a.Instance,
		&a.SourceRepository, &a.SourceRepositoryOwner, &a.SourceRepositoryRef, &a.SignerIssuer, &a.SignerIdentity,
		&logIndex, &attested, &stored); err != nil {
		return StoredAttestation{}, err
	}
	if logIndex.Valid {
		v := logIndex.Int64
		a.LogIndex = &v
	}
	a.AttestedAt, a.StoredAt = attested.t, stored.t
	return a, nil
}

// getAttestation reads one bundle.
func (d attSQL) getAttestation(ctx context.Context, ex sqlExec, tenantID, digest string) (StoredAttestation, error) {
	a, err := scanBundle(ex.QueryRowContext(ctx, d.q(`SELECT `+bundleColumns+` FROM attestations
		WHERE tenant_id = ? AND digest = ?`), tenantID, digest))
	if errors.Is(err, sql.ErrNoRows) {
		return StoredAttestation{}, ErrNotFound
	}
	return a, err
}

// listAttestations reads the tenant's bundles by digest.
func (d attSQL) listAttestations(ctx context.Context, ex sqlExec, tenantID string) ([]StoredAttestation, error) {
	rows, err := ex.QueryContext(ctx, d.q(`SELECT `+bundleColumns+` FROM attestations
		WHERE tenant_id = ? ORDER BY digest`), tenantID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []StoredAttestation
	for rows.Next() {
		a, err := scanBundle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// listLinks reads the tenant's links that f selects.
func (d attSQL) listLinks(ctx context.Context, ex sqlExec, tenantID string, f LinkFilter) ([]AttestationLink, error) {
	query := `SELECT l.artifact_id, l.version, l.predicate_type, l.instance, l.signer_issuer, l.signer_identity,
			l.digest, l.verdict, l.verified_under, l.linked_at, a.attested_at
		FROM artifact_attestations l LEFT JOIN attestations a ON a.tenant_id = l.tenant_id AND a.digest = l.digest
		WHERE l.tenant_id = ?`
	args := []any{tenantID}
	for _, c := range []struct{ col, val string }{{"l.artifact_id", f.ArtifactID}, {"l.version", f.Version}, {"l.digest", f.Digest}} {
		if c.val != "" {
			query += ` AND ` + c.col + ` = ?`
			args = append(args, c.val)
		}
	}
	if f.LayerID != "" {
		query += ` AND EXISTS (SELECT 1 FROM manifests m WHERE m.tenant_id = l.tenant_id
			AND m.artifact_id = l.artifact_id AND m.version = l.version AND m.layer = ?)`
		args = append(args, f.LayerID)
	}
	rows, err := ex.QueryContext(ctx, d.q(query), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []AttestationLink
	for rows.Next() {
		var l AttestationLink
		var linked, attested timeCol
		if err := rows.Scan(&l.ArtifactID, &l.Version, &l.PredicateType, &l.Instance, &l.SignerIssuer, &l.SignerIdentity,
			&l.Digest, &l.Verdict, &l.VerifiedUnder, &linked, &attested); err != nil {
			return nil, err
		}
		l.LinkedAt, l.AttestedAt = linked.t, attested.t
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortLinks(out)
	return out, nil
}

// listRoots reads every root record.
func (d attSQL) listRoots(ctx context.Context, ex sqlExec) ([]RootRecord, error) {
	rows, err := ex.QueryContext(ctx, `SELECT instance, root_digest, root_time FROM `+d.rootsTable()+` ORDER BY instance`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []RootRecord
	for rows.Next() {
		var r RootRecord
		var at timeCol
		if err := rows.Scan(&r.Instance, &r.RootDigest, &at); err != nil {
			return nil, err
		}
		r.RootTime = at.t
		out = append(out, r)
	}
	return out, rows.Err()
}

// replaceRoot writes next when the stored record equals prev, in one
// conditional statement: an insert that a stored record turns into nothing
// for prev nil, and an update matched on the stored values otherwise.
func (d attSQL) replaceRoot(ctx context.Context, ex sqlExec, prev *RootRecord, next RootRecord) (bool, error) {
	if prev == nil {
		return rowsChanged(ex.ExecContext(ctx, d.q(`INSERT INTO `+d.rootsTable()+` (instance, root_digest, root_time)
			VALUES (?, ?, ?) ON CONFLICT (instance) DO NOTHING`), next.Instance, next.RootDigest, d.tm(next.RootTime)))
	}
	return rowsChanged(ex.ExecContext(ctx, d.q(`UPDATE `+d.rootsTable()+` SET root_digest = ?, root_time = ?
		WHERE instance = ? AND root_digest = ? AND root_time = ?`),
		next.RootDigest, d.tm(next.RootTime), next.Instance, prev.RootDigest, d.tm(prev.RootTime)))
}

// sweepStmts apply the §8.4 retention of a purge: the links of every version
// the store no longer holds go, and then every bundle that no version links.
//
// Spec: §4.7.11 (Attestation storage), §8.4
var sweepStmts = []string{
	`DELETE FROM artifact_attestations WHERE NOT EXISTS (SELECT 1 FROM manifests m
		WHERE m.tenant_id = artifact_attestations.tenant_id AND m.artifact_id = artifact_attestations.artifact_id
			AND m.version = artifact_attestations.version)`,
	`DELETE FROM attestations WHERE NOT EXISTS (SELECT 1 FROM artifact_attestations l
		WHERE l.tenant_id = attestations.tenant_id AND l.digest = attestations.digest)`,
}

// pgSweepStmts are the Postgres form of sweepStmts: the bundle deletion
// skips a row that an attestation write in flight holds locked
// (insertBundle), whose link would reference it once that write commits.
var pgSweepStmts = []string{
	sweepStmts[0],
	`DELETE FROM attestations WHERE (tenant_id, digest) IN (
		SELECT a.tenant_id, a.digest FROM attestations a WHERE NOT EXISTS (SELECT 1 FROM artifact_attestations l
			WHERE l.tenant_id = a.tenant_id AND l.digest = a.digest)
		FOR UPDATE SKIP LOCKED)`,
}

// sweepAttestations runs the sweep statements on ex.
func sweepAttestations(ctx context.Context, ex sqlExec) error {
	return runSweep(ctx, ex, sweepStmts)
}

// runSweep runs stmts on ex.
func runSweep(ctx context.Context, ex sqlExec, stmts []string) error {
	for _, stmt := range stmts {
		if _, err := ex.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("store: sweep attestations: %w", err)
		}
	}
	return nil
}

// sqliteAtt is the SQLite form of the attestation statements.
var sqliteAtt = attSQL{}

// WriteAttestations applies w in one transaction (§4.7.11 Attestation
// storage).
func (s *SQLite) WriteAttestations(ctx context.Context, tenantID, layerID, claim string, w AttestationWrites) (AttestationWriteResult, error) {
	if w.empty() && claim == "" {
		return AttestationWriteResult{}, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AttestationWriteResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := sqliteAtt.writeAttestations(ctx, tx, tenantID, layerID, claim, w)
	if err != nil {
		return AttestationWriteResult{}, err
	}
	return res, tx.Commit()
}

// GetAttestation returns the stored bundle of digest.
func (s *SQLite) GetAttestation(ctx context.Context, tenantID, digest string) (StoredAttestation, error) {
	return sqliteAtt.getAttestation(ctx, s.db, tenantID, digest)
}

// ListAttestations returns the tenant's stored bundles by digest.
func (s *SQLite) ListAttestations(ctx context.Context, tenantID string) ([]StoredAttestation, error) {
	return sqliteAtt.listAttestations(ctx, s.db, tenantID)
}

// ListAttestationLinks returns the tenant's links that f selects.
func (s *SQLite) ListAttestationLinks(ctx context.Context, tenantID string, f LinkFilter) ([]AttestationLink, error) {
	return sqliteAtt.listLinks(ctx, s.db, tenantID, f)
}

// ListRootRecords returns every root record by instance.
func (s *SQLite) ListRootRecords(ctx context.Context) ([]RootRecord, error) {
	return sqliteAtt.listRoots(ctx, s.db)
}

// ReplaceRootRecord writes next when the stored record equals prev.
func (s *SQLite) ReplaceRootRecord(ctx context.Context, prev *RootRecord, next RootRecord) (bool, error) {
	return sqliteAtt.replaceRoot(ctx, s.db, prev, next)
}

// pgAtt is the Postgres form of the attestation statements.
var pgAtt = attSQL{pg: true}

// WriteAttestations applies w in one transaction on the tenant's org schema
// (§4.7.11 Attestation storage).
func (p *Postgres) WriteAttestations(ctx context.Context, tenantID, layerID, claim string, w AttestationWrites) (AttestationWriteResult, error) {
	if w.empty() && claim == "" {
		return AttestationWriteResult{}, nil
	}
	conn, release, err := p.org(ctx, tenantID)
	if err != nil {
		return AttestationWriteResult{}, err
	}
	defer release()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return AttestationWriteResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := pgAtt.writeAttestations(ctx, tx, tenantID, layerID, claim, w)
	if err != nil {
		return AttestationWriteResult{}, err
	}
	return res, tx.Commit()
}

// GetAttestation returns the stored bundle of digest.
func (p *Postgres) GetAttestation(ctx context.Context, tenantID, digest string) (StoredAttestation, error) {
	conn, release, err := p.org(ctx, tenantID)
	if err != nil {
		return StoredAttestation{}, err
	}
	defer release()
	return pgAtt.getAttestation(ctx, conn, tenantID, digest)
}

// ListAttestations returns the tenant's stored bundles by digest.
func (p *Postgres) ListAttestations(ctx context.Context, tenantID string) ([]StoredAttestation, error) {
	conn, release, err := p.org(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer release()
	return pgAtt.listAttestations(ctx, conn, tenantID)
}

// ListAttestationLinks returns the tenant's links that f selects.
func (p *Postgres) ListAttestationLinks(ctx context.Context, tenantID string, f LinkFilter) ([]AttestationLink, error) {
	conn, release, err := p.org(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer release()
	return pgAtt.listLinks(ctx, conn, tenantID, f)
}

// ListRootRecords returns every root record by instance.
func (p *Postgres) ListRootRecords(ctx context.Context) ([]RootRecord, error) {
	return pgAtt.listRoots(ctx, p.db)
}

// ReplaceRootRecord writes next when the stored record equals prev.
func (p *Postgres) ReplaceRootRecord(ctx context.Context, prev *RootRecord, next RootRecord) (bool, error) {
	return pgAtt.replaceRoot(ctx, p.db, prev, next)
}

// pgSweepAttestations runs the sweep in tx on an org schema. A schema that
// an earlier release created gets the attestation tables on its first
// request (ensureOrg), so the purge can meet a schema without them.
func pgSweepAttestations(ctx context.Context, tx sqlExec) error {
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT to_regclass('artifact_attestations') IS NOT NULL AND to_regclass('attestations') IS NOT NULL`).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return nil
	}
	return runSweep(ctx, tx, pgSweepStmts)
}
