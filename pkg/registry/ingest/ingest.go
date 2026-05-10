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
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/lennylabs/podium/internal/clock"
	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/manifest"
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
	Name    string
	Start   time.Time
	End     time.Time
	Blocks  []string // typically ["ingest"], may include "layer-config"
	BreakGlass bool   // when true, this ingest invocation has been
	                  // approved by dual-signoff break-glass.
}

// Active reports whether the window blocks ingest at now.
func (w FreezeWindow) Active(now time.Time, op string) bool {
	if w.BreakGlass {
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
			if req.AuditEmit != nil {
				req.AuditEmit("freeze.break_glass", req.LayerID, map[string]string{
					"window": w.Name,
					"caller": req.CallerID,
				})
			}
		}
	}

	now := req.Clock.Now().UTC()

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

		// §4.7.6 extends:-pin resolution. If the artifact extends a
		// parent reference, resolve the parent against existing
		// manifests and pin to an exact version. Parent updates do
		// not silently propagate; only re-ingesting the child does.
		if rec.Artifact.Extends != "" {
			pin, perr := resolveExtendsPin(ctx, st, req.TenantID, rec.Artifact.Extends, rec.ID)
			if perr != nil {
				res.Rejected = append(res.Rejected, RejectedArtifact{
					ArtifactID: rec.ID,
					Reason:     fmt.Sprintf("extends: %v", perr),
					Code:       "ingest.invalid_artifact",
				})
				continue
			}
			mr.ExtendsPin = pin
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
		// audit sink so SIEM pipelines see the publish.
		if req.AuditEmit != nil {
			req.AuditEmit("artifact.published", mr.ArtifactID, map[string]string{
				"version":      mr.Version,
				"content_hash": mr.ContentHash,
				"layer":        mr.Layer,
			})
			if mr.Deprecated {
				req.AuditEmit("artifact.deprecated", mr.ArtifactID, map[string]string{
					"version": mr.Version,
					"layer":   mr.Layer,
				})
			}
			if mr.Signature != "" {
				req.AuditEmit("artifact.signed", mr.ArtifactID, map[string]string{
					"version":      mr.Version,
					"content_hash": mr.ContentHash,
				})
			}
		}

		// Populate cross-type dependency edges for §4.7.3.
		for _, edge := range edgesFor(rec.Artifact, rec.ID) {
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
// embedding provider sees: id + description + tags + body prefix.
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

// composeEmbeddingText is the canonical embedding-input projection.
// Authors that want to influence retrieval write better descriptions
// and tags; bundle-resource bytes don't enter the embedding because
// they're opaque to the registry per §1.1.
func composeEmbeddingText(mr store.ManifestRecord) string {
	const bodyPrefixMax = 1024
	body := string(mr.Body)
	if len(body) > bodyPrefixMax {
		body = body[:bodyPrefixMax]
	}
	parts := []string{mr.ArtifactID, mr.Description}
	if len(mr.Tags) > 0 {
		parts = append(parts, strings.Join(mr.Tags, " "))
	}
	if body != "" {
		parts = append(parts, body)
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
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

func loadOne(fsys fs.FS, artifactPath, layerID string) (filesystem.ArtifactRecord, error) {
	dir := dirOf(artifactPath)
	id := dirToCanonical(dir)

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
	if rec.Artifact.Type == manifest.TypeSkill && rec.Skill != nil {
		body = rec.Skill.Body
	}
	return store.ManifestRecord{
		TenantID:    tenantID,
		ArtifactID:  rec.ID,
		Version:     rec.Artifact.Version,
		ContentHash: "sha256:" + hash,
		Type:        string(rec.Artifact.Type),
		Description: rec.Artifact.Description,
		Tags:        rec.Artifact.Tags,
		Sensitivity: string(rec.Artifact.Sensitivity),
		Layer:       layerID,
		Deprecated:  rec.Artifact.Deprecated,
		IngestedAt:  ingestedAt,
		Frontmatter: rec.ArtifactBytes,
		Body:        []byte(body),
	}, nil
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
func edgesFor(a *manifest.Artifact, id string) []store.DependencyEdge {
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
	for _, srv := range a.MCPServers {
		out = append(out, store.DependencyEdge{
			From: id, To: srv.Name, Kind: "mcpServers",
		})
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
// tenant and returns the pinned form "<id>@<exact-version>". childID
// is the artifact being ingested; we use it to detect a self-reference
// (the simplest cycle case).
func resolveExtendsPin(ctx context.Context, st store.Store, tenantID, ref, childID string) (string, error) {
	id, pinStr := splitRef(ref)
	if id == "" {
		return "", fmt.Errorf("invalid extends reference %q", ref)
	}
	if id == childID {
		return "", fmt.Errorf("self-extends cycle: %q", ref)
	}
	pin, err := version.ParsePin(pinStr)
	if err != nil {
		return "", fmt.Errorf("parse pin: %w", err)
	}

	all, err := st.ListManifests(ctx, tenantID)
	if err != nil {
		return "", err
	}
	versions := make([]string, 0, 4)
	hashByVersion := map[string]string{}
	for _, m := range all {
		if m.ArtifactID == id {
			versions = append(versions, m.Version)
			hashByVersion[m.Version] = m.ContentHash
		}
	}
	if len(versions) == 0 {
		return "", fmt.Errorf("no parent artifact %q ingested yet", id)
	}

	if pin.Kind == version.PinContentHash {
		for v, h := range hashByVersion {
			if h == "sha256:"+pin.Hash {
				return id + "@" + v, nil
			}
		}
		return "", fmt.Errorf("no parent version with content hash sha256:%s", pin.Hash)
	}
	resolved, err := version.Resolve(pin, versions)
	if err != nil {
		return "", fmt.Errorf("no parent version satisfies %q", ref)
	}
	return id + "@" + resolved, nil
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
