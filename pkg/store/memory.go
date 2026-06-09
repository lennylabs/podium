package store

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Memory is an in-memory Store implementation used by tests and
// standalone bootstrapping. Production code uses SQLite or Postgres.
type Memory struct {
	mu        sync.Mutex
	tenants   map[string]Tenant
	manifests map[string]ManifestRecord
	deps      map[string][]DependencyEdge
	admins    map[string]AdminGrant // key: userID + "/" + orgID
	// operators holds instance-level operator grants (§4.7.1 Operator
	// role), keyed by identity with no org scope.
	operators map[string]bool
	layers    map[string]LayerConfig  // key: tenantID + "/" + id
	domains   map[string]DomainRecord // key: tenantID + "/" + layer + "/" + path
	// vectorPending is the §4.7.2 transactional vector outbox, keyed like
	// manifests (tenant/artifact@version).
	vectorPending map[string]VectorPending
}

// NewMemory returns a fresh in-memory Store.
func NewMemory() *Memory {
	return &Memory{
		tenants:       map[string]Tenant{},
		manifests:     map[string]ManifestRecord{},
		deps:          map[string][]DependencyEdge{},
		admins:        map[string]AdminGrant{},
		operators:     map[string]bool{},
		layers:        map[string]LayerConfig{},
		domains:       map[string]DomainRecord{},
		vectorPending: map[string]VectorPending{},
	}
}

func mkey(tenantID, artifactID, version string) string {
	return tenantID + "/" + artifactID + "@" + version
}

// CreateTenant inserts a new tenant. It is idempotent: creating an
// already-present ID is a no-op that leaves the stored tenant unchanged,
// matching the SQLite INSERT OR IGNORE and Postgres ON CONFLICT (id) DO
// NOTHING backends.
func (s *Memory) CreateTenant(_ context.Context, t Tenant) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.tenants[t.ID]; exists {
		return nil
	}
	// A created tenant is active. The SQL backends achieve this through the
	// active-column default; Memory sets it explicitly because the zero
	// value of Active is false (§4.7.1).
	t.Active = true
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

// ListTenants returns every provisioned tenant, ordered by ID.
func (s *Memory) ListTenants(_ context.Context) ([]Tenant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Tenant, 0, len(s.tenants))
	for _, t := range s.tenants {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// UpdateTenant writes a tenant's mutable configuration (Quota,
// ExposeScopePreview, Active) and preserves the ID and name. An unknown ID
// returns ErrTenantNotFound.
func (s *Memory) UpdateTenant(_ context.Context, t Tenant) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.tenants[t.ID]
	if !ok {
		return ErrTenantNotFound
	}
	cur.Quota = t.Quota
	cur.ExposeScopePreview = t.ExposeScopePreview
	cur.Active = t.Active
	s.tenants[t.ID] = cur
	return nil
}

// DeactivateTenant sets a tenant's Active flag false without deleting its
// data. An unknown ID returns ErrTenantNotFound.
func (s *Memory) DeactivateTenant(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.tenants[id]
	if !ok {
		return ErrTenantNotFound
	}
	cur.Active = false
	s.tenants[id] = cur
	return nil
}

// PutManifest enforces the immutability invariant: the same
// (tenant, id, version) with a different content hash is rejected.
func (s *Memory) PutManifest(_ context.Context, rec ManifestRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stampDeprecation(&rec)
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

// PutManifestWithVectorPending commits the manifest and the §4.7.2 outbox row
// atomically (under the single store lock). The pending row is written only
// when the manifest is newly inserted, so an idempotent re-ingest does not
// re-queue an unchanged artifact.
func (s *Memory) PutManifestWithVectorPending(_ context.Context, rec ManifestRecord, pending VectorPending) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stampDeprecation(&rec)
	key := mkey(rec.TenantID, rec.ArtifactID, rec.Version)
	if existing, ok := s.manifests[key]; ok {
		if existing.ContentHash != rec.ContentHash {
			return ErrImmutableViolation
		}
		return nil // idempotent; an existing pending row stands
	}
	s.manifests[key] = rec
	s.vectorPending[key] = pending
	return nil
}

// ListVectorPending returns up to limit eligible outbox rows, oldest first.
func (s *Memory) ListVectorPending(_ context.Context, limit int, now time.Time) ([]VectorPending, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]VectorPending, 0, len(s.vectorPending))
	for _, p := range s.vectorPending {
		if !p.NextRetryAt.After(now) {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EnqueuedAt.Before(out[j].EnqueuedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// MarkVectorPendingDone removes a drained outbox row.
func (s *Memory) MarkVectorPendingDone(_ context.Context, tenantID, artifactID, version string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.vectorPending, mkey(tenantID, artifactID, version))
	return nil
}

// MarkVectorPendingRetry records a failed drain attempt with backoff.
func (s *Memory) MarkVectorPendingRetry(_ context.Context, tenantID, artifactID, version string, nextRetryAt time.Time, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := mkey(tenantID, artifactID, version)
	p, ok := s.vectorPending[key]
	if !ok {
		return nil
	}
	p.Attempts++
	p.NextRetryAt = nextRetryAt
	p.LastError = errMsg
	s.vectorPending[key] = p
	return nil
}

// VectorOutboxStats returns the outbox depth and the oldest enqueue time.
func (s *Memory) VectorOutboxStats(_ context.Context) (int, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var oldest time.Time
	for _, p := range s.vectorPending {
		if oldest.IsZero() || p.EnqueuedAt.Before(oldest) {
			oldest = p.EnqueuedAt
		}
	}
	return len(s.vectorPending), oldest, nil
}

// GetManifest returns the manifest or ErrNotFound.
func (s *Memory) GetManifest(_ context.Context, tenantID, artifactID, version string) (ManifestRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.manifests[mkey(tenantID, artifactID, version)]
	if !ok || rec.DeletedAt != nil {
		// A soft-deleted manifest is hidden from normal reads (§8.4).
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
		if rec.TenantID == tenantID && rec.DeletedAt == nil {
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

// PurgeDeprecatedManifests removes deprecated versions whose
// DeprecatedAt predates `before` (§8.4 90-day window).
func (s *Memory) PurgeDeprecatedManifests(_ context.Context, before time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// §4.6/§4.7.6 extends-pin protection: collect the set of versions still
	// pinned as an extends parent so a deprecated parent a live child depends on
	// is not purged out from under it (purging it would orphan the child's
	// load_artifact). The pin form is "<artifact_id>@<version>".
	pinned := map[string]bool{}
	for _, rec := range s.manifests {
		if rec.TenantID != "" && rec.ExtendsPin != "" {
			pinned[rec.TenantID+"\x00"+rec.ExtendsPin] = true
		}
	}
	n := 0
	for key, rec := range s.manifests {
		if rec.Deprecated && rec.DeprecatedAt != nil && rec.DeprecatedAt.Before(before) {
			if pinned[rec.TenantID+"\x00"+rec.ArtifactID+"@"+rec.Version] {
				continue
			}
			delete(s.manifests, key)
			n++
		}
	}
	return n, nil
}

func domainKey(tenantID, layer, path string) string {
	return tenantID + "/" + layer + "/" + path
}

// PutDomain upserts the DOMAIN.md record for a (tenant, layer, path).
func (s *Memory) PutDomain(_ context.Context, rec DomainRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.domains[domainKey(rec.TenantID, rec.Layer, rec.Path)] = rec
	return nil
}

// ListDomains returns every domain record for the tenant in a stable
// order (path, then layer).
func (s *Memory) ListDomains(_ context.Context, tenantID string) ([]DomainRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []DomainRecord
	for _, rec := range s.domains {
		if rec.TenantID == tenantID {
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Layer < out[j].Layer
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

// DependencyInDegree counts distinct dependents per target artifact (§4.7.3).
func (s *Memory) DependencyInDegree(_ context.Context, tenantID string) (map[string]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Collect the distinct source set per target, then count, so an artifact
	// that both extends and delegates to the same target counts once.
	sources := map[string]map[string]struct{}{}
	for _, e := range s.deps[tenantID] {
		if e.To == "" || e.From == "" {
			continue
		}
		set := sources[e.To]
		if set == nil {
			set = map[string]struct{}{}
			sources[e.To] = set
		}
		set[e.From] = struct{}{}
	}
	out := make(map[string]int, len(sources))
	for to, set := range sources {
		out[to] = len(set)
	}
	return out, nil
}

// RevokeAdmin removes the admin grant; missing key is a no-op.
func (s *Memory) RevokeAdmin(_ context.Context, userID, orgID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.admins, userID+"/"+orgID)
	return nil
}

// GrantAdmin records an admin grant.
func (s *Memory) GrantAdmin(_ context.Context, g AdminGrant) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g.Granted.IsZero() {
		g.Granted = time.Now().UTC()
	}
	s.admins[g.UserID+"/"+g.OrgID] = g
	return nil
}

// IsAdmin checks the admin grant table.
func (s *Memory) IsAdmin(_ context.Context, userID, orgID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.admins[userID+"/"+orgID]
	return ok, nil
}

// ListAdminGrants returns every grant for orgID, ordered by UserID.
func (s *Memory) ListAdminGrants(_ context.Context, orgID string) ([]AdminGrant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []AdminGrant
	for _, g := range s.admins {
		if g.OrgID == orgID {
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UserID < out[j].UserID })
	return out, nil
}

// GrantOperator records an instance-level operator grant (§4.7.1 Operator
// role). The key is an identity with no org scope.
func (s *Memory) GrantOperator(_ context.Context, identity string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.operators[identity] = true
	return nil
}

// IsOperator reports whether the identity holds an operator grant.
func (s *Memory) IsOperator(_ context.Context, identity string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.operators[identity], nil
}

func layerKey(tenantID, id string) string { return tenantID + "/" + id }

// PutLayerConfig inserts or replaces a layer config.
func (s *Memory) PutLayerConfig(_ context.Context, cfg LayerConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.layers[layerKey(cfg.TenantID, cfg.ID)] = cfg
	return nil
}

// GetLayerConfig returns the layer or ErrNotFound.
func (s *Memory) GetLayerConfig(_ context.Context, tenantID, id string) (LayerConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, ok := s.layers[layerKey(tenantID, id)]
	if !ok || cfg.DeletedAt != nil {
		// Soft-deleted layers are hidden from normal reads (§8.4).
		return LayerConfig{}, ErrNotFound
	}
	return cfg, nil
}

// ListLayerConfigs returns every layer for the tenant in declared
// order (Order ascending; ties break alphabetical by ID).
func (s *Memory) ListLayerConfigs(_ context.Context, tenantID string) ([]LayerConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []LayerConfig{}
	for _, cfg := range s.layers {
		if cfg.TenantID == tenantID && cfg.DeletedAt == nil {
			out = append(out, cfg)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// DeleteLayerConfig soft-deletes a layer and the artifacts ingested from
// it (§8.4): both are tombstoned with the current time and excluded from
// normal reads, but recoverable via RestoreLayerConfig for 30 days.
func (s *Memory) DeleteLayerConfig(_ context.Context, tenantID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, ok := s.layers[layerKey(tenantID, id)]
	if !ok || cfg.DeletedAt != nil {
		return nil
	}
	now := time.Now().UTC()
	cfg.DeletedAt = &now
	s.layers[layerKey(tenantID, id)] = cfg
	for key, rec := range s.manifests {
		if rec.TenantID == tenantID && rec.Layer == id && rec.DeletedAt == nil {
			rec.DeletedAt = &now
			s.manifests[key] = rec
		}
	}
	return nil
}

// RestoreLayerConfig clears the soft-delete tombstone on a layer and its
// artifacts (§8.4 admin recovery).
func (s *Memory) RestoreLayerConfig(_ context.Context, tenantID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg, ok := s.layers[layerKey(tenantID, id)]
	if !ok || cfg.DeletedAt == nil {
		return ErrNotFound
	}
	cfg.DeletedAt = nil
	s.layers[layerKey(tenantID, id)] = cfg
	for key, rec := range s.manifests {
		if rec.TenantID == tenantID && rec.Layer == id && rec.DeletedAt != nil {
			rec.DeletedAt = nil
			s.manifests[key] = rec
		}
	}
	return nil
}

// ListDeletedLayerConfigs returns the tenant's soft-deleted layers.
func (s *Memory) ListDeletedLayerConfigs(_ context.Context, tenantID string) ([]LayerConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []LayerConfig{}
	for _, cfg := range s.layers {
		if cfg.TenantID == tenantID && cfg.DeletedAt != nil {
			out = append(out, cfg)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// PurgeExpiredLayerDeletions hard-deletes soft-deleted layers and their
// artifacts whose DeletedAt predates `before` (§8.4 30-day window end).
func (s *Memory) PurgeExpiredLayerDeletions(_ context.Context, before time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for key, cfg := range s.layers {
		if cfg.DeletedAt != nil && cfg.DeletedAt.Before(before) {
			delete(s.layers, key)
			n++
			for mk, rec := range s.manifests {
				if rec.TenantID == cfg.TenantID && rec.Layer == cfg.ID && rec.DeletedAt != nil {
					delete(s.manifests, mk)
				}
			}
		}
	}
	return n, nil
}
