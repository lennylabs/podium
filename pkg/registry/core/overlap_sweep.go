package core

import (
	"context"
	"sort"

	"github.com/lennylabs/podium/pkg/store"
)

// RebuildOverlap computes the §7.3.1 overlap records of every served artifact
// of the request's tenant and reports the counts. It backs POST
// /v1/admin/overlap/rebuild; the caller authorizes the request. It returns an
// error only when the check is off, no search index is wired, or the store
// cannot list the tenant's artifacts.
//
// Spec: §7.3.1
func (r *Registry) RebuildOverlap(ctx context.Context) (OverlapComputeResult, error) {
	if err := r.overlapReady(); err != nil {
		return OverlapComputeResult{}, err
	}
	served, err := r.servedArtifacts(ctx)
	if err != nil {
		return OverlapComputeResult{}, err
	}
	computed, failed := r.computeOverlapBatch(ctx, sortedServed(served, nil), served)
	return OverlapComputeResult{Computed: len(computed), Failed: failed}, nil
}

// RefreshOverlap is one pass of the §7.3.1 periodic sweep over the request's
// tenant: it computes every served artifact that has no stored records, whose
// records carry another model identifier, or whose records name a version
// that is no longer served, and leaves current artifacts alone. Replicas run
// it independently, and because a computation replaces records by key, two
// replicas computing one artifact store the same result.
//
// Spec: §7.3.1
func (r *Registry) RefreshOverlap(ctx context.Context) (OverlapComputeResult, error) {
	if err := r.overlapReady(); err != nil {
		return OverlapComputeResult{}, err
	}
	served, err := r.servedArtifacts(ctx)
	if err != nil {
		return OverlapComputeResult{}, err
	}
	records, err := r.store.ListOverlapRecords(ctx, r.TenantFor(ctx))
	if err != nil {
		return OverlapComputeResult{}, err
	}
	stale := staleOverlapArtifacts(served, records, r.overlapModel())
	computed, failed := r.computeOverlapBatch(ctx, sortedServed(served, stale), served)
	return OverlapComputeResult{Computed: len(computed), Failed: failed}, nil
}

// staleOverlapArtifacts returns the served artifacts the sweep computes: one
// that no record names, and one named by a record that carries a model other
// than model or that names a version that is no longer served, on either
// side. A record naming a removed artifact therefore makes its live peer
// stale, and recomputing the peer deletes the record.
//
// Spec: §7.3.1
func staleOverlapArtifacts(served map[string]store.ManifestRecord, records []store.OverlapRecord, model string) map[string]bool {
	named := map[string]bool{}
	stale := map[string]bool{}
	isServed := func(id, version string) bool {
		m, ok := served[id]
		return ok && m.Version == version
	}
	for _, rec := range records {
		named[rec.ArtifactA], named[rec.ArtifactB] = true, true
		if rec.ModelID != model || !isServed(rec.ArtifactA, rec.VersionA) || !isServed(rec.ArtifactB, rec.VersionB) {
			stale[rec.ArtifactA], stale[rec.ArtifactB] = true, true
		}
	}
	out := map[string]bool{}
	for id := range served {
		if !named[id] || stale[id] {
			out[id] = true
		}
	}
	return out
}

// sortedServed returns the served records in identifier order, restricted to
// the identifiers in only when only is non-nil.
func sortedServed(served map[string]store.ManifestRecord, only map[string]bool) []store.ManifestRecord {
	out := make([]store.ManifestRecord, 0, len(served))
	for id, m := range served {
		if only == nil || only[id] {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ArtifactID < out[j].ArtifactID })
	return out
}
