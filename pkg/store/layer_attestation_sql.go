package store

import (
	"context"
	"database/sql"
	"errors"
)

// recordColumns are the columns of the attestation record a claim reads back.
const recordColumns = `attestation_mark, attested_commit, attestation_status, attestation_details, attestation_dropped, attestation_checked_at`

// WriteLayerAttestation writes the attestation record of the live layer that
// holds claim with one update (§4.7.11 Attestation record). A write that
// advances the mark also matches the stored mark and commit, and the SQLite
// IS operator compares a NULL mark as equal.
func (s *SQLite) WriteLayerAttestation(ctx context.Context, tenantID, layerID, claim string, w AttestationWrite) error {
	details, err := attestationDetailsColumn(w.Details)
	if err != nil {
		return err
	}
	checked := normalizeMark(w.CheckedAt)
	q := `UPDATE layer_configs SET attestation_status = ?, attestation_details = ?, attestation_dropped = ?, attestation_checked_at = ?`
	args := []any{w.Status, details, w.Dropped, nullTimeText(&checked)}
	cond := ``
	var condArgs []any
	if w.Advance {
		mark := normalizeMark(w.Mark)
		q += `, attestation_mark = ?, attested_commit = CASE WHEN ? = '' THEN attested_commit ELSE ? END`
		args = append(args, nullTimeText(&mark), w.Commit, w.Commit)
		cond = ` AND attestation_mark IS ? AND attested_commit = ?`
		condArgs = []any{nullTimeText(normalizedMarkPtr(w.ReadMark)), w.ReadCommit}
	}
	if v := w.Visibility; v != nil {
		groups, users, pending, err := visibilityColumns(layerID, *v)
		if err != nil {
			return err
		}
		q += `, public = ?, organization = ?, groups = ?, users = ?, visibility_pending = ?,
			visibility_source = CASE WHEN ? = '' THEN visibility_source ELSE ? END`
		args = append(args, boolToInt(v.Visibility.Public), boolToInt(v.Visibility.Organization), groups, users, pending, v.Source, v.Source)
	}
	q += ` WHERE tenant_id = ? AND id = ? AND deleted_at IS NULL AND ingest_claim = ?` + cond
	args = append(append(args, tenantID, layerID, claim), condArgs...)
	written, err := rowsChanged(s.db.ExecContext(ctx, q, args...))
	if err != nil {
		return err
	}
	if !written {
		return ErrIngestSuperseded
	}
	return nil
}

// ClaimLayerIngestWithReset claims the live layer and applies the mark reset
// in one transaction (§4.7.11 Mark reset).
func (s *SQLite) ClaimLayerIngestWithReset(ctx context.Context, tenantID, layerID, claim, toCommit string) (LayerConfig, string, AttestationRecord, error) {
	return s.claimLayer(ctx, tenantID, layerID, claim, &toCommit)
}

// claimLayer makes claim the live layer's ingest claim and, when reset is
// not nil, applies the mark reset to *reset in the same transaction. A no-op
// update takes the write lock before the read, so no other claim can land
// between reading the old claim and writing the new one.
func (s *SQLite) claimLayer(ctx context.Context, tenantID, layerID, claim string, reset *string) (LayerConfig, string, AttestationRecord, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LayerConfig{}, "", AttestationRecord{}, err
	}
	defer func() { _ = tx.Rollback() }()
	locked, err := rowsChanged(tx.ExecContext(ctx, `
		UPDATE layer_configs SET ingest_claim = ingest_claim
		WHERE tenant_id = ? AND id = ? AND deleted_at IS NULL`, tenantID, layerID))
	if err != nil {
		return LayerConfig{}, "", AttestationRecord{}, err
	}
	if !locked {
		return LayerConfig{}, "", AttestationRecord{}, ErrNotFound
	}
	var previous sql.NullString
	var mark, checked sql.NullString
	var prior AttestationRecord
	var details string
	if err := tx.QueryRowContext(ctx, `
		SELECT ingest_claim, `+recordColumns+` FROM layer_configs WHERE tenant_id = ? AND id = ?`,
		tenantID, layerID).Scan(&previous, &mark, &prior.Commit, &prior.Status, &details, &prior.Dropped, &checked); err != nil {
		return LayerConfig{}, "", AttestationRecord{}, err
	}
	if prior.Details, err = parseAttestationDetails(details); err != nil {
		return LayerConfig{}, "", AttestationRecord{}, err
	}
	prior.Mark, prior.CheckedAt = parseNullTimeText(mark), parseNullTimeText(checked)
	q, args := `UPDATE layer_configs SET ingest_claim = ?`, []any{claim}
	if reset != nil {
		q += `, attestation_mark = NULL, attested_commit = ?`
		args = append(args, *reset)
	}
	if _, err := tx.ExecContext(ctx, q+` WHERE tenant_id = ? AND id = ?`, append(args, tenantID, layerID)...); err != nil {
		return LayerConfig{}, "", AttestationRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return LayerConfig{}, "", AttestationRecord{}, err
	}
	cfg, err := s.GetLayerConfig(ctx, tenantID, layerID)
	return cfg, previous.String, prior, err
}

// MigrateLayerConfig writes cfg and applies the §13.4 copy rule to its
// attestation record in one transaction.
func (s *SQLite) MigrateLayerConfig(ctx context.Context, cfg LayerConfig) (AttestationCopy, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	var mark, checked sql.NullString
	var target AttestationRecord
	err = tx.QueryRowContext(ctx, `
		SELECT attestation_mark, attested_commit, attestation_checked_at FROM layer_configs
		WHERE tenant_id = ? AND id = ?`, cfg.TenantID, cfg.ID).Scan(&mark, &target.Commit, &checked)
	held := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	target.Mark, target.CheckedAt = parseNullTimeText(mark), parseNullTimeText(checked)
	if err := sqlitePutLayer(ctx, tx, cfg); err != nil {
		return "", err
	}
	out := migrationOutcome(held, target, cfg.AttestationRecord)
	if out != AttestationCopyLeft {
		if _, err := tx.ExecContext(ctx, `
			UPDATE layer_configs SET attestation_mark = ?, attested_commit = ?
			WHERE tenant_id = ? AND id = ?`,
			nullTimeText(normalizedMarkPtr(cfg.Mark)), cfg.Commit, cfg.TenantID, cfg.ID); err != nil {
			return "", err
		}
	}
	return out, tx.Commit()
}

// migrationOutcome decides the §13.4 copy for a target record: written when
// the target held no row, replaced under migrationReplaces, left otherwise.
func migrationOutcome(held bool, target, src AttestationRecord) AttestationCopy {
	switch {
	case !held:
		return AttestationCopyWritten
	case migrationReplaces(target, src):
		return AttestationCopyReplaced
	}
	return AttestationCopyLeft
}

// WriteLayerAttestation writes the attestation record of the live layer that
// holds claim with one update (§4.7.11 Attestation record).
func (p *Postgres) WriteLayerAttestation(ctx context.Context, tenantID, layerID, claim string, w AttestationWrite) error {
	details, err := attestationDetailsColumn(w.Details)
	if err != nil {
		return err
	}
	vis := VisibilityWrite{}
	if w.Visibility != nil {
		vis = *w.Visibility
	}
	groups, users, pending, err := visibilityColumns(layerID, vis)
	if err != nil {
		return err
	}
	conn, release, err := p.org(ctx, tenantID)
	if err != nil {
		return err
	}
	defer release()
	written, err := rowsChanged(conn.ExecContext(ctx, `
		UPDATE layer_configs SET attestation_status = $1, attestation_details = $2,
			attestation_dropped = $3, attestation_checked_at = $4,
			attestation_mark = CASE WHEN $5 THEN $6 ELSE attestation_mark END,
			attested_commit = CASE WHEN $5 AND $7 <> '' THEN $7 ELSE attested_commit END,
			public = CASE WHEN $13 THEN $14 ELSE public END,
			organization = CASE WHEN $13 THEN $15 ELSE organization END,
			groups = CASE WHEN $13 THEN $16 ELSE groups END,
			users = CASE WHEN $13 THEN $17 ELSE users END,
			visibility_pending = CASE WHEN $13 THEN $18 ELSE visibility_pending END,
			visibility_source = CASE WHEN $13 AND $19 <> '' THEN $19 ELSE visibility_source END
		WHERE tenant_id = $8 AND id = $9 AND deleted_at IS NULL AND ingest_claim = $10
			AND (NOT $5 OR (attestation_mark IS NOT DISTINCT FROM $11 AND attested_commit = $12))`,
		w.Status, details, w.Dropped, normalizeMark(w.CheckedAt),
		w.Advance, normalizeMark(w.Mark), w.Commit,
		tenantID, layerID, claim,
		nullTimePG(normalizedMarkPtr(w.ReadMark)), w.ReadCommit,
		w.Visibility != nil, vis.Visibility.Public, vis.Visibility.Organization, groups, users, pending, vis.Source))
	if err != nil {
		return err
	}
	if !written {
		return ErrIngestSuperseded
	}
	return nil
}

// ClaimLayerIngestWithReset claims the live layer and applies the mark reset
// in one statement, which reads the earlier claim and record from the row
// version it locked (§4.7.11 Mark reset).
func (p *Postgres) ClaimLayerIngestWithReset(ctx context.Context, tenantID, layerID, claim, toCommit string) (LayerConfig, string, AttestationRecord, error) {
	conn, release, err := p.org(ctx, tenantID)
	if err != nil {
		return LayerConfig{}, "", AttestationRecord{}, err
	}
	var previous sql.NullString
	var mark, checked sql.NullTime
	var prior AttestationRecord
	var details string
	err = conn.QueryRowContext(ctx, `
		UPDATE layer_configs AS l SET ingest_claim = $1, attestation_mark = NULL, attested_commit = $4
		FROM (SELECT tenant_id, id, ingest_claim, `+recordColumns+` FROM layer_configs
		      WHERE tenant_id = $2 AND id = $3 AND deleted_at IS NULL FOR UPDATE) AS old
		WHERE l.tenant_id = old.tenant_id AND l.id = old.id
		RETURNING old.ingest_claim, old.attestation_mark, old.attested_commit, old.attestation_status,
			old.attestation_details, old.attestation_dropped, old.attestation_checked_at`,
		claim, tenantID, layerID, toCommit).Scan(&previous, &mark, &prior.Commit, &prior.Status, &details, &prior.Dropped, &checked)
	release()
	if errors.Is(err, sql.ErrNoRows) {
		return LayerConfig{}, "", AttestationRecord{}, ErrNotFound
	}
	if err != nil {
		return LayerConfig{}, "", AttestationRecord{}, err
	}
	if prior.Details, err = parseAttestationDetails(details); err != nil {
		return LayerConfig{}, "", AttestationRecord{}, err
	}
	prior.Mark, prior.CheckedAt = ptrFromNullTime(mark), ptrFromNullTime(checked)
	cfg, err := p.GetLayerConfig(ctx, tenantID, layerID)
	return cfg, previous.String, prior, err
}

// MigrateLayerConfig writes cfg and applies the §13.4 copy rule to its
// attestation record in one transaction, with the target row locked.
func (p *Postgres) MigrateLayerConfig(ctx context.Context, cfg LayerConfig) (AttestationCopy, error) {
	conn, release, err := p.org(ctx, cfg.TenantID)
	if err != nil {
		return "", err
	}
	defer release()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	var mark, checked sql.NullTime
	var target AttestationRecord
	err = tx.QueryRowContext(ctx, `
		SELECT attestation_mark, attested_commit, attestation_checked_at FROM layer_configs
		WHERE tenant_id = $1 AND id = $2 FOR UPDATE`, cfg.TenantID, cfg.ID).Scan(&mark, &target.Commit, &checked)
	held := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	target.Mark, target.CheckedAt = ptrFromNullTime(mark), ptrFromNullTime(checked)
	if err := pgPutLayer(ctx, tx, cfg); err != nil {
		return "", err
	}
	out := migrationOutcome(held, target, cfg.AttestationRecord)
	if out != AttestationCopyLeft {
		if _, err := tx.ExecContext(ctx, `
			UPDATE layer_configs SET attestation_mark = $1, attested_commit = $2
			WHERE tenant_id = $3 AND id = $4`,
			nullTimePG(normalizedMarkPtr(cfg.Mark)), cfg.Commit, cfg.TenantID, cfg.ID); err != nil {
			return "", err
		}
	}
	return out, tx.Commit()
}
