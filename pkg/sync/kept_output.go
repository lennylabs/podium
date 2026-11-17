package sync

import "github.com/lennylabs/podium/pkg/layer"

// keptOutput is the previous output a filesystem-source sync leaves in place
// for the artifacts the §4.1 unparseable-manifest rule dropped this run. An
// unparseable manifest is usually a file in the middle of an edit, so the
// files and lock entries an earlier run wrote for such an artifact survive the
// run instead of being reconciled away as stale.
//
// Spec: §13.11.3
type keptOutput struct {
	// ids holds the canonical IDs whose previous output is kept.
	ids map[string]bool
	// entries holds the prior lock's entries for those IDs, copied verbatim
	// into the new lock.
	entries []LockArtifact
	// standalone holds the kept entries' standalone paths, which the
	// stale-file cleanup leaves on disk. A config-merge path is shared with
	// other artifacts and the operator, so it is reconciled with the kept
	// artifacts' entries exempted rather than skipped.
	standalone map[string]bool
}

// keepPreviousOutput returns the previous output to keep and marks each drop
// whose output it keeps. Under OnUnparseableRemove it keeps nothing, so the
// unparseable artifact's previous output is reconciled away like any other
// drop's. Otherwise an artifact qualifies when the unparseable-manifest
// rule dropped it, the prior lock records it, and the run resolved no other
// copy of it: a lower-precedence layer or the workspace overlay can still
// contribute the ID, and that copy is then materialized and reconciled as
// usual. A collision, no-inheritance, or removed-type drop keeps nothing,
// because each reflects a lasting choice in the source, and an artifact the
// lock does not record gets no files.
//
// Spec: §13.11.3
func keepPreviousOutput(dropped []layer.Drop, prior *LockFile, resolved []materialRecord, policy OnUnparseable) keptOutput {
	kept := keptOutput{ids: map[string]bool{}, standalone: map[string]bool{}}
	if prior == nil || policy == OnUnparseableRemove {
		return kept
	}
	locked := map[string]bool{}
	for _, la := range prior.Artifacts {
		locked[la.ID] = true
	}
	present := make(map[string]bool, len(resolved))
	for _, rec := range resolved {
		present[rec.ID] = true
	}
	for i, d := range dropped {
		if d.Unparseable() && locked[d.ArtifactID] && !present[d.ArtifactID] {
			kept.ids[d.ArtifactID] = true
			dropped[i].PreviousOutputKept = true
		}
	}
	for _, la := range prior.Artifacts {
		if !kept.ids[la.ID] {
			continue
		}
		kept.entries = append(kept.entries, la)
		if la.MaterializedPath != "" && la.Merge == "" {
			kept.standalone[la.MaterializedPath] = true
		}
	}
	return kept
}

// keepFunc returns the predicate materialize uses to exempt the kept
// artifacts' entries from the config-merge and inject reconciliation, or nil
// when nothing is kept.
func (k keptOutput) keepFunc() func(id string) bool {
	if len(k.ids) == 0 {
		return nil
	}
	return func(id string) bool { return k.ids[id] }
}
