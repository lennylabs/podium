package serverboot

import (
	"context"
	"errors"
	"fmt"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// directoryVisibility is the visibility a start reads from a bootstrap
// layer's directory: the visibility its .layer-config declares, or the
// deployment default with declared unset.
type directoryVisibility struct {
	vis      layer.Visibility
	declared bool
}

// storedBootstrapRow returns the row the store holds for the layer, live or
// soft-deleted, and whether it holds one.
func storedBootstrapRow(ctx context.Context, st store.Store, tenantID, id string) (store.LayerConfig, bool, error) {
	lc, err := st.GetLayerConfig(ctx, tenantID, id)
	if err == nil {
		return lc, true, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return store.LayerConfig{}, false, fmt.Errorf("read layer config %s: %w", id, err)
	}
	deleted, err := st.ListDeletedLayerConfigs(ctx, tenantID)
	if err != nil {
		return store.LayerConfig{}, false, fmt.Errorf("read the deleted layer configs of tenant %s: %w", tenantID, err)
	}
	for _, d := range deleted {
		if d.ID == id {
			return d, true, nil
		}
	}
	return store.LayerConfig{}, false, nil
}

// startVisibility sets the visibility members of cfg, the row a start writes
// for a layer that --layer-path bootstraps, and reports whether the start
// creates the row. Under the resolved mode none the start applies the
// directory visibility, as it did before ingest verification. Under warn and
// require it writes the meet of the stored row's baseline and the directory
// visibility, so an unsigned file narrows and never widens; a row the store
// does not hold is created with no visibility field set and the directory
// visibility pending, because no layer step has passed for it. A change of
// the stored members records layer.config_changed with the cause start
// through emit.
//
// Spec: §4.7.11 (Visibility of a path-bootstrapped layer: Start write, Mode none)
func startVisibility(ctx context.Context, st store.Store, cfg store.LayerConfig, mode string, dir directoryVisibility, def layer.Visibility, emit ingest.AuditEmitterFunc) (store.LayerConfig, bool, error) {
	prev, found, err := storedBootstrapRow(ctx, st, cfg.TenantID, cfg.ID)
	if err != nil {
		return cfg, false, err
	}
	applied, source := dir.vis, ingest.SourceFor(dir.declared)
	switch {
	case mode == layer.TrustModeNone:
	case !found:
		applied, source = layer.Visibility{}, store.VisibilitySourceDefault
	default:
		applied, source = layer.Meet(ingest.Baseline(prev, def), dir.vis), prev.VisibilitySource
		// A row an earlier release stored carries no source, and its
		// baseline is its stored visibility, which declared keeps.
		if source == "" {
			source = store.VisibilitySourceDeclared
		}
	}
	cfg.SetVisibility(ingest.ToStoreVisibility(applied))
	cfg.VisibilitySource = source
	cfg.VisibilityPending = nil
	if p := ingest.PendingFor(applied, dir.vis); p != nil {
		v := ingest.ToStoreVisibility(*p)
		cfg.VisibilityPending = &v
	}
	if found {
		emitStartVisibility(emit, prev, cfg)
	}
	return cfg, !found, nil
}

// emitStartVisibility records layer.config_changed with the cause start
// when the start write changes the visibility members of a stored row.
func emitStartVisibility(emit ingest.AuditEmitterFunc, prev, next store.LayerConfig) {
	if emit == nil {
		return
	}
	pv, nv := ingest.FromStoreVisibility(prev.Visibility()), ingest.FromStoreVisibility(next.Visibility())
	pp, np := pendingOf(prev), pendingOf(next)
	if layer.SameVisibility(pv, nv) && prev.VisibilitySource == next.VisibilitySource && samePending(pp, np) {
		return
	}
	emit(string(audit.EventLayerConfigChanged), next.ID, ingest.VisibilityEventFields(ingest.VisibilityCauseStart,
		pv, prev.VisibilitySource, pp, nv, next.VisibilitySource, np))
}

// pendingOf returns a row's visibility_pending in the evaluator's form.
func pendingOf(lc store.LayerConfig) *layer.Visibility {
	if lc.VisibilityPending == nil {
		return nil
	}
	v := ingest.FromStoreVisibility(*lc.VisibilityPending)
	return &v
}

// samePending reports whether two optional visibilities are equal.
func samePending(a, b *layer.Visibility) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return layer.SameVisibility(*a, *b)
}

// startupAuditEmitter returns the emitter of the visibility events of a
// start: the §8.3 stream, scrubbed, recording tenant and no caller. Nil when
// the start has no audit stream.
func startupAuditEmitter(ctx context.Context, sink audit.Sink, scrubber *audit.PIIScrubber, tenant string) ingest.AuditEmitterFunc {
	if sink == nil {
		return nil
	}
	return ingestAuditEmitter(ctx, sink, scrubber, "", tenant)
}
