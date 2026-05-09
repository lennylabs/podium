package vector

import (
	"context"
	"math"
	"sort"
	"sync"
)

// Memory is an in-process vector store. Used by tests and by tiny
// standalone deployments that prefer no external dependencies. The
// implementation walks every stored vector on Query (linear scan);
// it's not intended for catalogues larger than a few thousand
// artifacts.
type Memory struct {
	dim int
	mu  sync.RWMutex
	rows map[string]map[string]memVec // tenantID → key(id@ver) → vec
}

type memVec struct {
	id, ver string
	vec     []float32
}

// NewMemory returns an empty in-memory backend with the given
// dimension. dim must be > 0; passing 0 panics.
func NewMemory(dim int) *Memory {
	if dim <= 0 {
		panic("vector.NewMemory: dim must be > 0")
	}
	return &Memory{dim: dim, rows: map[string]map[string]memVec{}}
}

// ID returns "memory".
func (m *Memory) ID() string { return "memory" }

// Dimensions returns the configured dimension.
func (m *Memory) Dimensions() int { return m.dim }

// Put stores or replaces the vector for (tenant, id, version).
func (m *Memory) Put(_ context.Context, tenantID, artifactID, version string, vec []float32) error {
	if tenantID == "" || artifactID == "" || version == "" {
		return ErrInvalidArgument
	}
	if err := validateDim(vec, m.dim); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	bucket, ok := m.rows[tenantID]
	if !ok {
		bucket = map[string]memVec{}
		m.rows[tenantID] = bucket
	}
	cp := make([]float32, len(vec))
	copy(cp, vec)
	bucket[memKey(artifactID, version)] = memVec{id: artifactID, ver: version, vec: cp}
	return nil
}

// Query walks every stored vector for the tenant and returns the
// topK nearest by cosine distance.
func (m *Memory) Query(_ context.Context, tenantID string, vec []float32, topK int) ([]Match, error) {
	if tenantID == "" || topK < 1 {
		return nil, ErrInvalidArgument
	}
	if err := validateDim(vec, m.dim); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	bucket := m.rows[tenantID]
	if len(bucket) == 0 {
		return nil, nil
	}
	out := make([]Match, 0, len(bucket))
	for _, row := range bucket {
		out = append(out, Match{
			ArtifactID: row.id,
			Version:    row.ver,
			Distance:   cosineDistance(vec, row.vec),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Distance < out[j].Distance })
	if len(out) > topK {
		out = out[:topK]
	}
	return out, nil
}

// Delete removes the vector for (tenant, id, version). Missing key
// is a no-op.
func (m *Memory) Delete(_ context.Context, tenantID, artifactID, version string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if bucket, ok := m.rows[tenantID]; ok {
		delete(bucket, memKey(artifactID, version))
	}
	return nil
}

// Close is a no-op for the in-memory backend.
func (m *Memory) Close() error { return nil }

func memKey(id, ver string) string { return id + "@" + ver }

// cosineDistance returns 1 - cosine_similarity. Vectors of zero
// magnitude produce distance 1 (ortho/unknown).
func cosineDistance(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 1
	}
	var dot, na, nb float64
	for i := range a {
		af, bf := float64(a[i]), float64(b[i])
		dot += af * bf
		na += af * af
		nb += bf * bf
	}
	if na == 0 || nb == 0 {
		return 1
	}
	sim := dot / (math.Sqrt(na) * math.Sqrt(nb))
	return float32(1 - sim)
}
