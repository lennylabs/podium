package vector

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// StoredVector reads the vec0 embedding of the (tenant, id, version) row and
// the model it is tagged with.
func (v *SQLiteVec) StoredVector(ctx context.Context, tenantID, artifactID, version string) ([]float32, string, error) {
	var (
		blob  []byte
		model string
	)
	err := v.db.QueryRowContext(ctx, `
		SELECT va.embedding, vi.model_id
		FROM vec_index vi
		JOIN vec_artifacts va ON va.rowid = vi.rowid
		WHERE vi.tenant_id = ? AND vi.artifact_id = ? AND vi.version = ?`,
		tenantID, artifactID, version).Scan(&blob, &model)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	vec, err := deserialize(blob)
	if err != nil {
		return nil, "", err
	}
	return vec, model, nil
}

// CosineSimilarities scores each row with sqlite-vec's vec_distance_cosine.
// The vec0 table is created without a distance metric, so its KNN query
// orders and reports by L2 distance; the §7.3.1 similarity is a cosine
// similarity, so it is computed here rather than taken from Query.
func (v *SQLiteVec) CosineSimilarities(ctx context.Context, tenantID string, vec []float32, rows []RowKey) (map[RowKey]float64, error) {
	if err := validateDim(vec, v.dim); err != nil {
		return nil, err
	}
	blob := serialize(vec)
	out := make(map[RowKey]float64, len(rows))
	for _, k := range rows {
		var dist float64
		err := v.db.QueryRowContext(ctx, `
			SELECT vec_distance_cosine(va.embedding, ?)
			FROM vec_index vi
			JOIN vec_artifacts va ON va.rowid = vi.rowid
			WHERE vi.tenant_id = ? AND vi.artifact_id = ? AND vi.version = ?`,
			blob, tenantID, k.ArtifactID, k.Version).Scan(&dist)
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

// deserialize decodes the little-endian float32 blob serialize writes.
func deserialize(blob []byte) ([]float32, error) {
	if len(blob)%4 != 0 {
		return nil, fmt.Errorf("vector.sqlite-vec: stored embedding has %d bytes, not a multiple of 4", len(blob))
	}
	out := make([]float32, len(blob)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(blob[i*4:]))
	}
	return out, nil
}
