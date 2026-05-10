package ingest

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/lint"
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
	Linter       *lint.Linter
	Emit         HistoryEmitter
	Embedder     EmbedderFunc
	VectorPut    VectorPutFunc
	PublishEvent EventEmitter
	// AuditEmit, when non-nil, receives §8.1 audit events the
	// orchestrator and ingest pipeline produce (artifact.published,
	// layer.ingested, layer.history_rewritten, freeze.break_glass).
	AuditEmit AuditEmitterFunc
	// CallerID identifies the operator triggering the ingest;
	// embedded into emitted audit events.
	CallerID string
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
	srcCfg := source.LayerConfig{
		ID:       cfg.ID,
		Repo:     cfg.Repo,
		Ref:      cfg.Ref,
		Root:     cfg.Root,
		PriorRef: cfg.LastIngestedRef,
		Path:     cfg.LocalPath,
	}
	snap, err := provider.Snapshot(ctx, srcCfg)
	if err != nil {
		return nil, err
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
				opts.PublishEvent("layer.history_rewritten", map[string]any{
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
		TenantID:     cfg.TenantID,
		LayerID:      cfg.ID,
		Files:        snap.Files,
		Linter:       opts.Linter,
		Embedder:     opts.Embedder,
		VectorPut:    opts.VectorPut,
		PublishEvent: opts.PublishEvent,
		AuditEmit:    opts.AuditEmit,
		CallerID:     opts.CallerID,
	})
	if err != nil {
		return nil, err
	}

	// §7.6 layer.ingested: one event per completed layer cycle with
	// the result summary. Useful for build pipelines that gate on
	// "ingest of the prod layer just completed."
	if opts.PublishEvent != nil {
		opts.PublishEvent("layer.ingested", map[string]any{
			"tenant":         cfg.TenantID,
			"layer":          cfg.ID,
			"reference":      snap.Reference,
			"accepted":       res.Accepted,
			"idempotent":     res.Idempotent,
			"conflicts":      len(res.Conflicts),
			"lint_failures":  len(res.LintFailures),
			"embed_failures": len(res.EmbeddingFailures),
		})
	}
	if opts.AuditEmit != nil {
		opts.AuditEmit("layer.ingested", cfg.ID, map[string]string{
			"reference": snap.Reference,
			"accepted":  fmt.Sprintf("%d", res.Accepted),
		})
	}

	cfg.LastIngestedRef = snap.Reference
	if perr := st.PutLayerConfig(ctx, cfg); perr != nil {
		return res, fmt.Errorf("update last_ingested_ref: %w", perr)
	}
	return res, nil
}

// silence unused-import linters when build paths don't reach fs.
var _ fs.FS = (fs.FS)(nil)
