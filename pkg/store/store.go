// Package store defines RegistryStore SPI (spec §9.1) plus the shared
// types every backend implementation works with: tenant identity,
// manifest metadata records, dependency edges, layer config rows, and
// admin grants. Phase 5 introduces the SPI and a SQLite reference
// implementation; the Postgres backend lands alongside it.
package store

import (
	"context"
	"errors"
	"time"
)

// Errors returned by Store implementations.
var (
	// ErrNotFound is returned by Get* methods when the record is absent.
	ErrNotFound = errors.New("store: not found")
	// ErrImmutableViolation is returned when an ingest attempts to
	// re-write a (artifact_id, version) pair with different content
	// (§4.7 immutability invariant). Maps to ingest.immutable_violation.
	ErrImmutableViolation = errors.New("store: immutable_violation")
	// ErrTenantNotFound signals that the tenant referenced by an
	// operation does not exist.
	ErrTenantNotFound = errors.New("store: tenant_not_found")
)

// Tenant identifies a Podium tenant (org). Tenant boundaries are the
// unit of multi-tenancy per §4.7.1.
type Tenant struct {
	ID    string
	Name  string
	Quota Quota
}

// Quota is the per-tenant resource budget (§4.7.8).
type Quota struct {
	StorageBytes      int64
	SearchQPS         int
	MaterializeRate   int
	AuditVolumePerDay int64
}

// ManifestRecord is the indexed metadata for one (artifact_id, version)
// pair. Body bytes live in the object store; this carries everything
// the registry indexes for search and listing.
type ManifestRecord struct {
	TenantID    string
	ArtifactID  string
	Version     string
	ContentHash string
	Type        string
	Description string
	Tags        []string
	Sensitivity string
	Layer       string
	Deprecated  bool
	IngestedAt  time.Time
	Frontmatter []byte
	Body        []byte
	// ExtendsPin is the parent reference pinned at this child's
	// ingest time per §4.7.6: "extends: parent version pinned at
	// child ingest time". Empty when the artifact does not extend.
	// Format: "<artifact_id>@<exact-version>".
	ExtendsPin string
}

// DependencyEdge is one cross-artifact relation indexed for impact
// analysis (§4.7.3).
type DependencyEdge struct {
	From string
	To   string
	Kind string // "extends" | "delegates_to" | "mcpServers"
}

// AdminGrant is one (identity, org_id, "admin") row (§4.7.2).
type AdminGrant struct {
	UserID  string
	OrgID   string
	Granted time.Time
}

// LayerConfig is one entry in a tenant's ordered layer list (§4.6).
// Admins manage admin-defined layers via the layer-config CLI; users
// register personal layers (which get implicit visibility:
// users:[<registrant>]).
type LayerConfig struct {
	TenantID    string
	ID          string
	SourceType  string   // "git" | "local"
	Repo        string   // git source
	Ref         string   // git source
	Root        string   // optional subpath
	LocalPath   string   // local source
	Order       int      // precedence within the tenant (lower = lower precedence)
	UserDefined bool
	Owner       string   // OIDC sub of the registrant for user-defined layers
	// Visibility fields (subset of layer.Visibility per §4.6).
	Public        bool
	Organization  bool
	Groups        []string
	Users         []string
	WebhookSecret string // HMAC secret for git source webhook (§7.3.1)
	CreatedAt     time.Time
}

// Store is the SPI implementations satisfy. Methods take a
// context.Context first per §9.3.
type Store interface {
	// Tenants
	CreateTenant(ctx context.Context, t Tenant) error
	GetTenant(ctx context.Context, id string) (Tenant, error)

	// Manifests
	PutManifest(ctx context.Context, rec ManifestRecord) error
	GetManifest(ctx context.Context, tenantID, artifactID, version string) (ManifestRecord, error)
	ListManifests(ctx context.Context, tenantID string) ([]ManifestRecord, error)

	// Dependencies
	PutDependency(ctx context.Context, tenantID string, edge DependencyEdge) error
	DependentsOf(ctx context.Context, tenantID, artifactID string) ([]DependencyEdge, error)

	// Admin grants
	GrantAdmin(ctx context.Context, g AdminGrant) error
	IsAdmin(ctx context.Context, userID, orgID string) (bool, error)

	// Layer configs (§4.6 layer list, managed via the layer CLI).
	PutLayerConfig(ctx context.Context, cfg LayerConfig) error
	GetLayerConfig(ctx context.Context, tenantID, id string) (LayerConfig, error)
	ListLayerConfigs(ctx context.Context, tenantID string) ([]LayerConfig, error)
	DeleteLayerConfig(ctx context.Context, tenantID, id string) error
}

// SuiteName is the canonical name of the conformance suite (§9.3).
// Implementations import test/conformance/store and reference this
// constant in their test names so suite discovery is deterministic.
const SuiteName = "store.RegistryStore"
