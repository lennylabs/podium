package store

import (
	"context"
	"sort"
	"sync"
)

// Memory is an in-memory Store implementation used by tests and
// standalone bootstrapping. Production code uses SQLite or Postgres.
type Memory struct {
	mu          sync.Mutex
	tenants     map[string]Tenant
	manifests   map[string]ManifestRecord
	deps        map[string][]DependencyEdge
	admins      map[string]bool
}

// NewMemory returns a fresh in-memory Store.
func NewMemory() *Memory {
	return &Memory{
		tenants:   map[string]Tenant{},
		manifests: map[string]ManifestRecord{},
		deps:      map[string][]DependencyEdge{},
		admins:    map[string]bool{},
	}
}

func mkey(tenantID, artifactID, version string) string {
	return tenantID + "/" + artifactID + "@" + version
}

// CreateTenant inserts a new tenant.
func (s *Memory) CreateTenant(_ context.Context, t Tenant) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tenants[t.ID] = t
	return nil
}

// GetTenant returns the tenant or ErrTenantNotFound.
func (s *Memory) GetTenant(_ context.Context, id string) (Tenant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[id]
	if !ok {
		return Tenant{}, ErrTenantNotFound
	}
	return t, nil
}

// PutManifest enforces the immutability invariant: the same
// (tenant, id, version) with a different content hash is rejected.
func (s *Memory) PutManifest(_ context.Context, rec ManifestRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := mkey(rec.TenantID, rec.ArtifactID, rec.Version)
	if existing, ok := s.manifests[key]; ok {
		if existing.ContentHash != rec.ContentHash {
			return ErrImmutableViolation
		}
		// Same content hash is a no-op (idempotent ingest).
		return nil
	}
	s.manifests[key] = rec
	return nil
}

// GetManifest returns the manifest or ErrNotFound.
func (s *Memory) GetManifest(_ context.Context, tenantID, artifactID, version string) (ManifestRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.manifests[mkey(tenantID, artifactID, version)]
	if !ok {
		return ManifestRecord{}, ErrNotFound
	}
	return rec, nil
}

// ListManifests returns every manifest for the tenant in stable order.
func (s *Memory) ListManifests(_ context.Context, tenantID string) ([]ManifestRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []ManifestRecord
	for _, rec := range s.manifests {
		if rec.TenantID == tenantID {
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ArtifactID != out[j].ArtifactID {
			return out[i].ArtifactID < out[j].ArtifactID
		}
		return out[i].Version < out[j].Version
	})
	return out, nil
}

// PutDependency records a dependency edge.
func (s *Memory) PutDependency(_ context.Context, tenantID string, edge DependencyEdge) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deps[tenantID] = append(s.deps[tenantID], edge)
	return nil
}

// DependentsOf returns every edge ending at artifactID.
func (s *Memory) DependentsOf(_ context.Context, tenantID, artifactID string) ([]DependencyEdge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []DependencyEdge
	for _, e := range s.deps[tenantID] {
		if e.To == artifactID {
			out = append(out, e)
		}
	}
	return out, nil
}

// GrantAdmin records an admin grant.
func (s *Memory) GrantAdmin(_ context.Context, g AdminGrant) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.admins[g.UserID+"/"+g.OrgID] = true
	return nil
}

// IsAdmin checks the admin grant table.
func (s *Memory) IsAdmin(_ context.Context, userID, orgID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.admins[userID+"/"+orgID], nil
}
