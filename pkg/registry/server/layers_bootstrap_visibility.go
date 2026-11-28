package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// declareUpdatedVisibility records an update that changed the visibility of
// a layer that --layer-path bootstraps, which its visibility_source marks:
// the source becomes declared, so the updated visibility is the baseline the
// next start and cycle meet with the directory, and a pending visibility the
// update now applies is cleared. An update that leaves the visibility as it
// was changes neither member, so a client that returns the layer object it
// read stores nothing.
//
// Spec: §4.7.11 (Visibility of a path-bootstrapped layer: Updates), §7.3.1
func declareUpdatedVisibility(before, cfg store.LayerConfig) store.LayerConfig {
	vis := core.VisibilityOf(cfg)
	if cfg.VisibilitySource == "" || layer.SameVisibility(core.VisibilityOf(before), vis) {
		return cfg
	}
	cfg.VisibilitySource = store.VisibilitySourceDeclared
	if p := cfg.VisibilityPending; p != nil {
		pending := layer.Visibility{Public: p.Public, Organization: p.Organization, Groups: p.Groups, Users: p.Users}
		if layer.SameVisibility(pending, vis) {
			cfg.VisibilityPending = nil
		}
	}
	return cfg
}

// carried reports whether the update body carries a visibility member.
func (p visibilityPatch) carried() bool {
	return p.public != nil || p.organization != nil || p.groups != nil || p.users != nil
}

// updateVisibilityRetries bounds the rereads of an update whose conditional
// visibility write meets members another writer changed.
const updateVisibilityRetries = 5

// errVisibilityContended reports an update whose visibility write met a
// changed row on every attempt.
var errVisibilityContended = errors.New("the layer's visibility changed during each attempt to update it; retry the update")

// writeUpdatedLayer stores an updated layer and returns the row as stored and
// the row it replaced, both with the visibility members the store held.
//
// The non-visibility members are written with the keeping write, so the
// stored visibility members stay as they are. An update that carries a
// visibility member then writes the visibility members conditional on the
// values its handler read, before; when another writer, such as an ingest
// cycle, changed them in between, it reads the row again, applies the
// members the body carries to the fresh values, and retries, so a narrowing
// of a member the body omits is kept. An update that carries none reads the
// row back so the response reports the members the store kept.
//
// Spec: §7.3.1, §4.7.11 (Visibility of a path-bootstrapped layer: Writes that keep the visibility)
func (e *LayerEndpoint) writeUpdatedLayer(ctx context.Context, before, cfg store.LayerConfig, vis visibilityPatch) (store.LayerConfig, store.LayerConfig, error) {
	if err := e.store.PutLayerConfigKeepingVisibility(ctx, cfg); err != nil {
		return cfg, before, err
	}
	if !vis.carried() {
		stored, err := e.store.GetLayerConfig(ctx, cfg.TenantID, cfg.ID)
		if err != nil {
			return cfg, before, nil
		}
		return stored, withVisibilityOf(before, stored), nil
	}
	read := before
	for range updateVisibilityRetries {
		next := declareUpdatedVisibility(read, vis.applyTo(read))
		switch err := e.store.SetLayerVisibilityIf(ctx, cfg.TenantID, cfg.ID, read.VisibilityState(), next.VisibilityState()); {
		case err == nil:
			return withVisibilityOf(cfg, next), withVisibilityOf(before, read), nil
		case !errors.Is(err, store.ErrVisibilityChanged):
			return cfg, before, err
		}
		fresh, err := e.store.GetLayerConfig(ctx, cfg.TenantID, cfg.ID)
		if err != nil {
			return cfg, before, err
		}
		read = fresh
	}
	return cfg, before, errVisibilityContended
}

// withVisibilityOf returns cfg with the visibility members of from.
func withVisibilityOf(cfg, from store.LayerConfig) store.LayerConfig {
	s := from.VisibilityState()
	cfg.SetVisibility(s.Visibility)
	cfg.VisibilitySource, cfg.VisibilityPending = s.Source, s.Pending
	return cfg
}

// signedVisibilityWarning is the warning of an update that changed the
// visibility of a layer that --layer-path bootstraps whose trust mode
// resolves to warn or require: the next cycle that passes the layer step
// applies the signed .layer-config again, so a narrowing made through the
// update lasts until then.
//
// Spec: §7.3.1, §4.7.11 (Visibility of a path-bootstrapped layer: Updates)
func (e *LayerEndpoint) signedVisibilityWarning(before, cfg store.LayerConfig) []string {
	if cfg.VisibilitySource == "" || layer.SameVisibility(core.VisibilityOf(before), core.VisibilityOf(cfg)) {
		return nil
	}
	if mode := layer.ResolveTrustMode(cfg.TrustMode, e.trustDefault); mode != layer.TrustModeWarn && mode != layer.TrustModeRequire {
		return nil
	}
	return []string{fmt.Sprintf("layer %q is loaded from --layer-path under trust mode %s: the next ingest cycle that passes the layer step applies the signed .layer-config again, so this visibility lasts until then; make a lasting narrowing in .layer-config", cfg.ID, layer.ResolveTrustMode(cfg.TrustMode, e.trustDefault))}
}
