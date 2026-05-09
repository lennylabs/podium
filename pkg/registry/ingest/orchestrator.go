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

// SourceIngestOptions threads the §4.7 embedding pieces through the
// orchestrator without forcing every call site to populate them.
// Both Embedder and VectorPut must be set together for embedding to
// run; either nil disables the path and the artifact is BM25-only.
type SourceIngestOptions struct {
	Linter    *lint.Linter
	Emit      HistoryEmitter
	Embedder  EmbedderFunc
	VectorPut VectorPutFunc
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
		}
	}

	res, err := Ingest(ctx, st, Request{
		TenantID:  cfg.TenantID,
		LayerID:   cfg.ID,
		Files:     snap.Files,
		Linter:    opts.Linter,
		Embedder:  opts.Embedder,
		VectorPut: opts.VectorPut,
	})
	if err != nil {
		return nil, err
	}

	cfg.LastIngestedRef = snap.Reference
	if perr := st.PutLayerConfig(ctx, cfg); perr != nil {
		return res, fmt.Errorf("update last_ingested_ref: %w", perr)
	}
	return res, nil
}

// silence unused-import linters when build paths don't reach fs.
var _ fs.FS = (fs.FS)(nil)
