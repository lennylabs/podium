package store

import (
	"context"
	"fmt"
)

// AddUsageCounts adds the counts to the tenant's usage_daily rows in one
// transaction, inserting a row for a new key and adding to an existing one.
//
// Spec: §8.7
func (s *SQLite) AddUsageCounts(ctx context.Context, tenantID string, counts []UsageCount) error {
	norm, err := normalizeUsageCounts(tenantID, counts)
	if err != nil || len(norm) == 0 {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, c := range norm {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO usage_daily (tenant_id, day, layer, artifact_id, client_kind, harness, loads, syncs, search_hits)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (tenant_id, day, layer, artifact_id, client_kind, harness) DO UPDATE SET
				loads = loads + excluded.loads, syncs = syncs + excluded.syncs,
				search_hits = search_hits + excluded.search_hits`,
			c.TenantID, c.Day, c.Layer, c.ArtifactID, c.ClientKind, c.Harness, c.Loads, c.Syncs, c.SearchHits); err != nil {
			return fmt.Errorf("store: add usage counts: %w", err)
		}
	}
	return tx.Commit()
}

// ListUsageCounts returns the tenant's usage_daily rows from fromDay to toDay.
// day is stored as YYYY-MM-DD text, so text order is date order.
func (s *SQLite) ListUsageCounts(ctx context.Context, tenantID, fromDay, toDay string) ([]UsageCount, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT tenant_id, day, layer, artifact_id, client_kind, harness, loads, syncs, search_hits
		FROM usage_daily
		WHERE tenant_id = ? AND day >= ? AND day <= ?
		ORDER BY day, layer, artifact_id, client_kind, harness`, tenantID, fromDay, toDay)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanUsageRows(rows)
}

// SumUsageLoads returns the tenant's loads from fromDay to toDay per layer and
// artifact.
func (s *SQLite) SumUsageLoads(ctx context.Context, tenantID, fromDay, toDay string) ([]UsageLoads, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT layer, artifact_id, SUM(loads)
		FROM usage_daily
		WHERE tenant_id = ? AND day >= ? AND day <= ? AND loads > 0
		GROUP BY layer, artifact_id
		ORDER BY layer, artifact_id`, tenantID, fromDay, toDay)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanUsageLoads(rows)
}

// DeleteUsageBefore removes every tenant's usage_daily rows dated before day.
func (s *SQLite) DeleteUsageBefore(ctx context.Context, day string) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM usage_daily WHERE day < ?`, day)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// AddUsageCounts adds the counts to the org's usage_daily rows in one
// transaction, inserting a row for a new key and adding to an existing one.
// ON CONFLICT DO UPDATE takes the row lock, so concurrent flushes from two
// replicas both add.
//
// Spec: §8.7
func (p *Postgres) AddUsageCounts(ctx context.Context, tenantID string, counts []UsageCount) error {
	norm, err := normalizeUsageCounts(tenantID, counts)
	if err != nil || len(norm) == 0 {
		return err
	}
	conn, release, err := p.org(ctx, tenantID)
	if err != nil {
		return err
	}
	defer release()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, c := range norm {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO usage_daily (tenant_id, day, layer, artifact_id, client_kind, harness, loads, syncs, search_hits)
			VALUES ($1, $2::date, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (tenant_id, day, layer, artifact_id, client_kind, harness) DO UPDATE SET
				loads = usage_daily.loads + EXCLUDED.loads, syncs = usage_daily.syncs + EXCLUDED.syncs,
				search_hits = usage_daily.search_hits + EXCLUDED.search_hits`,
			c.TenantID, c.Day, c.Layer, c.ArtifactID, c.ClientKind, c.Harness, c.Loads, c.Syncs, c.SearchHits); err != nil {
			return fmt.Errorf("store: add usage counts: %w", err)
		}
	}
	return tx.Commit()
}

// ListUsageCounts returns the org's usage_daily rows from fromDay to toDay.
func (p *Postgres) ListUsageCounts(ctx context.Context, tenantID, fromDay, toDay string) ([]UsageCount, error) {
	conn, release, err := p.org(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer release()
	rows, err := conn.QueryContext(ctx, `
		SELECT tenant_id, to_char(day, 'YYYY-MM-DD'), layer, artifact_id, client_kind, harness, loads, syncs, search_hits
		FROM usage_daily
		WHERE tenant_id = $1 AND day >= $2::date AND day <= $3::date
		ORDER BY day, layer, artifact_id, client_kind, harness`, tenantID, fromDay, toDay)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanUsageRows(rows)
}

// SumUsageLoads returns the org's loads from fromDay to toDay per layer and
// artifact.
func (p *Postgres) SumUsageLoads(ctx context.Context, tenantID, fromDay, toDay string) ([]UsageLoads, error) {
	conn, release, err := p.org(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer release()
	rows, err := conn.QueryContext(ctx, `
		SELECT layer, artifact_id, SUM(loads)
		FROM usage_daily
		WHERE tenant_id = $1 AND day >= $2::date AND day <= $3::date AND loads > 0
		GROUP BY layer, artifact_id
		ORDER BY layer, artifact_id`, tenantID, fromDay, toDay)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanUsageLoads(rows)
}

// DeleteUsageBefore removes the usage_daily rows dated before day in every
// org schema.
func (p *Postgres) DeleteUsageBefore(ctx context.Context, day string) (int, error) {
	idents, err := p.listOrgSchemas(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, ident := range idents {
		n, err := p.deleteUsageInSchema(ctx, ident, day)
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

// deleteUsageInSchema removes one org schema's usage_daily rows dated before
// day.
func (p *Postgres) deleteUsageInSchema(ctx context.Context, ident, day string) (int, error) {
	conn, err := p.db.Conn(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, `SET search_path TO `+ident+`, public`); err != nil {
		return 0, err
	}
	res, err := conn.ExecContext(ctx, `DELETE FROM usage_daily WHERE day < $1::date`, day)
	if isUndefinedTable(err) {
		// An org schema provisioned by an earlier release gains usage_daily
		// on its first access in this process, and the startup retention
		// pass runs before any access; such a schema holds no usage rows.
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// usageRows is the subset of *sql.Rows scanUsageRows reads.
type usageRows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}

// scanUsageLoads reads (layer, artifact_id, loads) rows.
func scanUsageLoads(rows usageRows) ([]UsageLoads, error) {
	out := []UsageLoads{}
	for rows.Next() {
		var l UsageLoads
		if err := rows.Scan(&l.Layer, &l.ArtifactID, &l.Loads); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// scanUsageRows reads usage_daily rows selected in UsageCount field order.
func scanUsageRows(rows usageRows) ([]UsageCount, error) {
	out := []UsageCount{}
	for rows.Next() {
		var c UsageCount
		if err := rows.Scan(&c.TenantID, &c.Day, &c.Layer, &c.ArtifactID, &c.ClientKind, &c.Harness, &c.Loads, &c.Syncs, &c.SearchHits); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
