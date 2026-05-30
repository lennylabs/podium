// Package ingest implements the ingest pipeline described in spec
// §7.3.1: fetch a layer's snapshot, walk the diff, run lint as defense
// in depth, validate immutability, content-hash, store manifest and
// bundled resources, and emit an event.
//
// Ingest is the single entry point both the filesystem-source path
// and the (future) Git webhook path call into. Callers supply a layer
// source's fs.FS; ingest does the rest.
package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/lennylabs/podium/internal/clock"
	"github.com/lennylabs/podium/pkg/audit"
	domainpkg "github.com/lennylabs/podium/pkg/domain"
	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/version"
)

// Errors mapping to §6.10 namespaces.
var (
	// ErrLintFailed maps to ingest.lint_failed (§7.3.1).
	ErrLintFailed = errors.New("ingest.lint_failed")
	// ErrFrozen maps to ingest.frozen — a freeze window blocks ingest.
	ErrFrozen = errors.New("ingest.frozen")
	// ErrPublicModeSensitive maps to
	// ingest.public_mode_rejects_sensitive (§13.10).
	ErrPublicModeSensitive = errors.New("ingest.public_mode_rejects_sensitive")
	// ErrInvalidArtifact wraps parse / structural errors that surface at
	// ingest as ingest.lint_failed.
	ErrInvalidArtifact = errors.New("ingest.invalid_artifact")
	// ErrQuotaExceeded maps to quota.storage_exceeded (§4.7.8).
	ErrQuotaExceeded = errors.New("quota.storage_exceeded")
)

// FreezeWindow is one §4.7.2 freeze window: ingest is blocked when
// the current time falls within [Start, End) and the window's blocks
// list includes "ingest".
type FreezeWindow struct {
	Name   string
	Start  time.Time
	End    time.Time
	Blocks []string // typically ["ingest"], may include "layer-config"
	// BreakGlass marks this window as carrying an active
	// break-glass override. The override only applies when the
	// supporting fields (Approvers, Justification, GrantedAt)
	// satisfy the §4.7.2 rule — see ValidateBreakGlass.
	BreakGlass    bool
	Approvers     []string  // ≥ 2 unique IDs required for §4.7.2 dual-signoff
	Justification string    // non-empty string explaining the override
	GrantedAt     time.Time // approval timestamp; auto-expires after 24h
}

// breakGlassMaxAge is the §4.7.2 auto-expiry window for a
// break-glass grant. Grants older than this are refused.
const breakGlassMaxAge = 24 * time.Hour

// ValidateBreakGlass returns nil when the BreakGlass override
// satisfies the §4.7.2 rule (two distinct approvers, non-empty
// justification, ≤24h since GrantedAt). Returns a descriptive
// error otherwise. Callers fold the result into ErrFrozen so the
// freeze stays in effect when the grant is malformed.
func (w FreezeWindow) ValidateBreakGlass(now time.Time) error {
	if !w.BreakGlass {
		return nil
	}
	if w.Justification == "" {
		return fmt.Errorf("break-glass missing justification")
	}
	unique := map[string]bool{}
	for _, a := range w.Approvers {
		unique[a] = true
	}
	if len(unique) < 2 {
		return fmt.Errorf("break-glass requires two distinct approvers (got %d)", len(unique))
	}
	if !w.GrantedAt.IsZero() && now.Sub(w.GrantedAt) > breakGlassMaxAge {
		return fmt.Errorf("break-glass grant expired (>24h since approval)")
	}
	return nil
}

// Active reports whether the window blocks the named operation
// at the given time. A BreakGlass override that fails
// ValidateBreakGlass is treated as if the override were absent
// (the freeze stays in effect).
func (w FreezeWindow) Active(now time.Time, op string) bool {
	if w.BreakGlass && w.ValidateBreakGlass(now) == nil {
		return false
	}
	if now.Before(w.Start) || !now.Before(w.End) {
		return false
	}
	for _, b := range w.Blocks {
		if b == op {
			return true
		}
	}
	return false
}

// uniqueStrings returns its input with duplicates removed. Order
// preserved (first occurrence wins).
func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// wouldBlockWithoutBreakGlass returns true when the window's
// time range and Blocks list would block op at now even though
// BreakGlass is set. Used to fire the §8.1 freeze.break_glass
// audit event only on actual overrides (not on out-of-range
// windows that wouldn't have blocked anyway).
func wouldBlockWithoutBreakGlass(w FreezeWindow, now time.Time, op string) bool {
	if now.Before(w.Start) || !now.Before(w.End) {
		return false
	}
	for _, b := range w.Blocks {
		if b == op {
			return true
		}
	}
	return false
}

// Request is a single layer ingest invocation.
type Request struct {
	// TenantID identifies the org owning the layer. Required.
	TenantID string
	// LayerID identifies the layer being ingested.
	LayerID string
	// Sensitivity floor: artifacts with sensitivity below the floor are
	// rejected at ingest. Used by §13.10 public mode to refuse medium
	// and high sensitivity. Empty means no floor.
	RejectAtOrAbove manifest.Sensitivity
	// FreezeWindows blocks ingest when any window is currently active
	// (§4.7.2). An ingest with BreakGlass=true bypasses the windows.
	FreezeWindows []FreezeWindow
	// StorageQuotaBytes is the per-tenant storage budget. Zero disables.
	// Ingest sums frontmatter + body + bundled-resource bytes against
	// the quota and returns ErrQuotaExceeded when exceeded (§4.7.8).
	StorageQuotaBytes int64
	// CurrentStorageBytes is the bytes already stored against the
	// quota. Caller queries the store for this; ingest cannot
	// (it would have to walk every manifest).
	CurrentStorageBytes int64
	// ArtifactCountQuota is the per-tenant maximum number of
	// distinct artifact IDs (§4.7.8). Zero disables.
	ArtifactCountQuota int
	// CurrentArtifactCount is the current number of distinct
	// artifact IDs already stored against the quota. The caller
	// queries the store for this.
	CurrentArtifactCount int
	// Files is the source's snapshot exposed as fs.FS. The Local
	// LayerSourceProvider produces this from os.DirFS; the Git
	// provider exposes the checked-out tree the same way.
	Files fs.FS
	// Linter applies §4.3 lint rules. Lint errors are reported in
	// Result.LintFailures and abort the per-artifact ingest.
	Linter *lint.Linter
	// Clock provides ingest timestamps; defaults to clock.Real.
	Clock clock.Clock
	// Embedder generates the §4.7 vector embedding for each newly
	// accepted artifact. Optional: when nil, ingest writes manifests
	// without embeddings and search degrades to BM25-only for those
	// artifacts. An EmbeddingFailure entry records artifacts whose
	// embedding call failed transiently; `podium admin reembed`
	// retries.
	Embedder EmbedderFunc
	// VectorPut persists the embedding atomically per row. Required
	// when Embedder is set; nil otherwise. Atomic-per-row upsert
	// means search continues to return the prior vector until the
	// new one lands.
	VectorPut VectorPutFunc
	// DomainVectorPut persists a DOMAIN.md projection embedding (§4.7
	// "Domain embeddings") into the domain index that search_domains
	// queries. Set alongside Embedder to embed domains at ingest; nil
	// leaves search_domains BM25-only. The wiring closure supplies the
	// reserved domain version sentinel, so ingest stays unaware of it.
	DomainVectorPut DomainVectorPutFunc
	// PublishEvent fires §7.6 change events as artifacts ingest.
	// Optional: when nil, ingest stays silent. Per-artifact:
	//   - artifact.published with {id, version, content_hash, layer, tenant}
	//   - artifact.deprecated when an ingested manifest sets deprecated:true
	// The orchestrator wraps Server.PublishEvent for the production
	// path; tests use a fake.
	PublishEvent EventEmitter
	// Signer signs every newly accepted manifest's content hash and
	// stores the resulting envelope on the ManifestRecord. Optional:
	// when nil, ingest stores no signature and downstream
	// materialize-time verification (PODIUM_VERIFY_SIGNATURES) sees
	// an empty envelope. Production deployments wire a real signer
	// (sign.SigstoreKeyless / sign.RegistryManagedKey).
	Signer SignerFunc
	// AuditEmit, when non-nil, receives §8.1 audit events the
	// ingest pipeline produces. Distinct from PublishEvent: the
	// latter is the §7.6 SSE / outbound webhook stream for
	// downstream tooling; AuditEmit feeds the operator-side §8
	// audit log.
	AuditEmit AuditEmitterFunc
	// ResourcePut uploads each bundled resource to the §7.2 data-plane
	// object store keyed by its content hash (§4.4 deduplicates identical
	// bytes across versions). Optional: when nil, ingest keeps every
	// resource's bytes inline on the manifest record (the no-object-store
	// deployment, where resources stay inline regardless of size). When
	// set, resources above the §4.2 inline cutoff upload to the store and
	// drop their inline copy so the control plane never carries large
	// bytes.
	ResourcePut ResourcePutFunc
	// CallerID identifies the operator triggering ingest. Embedded
	// into the audit event's Caller field. Optional.
	CallerID string
}

// AuditEmitterFunc is the audit-emission seam ingest uses to
// surface ingest events into the §8 audit log.
type AuditEmitterFunc func(eventType, target string, ctxFields map[string]string)

// SignerFunc signs the content hash of a freshly-ingested manifest
// and returns an opaque envelope. Wraps sign.Provider.Sign.
type SignerFunc func(contentHash string) (string, error)

// tenantHasArtifact reports whether the tenant already has a
// manifest for artifactID — we don't double-count distinct
// artifacts when ingesting a new version of an existing one.
func (r *Request) tenantHasArtifact(ctx context.Context, st store.Store, artifactID string) bool {
	all, err := st.ListManifests(ctx, r.TenantID)
	if err != nil {
		return false
	}
	for _, m := range all {
		if m.ArtifactID == artifactID {
			return true
		}
	}
	return false
}

// EventEmitter is the §7.6 publish surface. The function shape
// matches Server.PublishEvent so the orchestrator passes the
// server's method directly.
type EventEmitter func(eventType string, data map[string]any)

// EmbedderFunc converts the embedding text projection of a manifest
// into a vector. Implementations wrap pkg/embedding.Provider.Embed
// for a single text.
type EmbedderFunc func(ctx context.Context, text string) ([]float32, error)

// VectorPutFunc persists the embedding for one (tenant, id,
// version) tuple. Atomic per row.
type VectorPutFunc func(ctx context.Context, tenantID, artifactID, version string, vec []float32) error

// DomainVectorPutFunc persists the embedding for one domain projection,
// keyed by (tenant, domain path). The §4.7 domain index reuses the same
// vector backend as artifacts under a reserved version sentinel; the
// wiring closure supplies that sentinel so ingest need not know it.
type DomainVectorPutFunc func(ctx context.Context, tenantID, domainPath string, vec []float32) error

// ResourcePutFunc uploads one bundled resource's bytes to the §7.2
// data-plane object store, keyed by its content hash. Wraps
// objectstore.Provider.Put; the wiring closure supplies the configured
// store so ingest stays unaware of the backend.
type ResourcePutFunc func(ctx context.Context, key string, body []byte, contentType string) error

// Result reports what happened.
type Result struct {
	// Accepted is the number of (artifact_id, version) pairs newly stored.
	Accepted int
	// Idempotent is the number of pairs that matched an existing
	// (id, version, content_hash) triple and were no-ops.
	Idempotent int
	// LintFailures collects diagnostics for artifacts rejected by lint.
	LintFailures []lint.Diagnostic
	// Conflicts reports (id, version) pairs that already exist with a
	// different content hash. Each entry is a distinct §4.7 immutability
	// violation.
	Conflicts []ConflictReport
	// Rejected reports artifacts rejected for other reasons (sensitivity
	// floor, parse failure, missing SKILL.md for type: skill, etc.).
	Rejected []RejectedArtifact
	// EmbeddingFailures records artifacts that ingested successfully
	// but whose embedding call failed (provider unreachable, vector
	// store down). The manifest is searchable via BM25 only until
	// `podium admin reembed` retries.
	EmbeddingFailures []EmbeddingFailure
	// Advisories collects the §3.3 / §12 description-quality flags the
	// registry raises at ingest time: thin descriptions and clusters of
	// artifacts whose summaries collide. They are advisory (warning
	// severity) and never block ingest; they surface to domain owners so
	// authored descriptions can be improved.
	Advisories []lint.Diagnostic
}

// EmbeddingFailure names an artifact whose post-ingest embedding
// step failed. Search degrades to BM25 for that artifact.
type EmbeddingFailure struct {
	ArtifactID string
	Version    string
	Reason     string
}

// ConflictReport names a (id, version) that already has different bytes.
type ConflictReport struct {
	ArtifactID string
	Version    string
	OldHash    string
	NewHash    string
}

// RejectedArtifact names an artifact rejected at ingest with a reason.
type RejectedArtifact struct {
	ArtifactID string
	Reason     string
	Code       string // §6.10 namespaced code
}

// Ingest runs the pipeline against st and returns a Result.
//
// Ingest is idempotent: running it twice with the same inputs has the
// same effect as running it once.
func Ingest(ctx context.Context, st store.Store, req Request) (*Result, error) {
	if req.TenantID == "" {
		return nil, errors.New("ingest: TenantID is required")
	}
	if req.Files == nil {
		return nil, fmt.Errorf("ingest: Files is required (no snapshot)")
	}
	if req.Clock == nil {
		req.Clock = clock.Real{}
	}
	if req.Linter == nil {
		req.Linter = &lint.Linter{}
	}

	// §4.7.2 freeze-window enforcement: refuse ingest when any active
	// window blocks it. Break-glass windows that would otherwise
	// block emit a §8.1 freeze.break_glass audit event so the
	// override is recorded.
	for _, w := range req.FreezeWindows {
		if w.Active(req.Clock.Now(), "ingest") {
			return nil, fmt.Errorf("%w: window %q active", ErrFrozen, w.Name)
		}
		if w.BreakGlass && wouldBlockWithoutBreakGlass(w, req.Clock.Now(), "ingest") {
			// Only fire when the §4.7.2 grant is valid; an
			// invalid grant left the window Active above and
			// already returned ErrFrozen.
			if w.ValidateBreakGlass(req.Clock.Now()) != nil {
				continue
			}
			if req.AuditEmit != nil {
				req.AuditEmit("freeze.break_glass", req.LayerID, map[string]string{
					"window":        w.Name,
					"caller":        req.CallerID,
					"approvers":     strings.Join(uniqueStrings(w.Approvers), ","),
					"justification": w.Justification,
				})
			}
		}
	}

	now := req.Clock.Now().UTC()

	// §4.5.1 — persist every DOMAIN.md so load_domain can read domain
	// composition (description, keywords, unlisted, include/exclude,
	// per-domain discovery overrides) and merge candidates across
	// layers (§4.5.4). DOMAIN.md is not an artifact and is not subject
	// to artifact lint; a malformed one is skipped (manifest-parse
	// lint rules cover it) so it never blocks artifact ingest.
	// §8.1: a DOMAIN.md that is newly added or whose source changed since
	// the previous ingest emits domain.published. Compare against the
	// stored record so an unchanged re-ingest stays quiet.
	prevDomains := map[string]string{}
	if existing, lerr := st.ListDomains(ctx, req.TenantID); lerr == nil {
		for _, d := range existing {
			if d.Layer == req.LayerID {
				prevDomains[d.Path] = string(d.Raw)
			}
		}
	}
	domainRecs := walkDomains(req.Files, req.TenantID, req.LayerID)
	for _, dr := range domainRecs {
		prev, seen := prevDomains[dr.Path]
		if err := st.PutDomain(ctx, dr); err != nil {
			return nil, err
		}
		if (!seen || prev != string(dr.Raw)) && req.AuditEmit != nil {
			req.AuditEmit(string(audit.EventDomainPublished), dr.Path,
				map[string]string{"layer": dr.Layer})
		}
	}

	// Walk the layer's filesystem to find every ARTIFACT.md.
	records, err := walkLayer(req.Files, req.LayerID)
	if err != nil {
		return nil, err
	}

	// Run lint over the collected set; diagnostics with severity Error
	// abort their artifact's ingest.
	diags := req.Linter.Lint(nil, records)

	res := &Result{}
	errsByID := groupLintErrors(diags)

	// §3.3 / §12 — the registry flags thin descriptions and clusters of
	// artifacts whose summaries collide. These checks are advisory and do
	// not gate ingest, so they run independently of req.Linter (which the
	// author-facing `podium lint` shares) over the ingested record set;
	// the colliding-summary check needs that set to spot a cluster.
	res.Advisories = (&lint.Linter{Rules: lint.DescriptionAdvisoryRules()}).Lint(nil, records)

	// §4.7 "Domain embeddings": embed each DOMAIN.md projection
	// (description + keywords + truncated body) into the domain index so
	// search_domains has a semantic ranker. A failed embed does not block
	// ingest; the domain stays BM25-searchable and `podium admin reembed`
	// can retry. Runs after res is initialized so failures are reported.
	if req.Embedder != nil && req.DomainVectorPut != nil {
		for _, dr := range domainRecs {
			if err := embedDomain(ctx, req, dr); err != nil {
				res.EmbeddingFailures = append(res.EmbeddingFailures, EmbeddingFailure{
					ArtifactID: dr.Path,
					Reason:     err.Error(),
				})
			}
		}
	}

	// spec: §4.6 — two layers contributing the same canonical ID is a
	// forbidden silent shadow unless the higher-precedence artifact
	// declares extends: <lower-precedence-id>. The server-store path keys
	// records by (tenant, id, version) with the layer excluded, so without
	// this check a same-ID record from another layer is stored and
	// silently shadows at read time. Index the manifests already
	// contributed by other layers so the per-record check can spot it.
	crossLayerByID := map[string][]store.ManifestRecord{}
	if existing, lerr := st.ListManifests(ctx, req.TenantID); lerr == nil {
		for _, m := range existing {
			if m.Layer != req.LayerID {
				crossLayerByID[m.ArtifactID] = append(crossLayerByID[m.ArtifactID], m)
			}
		}
	}

	// spec: §4.7.3 — mcpServers edges resolve to the mcp-server
	// artifact that declares the matching server_identifier. Index the
	// server_identifier of every mcp-server in this ingest set once so
	// edgesFor can resolve consumer references against siblings.
	serverIDs := serverIdentifierIndex(records)

	for _, rec := range records {
		// Lint blocks ingest at error severity.
		if errs, ok := errsByID[rec.ID]; ok {
			res.LintFailures = append(res.LintFailures, errs...)
			continue
		}

		// Sensitivity floor (public mode) per §13.10.
		if rejectsSensitivity(req.RejectAtOrAbove, rec.Artifact.Sensitivity) {
			res.Rejected = append(res.Rejected, RejectedArtifact{
				ArtifactID: rec.ID,
				Reason: fmt.Sprintf("sensitivity %q rejected at floor %q",
					rec.Artifact.Sensitivity, req.RejectAtOrAbove),
				Code: "ingest.public_mode_rejects_sensitive",
			})
			continue
		}

		mr, err := manifestRecordFor(rec, req.TenantID, req.LayerID, now)
		if err != nil {
			res.Rejected = append(res.Rejected, RejectedArtifact{
				ArtifactID: rec.ID,
				Reason:     err.Error(),
				Code:       "ingest.invalid_artifact",
			})
			continue
		}

		// §4.7.8 quota enforcement: refuse the artifact if accepting
		// it would push current storage past the configured budget.
		if req.StorageQuotaBytes > 0 {
			projected := req.CurrentStorageBytes + recordBytes(mr)
			if projected > req.StorageQuotaBytes {
				res.Rejected = append(res.Rejected, RejectedArtifact{
					ArtifactID: rec.ID,
					Reason: fmt.Sprintf("would push storage to %d bytes; quota is %d",
						projected, req.StorageQuotaBytes),
					Code: "quota.storage_exceeded",
				})
				continue
			}
			req.CurrentStorageBytes = projected
		}

		// §4.7.8 artifact-count quota: refuse the artifact when it
		// would be the (Quota+1)-th distinct artifact id. Versions
		// of an existing id don't count against the cap.
		if req.ArtifactCountQuota > 0 && !req.tenantHasArtifact(ctx, st, mr.ArtifactID) {
			projected := req.CurrentArtifactCount + 1
			if projected > req.ArtifactCountQuota {
				res.Rejected = append(res.Rejected, RejectedArtifact{
					ArtifactID: rec.ID,
					Reason: fmt.Sprintf(
						"artifact count %d would exceed quota %d",
						projected, req.ArtifactCountQuota),
					Code: "quota.artifact_count_exceeded",
				})
				continue
			}
			req.CurrentArtifactCount = projected
		}

		// §4.7.6 extends:-pin resolution. If the artifact extends a
		// parent reference, resolve the parent against existing
		// manifests and pin to an exact version. Parent updates do
		// not silently propagate; only re-ingesting the child does.
		if rec.Artifact.Extends != "" {
			pin, parentType, perr := resolveExtendsPin(ctx, st, req.TenantID, rec.Artifact.Extends, rec.ID, rec.Artifact.Version)
			if perr != nil {
				res.Rejected = append(res.Rejected, RejectedArtifact{
					ArtifactID: rec.ID,
					Reason:     fmt.Sprintf("extends: %v", perr),
					Code:       "ingest.invalid_artifact",
				})
				continue
			}
			// spec: §4.6 — "The child's type: must match the parent's;
			// ingest rejects an extends: chain that crosses types."
			if parentType != string(rec.Artifact.Type) {
				res.Rejected = append(res.Rejected, RejectedArtifact{
					ArtifactID: rec.ID,
					Reason: fmt.Sprintf("extends: child type %q does not match parent %s type %q",
						rec.Artifact.Type, stripPin(rec.Artifact.Extends), parentType),
					Code: "ingest.invalid_artifact",
				})
				continue
			}
			mr.ExtendsPin = pin
		}

		// spec: §4.6 — reject a cross-layer same-ID collision unless an
		// extends: overlay links the two records. The overlay is sanctioned
		// when the incoming record extends the colliding ID, or when an
		// existing cross-layer record does (the existing record is the
		// overlay). Anything else is a silent shadow, which the spec forbids.
		if crossLayer := crossLayerByID[mr.ArtifactID]; len(crossLayer) > 0 {
			overlay := stripPin(rec.Artifact.Extends) == mr.ArtifactID
			if !overlay {
				for _, ex := range crossLayer {
					if stripPin(ex.ExtendsPin) == mr.ArtifactID {
						overlay = true
						break
					}
				}
			}
			if !overlay {
				res.Rejected = append(res.Rejected, RejectedArtifact{
					ArtifactID: mr.ArtifactID,
					Reason: fmt.Sprintf("cross-layer collision: %q already contributed by layer %q; declare extends: %s to overlay it",
						mr.ArtifactID, crossLayer[0].Layer, mr.ArtifactID),
					Code: "ingest.collision",
				})
				continue
			}
		}

		// Check current state to distinguish accepted vs idempotent vs
		// immutable-violation.
		existing, err := st.GetManifest(ctx, req.TenantID, mr.ArtifactID, mr.Version)
		switch {
		case err == nil:
			if existing.ContentHash == mr.ContentHash {
				res.Idempotent++
				continue
			}
			res.Conflicts = append(res.Conflicts, ConflictReport{
				ArtifactID: mr.ArtifactID,
				Version:    mr.Version,
				OldHash:    existing.ContentHash,
				NewHash:    mr.ContentHash,
			})
			continue
		case errors.Is(err, store.ErrNotFound):
			// Insert below.
		default:
			return nil, err
		}

		// §4.7.9 signing: when a Signer is configured, attach the
		// signature envelope before PutManifest commits so callers
		// see signed bytes from the moment ingest accepts them. A
		// signing failure rejects the artifact — unsigned bytes
		// must not sneak in when a signer is configured.
		if req.Signer != nil {
			env, err := req.Signer(mr.ContentHash)
			if err != nil {
				res.Rejected = append(res.Rejected, RejectedArtifact{
					ArtifactID: mr.ArtifactID,
					Reason:     fmt.Sprintf("sign: %v", err),
					Code:       "ingest.sign_failed",
				})
				continue
			}
			mr.Signature = env
		}

		// §7.2 data plane: persist bundled resources before the manifest
		// commits so a served artifact's resources are retrievable the
		// moment ingest accepts it. Large resources upload to object
		// storage and drop their inline bytes; small ones stay inline.
		if err := persistResources(ctx, req.ResourcePut, mr.Resources); err != nil {
			res.Rejected = append(res.Rejected, RejectedArtifact{
				ArtifactID: mr.ArtifactID,
				Reason:     fmt.Sprintf("persist resources: %v", err),
				Code:       "ingest.resource_store_failed",
			})
			continue
		}

		if err := st.PutManifest(ctx, mr); err != nil {
			if errors.Is(err, store.ErrImmutableViolation) {
				res.Conflicts = append(res.Conflicts, ConflictReport{
					ArtifactID: mr.ArtifactID,
					Version:    mr.Version,
					NewHash:    mr.ContentHash,
				})
				continue
			}
			return nil, err
		}
		res.Accepted++

		// §7.6 change events. Fire after the manifest commits so
		// subscribers never see a published event for an ingest that
		// rolled back. artifact.published carries the canonical
		// metadata consumers need to look the artifact up.
		if req.PublishEvent != nil {
			req.PublishEvent("artifact.published", map[string]any{
				"id":           mr.ArtifactID,
				"version":      mr.Version,
				"content_hash": mr.ContentHash,
				"layer":        mr.Layer,
				"tenant":       mr.TenantID,
			})
			if mr.Deprecated {
				req.PublishEvent("artifact.deprecated", map[string]any{
					"id":      mr.ArtifactID,
					"version": mr.Version,
					"layer":   mr.Layer,
				})
			}
		}

		// §8.1 audit log: same events fan into the operator-side
		// audit sink so SIEM pipelines see the publish. §8.2
		// manifest-declared redaction: when the manifest names
		// fields in audit_redact, the registry replaces those
		// values with [redacted] before emitting.
		if req.AuditEmit != nil {
			req.AuditEmit("artifact.published", mr.ArtifactID, audit.RedactFields(map[string]string{
				"version":      mr.Version,
				"content_hash": mr.ContentHash,
				"layer":        mr.Layer,
			}, mr.AuditRedact))
			if mr.Deprecated {
				req.AuditEmit("artifact.deprecated", mr.ArtifactID, audit.RedactFields(map[string]string{
					"version": mr.Version,
					"layer":   mr.Layer,
				}, mr.AuditRedact))
			}
			if mr.Signature != "" {
				req.AuditEmit("artifact.signed", mr.ArtifactID, audit.RedactFields(map[string]string{
					"version":      mr.Version,
					"content_hash": mr.ContentHash,
				}, mr.AuditRedact))
			}
		}

		// Populate cross-type dependency edges for §4.7.3.
		for _, edge := range edgesFor(rec.Artifact, rec.ID, serverIDs) {
			if err := st.PutDependency(ctx, req.TenantID, edge); err != nil {
				return nil, err
			}
		}

		// §4.7 embedding generation. Atomic per row in the vector
		// store: search returns the prior embedding until this
		// upsert lands. A failed embedding does not reject the
		// artifact; it ingests BM25-only and `podium admin reembed`
		// can retry.
		if req.Embedder != nil && req.VectorPut != nil {
			if err := embedAndStore(ctx, req, mr); err != nil {
				res.EmbeddingFailures = append(res.EmbeddingFailures, EmbeddingFailure{
					ArtifactID: mr.ArtifactID,
					Version:    mr.Version,
					Reason:     err.Error(),
				})
			}
		}
	}

	if len(res.LintFailures) > 0 && res.Accepted == 0 && res.Idempotent == 0 && len(res.Conflicts) == 0 {
		return res, fmt.Errorf("%w: %d diagnostics", ErrLintFailed, len(res.LintFailures))
	}
	return res, nil
}

// embedAndStore composes the §4.7 embedding text projection from the
// manifest, calls the configured embedder, and upserts the vector.
// The composition is the canonical input format every Podium
// embedding provider sees: name + description + when_to_use + tags.
func embedAndStore(ctx context.Context, req Request, mr store.ManifestRecord) error {
	text := composeEmbeddingText(mr)
	if text == "" {
		return nil
	}
	vec, err := req.Embedder(ctx, text)
	if err != nil {
		return fmt.Errorf("embed: %w", err)
	}
	if err := req.VectorPut(ctx, mr.TenantID, mr.ArtifactID, mr.Version, vec); err != nil {
		return fmt.Errorf("vector put: %w", err)
	}
	return nil
}

// embedDomain composes the §4.7 domain projection from a DOMAIN.md
// record and upserts its embedding into the domain index. Malformed
// frontmatter is skipped (the linter reports it at ingest); a domain with
// no projectable text (e.g. an include-only DOMAIN.md) is a no-op.
func embedDomain(ctx context.Context, req Request, dr store.DomainRecord) error {
	d, err := manifest.ParseDomain(dr.Raw)
	if err != nil {
		return nil
	}
	text := domainpkg.EmbeddingProjection(d)
	if text == "" {
		return nil
	}
	vec, err := req.Embedder(ctx, text)
	if err != nil {
		return fmt.Errorf("embed: %w", err)
	}
	if err := req.DomainVectorPut(ctx, dr.TenantID, dr.Path, vec); err != nil {
		return fmt.Errorf("vector put: %w", err)
	}
	return nil
}

// composeEmbeddingText is the canonical §4.7 embedding-input
// projection, built from frontmatter only: name, description,
// when_to_use (joined with newlines), and tags (joined). The prose
// body is deliberately excluded ("The prose body is not embedded");
// it is noisy for retrieval and risks busting embedding-model context
// limits. Authors influence recall via description and when_to_use.
// spec: §4.7 "Artifact embeddings".
func composeEmbeddingText(mr store.ManifestRecord) string {
	parts := []string{mr.Name, mr.Description}
	if len(mr.WhenToUse) > 0 {
		parts = append(parts, strings.Join(mr.WhenToUse, "\n"))
	}
	if len(mr.Tags) > 0 {
		parts = append(parts, strings.Join(mr.Tags, " "))
	}
	return joinNonEmpty(parts, "\n")
}

// joinNonEmpty joins the non-empty parts with sep so an absent name,
// description, or when_to_use list does not leave a blank line in the
// embedding projection.
func joinNonEmpty(parts []string, sep string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

func walkLayer(fsys fs.FS, layerID string) ([]filesystem.ArtifactRecord, error) {
	var out []filesystem.ArtifactRecord
	walkErr := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && p != "." {
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() != "ARTIFACT.md" {
			return nil
		}
		rec, err := loadOne(fsys, p, layerID)
		if err != nil {
			return err
		}
		out = append(out, rec)
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// walkDomains finds every DOMAIN.md in the snapshot and returns one
// store.DomainRecord per file, keyed by the canonical domain path
// (directory relative to the layer root). A root-level DOMAIN.md is
// skipped: §4.5.5 gives the registry root no DOMAIN.md. Parse failures
// are not surfaced here; the record stores the raw bytes and the
// registry re-parses (and lint reports malformed frontmatter).
func walkDomains(fsys fs.FS, tenantID, layerID string) []store.DomainRecord {
	var out []store.DomainRecord
	_ = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && p != "." {
				return fs.SkipDir
			}
			return nil
		}
		if d.Name() != "DOMAIN.md" {
			return nil
		}
		path := dirToCanonical(dirOf(p))
		if path == "" {
			return nil // root has no DOMAIN.md (§4.5.5)
		}
		data, rerr := fs.ReadFile(fsys, p)
		if rerr != nil {
			return nil
		}
		out = append(out, store.DomainRecord{
			TenantID: tenantID,
			Layer:    layerID,
			Path:     path,
			Raw:      data,
		})
		return nil
	})
	return out
}

func loadOne(fsys fs.FS, artifactPath, layerID string) (filesystem.ArtifactRecord, error) {
	dir := dirOf(artifactPath)
	id := dirToCanonical(dir)
	// spec: §4.2 — enforce the canonical-ID invariants the filesystem-source
	// registry already enforces, so both walk paths reject a root-level
	// ARTIFACT.md (empty ID) and any segment containing "@" identically.
	if err := filesystem.ValidateCanonicalID(id); err != nil {
		return filesystem.ArtifactRecord{}, fmt.Errorf("%s: %w", artifactPath, err)
	}

	bytes, err := fs.ReadFile(fsys, artifactPath)
	if err != nil {
		return filesystem.ArtifactRecord{}, err
	}
	a, err := manifest.ParseArtifact(bytes)
	if err != nil {
		return filesystem.ArtifactRecord{}, fmt.Errorf("%s: %w", id, err)
	}
	rec := filesystem.ArtifactRecord{
		ID:            id,
		Layer:         filesystem.Layer{ID: layerID},
		Artifact:      a,
		ArtifactBytes: bytes,
		Resources:     map[string][]byte{},
	}
	if a.Type == manifest.TypeSkill {
		skillPath := joinPath(dir, "SKILL.md")
		skillBytes, err := fs.ReadFile(fsys, skillPath)
		if err != nil {
			return filesystem.ArtifactRecord{}, fmt.Errorf("%s: type: skill missing SKILL.md", id)
		}
		s, err := manifest.ParseSkill(skillBytes)
		if err != nil {
			return filesystem.ArtifactRecord{}, fmt.Errorf("%s/SKILL.md: %w", id, err)
		}
		rec.Skill = s
		rec.SkillBytes = skillBytes
	}
	// Walk the artifact's directory for bundled resources.
	walkErr := fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// spec: §4.2/§4.4 — stop at a nested artifact-package boundary so
			// a child artifact's files are not captured as the parent's
			// bundled resources.
			if p != dir && fsHasArtifactManifest(fsys, p) {
				return fs.SkipDir
			}
			return nil
		}
		base := d.Name()
		if base == "ARTIFACT.md" || (a.Type == manifest.TypeSkill && base == "SKILL.md") {
			return nil
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(p, dir+"/")
		rec.Resources[rel] = data
		return nil
	})
	if walkErr != nil {
		return filesystem.ArtifactRecord{}, walkErr
	}
	return rec, nil
}

// manifestRecordFor canonicalizes one ArtifactRecord into a ManifestRecord
// suitable for the store, computing the content hash per §4.7.6.
func manifestRecordFor(rec filesystem.ArtifactRecord, tenantID, layerID string, ingestedAt time.Time) (store.ManifestRecord, error) {
	hash := contentHashOf(rec)
	body := rec.Artifact.Body
	name := rec.Artifact.Name
	description := rec.Artifact.Description
	if rec.Artifact.Type == manifest.TypeSkill && rec.Skill != nil {
		body = rec.Skill.Body
		// spec: §4.3.4 — a skill's name and description live in SKILL.md;
		// ARTIFACT.md omits them ("Podium reads from SKILL.md"). Index the
		// SKILL.md name and description so the skill is searchable without
		// duplicating the fields into ARTIFACT.md. The §4.7 embedding
		// projection leads with `name`, so it must be populated here.
		if rec.Skill.Name != "" {
			name = rec.Skill.Name
		}
		if rec.Skill.Description != "" {
			description = rec.Skill.Description
		}
	}
	return store.ManifestRecord{
		TenantID:         tenantID,
		ArtifactID:       rec.ID,
		Version:          rec.Artifact.Version,
		ContentHash:      "sha256:" + hash,
		Type:             string(rec.Artifact.Type),
		Name:             name,
		Description:      description,
		WhenToUse:        append([]string(nil), rec.Artifact.WhenToUse...),
		Tags:             rec.Artifact.Tags,
		Sensitivity:      string(rec.Artifact.Sensitivity),
		SearchVisibility: string(rec.Artifact.SearchVisibility),
		Layer:            layerID,
		Deprecated:       rec.Artifact.Deprecated,
		ReplacedBy:       rec.Artifact.ReplacedBy,
		AuditRedact:      append([]string(nil), rec.Artifact.AuditRedact...),
		IngestedAt:       ingestedAt,
		Frontmatter:      rec.ArtifactBytes,
		Body:             []byte(body),
		Resources:        resourceRefsFor(rec),
	}, nil
}

// resourceRefsFor builds the §4.4 bundled-resource refs for a record in
// sorted-path order. Each ref carries the per-resource content hash,
// size, guessed content type, and (initially) the bytes inline. The
// ingest loop later uploads the bytes to the object store and drops the
// inline copy for resources above the §4.2 cutoff (see persistResources).
func resourceRefsFor(rec filesystem.ArtifactRecord) []store.ResourceRef {
	if len(rec.Resources) == 0 {
		return nil
	}
	paths := make([]string, 0, len(rec.Resources))
	for p := range rec.Resources {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	refs := make([]store.ResourceRef, 0, len(paths))
	for _, p := range paths {
		body := rec.Resources[p]
		h := sha256.Sum256(body)
		refs = append(refs, store.ResourceRef{
			Path:        p,
			ContentHash: "sha256:" + hex.EncodeToString(h[:]),
			Size:        int64(len(body)),
			ContentType: objectstore.GuessContentType(p),
			Inline:      body,
		})
	}
	return refs
}

// persistResources realizes the §7.2 data-plane split for one record's
// bundled resources before the manifest commits. With an object store
// configured (put non-nil), every resource uploads keyed by its content
// hash (§4.4 deduplicates identical bytes), and resources above the
// §4.2 inline cutoff drop their inline copy so the control plane never
// carries large bytes. Without an object store, every resource keeps its
// bytes inline regardless of size (the standalone-without-storage mode).
func persistResources(ctx context.Context, put ResourcePutFunc, refs []store.ResourceRef) error {
	if put == nil {
		return nil
	}
	for i := range refs {
		ref := &refs[i]
		key := strings.TrimPrefix(ref.ContentHash, "sha256:")
		if err := put(ctx, key, ref.Inline, ref.ContentType); err != nil {
			return fmt.Errorf("%s: %w", ref.Path, err)
		}
		if ref.Size > objectstore.InlineCutoff {
			ref.Inline = nil
		}
	}
	return nil
}

// contentHashOf computes the canonical content hash for an artifact:
// SHA-256 over the artifact bytes, the optional SKILL.md bytes, and
// every bundled resource in sorted-path order. Spec §4.7.6.
func contentHashOf(rec filesystem.ArtifactRecord) string {
	parts := [][]byte{rec.ArtifactBytes, rec.SkillBytes}
	keys := make([]string, 0, len(rec.Resources))
	for k := range rec.Resources {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, []byte(k))
		parts = append(parts, rec.Resources[k])
	}
	return version.ContentHash(parts...)
}

// edgesFor extracts cross-type dependency edges from the artifact
// frontmatter (§4.7.3): extends, delegates_to, mcpServers references.
// serverIDs maps a canonical server_identifier to the mcp-server-type
// artifact ID that declares it; an mcpServers entry produces an edge
// only when its derived server identifier resolves to such an artifact.
func edgesFor(a *manifest.Artifact, id string, serverIDs map[string]string) []store.DependencyEdge {
	var out []store.DependencyEdge
	if a.Extends != "" {
		out = append(out, store.DependencyEdge{
			From: id, To: stripPin(a.Extends), Kind: "extends",
		})
	}
	for _, target := range a.DelegatesTo {
		out = append(out, store.DependencyEdge{
			From: id, To: stripPin(target), Kind: "delegates_to",
		})
	}
	// spec: §4.7.3 — an mcpServers reference resolves to an
	// mcp-server-type artifact via server_identifier. The consumer-side
	// entry carries only name/transport/command/args, so derive the
	// canonical identifier from it and match it against the ingested
	// mcp-server artifacts. Emit no edge when nothing resolves: keying
	// on the local consumer-side name would point the index at a
	// non-existent artifact.
	for _, srv := range a.MCPServers {
		sid := serverIdentifierFor(srv)
		if sid == "" {
			continue
		}
		target, ok := serverIDs[sid]
		if !ok {
			continue
		}
		out = append(out, store.DependencyEdge{
			From: id, To: target, Kind: "mcpServers",
		})
	}
	return out
}

// serverIdentifierFor derives the canonical server_identifier (§4.3,
// §4.7.3) from a consumer-side mcpServers entry. The spec example
// `server_identifier: npx:@company/finance-warehouse-mcp` corresponds
// to `command: npx` with `args: ["-y", "@company/finance-warehouse-mcp"]`,
// so the identifier is `<command>:<first non-flag arg>`. A flag arg
// begins with "-". When the entry has no command, the identifier
// cannot be derived and the empty string is returned.
func serverIdentifierFor(srv manifest.MCPServerRef) string {
	if srv.Command == "" {
		return srv.Transport
	}
	for _, arg := range srv.Args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return srv.Command + ":" + arg
	}
	return srv.Command
}

// serverIdentifierIndex maps each mcp-server-type artifact's
// server_identifier to its canonical artifact ID, for §4.7.3
// mcpServers edge resolution. Records that are not mcp-server type or
// that lack a server_identifier are skipped.
func serverIdentifierIndex(records []filesystem.ArtifactRecord) map[string]string {
	out := map[string]string{}
	for _, rec := range records {
		if rec.Artifact == nil {
			continue
		}
		if rec.Artifact.Type != manifest.TypeMCPServer {
			continue
		}
		if rec.Artifact.ServerIdentifier == "" {
			continue
		}
		out[rec.Artifact.ServerIdentifier] = rec.ID
	}
	return out
}

// recordBytes is the size of a manifest record's persisted bytes.
// Used by the §4.7.8 quota check.
func recordBytes(rec store.ManifestRecord) int64 {
	return int64(len(rec.Frontmatter)) + int64(len(rec.Body))
}

// stripPin removes the @semver / @sha256 suffix from a reference so the
// dependency graph keys on the canonical artifact ID.
func stripPin(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		return ref[:i]
	}
	return ref
}

// resolveExtendsPin resolves a parent reference (e.g.,
// "finance/parent" or "finance/parent@1.x" or
// "finance/parent@sha256:<hex>") against existing manifests for the
// tenant and returns the pinned form "<id>@<exact-version>" together with
// the parent's type at the resolved version. childID is the artifact
// being ingested; we use it to detect a self-reference (the simplest
// cycle case). The returned type lets the caller enforce the §4.6 rule
// that a child's type must match its parent's.
func resolveExtendsPin(ctx context.Context, st store.Store, tenantID, ref, childID, childVersion string) (pin, parentType string, err error) {
	id, pinStr := splitRef(ref)
	if id == "" {
		return "", "", fmt.Errorf("invalid extends reference %q", ref)
	}
	p, err := version.ParsePin(pinStr)
	if err != nil {
		return "", "", fmt.Errorf("parse pin: %w", err)
	}

	all, err := st.ListManifests(ctx, tenantID)
	if err != nil {
		return "", "", err
	}
	versions := make([]string, 0, 4)
	hashByVersion := map[string]string{}
	typeByVersion := map[string]string{}
	for _, m := range all {
		if m.ArtifactID != id {
			continue
		}
		// A child may extend its own canonical ID to overlay a
		// lower-precedence layer's artifact (§4.6 same-ID extends
		// exception). Its own version is never a valid parent — that
		// would be a self-cycle — so exclude it from the candidate set.
		if id == childID && m.Version == childVersion {
			continue
		}
		versions = append(versions, m.Version)
		hashByVersion[m.Version] = m.ContentHash
		typeByVersion[m.Version] = m.Type
	}
	if len(versions) == 0 {
		if id == childID {
			return "", "", fmt.Errorf("self-extends cycle: %q", ref)
		}
		return "", "", fmt.Errorf("no parent artifact %q ingested yet", id)
	}

	var resolved string
	if p.Kind == version.PinContentHash {
		for v, h := range hashByVersion {
			if h == "sha256:"+p.Hash {
				resolved = v
				break
			}
		}
		if resolved == "" {
			return "", "", fmt.Errorf("no parent version with content hash sha256:%s", p.Hash)
		}
	} else {
		resolved, err = version.Resolve(p, versions)
		if err != nil {
			return "", "", fmt.Errorf("no parent version satisfies %q", ref)
		}
	}
	return id + "@" + resolved, typeByVersion[resolved], nil
}

func splitRef(ref string) (id, pin string) {
	if i := strings.Index(ref, "@"); i >= 0 {
		return ref[:i], ref[i+1:]
	}
	return ref, ""
}

func dirOf(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return "."
}

func joinPath(dir, name string) string {
	if dir == "" || dir == "." {
		return name
	}
	return dir + "/" + name
}

func dirToCanonical(dir string) string {
	if dir == "" || dir == "." {
		return ""
	}
	return dir
}

// fsHasArtifactManifest reports whether dir directly contains an ARTIFACT.md
// file, marking it as a nested artifact-package boundary (§4.2).
func fsHasArtifactManifest(fsys fs.FS, dir string) bool {
	info, err := fs.Stat(fsys, joinPath(dir, "ARTIFACT.md"))
	return err == nil && !info.IsDir()
}

func rejectsSensitivity(floor, actual manifest.Sensitivity) bool {
	if floor == "" {
		return false
	}
	rank := func(s manifest.Sensitivity) int {
		switch s {
		case manifest.SensitivityHigh:
			return 3
		case manifest.SensitivityMedium:
			return 2
		case manifest.SensitivityLow:
			return 1
		}
		return 0
	}
	return rank(actual) >= rank(floor)
}

// groupLintErrors keeps only error-severity diagnostics, grouped by
// artifact ID. Warning and info diagnostics do not block ingest.
func groupLintErrors(diags []lint.Diagnostic) map[string][]lint.Diagnostic {
	out := map[string][]lint.Diagnostic{}
	for _, d := range diags {
		if d.Severity != lint.SeverityError {
			continue
		}
		out[d.ArtifactID] = append(out[d.ArtifactID], d)
	}
	return out
}
