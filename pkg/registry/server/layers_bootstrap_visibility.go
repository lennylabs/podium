package server

import (
	"context"
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

// writeUpdatedLayer stores an updated layer and returns the row as stored.
// An update that carries no visibility member keeps the stored visibility
// members, and the response then reports the members the store kept, read
// back; a failed read back reports the layer as the update built it.
//
// Spec: §4.7.11 (Visibility of a path-bootstrapped layer: Writes that keep the visibility), §7.3.1
func (e *LayerEndpoint) writeUpdatedLayer(ctx context.Context, cfg store.LayerConfig, visibility bool) (store.LayerConfig, error) {
	if visibility {
		return cfg, e.store.PutLayerConfig(ctx, cfg)
	}
	if err := e.store.PutLayerConfigKeepingVisibility(ctx, cfg); err != nil {
		return cfg, err
	}
	if stored, err := e.store.GetLayerConfig(ctx, cfg.TenantID, cfg.ID); err == nil {
		return stored, nil
	}
	return cfg, nil
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
