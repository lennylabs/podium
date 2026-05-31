// Package store defines RegistryStore SPI (spec §9.1) plus the
// shared types every backend implementation works with: tenant
// identity, manifest metadata records, dependency edges, layer
// config rows, and admin grants. Built-ins cover Memory, SQLite,
// and Postgres backends.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// nullBoolFromPtr converts an optional bool to a sql.NullBool for the SQL
// backends. A nil pointer persists as NULL so the tri-state survives a
// round trip (NULL = unset, distinct from an explicit false).
func nullBoolFromPtr(b *bool) sql.NullBool {
	if b == nil {
		return sql.NullBool{}
	}
	return sql.NullBool{Bool: *b, Valid: true}
}

// ptrFromNullBool is the inverse of nullBoolFromPtr: a NULL column reads
// back as a nil pointer.
func ptrFromNullBool(nb sql.NullBool) *bool {
	if !nb.Valid {
		return nil
	}
	v := nb.Bool
	return &v
}

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
	// ExposeScopePreview is the §3.5 tenant gate for GET
	// /v1/scope/preview. A nil pointer selects the documented default
	// (true); a non-nil false disables the endpoint so the registry
	// answers 403 scope_preview_disabled. The tri-state distinguishes
	// "operator left it unset" from "operator set it false".
	ExposeScopePreview *bool
}

// ScopePreviewEnabled resolves the §3.5 expose_scope_preview gate,
// defaulting to true when the tenant config leaves it unset.
func (t Tenant) ScopePreviewEnabled() bool {
	return t.ExposeScopePreview == nil || *t.ExposeScopePreview
}

// Quota is the per-tenant resource budget (§4.7.8).
type Quota struct {
	StorageBytes      int64
	SearchQPS         int
	MaterializeRate   int
	AuditVolumePerDay int64
	// MaxUserLayers is the §7.3.1 / §1.4 cap on user-defined layers per
	// identity ("Default cap: 3 user-defined layers per identity,
	// configurable per tenant"; §4.4 calls it the tenant's "default
	// user-layer cap"). Zero selects the deployment default (3); a
	// negative value disables the cap. The register handler resolves and
	// enforces it (pkg/registry/server/layers.go).
	MaxUserLayers int
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
	// Name is the §4.3 universal `name` field (from SKILL.md for
	// skills, ARTIFACT.md frontmatter otherwise). It feeds the §4.7
	// embedding text projection, which leads with `name`.
	Name        string
	Description string
	// WhenToUse is the §4.3 `when_to_use` list. The §4.7 embedding
	// projection joins it with newlines; the prose body is never
	// embedded.
	WhenToUse   []string
	Tags        []string
	Sensitivity string
	// SearchVisibility is the §4.3 universal field controlling whether
	// the artifact appears in search_artifacts results. "indexed" (the
	// default, also the empty string) appears normally; "direct-only"
	// is excluded from default search results and reachable only via
	// load_artifact. SearchArtifacts (§4.5.3) filters on it.
	SearchVisibility string
	Layer            string
	Deprecated       bool
	// ReplacedBy is the §4.7.4 upgrade target the manifest names
	// when deprecated. Empty when not set.
	ReplacedBy string
	// DeprecatedAt records when the §8.4 deprecation flag was set for
	// this version. Nil when the version is not deprecated. The
	// deprecated-version purge (§8.4 "90 days after the deprecation flag
	// is set") computes its window from this timestamp; PutManifest
	// stamps it from IngestedAt when Deprecated is true and it is unset.
	DeprecatedAt *time.Time
	// DeletedAt is the §8.4 soft-delete tombstone. A non-nil value marks
	// the manifest as recoverable-but-hidden: it was soft-deleted when
	// the layer it was ingested from was unregistered ("artifacts
	// soft-deleted, recoverable via admin"). Get/List exclude soft-deleted
	// rows; RestoreLayerConfig clears it within the recovery window; the
	// purge job hard-deletes rows whose DeletedAt predates the window.
	DeletedAt *time.Time
	// AuditRedact lists field names whose values must be replaced
	// with "[redacted]" in audit context for events that reference
	// this artifact (§8.2 manifest-declared redaction).
	AuditRedact []string
	IngestedAt  time.Time
	Frontmatter []byte
	Body        []byte
	// ExtendsPin is the parent reference pinned at this child's
	// ingest time per §4.7.6: "extends: parent version pinned at
	// child ingest time". Empty when the artifact does not extend.
	// Format: "<artifact_id>@<exact-version>".
	ExtendsPin string
	// Signature is the §4.7.9 signature envelope produced at
	// ingest by the configured SignatureProvider. Empty when the
	// deployment has no signing provider configured. Consumers
	// verify via sign.EnforceVerification at materialize time
	// against PODIUM_VERIFY_SIGNATURES.
	Signature string
	// Resources lists the §4.4 bundled resources for this artifact
	// version, persisted at ingest so load_artifact can serve them
	// (§7.2 data plane). Each entry is content-addressed; small
	// resources carry their bytes inline (Inline) while large ones are
	// delivered from object storage by content hash. Empty when the
	// package bundles no resources.
	Resources []ResourceRef
}

// ResourceRef is one §4.4 bundled resource attached to a manifest
// record. The registry stores resource bytes content-addressed by
// SHA-256 in object storage and deduplicated across versions; this ref
// is the lightweight pointer the metadata store keeps so load_artifact
// can return the resource inline (below the §4.2 256 KB cutoff) or as a
// presigned URL (above it).
type ResourceRef struct {
	// Path is the package-relative resource path (slash-separated).
	Path string
	// ContentHash is the "sha256:<hex>" digest of the bytes. It is also
	// the object-store key (minus the "sha256:" prefix), which is what
	// makes identical bytes deduplicate across artifact versions.
	ContentHash string
	// Size is the resource length in bytes.
	Size int64
	// ContentType is the MIME type guessed from the path extension.
	ContentType string
	// Inline carries the bytes for a resource at or below the §4.2
	// inline cutoff so load_artifact returns it in the response body
	// without an object-store round-trip. It is nil for resources above
	// the cutoff, which the data plane delivers via a presigned URL.
	Inline []byte
}

// MarshalResources encodes a manifest's resource refs for the SQL
// backends' JSON column. An empty list encodes to nil so a row with no
// bundled resources stores NULL rather than a literal "null".
func MarshalResources(refs []ResourceRef) ([]byte, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	return json.Marshal(refs)
}

// UnmarshalResources decodes the SQL backends' JSON resources column.
// Empty or NULL data decodes to a nil slice.
func UnmarshalResources(data []byte) ([]ResourceRef, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var refs []ResourceRef
	if err := json.Unmarshal(data, &refs); err != nil {
		return nil, err
	}
	return refs, nil
}

// DomainRecord is the parsed DOMAIN.md for one (tenant, layer, domain
// path), persisted at ingest so load_domain can apply §4.5 domain
// composition (description, prose body, keywords, unlisted,
// include/exclude imports, per-domain discovery overrides). Raw holds
// the full DOMAIN.md source; the registry re-parses it via
// manifest.ParseDomain at load time and merges candidates for the same
// path across layers per §4.5.4. Keyed by (TenantID, Layer, Path);
// re-ingesting a layer replaces its record for a path.
type DomainRecord struct {
	TenantID string
	// Path is the canonical domain path (slash-separated, relative to
	// the layer root). The registry root has no DOMAIN.md, so Path is
	// never empty.
	Path  string
	Layer string
	Raw   []byte
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
	SourceType  string // "git" | "local"
	Repo        string // git source
	Ref         string // git source
	Root        string // optional subpath
	LocalPath   string // local source
	Order       int    // precedence within the tenant (lower = lower precedence)
	UserDefined bool
	Owner       string // OIDC sub of the registrant for user-defined layers
	// Visibility fields (subset of layer.Visibility per §4.6).
	Public        bool
	Organization  bool
	Groups        []string
	Users         []string
	WebhookSecret string // HMAC secret for git source webhook (§7.3.1)
	// GitProvider names the §9.1 GitProvider whose signature scheme
	// verifies this layer's inbound webhook deliveries (e.g. "github",
	// "gitlab", "bitbucket", or a custom provider registered via
	// webhook.Default.Register). Empty defaults to "github". spec §7.3.1.
	GitProvider string
	// LastIngestedRef records the source-specific reference (commit
	// SHA for git) of the most recent successful ingest. The ingest
	// pipeline reads it before snapshotting so the source provider can
	// detect history rewrites (§7.3.1 force-push tolerance).
	LastIngestedRef string
	// ForcePushPolicy governs what happens when a force-push is
	// detected for a git source. The empty string and "tolerant" both
	// proceed and emit a layer.history_rewritten audit event;
	// "strict" rejects the ingest with ingest.history_rewritten.
	ForcePushPolicy string
	CreatedAt       time.Time
	// DeletedAt is the §8.4 soft-delete tombstone for a layer
	// unregistered by its owner. A non-nil value hides the layer from
	// Get/List while keeping it (and its artifacts) recoverable via
	// RestoreLayerConfig for the 30-day window, after which the purge job
	// hard-deletes it. Nil for an active layer.
	DeletedAt *time.Time
}

// stampDeprecation sets DeprecatedAt from IngestedAt (falling back to
// now) when a manifest is deprecated and the timestamp is unset, so the
// §8.4 90-day purge window has an anchor. A version's deprecated state is
// fixed at ingest time; the flag is "set" when the deprecated version is
// stored. Every backend's PutManifest calls this before persisting.
func stampDeprecation(rec *ManifestRecord) {
	if rec.Deprecated && rec.DeprecatedAt == nil {
		t := rec.IngestedAt
		if t.IsZero() {
			t = time.Now().UTC()
		}
		t = t.UTC()
		rec.DeprecatedAt = &t
	}
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
	// PurgeDeprecatedManifests hard-deletes deprecated manifest versions
	// whose DeprecatedAt predates `before`, implementing the §8.4
	// "Deprecated artifact versions: 90 days after the deprecation flag
	// is set" rule. Returns the number of versions removed. Non-deprecated
	// and not-yet-expired versions are left untouched.
	PurgeDeprecatedManifests(ctx context.Context, before time.Time) (int, error)

	// Domains (§4.5 DOMAIN.md composition). PutDomain upserts the
	// record for a (tenant, layer, path); ListDomains returns every
	// domain record for the tenant so LoadDomain can merge candidates
	// across layers (§4.5.4).
	PutDomain(ctx context.Context, rec DomainRecord) error
	ListDomains(ctx context.Context, tenantID string) ([]DomainRecord, error)

	// Dependencies
	PutDependency(ctx context.Context, tenantID string, edge DependencyEdge) error
	DependentsOf(ctx context.Context, tenantID, artifactID string) ([]DependencyEdge, error)

	// Admin grants
	GrantAdmin(ctx context.Context, g AdminGrant) error
	RevokeAdmin(ctx context.Context, userID, orgID string) error
	IsAdmin(ctx context.Context, userID, orgID string) (bool, error)

	// Layer configs (§4.6 layer list, managed via the layer CLI).
	PutLayerConfig(ctx context.Context, cfg LayerConfig) error
	GetLayerConfig(ctx context.Context, tenantID, id string) (LayerConfig, error)
	ListLayerConfigs(ctx context.Context, tenantID string) ([]LayerConfig, error)
	// DeleteLayerConfig soft-deletes a layer per §8.4: the layer config
	// and every artifact ingested from it are tombstoned (DeletedAt set to
	// the current time) rather than hard-deleted, so they stay recoverable
	// for the 30-day window. Subsequent Get/List calls exclude them.
	DeleteLayerConfig(ctx context.Context, tenantID, id string) error
	// RestoreLayerConfig clears the soft-delete tombstone on a layer and
	// its artifacts, implementing the §8.4 admin recovery path. Returns
	// ErrNotFound when no soft-deleted layer matches.
	RestoreLayerConfig(ctx context.Context, tenantID, id string) error
	// ListDeletedLayerConfigs returns the tenant's soft-deleted layers so
	// an admin can see what is recoverable within the window.
	ListDeletedLayerConfigs(ctx context.Context, tenantID string) ([]LayerConfig, error)
	// PurgeExpiredLayerDeletions hard-deletes soft-deleted layers and
	// their artifacts whose DeletedAt predates `before`, ending the §8.4
	// 30-day recovery window. Returns the number of layers removed.
	PurgeExpiredLayerDeletions(ctx context.Context, before time.Time) (int, error)
}

// SuiteName is the canonical name of the conformance suite (§9.3).
// Implementations import test/conformance/store and reference this
// constant in their test names so suite discovery is deterministic.
const SuiteName = "store.RegistryStore"
