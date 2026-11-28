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
// An update that carries a visibility member writes the whole row in one
// write conditional on the visibility members its handler read, before; when
// another writer, such as an ingest cycle, changed them in between, nothing
// is written, and it reads the row again, applies the members the body
// carries to the fresh values, and retries, so a narrowing of a member the
// body omits is kept. A refused or failed write therefore persists nothing,
// and the caller records events only after a write that succeeded. An update
// that carries none writes with the keeping write and reads the row back so
// the response reports the members the store kept. With trustConditional
// every write applies only while the layer stores the trust requirements
// before holds, and a changed requirement returns
// store.ErrTrustRequirementsChanged.
//
// Spec: §7.3.1, §4.7.11 (Visibility of a path-bootstrapped layer: Writes that keep the visibility)
func (e *LayerEndpoint) writeUpdatedLayer(ctx context.Context, before, cfg store.LayerConfig, vis visibilityPatch, trustConditional bool) (store.LayerConfig, store.LayerConfig, error) {
	keep := e.store.PutLayerConfigKeepingVisibility
	putIf := e.store.PutLayerConfigIf
	if trustConditional {
		// Spec: §7.3.1 (Patch semantics on the update body) — the write
		// applies only while the layer stores the requirements read.
		keep = func(ctx context.Context, cfg store.LayerConfig) error {
			return e.store.PutLayerConfigIfTrust(ctx, cfg, before.TrustRequirements, nil)
		}
		putIf = func(ctx context.Context, cfg store.LayerConfig, s store.VisibilityState) error {
			return e.store.PutLayerConfigIfTrust(ctx, cfg, before.TrustRequirements, &s)
		}
	}
	if !vis.carried() {
		if err := keep(ctx, cfg); err != nil {
			return cfg, before, err
		}
		stored, err := e.store.GetLayerConfig(ctx, cfg.TenantID, cfg.ID)
		if err != nil {
			return cfg, before, nil
		}
		return stored, withVisibilityOf(before, stored), nil
	}
	read := before
	for range updateVisibilityRetries {
		next := withVisibilityOf(cfg, declareUpdatedVisibility(read, vis.applyTo(read)))
		switch err := putIf(ctx, next, read.VisibilityState()); {
		case err == nil:
			return next, withVisibilityOf(before, read), nil
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
//
// The mode reads the floor of the layer's tenant. A floor that cannot be
// read is read as the floor's mode require, so the warning is given rather
// than dropped; it changes nothing the update stored.
func (e *LayerEndpoint) signedVisibilityWarning(ctx context.Context, before, cfg store.LayerConfig) []string {
	if cfg.VisibilitySource == "" || layer.SameVisibility(core.VisibilityOf(before), core.VisibilityOf(cfg)) {
		return nil
	}
	floor, err := e.trustFloors.Floor(ctx, cfg.TenantID)
	if err != nil {
		floor.Mode = layer.TrustModeRequire
	}
	mode := layer.ResolveTrustMode(cfg.TrustMode, floor.Mode, e.trustDefault)
	if mode != layer.TrustModeWarn && mode != layer.TrustModeRequire {
		return nil
	}
	return []string{fmt.Sprintf("layer %q is loaded from --layer-path under trust mode %s: the next ingest cycle that passes the layer step applies the signed .layer-config again, so this visibility lasts until then; make a lasting narrowing in .layer-config", cfg.ID, mode)}
}
