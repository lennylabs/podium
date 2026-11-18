package vector

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// StoredVector reads the embedding of the (tenant, id, version) row and the
// model it is tagged with.
func (p *PgVector) StoredVector(ctx context.Context, tenantID, artifactID, version string) ([]float32, string, error) {
	var literal, model string
	err := p.db.QueryRowContext(ctx, `
		SELECT embedding::text, model_id
		FROM vec_artifacts
		WHERE tenant_id = $1 AND artifact_id = $2 AND version = $3`,
		tenantID, artifactID, version).Scan(&literal, &model)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	vec, err := parseVecLiteral(literal)
	if err != nil {
		return nil, "", err
	}
	return vec, model, nil
}

// CosineSimilarities scores each row with pgvector's cosine distance operator
// <=>, so the similarity is one minus the cosine distance whatever index
// serves the nearest-neighbour query.
func (p *PgVector) CosineSimilarities(ctx context.Context, tenantID string, vec []float32, rows []RowKey) (map[RowKey]float64, error) {
	if err := validateDim(vec, p.dim); err != nil {
		return nil, err
	}
	literal := vecLiteral(vec)
	out := make(map[RowKey]float64, len(rows))
	for _, k := range rows {
		var dist float64
		err := p.db.QueryRowContext(ctx, `
			SELECT embedding <=> $1::vector
			FROM vec_artifacts
			WHERE tenant_id = $2 AND artifact_id = $3 AND version = $4`,
			literal, tenantID, k.ArtifactID, k.Version).Scan(&dist)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
		}
		out[k] = 1 - dist
	}
	return out, nil
}

// parseVecLiteral parses pgvector's text form `[1,2,3]`.
func parseVecLiteral(s string) ([]float32, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return nil, fmt.Errorf("vector.pgvector: malformed vector literal")
	}
	body := strings.TrimSpace(s[1 : len(s)-1])
	if body == "" {
		return []float32{}, nil
	}
	parts := strings.Split(body, ",")
	out := make([]float32, len(parts))
	for i, part := range parts {
		f, err := strconv.ParseFloat(strings.TrimSpace(part), 32)
		if err != nil {
			return nil, fmt.Errorf("vector.pgvector: parse vector element %d: %w", i, err)
		}
		out[i] = float32(f)
	}
	return out, nil
}
