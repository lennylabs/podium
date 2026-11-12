package store

import (
	"context"
	"database/sql"
	"fmt"
)

// layerRepoRow is the part of a layer_configs row the start-time rewrite reads:
// its key and the two columns that hold the registered remote.
type layerRepoRow struct {
	tenantID string
	id       string
	repo     string
	userinfo sql.NullString
}

// layerRepoRowQuerier is the read access the rewrite's scan needs, which both
// *sql.DB and *sql.Conn offer.
type layerRepoRowQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// MigrateLayerRepoForm rewrites every layer row whose repo and repo_userinfo
// differ from the form its next write would take, for every tenant and
// including soft-deleted rows, and returns the number of rows it rewrote. It
// changes no other column.
//
// Spec: §13.4, §7.3.1 (Repository credentials)
func (s *SQLite) MigrateLayerRepoForm(ctx context.Context) (int, error) {
	rows, err := scanLayerRepoRows(ctx, s.db,
		`SELECT tenant_id, id, repo, repo_userinfo FROM layer_configs`)
	if err != nil {
		return 0, fmt.Errorf("store: migrate layer repo form: %w", err)
	}
	return rewriteLayerRepoRows(ctx, rows, func(ctx context.Context, row layerRepoRow, repo string, userinfo sql.NullString) (bool, error) {
		return rewriteLayerRepoRowSQLite(ctx, s.db, row, repo, userinfo)
	})
}

// MigrateLayerRepoForm rewrites every layer row whose repo and repo_userinfo
// differ from the form its next write would take, for every tenant and
// including soft-deleted rows, and returns the number of rows it rewrote. It
// changes no other column.
//
// Each tenant is reached through p.org, whose first use in a process runs the
// additive migration on the org schema, so an org schema that only 0.5.x used
// gains repo_userinfo before the scan reads it.
//
// Spec: §13.4, §7.3.1 (Repository credentials)
func (p *Postgres) MigrateLayerRepoForm(ctx context.Context) (int, error) {
	tenants, err := p.ListTenants(ctx)
	if err != nil {
		return 0, fmt.Errorf("store: migrate layer repo form: list tenants: %w", err)
	}
	total := 0
	for _, t := range tenants {
		n, err := p.migrateTenantLayerRepoForm(ctx, t.ID)
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// migrateTenantLayerRepoForm runs the rewrite over one tenant's org schema, on
// one connection pinned to that schema.
func (p *Postgres) migrateTenantLayerRepoForm(ctx context.Context, tenantID string) (int, error) {
	conn, release, err := p.org(ctx, tenantID)
	if err != nil {
		return 0, fmt.Errorf("store: migrate layer repo form: tenant %s: %w", tenantID, err)
	}
	defer release()
	rows, err := scanLayerRepoRows(ctx, conn,
		`SELECT tenant_id, id, repo, repo_userinfo FROM layer_configs WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return 0, fmt.Errorf("store: migrate layer repo form: tenant %s: %w", tenantID, err)
	}
	return rewriteLayerRepoRows(ctx, rows, func(ctx context.Context, row layerRepoRow, repo string, userinfo sql.NullString) (bool, error) {
		return rewriteLayerRepoRowPostgres(ctx, conn, row, repo, userinfo)
	})
}

// scanLayerRepoRows reads every row the query returns and closes the result
// set before it returns, so the caller's writes do not run against an open
// cursor on the same connection.
func scanLayerRepoRows(ctx context.Context, q layerRepoRowQuerier, query string, args ...any) ([]layerRepoRow, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []layerRepoRow
	for rows.Next() {
		var r layerRepoRow
		if err := rows.Scan(&r.tenantID, &r.id, &r.repo, &r.userinfo); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// layerRepoRowWriter runs one store's conditional per-row statement and
// reports whether it affected one row.
type layerRepoRowWriter func(ctx context.Context, row layerRepoRow, repo string, userinfo sql.NullString) (bool, error)

// rewriteLayerRepoRows writes the target form of each scanned row that is not
// already in it and counts the rows the statement affected. A row the
// statement does not match was changed by another writer after the scan and
// is left as that writer wrote it. An error names the row by tenant and ID
// only, because a column value can carry a credential.
func rewriteLayerRepoRows(ctx context.Context, rows []layerRepoRow, write layerRepoRowWriter) (int, error) {
	n := 0
	for _, row := range rows {
		repo, userinfo, changed := layerRepoFormTarget(row.repo, row.userinfo)
		if !changed {
			continue
		}
		affected, err := write(ctx, row, repo, userinfo)
		if err != nil {
			return n, fmt.Errorf("store: migrate layer repo form: %s/%s: %w", row.tenantID, row.id, err)
		}
		if affected {
			n++
		}
	}
	return n, nil
}

// rewriteLayerRepoRowSQLite sets one row's repo columns to their target, on the
// condition that both still hold the scanned values. The conjuncts keep a
// registration or rotation another writer committed after the scan.
func rewriteLayerRepoRowSQLite(ctx context.Context, db execContexter, row layerRepoRow, repo string, userinfo sql.NullString) (bool, error) {
	res, err := db.ExecContext(ctx, `
		UPDATE layer_configs SET repo = ?, repo_userinfo = ?
		WHERE tenant_id = ? AND id = ? AND repo = ? AND repo_userinfo IS ?`,
		repo, userinfo, row.tenantID, row.id, row.repo, row.userinfo)
	return oneRowAffected(res, err)
}

// rewriteLayerRepoRowPostgres is the Postgres form of rewriteLayerRepoRowSQLite.
// IS NOT DISTINCT FROM matches a NULL column against a NULL scanned value.
func rewriteLayerRepoRowPostgres(ctx context.Context, conn execContexter, row layerRepoRow, repo string, userinfo sql.NullString) (bool, error) {
	res, err := conn.ExecContext(ctx, `
		UPDATE layer_configs SET repo = $1, repo_userinfo = $2
		WHERE tenant_id = $3 AND id = $4 AND repo = $5 AND repo_userinfo IS NOT DISTINCT FROM $6`,
		repo, userinfo, row.tenantID, row.id, row.repo, row.userinfo)
	return oneRowAffected(res, err)
}

// oneRowAffected reports whether a statement affected exactly one row.
func oneRowAffected(res sql.Result, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}
