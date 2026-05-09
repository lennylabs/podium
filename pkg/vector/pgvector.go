package vector

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	_ "github.com/lib/pq"
)

// PgVector stores embeddings in Postgres via the pgvector extension.
// The default backend for the §13.10 standard deployment; collocates
// with the metadata store so a single Postgres instance handles
// manifests + vectors.
type PgVector struct {
	db  *sql.DB
	dim int
}

// PgVectorConfig is the constructor input.
type PgVectorConfig struct {
	// DSN is the lib/pq connection string.
	DSN string
	// Dimensions is the embedding vector size; the schema is
	// created at this dimension on first connect. Switching
	// dimensions requires a `podium admin reembed --all` and a
	// schema rebuild.
	Dimensions int
}

// OpenPgVector dials Postgres, ensures the `vector` extension is
// installed (CREATE EXTENSION IF NOT EXISTS vector), and creates
// the vec_artifacts table at the configured dimension.
func OpenPgVector(cfg PgVectorConfig) (*PgVector, error) {
	if cfg.Dimensions <= 0 {
		return nil, fmt.Errorf("vector.pgvector: Dimensions must be > 0")
	}
	db, err := sql.Open("postgres", cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	p := &PgVector{db: db, dim: cfg.Dimensions}
	if err := p.applySchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return p, nil
}

// applySchema runs the idempotent setup. CREATE EXTENSION requires
// superuser or membership in pg_create_extension; managed Postgres
// services (RDS, Cloud SQL, Aurora) all support pgvector via either
// of those paths.
func (p *PgVector) applySchema() error {
	stmts := []string{
		`CREATE EXTENSION IF NOT EXISTS vector`,
		fmt.Sprintf(`CREATE TABLE IF NOT EXISTS vec_artifacts (
			tenant_id TEXT NOT NULL,
			artifact_id TEXT NOT NULL,
			version TEXT NOT NULL,
			embedding vector(%d) NOT NULL,
			PRIMARY KEY (tenant_id, artifact_id, version)
		)`, p.dim),
		`CREATE INDEX IF NOT EXISTS idx_vec_artifacts_tenant
			ON vec_artifacts(tenant_id)`,
	}
	for _, s := range stmts {
		if _, err := p.db.Exec(s); err != nil {
			return fmt.Errorf("pgvector schema: %w (statement: %s)", err, s)
		}
	}
	return nil
}

// ID returns "pgvector".
func (*PgVector) ID() string { return "pgvector" }

// Dimensions returns the configured dimension.
func (p *PgVector) Dimensions() int { return p.dim }

// Put upserts the embedding for (tenant, id, version). Atomic per
// row via ON CONFLICT.
func (p *PgVector) Put(ctx context.Context, tenantID, artifactID, version string, vec []float32) error {
	if tenantID == "" || artifactID == "" || version == "" {
		return ErrInvalidArgument
	}
	if err := validateDim(vec, p.dim); err != nil {
		return err
	}
	literal := vecLiteral(vec)
	_, err := p.db.ExecContext(ctx, `
		INSERT INTO vec_artifacts (tenant_id, artifact_id, version, embedding)
		VALUES ($1, $2, $3, $4::vector)
		ON CONFLICT (tenant_id, artifact_id, version) DO UPDATE
			SET embedding = EXCLUDED.embedding`,
		tenantID, artifactID, version, literal)
	return err
}

// Query returns the topK nearest neighbours by cosine distance.
// pgvector's `<=>` operator is cosine distance when the column type
// uses `vector_cosine_ops`; without an index hint we get the same
// metric via the default operator class.
func (p *PgVector) Query(ctx context.Context, tenantID string, vec []float32, topK int) ([]Match, error) {
	if tenantID == "" || topK < 1 {
		return nil, ErrInvalidArgument
	}
	if err := validateDim(vec, p.dim); err != nil {
		return nil, err
	}
	literal := vecLiteral(vec)
	rows, err := p.db.QueryContext(ctx, `
		SELECT artifact_id, version, embedding <=> $1::vector AS distance
		FROM vec_artifacts
		WHERE tenant_id = $2
		ORDER BY embedding <=> $1::vector ASC
		LIMIT $3`,
		literal, tenantID, topK)
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

// Delete removes the (tenant, id, version) vector. Missing key is a
// no-op.
func (p *PgVector) Delete(ctx context.Context, tenantID, artifactID, version string) error {
	_, err := p.db.ExecContext(ctx, `
		DELETE FROM vec_artifacts
		WHERE tenant_id = $1 AND artifact_id = $2 AND version = $3`,
		tenantID, artifactID, version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return nil
}

// Close releases the connection pool.
func (p *PgVector) Close() error { return p.db.Close() }

// DB returns the underlying *sql.DB. Test fixtures use it to manage
// per-suite state (TRUNCATE between runs). Production callers should
// not need it.
func (p *PgVector) DB() *sql.DB { return p.db }

// vecLiteral renders a float32 slice as the pgvector text literal
// `'[1.0,2.0,3.0]'`. lib/pq accepts text-form vectors on the wire.
func vecLiteral(vec []float32) string {
	parts := make([]string, len(vec))
	for i, f := range vec {
		parts[i] = strconv.FormatFloat(float64(f), 'g', -1, 32)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
