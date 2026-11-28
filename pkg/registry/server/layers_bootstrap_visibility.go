package server

import (
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
