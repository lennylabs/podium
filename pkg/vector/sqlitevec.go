package vector

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"sync"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/mattn/go-sqlite3"
)

// SQLiteVec stores embeddings in SQLite via the sqlite-vec
// extension. The default backend for §13.10 standalone deployments;
// collocates with the metadata store so a single SQLite file holds
// manifests + vectors.
//
// The sqlite-vec extension is loaded automatically on each
// connection via a sql.Register-time callback. Operators do not need
// to install the extension separately; the cgo bindings statically
// link the C code.
type SQLiteVec struct {
	db  *sql.DB
	dim int
}

// SQLiteVecConfig is the constructor input.
type SQLiteVecConfig struct {
	// Path is the SQLite file path; ":memory:" for in-memory.
	Path string
	// Dimensions is the embedding vector size.
	Dimensions int
}

// driverRegister wraps the package-level call so we only register
// the extension-loading driver once.
var driverOnce sync.Once

// registerDriver registers a sqlite3 driver flavor that loads
// sqlite-vec on every connection. Idempotent across calls.
func registerDriver() {
	driverOnce.Do(func() {
		sqlite_vec.Auto()
		sql.Register("sqlite3-with-vec", &sqlite3.SQLiteDriver{
			ConnectHook: func(conn *sqlite3.SQLiteConn) error {
				// Auto() above registered the extension via the
				// global SQLite extension API; ConnectHook is here
				// for symmetry and future per-connection setup.
				_ = conn
				return nil
			},
		})
	})
}

// OpenSQLiteVec opens (or creates) a SQLite database at cfg.Path,
// loads sqlite-vec, and creates the vec_artifacts virtual table at
// the configured dimension.
func OpenSQLiteVec(cfg SQLiteVecConfig) (*SQLiteVec, error) {
	if cfg.Dimensions <= 0 {
		return nil, fmt.Errorf("vector.sqlite-vec: Dimensions must be > 0")
	}
	registerDriver()
	dsn := cfg.Path
	if dsn == "" {
		dsn = ":memory:"
	}
	db, err := sql.Open("sqlite3-with-vec", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// Single connection is fine for sqlite; multiple writers
	// serialize through the file lock anyway.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	v := &SQLiteVec{db: db, dim: cfg.Dimensions}
	if err := v.applySchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return v, nil
}

func (v *SQLiteVec) applySchema() error {
	stmts := []string{
		// vec0 is sqlite-vec's virtual-table module. The float32
		// embedding column is keyed by an opaque rowid; we maintain
		// our own (tenant, id, version) → rowid mapping in a
		// companion table because vec0 doesn't support compound PKs.
		fmt.Sprintf(`CREATE VIRTUAL TABLE IF NOT EXISTS vec_artifacts USING vec0(
			embedding float[%d]
		)`, v.dim),
		`CREATE TABLE IF NOT EXISTS vec_index (
			tenant_id TEXT NOT NULL,
			artifact_id TEXT NOT NULL,
			version TEXT NOT NULL,
			rowid INTEGER NOT NULL,
			PRIMARY KEY (tenant_id, artifact_id, version)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_vec_index_rowid
			ON vec_index(rowid)`,
	}
	for _, s := range stmts {
		if _, err := v.db.Exec(s); err != nil {
			return fmt.Errorf("sqlite-vec schema: %w (statement: %s)", err, s)
		}
	}
	return nil
}

// ID returns "sqlite-vec".
func (*SQLiteVec) ID() string { return "sqlite-vec" }

// Dimensions returns the configured dimension.
func (v *SQLiteVec) Dimensions() int { return v.dim }

// Put upserts the embedding. The implementation is a transaction:
// look up an existing rowid for the (tenant, id, version) tuple, and
// UPDATE the vec0 row in place; otherwise INSERT a fresh row and
// record the assigned rowid. Atomic per artifact.
func (v *SQLiteVec) Put(ctx context.Context, tenantID, artifactID, version string, vec []float32) error {
	if tenantID == "" || artifactID == "" || version == "" {
		return ErrInvalidArgument
	}
	if err := validateDim(vec, v.dim); err != nil {
		return err
	}
	blob := serialize(vec)

	tx, err := v.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRowContext(ctx, `
		SELECT rowid FROM vec_index
		WHERE tenant_id = ? AND artifact_id = ? AND version = ?`,
		tenantID, artifactID, version)
	var rowid int64
	switch err := row.Scan(&rowid); err {
	case nil:
		if _, err := tx.ExecContext(ctx, `
			UPDATE vec_artifacts SET embedding = ? WHERE rowid = ?`,
			blob, rowid); err != nil {
			return err
		}
	case sql.ErrNoRows:
		res, err := tx.ExecContext(ctx, `
			INSERT INTO vec_artifacts (embedding) VALUES (?)`, blob)
		if err != nil {
			return err
		}
		newRowid, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO vec_index (tenant_id, artifact_id, version, rowid)
			VALUES (?, ?, ?, ?)`,
			tenantID, artifactID, version, newRowid); err != nil {
			return err
		}
	default:
		return err
	}
	return tx.Commit()
}

// Query joins vec_index with vec_artifacts and ORDER BY distance.
// sqlite-vec's MATCH operator + `k=` parameter does the top-K
// selection; we filter by tenant in the join.
func (v *SQLiteVec) Query(ctx context.Context, tenantID string, vec []float32, topK int) ([]Match, error) {
	if tenantID == "" || topK < 1 {
		return nil, ErrInvalidArgument
	}
	if err := validateDim(vec, v.dim); err != nil {
		return nil, err
	}
	blob := serialize(vec)

	// sqlite-vec's KNN syntax: `WHERE embedding MATCH ? AND k = ?`.
	// The result has columns (rowid, distance). We then join vec_index
	// to recover artifact identifiers and filter to the tenant. To
	// avoid pulling other tenants into the KNN result, we ask for
	// (topK + tenantSize) candidates and trim — but a simpler
	// strategy that works at expected scale is to fetch a generous
	// over-K and filter; we use 10x topK clamped to 1000.
	overK := topK * 10
	if overK > 1000 {
		overK = 1000
	}
	rows, err := v.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT vi.artifact_id, vi.version, va.distance
		FROM (
			SELECT rowid, distance FROM vec_artifacts
			WHERE embedding MATCH ? AND k = %d
		) AS va
		JOIN vec_index vi ON vi.rowid = va.rowid
		WHERE vi.tenant_id = ?
		ORDER BY va.distance ASC
		LIMIT ?`, overK), blob, tenantID, topK)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer rows.Close()
	var out []Match
	for rows.Next() {
		var m Match
		var dist float64
		if err := rows.Scan(&m.ArtifactID, &m.Version, &dist); err != nil {
			return nil, err
		}
		m.Distance = float32(dist)
		out = append(out, m)
	}
	return out, rows.Err()
}

// Delete removes the (tenant, id, version) row from both the index
// and the vec0 table.
func (v *SQLiteVec) Delete(ctx context.Context, tenantID, artifactID, version string) error {
	tx, err := v.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	row := tx.QueryRowContext(ctx, `
		SELECT rowid FROM vec_index
		WHERE tenant_id = ? AND artifact_id = ? AND version = ?`,
		tenantID, artifactID, version)
	var rowid int64
	if err := row.Scan(&rowid); err != nil {
		if err == sql.ErrNoRows {
			return tx.Commit()
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM vec_artifacts WHERE rowid = ?`, rowid); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM vec_index WHERE rowid = ?`, rowid); err != nil {
		return err
	}
	return tx.Commit()
}

// Close releases the underlying connection.
func (v *SQLiteVec) Close() error { return v.db.Close() }

// DB returns the underlying *sql.DB; tests use it for setup/teardown.
func (v *SQLiteVec) DB() *sql.DB { return v.db }

// serialize encodes a float32 slice as little-endian bytes, the form
// sqlite-vec's MATCH operator and INSERT path expect.
func serialize(vec []float32) []byte {
	buf := make([]byte, len(vec)*4)
	for i, f := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// quiet unused-import warning when sqlite-vec is conditionally
// excluded by future build tags.
var _ = strings.TrimSpace
