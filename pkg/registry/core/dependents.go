package core

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/version"
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

// dependencyRanking returns the §4.7.3 "frequently-depended-on artifacts
// surface higher" signal restricted to the allowed candidate set: the IDs
// ordered by descending reverse-dependency in-degree, ties broken by ID for
// determinism. Artifacts with no dependents are omitted, so callers fuse this
// partial order with the lexical/vector/usage ranks. Returns nil (cheap skip)
// when no candidate has dependents or the store lookup fails; ranking is a
// best-effort signal and a store error must not fail the search.
func (r *Registry) dependencyRanking(ctx context.Context, allowed map[string]bool) []string {
	inDegree, err := r.store.DependencyInDegree(ctx, r.tenantID)
	if err != nil || len(inDegree) == 0 {
		return nil
	}
	ranked := make([]string, 0, len(inDegree))
	for id, n := range inDegree {
		if n <= 0 {
			continue
		}
		if allowed == nil || allowed[id] {
			ranked = append(ranked, id)
		}
	}
	if len(ranked) == 0 {
		return nil
	}
	sort.Slice(ranked, func(i, j int) bool {
		di, dj := inDegree[ranked[i]], inDegree[ranked[j]]
		if di != dj {
			return di > dj
		}
		return ranked[i] < ranked[j]
	})
	return ranked
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
// effective view per §3.5. The §3.5 tenant gate expose_scope_preview is
// checked first: a disabled tenant yields ErrScopePreviewDisabled, which
// the HTTP layer maps to 403 scope_preview_disabled.
func (r *Registry) PreviewScope(ctx context.Context, id layer.Identity) (*ScopePreview, error) {
	enabled, err := r.scopePreviewEnabled(ctx)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, ErrScopePreviewDisabled
	}

	visible, err := r.visibleManifests(ctx, id)
	if err != nil {
		return nil, err
	}

	// spec: §3.5 — counts are per distinct artifact, not per
	// (artifact, version) pair. Collapse each artifact ID to the version
	// `latest` would resolve to (§4.7.6) so a multi-version artifact is
	// counted once and its type and sensitivity reflect that version.
	latest := latestPerArtifact(visible)

	preview := &ScopePreview{
		Layers:        []string{},
		ArtifactCount: len(latest),
		ByType:        map[string]int{},
		BySensitivity: map[string]int{},
	}
	for _, m := range latest {
		preview.ByType[m.Type]++
		// spec: §3.5 — by_sensitivity keys are the documented buckets;
		// an artifact that omits the optional sensitivity field falls into
		// the `low` floor rather than an empty-string bucket (F-3.5.8).
		preview.BySensitivity[sensitivityBucket(m.Sensitivity)]++
	}

	// spec: §3.5 / §4.6 — `layers` is the ordered composition (lowest
	// precedence first) of every layer the identity is entitled to see,
	// including layers with zero visible artifacts. Derive it from the
	// per-request resolved layer list (admin + runtime-registered, F-4.6.1)
	// so the order is deterministic and an empty-but-visible layer is not
	// dropped (F-3.5.6).
	resolved := r.resolveLayers(ctx)
	if len(resolved) > 0 {
		for _, l := range layer.EffectiveLayersWith(resolved, id, r.resolveGroup) {
			preview.Layers = append(preview.Layers, l.ID)
		}
	} else {
		// No layer config (filesystem-source registry or a bare test core):
		// report the distinct layers present among visible records, sorted
		// for a stable response.
		set := map[string]bool{}
		for _, m := range latest {
			if m.Layer != "" {
				set[m.Layer] = true
			}
		}
		for l := range set {
			preview.Layers = append(preview.Layers, l)
		}
		sort.Strings(preview.Layers)
	}
	return preview, nil
}

// scopePreviewEnabled resolves the §3.5 expose_scope_preview tenant gate.
// A missing tenant (auto-bootstrap deployments may not create one) takes
// the documented default of true; a genuine store failure surfaces as
// ErrUnavailable so the endpoint reports unavailable rather than disabled.
func (r *Registry) scopePreviewEnabled(ctx context.Context) (bool, error) {
	t, err := r.store.GetTenant(ctx, r.tenantID)
	if errors.Is(err, store.ErrTenantNotFound) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return t.ScopePreviewEnabled(), nil
}

// latestPerArtifact collapses visible (artifact, version) records to one
// record per artifact ID — the version §4.7.6 `latest` resolves to. It
// powers the §3.5 preview counts, which are per distinct artifact.
func latestPerArtifact(records []store.ManifestRecord) []store.ManifestRecord {
	byID := map[string][]store.ManifestRecord{}
	order := make([]string, 0, len(records))
	for _, m := range records {
		if _, ok := byID[m.ArtifactID]; !ok {
			order = append(order, m.ArtifactID)
		}
		byID[m.ArtifactID] = append(byID[m.ArtifactID], m)
	}
	out := make([]store.ManifestRecord, 0, len(order))
	for _, id := range order {
		out = append(out, pickLatest(byID[id]))
	}
	return out
}

// pickLatest selects the §4.7.6 `latest` record among the versions of one
// artifact: the most recently ingested non-deprecated version, ties broken
// by higher semver, falling back to the deprecated set when every version
// is deprecated and to the first record when versions are not semver.
func pickLatest(recs []store.ManifestRecord) store.ManifestRecord {
	byVersion := make(map[string]store.ManifestRecord, len(recs))
	cands := make([]version.Candidate, 0, len(recs))
	for _, m := range recs {
		byVersion[m.Version] = m
		if !m.Deprecated {
			cands = append(cands, version.Candidate{Version: m.Version, IngestedAt: m.IngestedAt})
		}
	}
	if len(cands) == 0 {
		for _, m := range recs {
			cands = append(cands, version.Candidate{Version: m.Version, IngestedAt: m.IngestedAt})
		}
	}
	if v, err := version.ResolveLatest(cands); err == nil {
		if rec, ok := byVersion[v]; ok {
			return rec
		}
	}
	return recs[0]
}

// sensitivityBucket maps an artifact's optional §4.3 sensitivity field to a
// §3.5 by_sensitivity key. An unset value falls into the `low` floor so the
// response keys never include an empty-string bucket.
func sensitivityBucket(s string) string {
	if s == "" {
		return "low"
	}
	return s
}
