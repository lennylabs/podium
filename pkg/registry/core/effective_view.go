package core

import (
	"context"

	"github.com/lennylabs/podium/pkg/layer"
)

// EffectiveArtifact is one artifact in the caller's effective view,
// returned by EffectiveView. It carries the coordinates a consumer needs to
// load and materialize each artifact; the manifest body and bundled
// resources come from a follow-up load_artifact call.
type EffectiveArtifact struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Version     string `json:"version"`
	Layer       string `json:"layer"`
	ContentHash string `json:"content_hash"`
	Deprecated  bool   `json:"deprecated"`
}

// EffectiveView returns every artifact visible to id, deduplicated to the
// latest version per canonical ID (§4.7.6) and sorted by ID. Unlike
// SearchArtifacts it applies no relevance ranking and no top-K cap, so a
// consumer can enumerate the whole effective view in one pass. Visibility
// and §6.3.1 read-scope narrowing are applied exactly as every meta-tool
// does, via visibleManifests, so a caller never sees an artifact in a layer
// it cannot read.
//
// spec: §2.2 — the registry "composes the caller's effective view from the
// configured layer list per OAuth identity, applies per-layer visibility";
// §7.5 — `podium sync` "reads the user's effective view from the registry."
// This is the server-source enumeration `podium sync` walks (F-2.2.2).
func (r *Registry) EffectiveView(ctx context.Context, id layer.Identity) ([]EffectiveArtifact, error) {
	visible, err := r.visibleManifests(ctx, id)
	if err != nil {
		return nil, err
	}
	latest := dedupeLatest(visible)
	out := make([]EffectiveArtifact, 0, len(latest))
	for _, m := range latest {
		out = append(out, EffectiveArtifact{
			ID:          m.ArtifactID,
			Type:        m.Type,
			Version:     m.Version,
			Layer:       m.Layer,
			ContentHash: m.ContentHash,
			Deprecated:  m.Deprecated,
		})
	}
	return out, nil
}
