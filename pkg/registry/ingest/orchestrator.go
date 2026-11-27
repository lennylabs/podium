package ingest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"time"

	"github.com/lennylabs/podium/internal/clock"
	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// ErrHistoryRewritten maps to ingest.history_rewritten in §6.10.
// Returned when a layer with ForcePushPolicy="strict" detects that
// the new ref no longer reaches the prior ingested ref.
var ErrHistoryRewritten = errors.New("ingest.history_rewritten")

// HistoryEventKind is the audit-event type emitted when a layer
// detects a force-push and proceeds in tolerant mode (§7.3.1).
const HistoryEventKind = "layer.history_rewritten"

// HistoryEmitter records a layer.history_rewritten audit event. The
// shape mirrors AuditEmitter from the registry core; orchestrators
// that don't wire one in pass nil and the event is skipped.
type HistoryEmitter func(ctx context.Context, tenantID, layerID, priorRef, newRef string)

// SourceIngestOptions threads the §4.7 embedding pieces and the
// §7.6 change-event publisher through the orchestrator without
// forcing every call site to populate them. Embedder + VectorPut
// must be set together; PublishEvent is independent and turned on
// whenever the orchestrator runs against a server that exposes
// /v1/events.
type SourceIngestOptions struct {
	Linter *lint.Linter
	// Clock dates the freeze-window check before the claim and the ingest
	// Request. Nil uses the real clock.
	Clock           clock.Clock
	Emit            HistoryEmitter
	Embedder        EmbedderFunc
	VectorPut       VectorPutFunc
	DomainVectorPut DomainVectorPutFunc
	PublishEvent    EventEmitter
	// AuditEmit, when non-nil, receives §8.1 audit events the
	// orchestrator and ingest pipeline produce (artifact.published,
	// layer.ingested, layer.history_rewritten, freeze.break_glass).
	AuditEmit AuditEmitterFunc
	// CallerID identifies the operator triggering the ingest;
	// embedded into emitted audit events.
	CallerID string
	// ResourcePut uploads bundled resources to the §7.2 object store at
	// ingest. Nil keeps resource bytes inline on the manifest record.
	ResourcePut ResourcePutFunc
	// FreezeWindows are the §4.7.2 windows enforced for this ingest. An
	// active window blocking "ingest" rejects with ErrFrozen unless a
	// window carries a valid break-glass grant (§7.3.1 manual reingest).
	FreezeWindows []FreezeWindow
	// RejectAtOrAbove is the §13.10 public-mode sensitivity floor passed
	// through to the ingest Request. Empty means no floor.
	RejectAtOrAbove manifest.Sensitivity
	// EnforceSandboxProfile and EnforceableSandboxProfiles thread the §13.10
	// sandbox-profile ingest gate (PODIUM_ENFORCE_SANDBOX_PROFILE +
	// PODIUM_HOST_SANDBOXES) through to the ingest Request. See
	// ingest.Request for the semantics.
	EnforceSandboxProfile      bool
	EnforceableSandboxProfiles []string
	// Signer signs every newly accepted manifest's content hash (§4.7.9).
	// Nil leaves manifests unsigned, which the registry passes only when
	// signing is off (§13.10 signs by default; --sign none disables it).
	Signer SignerFunc
	// UseVectorOutbox routes embedding through the §4.7.2 transactional outbox
	// (set for an external vector backend). See ingest.Request.UseVectorOutbox.
	UseVectorOutbox bool
	// SemanticOverlap computes §7.3.1 overlap after the cycle's writes. See
	// ingest.Request.SemanticOverlap.
	SemanticOverlap SemanticOverlapFunc
	// CycleLocks serializes the cycles of one layer in this process. The
	// registry passes one set for every trigger; nil serializes nothing.
	CycleLocks *CycleLocks
	// Attestation configures the §4.7.11 ingest verification of every cycle.
	// Nil runs no attestation step, as a layer under the mode none does.
	Attestation *AttestationConfig
	// MarkReset, when non-nil, applies the §4.7.11 mark reset in the write
	// that claims the layer for this cycle.
	MarkReset *MarkReset
}

// MarkReset is the §4.7.11 mark reset of a manual reingest: it clears the
// attestation mark and sets the recorded attested commit to ToCommit, empty
// for none.
type MarkReset struct {
	ToCommit string
}

// SourceIngest snapshots the layer via the supplied provider, runs
// the ingest pipeline, updates the store's LastIngestedRef on
// success, and emits a layer.history_rewritten event when the source
// reports a rewritten history.
//
// In strict force-push mode (cfg.ForcePushPolicy == "strict"), a
// detected rewrite returns ErrHistoryRewritten and skips ingest.
// In tolerant mode (default / "tolerant"), ingest proceeds and the
// event is emitted.
func SourceIngest(
	ctx context.Context,
	st store.Store,
	provider source.Provider,
	cfg store.LayerConfig,
	linter *lint.Linter,
	emit HistoryEmitter,
) (*Result, error) {
	return SourceIngestWithOptions(ctx, st, provider, cfg, SourceIngestOptions{
		Linter: linter, Emit: emit,
	})
}

// SourceIngestWithOptions is the SPI variant that accepts the §4.7
// hybrid-search hooks. The plain SourceIngest preserves the
// pre-Phase-vector signature for back-compat.
func SourceIngestWithOptions(
	ctx context.Context,
	st store.Store,
	provider source.Provider,
	cfg store.LayerConfig,
	opts SourceIngestOptions,
) (*Result, error) {
	// Spec: §7.3.1 (Removed artifacts) — the cycle starts once the previous
	// cycle of the layer in this process has ended, and it claims the layer
	// after that, before its snapshot. The claim returns the layer as stored,
	// so a cycle that waited reads the configuration and the prior reference
	// an update or another cycle wrote meanwhile rather than the caller's copy.
	unlock, err := opts.CycleLocks.lock(ctx, cfg.TenantID, cfg.ID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	// Spec: §7.3.1 (Removed artifacts) — a refusal that needs no snapshot runs
	// before the claim, so a cycle a freeze window refuses supersedes no
	// cycle that the window does not block, such as a break-glass reingest.
	if opts.Clock == nil {
		opts.Clock = clock.Real{}
	}
	if err := freezeRefusal(opts.FreezeWindows, opts.Clock.Now()); err != nil {
		return nil, err
	}
	claim, previous, stored, err := claimForCycle(ctx, st, cfg, opts)
	if err != nil {
		return nil, err
	}
	// Spec: §7.3.1 (Removed artifacts) — only a cycle that failed before its
	// first store write hands the claim back. One that wrote, such as one
	// that removed artifacts and then failed lint, keeps it, because the
	// cycle it displaced would otherwise resume and undo those writes.
	// Spec: §4.7.11 (Mark reset) — the claim that applies a reset is a store
	// write, so such a cycle keeps its claim whatever the failure.
	wrote := opts.MarkReset != nil
	res, err := runClaimedCycle(ctx, st, provider, stored, opts, claim, func() { wrote = true })
	if err != nil && !wrote {
		ReleaseClaim(ctx, st, stored.TenantID, stored.ID, claim, previous)
	}
	return res, err
}

// freezeRefusal returns the ErrFrozen refusal Ingest would return when a
// window blocking ingest is active at now. Ingest checks again, and records
// any break-glass override, once the cycle runs.
func freezeRefusal(windows []FreezeWindow, now time.Time) error {
	for _, w := range windows {
		if w.Active(now, "ingest") {
			return fmt.Errorf("%w: window %q active", ErrFrozen, w.Name)
		}
	}
	return nil
}

// ReleaseClaim hands a failed cycle's claim back, so the cycle it displaced
// continues when no later cycle claimed the layer meanwhile. It runs on the
// failure path, possibly after the request's context ended, so it uses a
// context that keeps the caller's values without its cancellation, bounded
// by its own deadline.
//
// Spec: §7.3.1 (Removed artifacts)
func ReleaseClaim(ctx context.Context, st store.Store, tenantID, layerID, claim, previous string) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), claimReleaseTimeout)
	defer cancel()
	if err := st.ReleaseLayerIngest(rctx, tenantID, layerID, claim, previous); err != nil {
		log.Printf("ingest: layer %s: hand back the claim of a failed cycle: %v", layerID, err)
	}
}

// claimReleaseTimeout bounds the store update that hands a failed cycle's
// claim back. It is one statement on the layer row.
const claimReleaseTimeout = 10 * time.Second

// runClaimedCycle runs the cycle that holds claim on cfg, the layer as the
// claim read it: snapshot, history check, ingest, events, and the record.
// onFirstWrite runs once, before the cycle's first store write.
func runClaimedCycle(ctx context.Context, st store.Store, provider source.Provider, cfg store.LayerConfig, opts SourceIngestOptions, claim string, onFirstWrite func()) (*Result, error) {
	// Spec: §4.7.11 (Ingest verification) — a cycle of a layer whose policy
	// is invalid is refused after its claim and before its snapshot.
	att := opts.Attestation.Cycle(cfg, claim)
	if att != nil {
		att.bind(opts.Clock, opts.AuditEmit)
		if superseded, err := att.CheckPolicy(ctx, st, onFirstWrite); superseded || err != nil {
			return supersededResult(superseded, err)
		}
	}
	srcCfg := source.LayerConfig{
		ID: cfg.ID,
		// Spec: §7.3.1 (Repository credentials). The ingest clones with the stored
		// value; cfg.Repo is the reportable form.
		Repo:     cfg.CloneRepo(),
		Ref:      cfg.Ref,
		Root:     cfg.Root,
		PriorRef: cfg.LastIngestedRef,
		Path:     cfg.LocalPath,
	}
	snap, err := provider.Snapshot(ctx, srcCfg)
	if err != nil {
		// Spec: §4.7.11 (Attestation record) — a Git snapshot that reached its
		// fetch deadline is recorded, so the cycle keeps its claim.
		if att != nil {
			if _, rerr := att.RecordSourceFailure(ctx, st, err, onFirstWrite); rerr != nil {
				return nil, rerr
			}
		}
		return nil, err
	}
	if att != nil {
		att.WithLineage(snap.Reference, lineageFor(provider, srcCfg, snap.Reference))
	}

	if snap.HistoryRewritten {
		switch cfg.ForcePushPolicy {
		case "strict":
			return nil, fmt.Errorf("%w: prior %s no longer reachable from %s",
				ErrHistoryRewritten, cfg.LastIngestedRef, snap.Reference)
		default:
			if opts.Emit != nil {
				opts.Emit(ctx, cfg.TenantID, cfg.ID, cfg.LastIngestedRef, snap.Reference)
			}
			if opts.PublishEvent != nil {
				opts.PublishEvent(ctx, layerScope(cfg), "layer.history_rewritten", map[string]any{
					"tenant":    cfg.TenantID,
					"layer":     cfg.ID,
					"prior_ref": cfg.LastIngestedRef,
					"new_ref":   snap.Reference,
				})
			}
			if opts.AuditEmit != nil {
				opts.AuditEmit("layer.history_rewritten", cfg.ID, map[string]string{
					"prior_ref": cfg.LastIngestedRef,
					"new_ref":   snap.Reference,
				})
			}
		}
	}

	res, err := Ingest(ctx, st, Request{
		TenantID:                   cfg.TenantID,
		LayerID:                    cfg.ID,
		Files:                      snap.Files,
		Linter:                     opts.Linter,
		Embedder:                   opts.Embedder,
		VectorPut:                  opts.VectorPut,
		DomainVectorPut:            opts.DomainVectorPut,
		PublishEvent:               opts.PublishEvent,
		AuditEmit:                  opts.AuditEmit,
		CallerID:                   opts.CallerID,
		ResourcePut:                opts.ResourcePut,
		FreezeWindows:              opts.FreezeWindows,
		RejectAtOrAbove:            opts.RejectAtOrAbove,
		EnforceSandboxProfile:      opts.EnforceSandboxProfile,
		EnforceableSandboxProfiles: opts.EnforceableSandboxProfiles,
		Signer:                     opts.Signer,
		UseVectorOutbox:            opts.UseVectorOutbox,
		SemanticOverlap:            opts.SemanticOverlap,
		IngestClaim:                claim,
		OnFirstWrite:               onFirstWrite,
		Clock:                      opts.Clock,
		Attestation:                att,
	})
	if err != nil {
		// Spec: §7.3.1 (Removed artifacts) — a cycle that read the whole
		// snapshot and then failed lint has already removed the artifacts the
		// snapshot no longer holds, so the result travels with the error and
		// the response can report them.
		return res, err
	}
	// Spec: §7.3.1 (Removed artifacts) — a later cycle claimed the layer, and
	// its snapshot can be the newer one, so this cycle stopped at a check and
	// records nothing: recording its reference would move the layer back.
	if res.Superseded {
		log.Printf("ingest: layer %s: a later cycle claimed the layer; this cycle stopped and records nothing", cfg.ID)
		return res, nil
	}

	// §7.6 layer.ingested: one event per completed layer cycle with
	// the result summary. Useful for build pipelines that gate on
	// "ingest of the prod layer just completed."
	if opts.PublishEvent != nil {
		opts.PublishEvent(ctx, layerScope(cfg), "layer.ingested", map[string]any{
			"tenant":         cfg.TenantID,
			"layer":          cfg.ID,
			"reference":      snap.Reference,
			"accepted":       res.Accepted,
			"idempotent":     res.Idempotent,
			"conflicts":      len(res.Conflicts),
			"lint_failures":  len(res.LintFailures),
			"embed_failures": len(res.EmbeddingFailures),
			"removed":        len(res.Removed),
		})
	}
	if opts.AuditEmit != nil {
		opts.AuditEmit("layer.ingested", cfg.ID, map[string]string{
			"reference": snap.Reference,
			"accepted":  fmt.Sprintf("%d", res.Accepted),
		})
	}

	// §7.3.1 "last_ingested_at is exposed per layer for staleness
	// monitoring": stamp the completion time of this ingest cycle from the
	// snapshot timestamp the provider reported. Only the two ingest columns
	// are written, and only while the layer holds this cycle's claim, so an
	// update made during the cycle stands and an unregistered layer stays
	// unregistered.
	ingestedAt := cfg.LastIngestedAt
	if !snap.CreatedAt.IsZero() {
		ts := snap.CreatedAt.UTC()
		ingestedAt = &ts
	}
	switch err := st.RecordLayerIngest(ctx, cfg.TenantID, cfg.ID, claim, snap.Reference, ingestedAt); {
	case errors.Is(err, store.ErrIngestSuperseded):
		log.Printf("ingest: layer %s: a later cycle claimed the layer before this cycle recorded its reference", cfg.ID)
		res.Superseded = true
	case err != nil:
		return res, fmt.Errorf("update last_ingested_ref: %w", err)
	}
	return res, nil
}

// ClaimLayer claims layerID for a new ingest cycle with a fresh random claim,
// superseding every earlier cycle of the layer, and returns the claim, the
// claim it replaced, and the layer as stored. It returns an error wrapping
// store.ErrNotFound, which the reingest endpoint answers as
// registry.not_found, when no live layer exists. The claim is random rather
// than a counter, so a claim that an unregister cleared or a store write
// reset can never equal a later one.
//
// Spec: §7.3.1 (Removed artifacts)
func ClaimLayer(ctx context.Context, st store.Store, tenantID, layerID string) (claim, previous string, cfg store.LayerConfig, err error) {
	claim, err = newClaim()
	if err != nil {
		return "", "", store.LayerConfig{}, err
	}
	cfg, previous, err = st.ClaimLayerIngest(ctx, tenantID, layerID, claim)
	if err != nil {
		return "", "", store.LayerConfig{}, fmt.Errorf("ingest: claim layer %s: %w", layerID, err)
	}
	return claim, previous, cfg, nil
}

// claimForCycle claims the layer for a cycle, applying the mark reset of
// opts in the same write when it carries one, and records
// layer.attestation_mark_reset for an applied reset.
//
// Spec: §4.7.11 (Mark reset), §7.3.1 (Removed artifacts)
func claimForCycle(ctx context.Context, st store.Store, cfg store.LayerConfig, opts SourceIngestOptions) (claim, previous string, stored store.LayerConfig, err error) {
	if opts.MarkReset == nil {
		return ClaimLayer(ctx, st, cfg.TenantID, cfg.ID)
	}
	claim, err = newClaim()
	if err != nil {
		return "", "", store.LayerConfig{}, err
	}
	stored, previous, prior, err := st.ClaimLayerIngestWithReset(ctx, cfg.TenantID, cfg.ID, claim, opts.MarkReset.ToCommit)
	if err != nil {
		return "", "", store.LayerConfig{}, fmt.Errorf("ingest: claim layer %s: %w", cfg.ID, err)
	}
	if opts.AuditEmit != nil {
		mark := ""
		if prior.Mark != nil {
			mark = prior.Mark.UTC().Format(time.RFC3339Nano)
		}
		opts.AuditEmit(string(audit.EventLayerAttestationMarkReset), cfg.ID, map[string]string{
			"previous_mark":   mark,
			"previous_commit": prior.Commit,
			"to_commit":       opts.MarkReset.ToCommit,
			"caller":          opts.CallerID,
		})
	}
	return claim, previous, stored, nil
}

// lineageFor returns the lineage query of a cycle of provider for the
// snapshot at reference, or nil for a provider that answers none.
//
// Spec: §9.1 (Lineage query), §4.7.11 (Layer step)
func lineageFor(provider source.Provider, cfg source.LayerConfig, reference string) LineageFunc {
	lp, ok := provider.(source.LineageProvider)
	if !ok || !lp.AnswersLineage() {
		return nil
	}
	return func(ctx context.Context, base string, commits []string) ([]source.LineageAnswer, error) {
		if err := source.ValidateLineageRequest(reference, base, commits); err != nil {
			return nil, err
		}
		return lp.Lineage(ctx, cfg, reference, base, commits)
	}
}

// silence unused-import linters when build paths don't reach fs.
var _ fs.FS = (fs.FS)(nil)

// layerScope is the §7.6 event scope of a layer-level event: the
// layer's tenant and the layer itself, with no artifact path.
//
// Spec: §7.6
func layerScope(cfg store.LayerConfig) core.EventScope {
	return core.EventScope{TenantID: cfg.TenantID, Layers: []string{cfg.ID}}
}

// newClaim returns a fresh random ingest claim.
func newClaim() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("ingest: generate an ingest claim: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
