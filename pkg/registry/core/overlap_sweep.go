package core

import (
	"context"
	"log"
	"sort"

	"github.com/lennylabs/podium/pkg/lint"
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
// tenant. It deletes the records and computation entries of artifacts that
// are no longer served, then computes every served artifact whose last
// successful computation is missing or was for another version or model, or
// that owns a record naming a version that is no longer served, and leaves
// current artifacts alone, including one whose computation found no peer.
// Replicas run it independently, and because a computation replaces records
// by key, two replicas computing one artifact store the same result. A failed
// deletion is logged and does not stop the computations.
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
	tenant := r.TenantFor(ctx)
	records, err := r.store.ListOverlapRecords(ctx, tenant)
	if err != nil {
		return OverlapComputeResult{}, err
	}
	runs, err := r.store.ListOverlapComputations(ctx, tenant)
	if err != nil {
		return OverlapComputeResult{}, err
	}
	if gone := unservedOverlapArtifacts(served, records, runs); len(gone) > 0 {
		if err := r.store.DeleteOverlapArtifacts(ctx, tenant, gone); err != nil {
			log.Printf("%s: sweep: delete the records of %d artifacts no longer served: %v", lint.SemanticOverlapCode, len(gone), err)
		}
	}
	stale := staleOverlapArtifacts(served, records, runs, r.overlapModel())
	computed, failed := r.computeOverlapBatch(ctx, sortedServed(served, stale), served)
	return OverlapComputeResult{Computed: len(computed), Failed: failed}, nil
}

// staleOverlapArtifacts returns the served artifacts the sweep computes: one
// with no computation entry, one whose entry is for another version or for a
// model other than model, and one that owns a record naming a version that is
// no longer served, its own or its peer's. A record naming a removed peer
// therefore makes its owner stale, and recomputing the owner replaces the
// record. An artifact whose current computation found no peer owns no record
// and has a current entry, so it is left alone.
//
// Spec: §7.3.1
func staleOverlapArtifacts(served map[string]store.ManifestRecord, records []store.OverlapRecord, runs []store.OverlapComputation, model string) map[string]bool {
	entries := make(map[string]store.OverlapComputation, len(runs))
	for _, c := range runs {
		entries[c.ArtifactID] = c
	}
	isServed := func(id, version string) bool {
		m, ok := served[id]
		return ok && m.Version == version
	}
	stale := map[string]bool{}
	for id, m := range served {
		if c, ok := entries[id]; !ok || c.Version != m.Version || c.ModelID != model {
			stale[id] = true
		}
	}
	for _, rec := range records {
		if _, ok := served[rec.Owner]; !ok {
			continue
		}
		if !isServed(rec.Owner, rec.OwnerVersion) || !isServed(rec.Peer, rec.PeerVersion) {
			stale[rec.Owner] = true
		}
	}
	return stale
}

// unservedOverlapArtifacts returns, in identifier order, the artifacts that
// own a record or have a computation entry and are no longer served at any
// version. The sweep deletes their records and entries.
//
// Spec: §7.3.1
func unservedOverlapArtifacts(served map[string]store.ManifestRecord, records []store.OverlapRecord, runs []store.OverlapComputation) []string {
	gone := map[string]bool{}
	for _, rec := range records {
		if _, ok := served[rec.Owner]; !ok {
			gone[rec.Owner] = true
		}
	}
	for _, c := range runs {
		if _, ok := served[c.ArtifactID]; !ok {
			gone[c.ArtifactID] = true
		}
	}
	out := make([]string, 0, len(gone))
	for id := range gone {
		out = append(out, id)
	}
	sort.Strings(out)
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
