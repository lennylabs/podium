package core

import (
	"context"
	"fmt"
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
	if err := r.OverlapReady(); err != nil {
		return OverlapComputeResult{}, err
	}
	served, err := r.servedArtifacts(ctx)
	if err != nil {
		return OverlapComputeResult{}, err
	}
	out := r.computeOverlapBatch(ctx, sortedServed(served, nil), served, overlapBatchOptions{})
	return OverlapComputeResult{Computed: len(out.computed), Failed: out.failed}, nil
}

// OverlapSweepResult is the outcome of one sweep pass over a tenant: the
// counts of OverlapComputeResult, and Pending, the stale artifacts the pass
// skipped because the §4.7 outbox has not written their vectors yet.
type OverlapSweepResult struct {
	OverlapComputeResult
	Pending int
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
// The pass reads the records and the computation entries before the served
// set. An artifact ingested and computed while the pass runs is then either
// absent from the records the pass read or present in the served set, so the
// pass never deletes it as unserved. Reading the served set first would let
// such an artifact's fresh records be deleted. A computation of the pass
// whose version was superseded during the pass stores nothing (see
// storeOverlapChunk).
//
// The pass skips a stale artifact whose current version has a row in the
// §4.7 outbox, because its vector is not written yet and the outbox write
// computes it; a skipped artifact counts in Pending, as neither computed nor
// failed. The pass lists the outbox after the served set: the outbox row of
// a served version commits with its manifest, so a version in the served set
// whose vector is not written is in the listing, and a row drained in
// between has its vector written.
//
// Spec: §7.3.1
func (r *Registry) RefreshOverlap(ctx context.Context) (OverlapSweepResult, error) {
	if err := r.OverlapReady(); err != nil {
		return OverlapSweepResult{}, err
	}
	tenant := r.TenantFor(ctx)
	records, err := r.store.ListOverlapRecords(ctx, tenant)
	if err != nil {
		return OverlapSweepResult{}, err
	}
	runs, err := r.store.ListOverlapComputations(ctx, tenant)
	if err != nil {
		return OverlapSweepResult{}, err
	}
	served, err := r.servedArtifacts(ctx)
	if err != nil {
		return OverlapSweepResult{}, err
	}
	unwritten, err := r.unwrittenVectors(ctx, tenant)
	if err != nil {
		return OverlapSweepResult{}, err
	}
	if gone := unservedOverlapArtifacts(served, records, runs); len(gone) > 0 {
		if err := r.store.DeleteOverlapArtifacts(ctx, tenant, gone); err != nil {
			log.Printf("%s: sweep: delete the records of %d artifacts no longer served: %v", lint.SemanticOverlapCode, len(gone), err)
		}
	}
	stale := staleOverlapArtifacts(served, records, runs, r.overlapModel())
	pending := 0
	for id := range stale {
		if unwritten[store.VectorPendingKey{ArtifactID: id, Version: served[id].Version}] {
			delete(stale, id)
			pending++
		}
	}
	out := r.computeOverlapBatch(ctx, sortedServed(served, stale), served, overlapBatchOptions{})
	return OverlapSweepResult{
		OverlapComputeResult: OverlapComputeResult{Computed: len(out.computed), Failed: out.failed},
		Pending:              pending,
	}, nil
}

// unwrittenVectors returns the keys of tenant's §4.7 outbox rows, the
// versions whose vectors are not written yet. A store without the outbox
// capability writes vectors with the manifest, so nothing is unwritten.
//
// Spec: §7.3.1, §4.7
func (r *Registry) unwrittenVectors(ctx context.Context, tenant string) (map[store.VectorPendingKey]bool, error) {
	ob, ok := r.store.(store.VectorOutbox)
	if !ok {
		return nil, nil
	}
	keys, err := ob.ListVectorPendingKeys(ctx, tenant)
	if err != nil {
		return nil, fmt.Errorf("list the vector outbox: %w", err)
	}
	out := make(map[store.VectorPendingKey]bool, len(keys))
	for _, k := range keys {
		out[k] = true
	}
	return out, nil
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
