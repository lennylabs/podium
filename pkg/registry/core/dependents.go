package core

import (
	"context"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/store"
)

// DependentsEdge mirrors store.DependencyEdge for callers outside the
// store package.
type DependentsEdge struct {
	From string
	To   string
	Kind string
}

// DependentsOf walks the §4.7.3 reverse-dependency index for the
// artifact and returns every edge ending at it. Visibility filtering
// applies: edges from invisible artifacts are dropped so callers do
// not see what they shouldn't.
func (r *Registry) DependentsOf(ctx context.Context, id layer.Identity, artifactID string) ([]DependentsEdge, error) {
	r.emit(ctx, AuditEvent{
		Type:   "artifacts.dependents_of",
		Caller: callerOf(id),
		Target: artifactID,
	})
	edges, err := r.store.DependentsOf(ctx, r.tenantID, artifactID)
	if err != nil {
		return nil, err
	}
	visible, err := r.visibleManifests(ctx, id)
	if err != nil {
		return nil, err
	}
	visibleIDs := map[string]bool{}
	for _, m := range visible {
		visibleIDs[m.ArtifactID] = true
	}
	out := make([]DependentsEdge, 0, len(edges))
	for _, e := range edges {
		if visibleIDs[e.From] {
			out = append(out, DependentsEdge{From: e.From, To: e.To, Kind: e.Kind})
		}
	}
	return out, nil
}

// ScopePreview is the §3.5 aggregated scope preview: counts only,
// no per-artifact metadata.
type ScopePreview struct {
	Layers        []string       `json:"layers"`
	ArtifactCount int            `json:"artifact_count"`
	ByType        map[string]int `json:"by_type"`
	BySensitivity map[string]int `json:"by_sensitivity"`
}

// PreviewScope returns aggregated metadata for the calling identity's
// effective view per §3.5. Tenant flag expose_scope_preview gates
// access; this implementation ships the always-on path. Adding the
// 403 surface is one Tenant config check away.
func (r *Registry) PreviewScope(ctx context.Context, id layer.Identity) (*ScopePreview, error) {
	visible, err := r.visibleManifests(ctx, id)
	if err != nil {
		return nil, err
	}
	preview := &ScopePreview{
		ArtifactCount: len(visible),
		ByType:        map[string]int{},
		BySensitivity: map[string]int{},
	}
	layerSet := map[string]bool{}
	for _, m := range visible {
		preview.ByType[m.Type]++
		preview.BySensitivity[m.Sensitivity]++
		layerSet[m.Layer] = true
	}
	for l := range layerSet {
		preview.Layers = append(preview.Layers, l)
	}
	return preview, nil
}

// quiet unused-import linter when the build path doesn't reach store.
var _ = store.ManifestRecord{}
