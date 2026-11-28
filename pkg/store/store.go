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

	"github.com/lennylabs/podium/pkg/spi"
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

// Errors returned by Store implementations. Each is a structured *spi.Error
// carrying its §6.10 code so the RegistryStore SPI conforms to the §9.3
// "Structured errors" constraint. The pointer is a package-level sentinel, so
// errors.Is matching through fmt.Errorf wrapping is unchanged.
var (
	// ErrNotFound is returned by Get* methods when the record is absent.
	ErrNotFound = &spi.Error{Code: "registry.not_found", Message: "store: not found"}
	// ErrImmutableViolation is returned when an ingest attempts to
	// re-write a (artifact_id, version) pair with different content
	// (§4.7 immutability invariant).
	ErrImmutableViolation = &spi.Error{Code: "ingest.immutable_violation", Message: "store: immutable_violation"}
	// ErrTenantNotFound signals that the tenant referenced by an
	// operation does not exist.
	ErrTenantNotFound = &spi.Error{Code: "registry.tenant_not_found", Message: "store: tenant_not_found"}
)

// ErrIngestSuperseded is returned by SoftDeleteArtifacts and
// RecordLayerIngest, which then change nothing, when the live layer does not
// hold the caller's ingest claim: a later cycle of the layer claimed it, or
// the layer was unregistered. The ingest pipeline handles it and never
// returns it to a client.
//
// Spec: §7.3.1 (Removed artifacts)
var ErrIngestSuperseded = errors.New("store: a later ingest cycle of the layer claimed it")

// Tenant identifies a Podium tenant (org). Tenant boundaries are the
// unit of multi-tenancy per §4.7.1.
type Tenant struct {
	ID    string
	Name  string
	Quota Quota
	// ExposeScopePreview is the §3.5 tenant gate for GET
	// /v1/scope/preview. A nil pointer selects the documented default
	// (true); a non-nil false disables the endpoint so the registry
	// answers 403 config.scope_preview_disabled. The tri-state distinguishes
	// "operator left it unset" from "operator set it false".
	ExposeScopePreview *bool
	// Active reports whether the tenant resolves. A deactivated tenant
	// (Active false) stops resolving and is treated as unprovisioned
	// (§4.7.1) while its data persists. A newly created tenant is active;
	// DeactivateTenant is the only path that sets it false. On the SQL
	// backends an `active` column defaulting to true backs the value, so a
	// tenant created without writing the column is active.
	Active bool
}

// ScopePreviewEnabled resolves the §3.5 expose_scope_preview gate,
// defaulting to true when the tenant config leaves it unset.
func (t Tenant) ScopePreviewEnabled() bool {
	return t.ExposeScopePreview == nil || *t.ExposeScopePreview
}

// Quota is the per-tenant resource budget (§4.7.8). The JSON tags carry
// the §7.2.1 control-plane names, which are the names the §7.3.3 tenant
// object already uses for the same five numbers, so GET /v1/quota and
// GET /v1/admin/tenants share the field names. GET /v1/quota reports the
// enforced search_qps, materialize_rate, and audit_volume_per_day, which
// resolve a zero stored value to the deployment default and a negative one to
// 0, so they can differ from the stored values GET /v1/admin/tenants reports.
type Quota struct {
	StorageBytes      int64 `json:"storage_bytes"`
	SearchQPS         int   `json:"search_qps"`
	MaterializeRate   int   `json:"materialize_rate"`
	AuditVolumePerDay int64 `json:"audit_volume_per_day"`
	// MaxUserLayers is the §7.3.1 / §1.4 cap on user-defined layers per
	// identity ("Default cap: 3 user-defined layers per identity,
	// configurable per tenant"; §4.4 calls it the tenant's "default
	// user-layer cap"). Zero selects the deployment default (the
	// registry's WithMaxUserLayers value, which serverboot reads from
	// PODIUM_MAX_USER_LAYERS, or 3 when that is zero). A positive value is
	// enforced whatever the deployment default is, and a negative value
	// disables the cap. The register handler resolves and enforces it
	// (pkg/registry/server/layers.go).
	MaxUserLayers int `json:"max_user_layers"`
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
	// SkillRaw is the verbatim SKILL.md bytes for a type: skill artifact,
	// preserved so server-source delivery reproduces the authored skill file
	// exactly. The §11 filesystem ↔ server equivalence test requires the
	// materialized SKILL.md to be byte-identical across both consumers, and
	// the authored SKILL.md frontmatter (name, description, compatibility,
	// allowed-tools, and any other keys) cannot be reconstructed from the
	// indexed fields alone. Empty for non-skills.
	SkillRaw []byte
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
	// PackageDigest is the §4.7.6 package digest of the version, written
	// sha256:<hex>. Ingest stores the digest the walk computed from the
	// files it read, and the §13.4 fill computes it for a row stored
	// without one. Empty for a row the fill has not reached or whose stored
	// files give no entry list. The registry serves it as the load_artifact
	// package_digest member and reads it nowhere else.
	PackageDigest string
	// AttestationOutcome, AttestationFailure, and AttestationEvaluatedAt are
	// the §4.7.11 attestation outcome of the version. Every read returns an
	// outcome; a write that leaves it empty stores not_evaluated, and a
	// failure is stored only with the outcome failed.
	AttestationOutcome     string
	AttestationFailure     *AttestationFailure
	AttestationEvaluatedAt *time.Time
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
	Kind string // "delegates_to" | "mcpServers"
}

// indexedEdgeKindsSQL is the predicate the SQL stores apply to every
// dependency read. A database written by an earlier release can hold edges of
// a kind §4.7.3 no longer defines, such as extends, and DependentsOf and
// DependencyInDegree return and count none of them. No stored edge is deleted.
//
// Spec: §4.7.3
const indexedEdgeKindsSQL = "kind IN ('delegates_to','mcpServers')"

// isIndexedEdgeKind reports whether kind is a §4.7.3 edge kind, the Memory
// store's form of indexedEdgeKindsSQL.
//
// Spec: §4.7.3
func isIndexedEdgeKind(kind string) bool {
	return kind == "delegates_to" || kind == "mcpServers"
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
// users:[<registrant>]). The JSON tags below are the wire encoding of
// the §7.3.1 layer object under the §7.2.1 control-plane conventions;
// the SQL backends bind their columns to the Go fields rather than to
// this encoding, so a tag here moves no stored row.
type LayerConfig struct {
	// TenantID is withheld from every response that marshals a
	// LayerConfig, per §7.2.1 and §7.3.1. LayerEndpoint binds one tenant
	// at construction and every handler scopes its store calls to it, so
	// the value is constant across every record a response carries, and
	// emitting it would republish the registry's stored tenant identifier
	// on a read that is not admin-gated (notably GET /v1/layers). The SQL
	// backends persist it through an explicit column, so the tag does not
	// affect storage.
	TenantID   string `json:"-"`
	ID         string `json:"id"`
	SourceType string `json:"source_type"` // "git" | "local"
	// Repo is the git remote in the form a response may report. A LayerConfig
	// returned by a Store read carries no URL userinfo here for a repo in the
	// split class (see SplitLayerRepo). A config built by a caller and not yet
	// read back, such as the one a registration or the boot seed writes, may
	// still carry userinfo, so a response and a log line pass it through
	// source.RedactRepo.
	Repo string `json:"repo"` // git source
	// RegisteredRepo is the git remote exactly as registered, userinfo
	// included. A Store read sets it for a repo in the split class and leaves
	// it empty otherwise. It is withheld from every response by the json:"-"
	// tag, on the model of WebhookSecret. The clone reads it through CloneRepo.
	//
	// Spec: §7.3.1 (Repository credentials)
	RegisteredRepo RegisteredRepo `json:"-"`
	Ref            string         `json:"ref"`        // git source
	Root           string         `json:"root"`       // optional subpath
	LocalPath      string         `json:"local_path"` // local source
	Order          int            `json:"order"`      // precedence within the tenant (lower = lower precedence)
	UserDefined    bool           `json:"user_defined"`
	Owner          string         `json:"owner"` // OIDC sub of the registrant for user-defined layers
	// Visibility fields (subset of layer.Visibility per §4.6).
	Public       bool     `json:"public"`
	Organization bool     `json:"organization"`
	Groups       []string `json:"groups"`
	Users        []string `json:"users"`
	// WebhookSecret is the HMAC secret for a git source's inbound webhook
	// (§7.3.1). spec §7.3.1 frames it as a credential returned once at
	// registration ("podium layer register returns the webhook URL and HMAC
	// secret"). It is redacted from every API response that marshals a
	// LayerConfig (notably GET /v1/layers, which is not admin-gated) via the
	// json:"-" tag, mirroring the outbound-receiver masking in webhooks.go;
	// the secret reaches the caller only through the dedicated webhook_secret
	// field on the one-time register and rotate-secret responses.
	// The SQL backends persist it through explicit columns, so the tag does
	// not affect storage.
	WebhookSecret string `json:"-"`
	// GitProvider names the §9.1 GitProvider whose signature scheme
	// verifies this layer's inbound webhook deliveries (e.g. "github",
	// "gitlab", "bitbucket", or a custom provider registered via
	// webhook.Default.Register). Empty defaults to "github". spec §7.3.1.
	GitProvider string `json:"git_provider"`
	// LastIngestedRef records the source-specific reference (commit
	// SHA for git) of the most recent successful ingest. The ingest
	// pipeline reads it before snapshotting so the source provider can
	// detect history rewrites (§7.3.1 force-push tolerance).
	LastIngestedRef string `json:"last_ingested_ref"`
	// ForcePushPolicy governs what happens when a force-push is
	// detected for a git source. The empty string and "tolerant" both
	// proceed and emit a layer.history_rewritten audit event;
	// "strict" rejects the ingest with ingest.history_rewritten.
	// Settable per layer via the register/update API, the
	// `podium layer register/update --force-push-policy` CLI flag, and
	// the registry.yaml `source.git.force_push_policy` key (§7.3.1).
	ForcePushPolicy string `json:"force_push_policy,omitempty"`
	// TrustMode is the §4.7.11 trust mode: "none", "warn", "require", or
	// empty, which resolves to PODIUM_LAYER_TRUST_DEFAULT.
	TrustMode string `json:"trust_mode"`
	// TrustRequirements is the §4.7.11 requirement list, which holds at most
	// one requirement in this release. A signer's derived source binding is
	// stored here as the registry derived it.
	TrustRequirements []TrustRequirement `json:"trust_requirements"`
	// TrustLayerRoot is the §4.7.11 root under which a local layer's content
	// was signed. Empty expects the layer subject .podium/layer.
	TrustLayerRoot string `json:"trust_layer_root"`
	// LastIngestedAt records the wall-clock time of the most recent
	// successful ingest cycle for this layer. The §7.3.1 layer list
	// exposes it as `last_ingested_at` for staleness monitoring. Nil
	// until the layer has completed an ingest at least once.
	LastIngestedAt *time.Time `json:"last_ingested_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	// DeletedAt is the §8.4 soft-delete tombstone for a layer
	// unregistered by its owner. A non-nil value hides the layer from
	// Get/List while keeping it (and its artifacts) recoverable via
	// RestoreLayerConfig for the 30-day window, after which the purge job
	// hard-deletes it. Nil for an active layer.
	DeletedAt *time.Time `json:"deleted_at"`
	// AttestationRecord holds the layer's §4.7.11 attestation record. A read
	// returns it as stored, and the layer object reports its members as
	// read-only. PutLayerConfig ignores it and keeps the stored record.
	AttestationRecord
}

// stampDeprecation sets DeprecatedAt from IngestedAt (falling back to
// now) when a manifest is deprecated and the timestamp is unset, so the
// §8.4 deprecated-version purge window has an anchor.
//
// §8.4 specifies the window as "90 days after the deprecation flag is set".
// In this implementation `deprecated` is a per-version manifest frontmatter
// field, so a version's deprecated state is fixed when that version is
// ingested: deprecating an artifact means ingesting a manifest with
// `deprecated: true`, which produces a distinct record (new content hash)
// with its own IngestedAt. There is no in-place toggle that flips an
// already-stored version's flag, so IngestedAt of the deprecated record is
// the moment the flag was set, and anchoring DeprecatedAt to it matches the
// spec. Were a post-ingest deprecation path ever added, it would need to
// stamp DeprecatedAt at that transition instead of inheriting IngestedAt.
// Every backend's PutManifest calls this before persisting. spec: §8.4.
func stampDeprecation(rec *ManifestRecord) {
	// Spec: §4.7.11 (Attestation outcome) — every backend stores the
	// outcome in one form, which a write prepares here beside the stamp.
	normalizeOutcome(rec)
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
	// ListTenants returns every provisioned tenant. It is the only
	// cross-org tenant read (§7.3.3); on Postgres it re-scopes the FORCE'd
	// row-level-security policy on public.tenants through the
	// "*operator-list*" sentinel rather than a per-org org_id.
	ListTenants(ctx context.Context) ([]Tenant, error)
	// UpdateTenant writes a tenant's mutable configuration (Quota,
	// ExposeScopePreview, and Active) and returns ErrTenantNotFound for an
	// unknown ID. It changes neither the ID nor the name; any Name on the
	// passed Tenant is ignored. It writes the whole mutable record, so the
	// PATCH handler overlays the supplied fields onto the current row
	// before calling it.
	UpdateTenant(ctx context.Context, t Tenant) error
	// DeactivateTenant sets a tenant's Active flag false without deleting
	// its data (§4.7.1 soft deactivation) and returns ErrTenantNotFound for
	// an unknown ID.
	DeactivateTenant(ctx context.Context, id string) error

	// Operator grants (§4.7.1 Operator role). An operator is an
	// instance-level identity authorized to manage tenants across the
	// instance. The grant carries no org id and lives outside any per-org
	// schema (a cross-org table on the SQL backends), read and written
	// through a privileged path rather than the per-org row-level-security
	// predicate.
	GrantOperator(ctx context.Context, identity string) error
	IsOperator(ctx context.Context, identity string) (bool, error)

	// Manifests
	PutManifest(ctx context.Context, rec ManifestRecord) error
	GetManifest(ctx context.Context, tenantID, artifactID, version string) (ManifestRecord, error)
	ListManifests(ctx context.Context, tenantID string) ([]ManifestRecord, error)
	// RehashManifest replaces the content_hash and signature of one manifest
	// row, including a soft-deleted one, only when the stored content_hash
	// equals oldHash and the stored signature equals oldSignature. It returns
	// ErrNotFound when no row exists for the key and ErrImmutableViolation
	// when either condition fails. Its callers are the §13.4 stored-value
	// rewrite and the sign-stored-rows command, and the compare-and-swap
	// keeps a concurrent ingest, the same pass on a peer replica, or a second
	// command run from overwriting a hash or an envelope it did not read. A
	// call with oldSignature "" on an unchanged hash is the sign-once case: it
	// attaches a first envelope, and two replicas cannot both sign one row.
	// It is the only write to a stored content_hash after PutManifest, which
	// is the immutability anchor and refuses a differing hash for an
	// existing key, so no ingest path can repair a stored value.
	RehashManifest(ctx context.Context, tenantID, artifactID, version, oldHash, oldSignature, newHash, signature string) error
	// SetPackageDigest stores the §4.7.6 package digest of one manifest row,
	// including a soft-deleted one, only when the stored content_hash equals
	// contentHash, and changes no other column. It returns ErrNotFound when
	// no row exists for the key and ErrImmutableViolation when the stored
	// hash differs, so the §13.4 fill never writes a digest over a row whose
	// bytes changed after it read them.
	SetPackageDigest(ctx context.Context, tenantID, artifactID, version, contentHash, digest string) error
	// ListManifestsIncludingDeleted is ListManifests without the deleted_at
	// filter, so a migration reaches the rows a restored layer would serve.
	ListManifestsIncludingDeleted(ctx context.Context, tenantID string) ([]ManifestRecord, error)
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
	// DependencyInDegree returns, per depended-on artifact, the number of
	// distinct artifacts that depend on it (§4.7.3 "frequently-depended-on
	// artifacts surface higher"). The key is the target artifact ID; the
	// value counts distinct sources across the §4.7.3 kinds delegates_to and
	// mcpServers; a stored edge of any other kind is not read. Artifacts with
	// no dependents are absent. Tenant-scoped.
	DependencyInDegree(ctx context.Context, tenantID string) (map[string]int, error)

	// Admin grants
	GrantAdmin(ctx context.Context, g AdminGrant) error
	RevokeAdmin(ctx context.Context, userID, orgID string) error
	IsAdmin(ctx context.Context, userID, orgID string) (bool, error)
	// ListAdminGrants returns every admin grant for orgID, ordered by
	// UserID. Migration (§13.4 / §13.10 "admin grants ... are preserved")
	// enumerates grants here so they transfer to the target deployment.
	ListAdminGrants(ctx context.Context, orgID string) ([]AdminGrant, error)

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
	// PurgeExpiredLayerDeletions hard-deletes soft-deleted layers whose
	// DeletedAt predates `before`, and every soft-deleted manifest row whose
	// DeletedAt predates it, whether a layer unregistration or a §7.3.1
	// removed-artifact ingest tombstoned the row, ending the §8.4 recovery
	// window. Returns the number of layers removed.
	PurgeExpiredLayerDeletions(ctx context.Context, before time.Time) (int, error)
	// SoftDeleteArtifacts tombstones, at the given time, every live manifest
	// row of tenantID that layerID contributed for one of artifactIDs: the
	// §7.3.1 removed-artifact case. Rows of other layers and rows already
	// tombstoned are unchanged. A later PutManifest of a tombstoned row's
	// (artifact_id, version) with the same content hash and the same layer
	// restores the row as that put states it; a put from another layer leaves
	// it tombstoned, and a different hash is ErrImmutableViolation. When claim
	// is non-empty, the call first checks, in the transaction that tombstones,
	// that the live layer layerID holds claim (ClaimLayerIngest), and
	// otherwise returns ErrIngestSuperseded and changes nothing; it makes that
	// check even when artifactIDs is empty, which is how a cycle checks its
	// claim before it stores. An empty claim tombstones unconditionally.
	SoftDeleteArtifacts(ctx context.Context, tenantID, layerID string, artifactIDs []string, at time.Time, claim string) error
	// ClaimLayerIngest makes claim the live layer's ingest claim, superseding
	// the claim of every earlier §7.3.1 ingest cycle of the layer, and returns
	// the layer as stored and the claim it replaced, empty when the layer held
	// none. It returns ErrNotFound, claiming nothing, when no live layer
	// layerID exists. PutLayerConfig keeps the claim, and DeleteLayerConfig
	// clears it, so a cycle in flight when the layer is unregistered never
	// matches again after a restore.
	ClaimLayerIngest(ctx context.Context, tenantID, layerID, claim string) (LayerConfig, string, error)
	// ReleaseLayerIngest hands a failed cycle's claim back: when the live
	// layer still holds claim, it holds previous again, so the cycle claim
	// displaced continues. A later claim, or an unregistered layer, is left
	// as it is.
	ReleaseLayerIngest(ctx context.Context, tenantID, layerID, claim, previous string) error
	// RecordLayerIngest sets the live layer's LastIngestedRef to ref and its
	// LastIngestedAt to at when the layer holds claim, and changes no other
	// field. It returns ErrIngestSuperseded, changing nothing, otherwise.
	RecordLayerIngest(ctx context.Context, tenantID, layerID, claim, ref string, at *time.Time) error
	// WriteLayerAttestation writes the §4.7.11 attestation record of an ingest
	// cycle in one store write, while the live layer holds claim. A write that
	// advances the mark applies only while the stored mark and commit equal
	// the values the write read at its claim. It returns ErrIngestSuperseded,
	// changing nothing, when either condition fails.
	WriteLayerAttestation(ctx context.Context, tenantID, layerID, claim string, w AttestationWrite) error
	// ClaimLayerIngestWithReset claims the live layer as ClaimLayerIngest
	// does and, in the same write, applies the §4.7.11 mark reset: it clears
	// the mark and sets the recorded commit to toCommit, empty for none. It
	// returns the layer as stored after the reset, the claim it replaced, and
	// the attestation record before the reset.
	ClaimLayerIngestWithReset(ctx context.Context, tenantID, layerID, claim, toCommit string) (LayerConfig, string, AttestationRecord, error)
	// MigrateLayerConfig writes cfg as PutLayerConfig does and applies the
	// §13.4 copy rule to its attestation record in the same store write:
	// a target that holds no row for the layer, live or soft-deleted, takes
	// the mark and the commit of cfg.AttestationRecord, and a target record
	// takes them only under the rule migrationReplaces states.
	MigrateLayerConfig(ctx context.Context, cfg LayerConfig) (AttestationCopy, error)

	// WriteAttestations stores the bundles, links, and outcomes of w in one
	// store write (§4.7.11 Attestation storage). With a claim it applies only
	// while the live layer layerID holds claim, and returns
	// ErrIngestSuperseded, changing nothing, otherwise, which includes a
	// layer that no longer stores w.Policy; an empty claim applies the write
	// unconditionally and ignores layerID and w.Policy. The result names the
	// conditional link updates that applied.
	WriteAttestations(ctx context.Context, tenantID, layerID, claim string, w AttestationWrites) (AttestationWriteResult, error)
	// GetAttestation returns the stored bundle of digest, or ErrNotFound.
	GetAttestation(ctx context.Context, tenantID, digest string) (StoredAttestation, error)
	// ListAttestations returns every stored bundle of the tenant, by digest.
	ListAttestations(ctx context.Context, tenantID string) ([]StoredAttestation, error)
	// ListAttestationLinks returns the tenant's links that f selects, by
	// version, with the verified links of a version first.
	ListAttestationLinks(ctx context.Context, tenantID string, f LinkFilter) ([]AttestationLink, error)
	// ListRootRecords returns the §4.7.11 root record of every instance.
	ListRootRecords(ctx context.Context) ([]RootRecord, error)
	// ReplaceRootRecord writes next as the root record of its instance when
	// the stored record equals prev, nil for none, and reports whether it
	// wrote.
	ReplaceRootRecord(ctx context.Context, prev *RootRecord, next RootRecord) (bool, error)

	// Data-migration markers (§13.4). The §13.4 additive schema guarantee
	// does not cover the values stored under the schema, so a one-time
	// rewrite of stored values records that it has run and never runs twice
	// against one store.
	//
	// DataMigrationApplied reports whether the named one-time rewrite of
	// stored values has completed against this store. The marker is global
	// to the store rather than per tenant. The same table carries
	// deployment-level markers that are not data migrations, such as
	// DeploymentMarkerVerifiedIdentity.
	DataMigrationApplied(ctx context.Context, name string) (bool, error)
	// SetDataMigrationApplied records (applied true) or removes (applied
	// false) the named marker. Both directions are idempotent, so replicas
	// that finish the same pass together both succeed. The pass sets it.
	// podium admin migrate-to-standard, which copies rows another store
	// computed, removes it on the target so the rewrite runs again over the
	// copied rows: at the target's next start only when the target is the
	// SQLite store in the signing key file's directory, and otherwise
	// through sign-stored-rows.
	SetDataMigrationApplied(ctx context.Context, name string, applied bool) error

	// Semantic overlap records (§7.3.1). The registry derives them from the
	// search index and keeps them in the tenant's metadata store so a read
	// applies its threshold without querying the index.
	//
	// ReplaceOverlapRecords stores one successful computation of
	// comp.ArtifactID: it removes every record of tenantID that artifact
	// owns, stores recs, and upserts comp as the artifact's computation
	// entry, in one transaction, so the artifact's records always come from
	// one computation. Records other artifacts own are left in place, so a
	// pair one side found survives the other side's recomputation. Each
	// record must be owned by comp.ArtifactID and name another artifact as
	// its peer, or the call returns ErrInvalidOverlapRecord and changes
	// nothing. An empty recs leaves the artifact owning no record and still
	// records the computation. When the stored entry is for another version
	// and its ComputedAt is after comp.ComputedAt, the call returns
	// ErrOverlapSuperseded and changes nothing, because a computation of a
	// newer served version stored first. Concurrent calls for one artifact
	// are serialized, so the artifact's records come from one of them.
	ReplaceOverlapRecords(ctx context.Context, tenantID string, comp OverlapComputation, recs []OverlapRecord) error
	// ListOverlapRecords returns every record of tenantID ordered by owner
	// and then peer.
	ListOverlapRecords(ctx context.Context, tenantID string) ([]OverlapRecord, error)
	// ListOverlapComputations returns every computation entry of tenantID
	// ordered by artifact.
	ListOverlapComputations(ctx context.Context, tenantID string) ([]OverlapComputation, error)
	// DeleteOverlapArtifacts removes the records owned by and the computation
	// entries of each artifact in artifactIDs, in one transaction. The sweep
	// calls it for artifacts that are no longer served; a record naming such
	// an artifact as its peer is removed when its owner is recomputed.
	DeleteOverlapArtifacts(ctx context.Context, tenantID string, artifactIDs []string) error

	// Usage counts (§8.7): daily totals per layer, artifact, client,
	// and harness.
	//
	// AddUsageCounts adds each count's counters to the stored row of its
	// key, creating the row when none exists, in one transaction. Adding is
	// commutative, so replicas that flush to one store produce totals across
	// every replica. A count that names no day, layer, artifact, client
	// kind, or harness, carries a day that is not a YYYY-MM-DD date, or carries a
	// negative counter returns ErrInvalidUsageCount and changes nothing.
	AddUsageCounts(ctx context.Context, tenantID string, counts []UsageCount) error
	// ListUsageCounts returns the tenant's rows whose day lies from fromDay
	// to toDay, both YYYY-MM-DD dates and both included, ordered by day,
	// layer, artifact, client, and harness.
	ListUsageCounts(ctx context.Context, tenantID, fromDay, toDay string) ([]UsageCount, error)
	// SumUsageLoads returns the tenant's loads from fromDay to toDay, both
	// included, summed per layer and artifact across days, clients, and
	// harnesses. A pair with no loads in the range is absent, and the result
	// is ordered by layer and artifact.
	SumUsageLoads(ctx context.Context, tenantID, fromDay, toDay string) ([]UsageLoads, error)
	// SumUsage returns the tenant's rows that q selects, summed per group of
	// the key columns q names, with each group's latest day of loads and of
	// syncs, ordered by the grouped columns in byte order. A dimension
	// outside the UsageDim constants is an error. It returns sums rather than
	// rows, so its result is bounded by the number of groups rather than by
	// the number of stored rows in the range.
	SumUsage(ctx context.Context, tenantID string, q UsageSumQuery) ([]UsageSum, error)
	// DeleteUsageBefore removes the rows of every tenant whose day is before
	// day, a YYYY-MM-DD date, and returns the number removed.
	DeleteUsageBefore(ctx context.Context, day string) (int, error)
}

// DataMigrationContentHashFraming names the one-time rewrite of every stored
// content hash onto the §4.7.6 framed canonical serialization. The registry's
// first start on the new binary runs the pass and sets the marker when the
// store is the SQLite store in the signing key file's directory or holds no
// manifest row; for every other store, sign-stored-rows runs the pass and
// sets the marker, and a start refuses until it has. podium admin
// migrate-to-standard clears the marker on its target so the rewrite runs
// again over the rows it copied, by the same rule.
const DataMigrationContentHashFraming = "content-hash-framing"

// DataMigrationPackageDigest names the one-time fill of the §4.7.6 package
// digest of every stored row. Every start whose listener binds runs the fill
// while the marker is absent and sets it once every row holds its digest.
// podium admin migrate-to-standard clears it on its target, so the target's
// next start fills the rows the source stored without a digest.
//
// Spec: §13.4
const DataMigrationPackageDigest = "package-digest"

// DeploymentMarkerVerifiedIdentity names the record that an earlier registry
// start installed a request-time identity verifier. It lives in the same
// store-global marker table as the data-migration markers, because that table
// is the store's one place for deployment-level state: one row per deployment
// rather than per tenant, outside the per-org schemas on Postgres. Every start
// with a verifier sets it, a start without one is refused while it is set, and
// the acknowledged removal clears it. The in-memory store keeps nothing across
// restarts, so the record never outlives the process there and the refusal
// never applies to it.
//
// Spec: §13.10 (Identity provider removed between starts)
const DeploymentMarkerVerifiedIdentity = "identity-verified"

// SuiteName is the canonical name of the conformance suite (§9.3).
// Implementations import test/conformance/store and reference this
// constant in their test names so suite discovery is deterministic.
const SuiteName = "store.RegistryStore"

// VectorPending is one row in the §4.7.2 transactional vector outbox. It holds
// the composed embedding text for a (tenant, artifact, version) whose vector
// has not yet been written to an external backend, plus the retry bookkeeping
// the drain worker maintains.
type VectorPending struct {
	TenantID   string
	ArtifactID string
	Version    string
	// Text is the composed embedding text the drain worker embeds and puts to
	// the external backend.
	Text string
	// EnqueuedAt is when the row was committed alongside the manifest. The
	// drain worker uses the oldest EnqueuedAt to compute outbox age for the
	// vector.outbox_lagging signal.
	EnqueuedAt time.Time
	// Attempts counts drain attempts so the worker can back off and, after a
	// ceiling, surface a stuck row.
	Attempts int
	// NextRetryAt gates when the worker may try the row again (exponential
	// backoff). A row is eligible when NextRetryAt is not after now.
	NextRetryAt time.Time
	// LastError is the most recent drain failure, retained for diagnostics.
	LastError string
}

// VectorPendingKey identifies one row of a tenant's §4.7.2 vector outbox by
// the artifact and version whose vector is not yet written.
type VectorPendingKey struct {
	ArtifactID string
	Version    string
}

// VectorOutbox is the §4.7.2 transactional-outbox capability a RegistryStore
// backend may implement so ingest against an external vector backend commits
// the manifest and a vector_pending row atomically, and a background worker
// reconciles the backend without ingest blocking on it. Collocated backends
// (pgvector, sqlite-vec) keep the inline fast path and do not need this. A
// caller probes for it with a type assertion on the Store.
type VectorOutbox interface {
	// PutManifestWithVectorPending commits the manifest and the vector_pending
	// row in a single transaction, so a crash never leaves a stored artifact
	// without a queued embedding. It enforces the same immutability invariant
	// as PutManifest; on an idempotent re-ingest (same content hash) the row is
	// upserted so a previously-drained artifact is re-queued only when its
	// content actually changed.
	PutManifestWithVectorPending(ctx context.Context, rec ManifestRecord, pending VectorPending) error
	// ListVectorPending returns up to limit rows eligible for a drain attempt
	// (NextRetryAt not after now), oldest first.
	ListVectorPending(ctx context.Context, limit int, now time.Time) ([]VectorPending, error)
	// ListVectorPendingKeys returns the key of every row of tenantID's
	// outbox, whatever its NextRetryAt, in no particular order. The §7.3.1
	// startup pass and sweep read it once per pass to skip the artifacts
	// whose vectors the outbox has not written yet.
	ListVectorPendingKeys(ctx context.Context, tenantID string) ([]VectorPendingKey, error)
	// MarkVectorPendingDone removes the row after a successful backend write.
	MarkVectorPendingDone(ctx context.Context, tenantID, artifactID, version string) error
	// MarkVectorPendingRetry records a failed attempt: it increments Attempts,
	// sets NextRetryAt, and stores the error.
	MarkVectorPendingRetry(ctx context.Context, tenantID, artifactID, version string, nextRetryAt time.Time, errMsg string) error
	// VectorOutboxStats returns the current outbox depth and the oldest
	// EnqueuedAt across pending rows (zero time when the outbox is empty), for
	// the §13.8 depth gauge and the vector.outbox_lagging threshold.
	VectorOutboxStats(ctx context.Context) (depth int, oldest time.Time, err error)
}

// classifyExistingManifest decides a put that found rec's (tenant, artifact,
// version) already stored and did not revive it. A different content hash is
// an immutability violation. So is a tombstoned row another layer contributed:
// §7.3.1 lets only the contributing layer restore a version the removed-artifact
// rule tombstoned, and the key stays held until the purge, so a put from
// another layer cannot take it. Reporting that put as idempotent would count
// an artifact as stored that no read path serves. Anything else is the same
// bytes put again, which is a no-op.
//
// Spec: §7.3.1 (Removed artifacts)
func classifyExistingManifest(rec ManifestRecord, hash, layer string, deleted bool) error {
	if hash != rec.ContentHash {
		return ErrImmutableViolation
	}
	if deleted && layer != rec.Layer {
		return ErrImmutableViolation
	}
	return nil
}
