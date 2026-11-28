package sync

import (
	"os"
	"path/filepath"
	"slices"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/materialize"
)

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

// keepRun is what a run contributes to the keep decision: the adapter and
// policy it runs with, the selection it applies, and the output it writes.
type keepRun struct {
	// adapterID is the harness adapter this run materializes for.
	adapterID string
	// policy is the resolved on-unparseable setting.
	policy OnUnparseable
	// scope and toggles are the §7.5.1 scope and §7.5.5 toggles the run
	// selects its records with.
	scope   ScopeFilter
	toggles LockToggles
	// resolved is the full effective view before scope and toggles.
	resolved []materialRecord
	// files is the output this run materializes, with the paths the lock
	// records.
	files []adapter.File
	// target is the target directory, read for the config-merge files a kept
	// artifact holds an entry in.
	target string
	// refused holds the IDs the §4.7.11 checks refused this run, whose
	// previous output a workspace sync keeps (§7.5).
	refused map[string]bool
}

// keepPreviousOutput returns the previous output to keep and marks each drop
// whose output it keeps. Under OnUnparseableRemove it keeps nothing, so the
// unparseable artifact's previous output is reconciled away like any other
// drop's. Otherwise an artifact qualifies when the unparseable-manifest rule
// dropped it, the prior lock records it, and the run resolved no other copy
// of it: a lower-precedence layer or the workspace overlay can still
// contribute the ID, and that copy is then materialized and reconciled as
// usual. A collision, no-inheritance, or removed-type drop keeps nothing,
// because each reflects a lasting choice in the source, and an artifact the
// lock does not record gets no files.
//
// A qualifying artifact keeps its output only while that output still belongs
// in the target: the prior lock was written by the same harness adapter, the
// run's scope and toggles select the artifact, and no artifact this run
// materializes writes a path the kept output holds. Any other qualifying
// artifact is left unmarked, so its previous output is removed as under
// OnUnparseableRemove.
//
// Spec: §13.11.3
func keepPreviousOutput(dropped []layer.Drop, prior *LockFile, run keepRun) keptOutput {
	kept := keptOutput{ids: map[string]bool{}, standalone: map[string]bool{}}
	if prior == nil {
		return kept
	}
	// Output another adapter wrote sits at that harness's paths, which this
	// run neither writes nor reads, so none of it belongs in the target now.
	if prior.Harness != run.adapterID {
		return kept
	}
	locked := map[string][]LockArtifact{}
	for _, la := range prior.Artifacts {
		locked[la.ID] = append(locked[la.ID], la)
	}
	present := make(map[string]bool, len(run.resolved))
	for _, rec := range run.resolved {
		present[rec.ID] = true
	}
	written := run.writtenPaths()
	for i, d := range dropped {
		entries := locked[d.ArtifactID]
		if !run.keeps(d) || len(entries) == 0 || present[d.ArtifactID] {
			continue
		}
		if !run.selects(d.ArtifactID, entries[0].Type) || run.takesOver(d.ArtifactID, entries, written) {
			continue
		}
		kept.ids[d.ArtifactID] = true
		dropped[i].PreviousOutputKept = true
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

// keeps reports whether a drop's previous output qualifies to be kept: a
// refusal of the §4.7.11 checks always (§7.5), and a drop under the §4.1
// unparseable-manifest rule unless the setting is remove (§13.11.3).
func (r keepRun) keeps(d layer.Drop) bool {
	if r.refused[d.ArtifactID] {
		return true
	}
	return d.Unparseable() && r.policy != OnUnparseableRemove
}

// selects reports whether the run's §7.5.1 scope and §7.5.5 toggles select id,
// judging the type filter by typ, the type the prior lock entry records,
// because the unparseable manifest names none. A lock entry that records no
// type fails a non-empty type filter, as a record whose manifest does not
// parse does. It runs selectRecords itself, so the keep decision and the
// materialization set cannot apply different rules.
func (r keepRun) selects(id, typ string) bool {
	rec := materialRecord{ID: id, Artifact: &manifest.Artifact{Type: manifest.ArtifactType(typ)}}
	return len(selectRecords(r.scope, []materialRecord{rec}, r.toggles)) == 1
}

// writtenPaths returns each path the run writes, mapped to the config-merge
// fragments it writes there. A standalone path maps to no fragment.
func (r keepRun) writtenPaths() map[string][][]byte {
	out := make(map[string][][]byte, len(r.files))
	for _, f := range r.files {
		if f.Op == adapter.OpMergeJSON {
			out[f.Path] = append(out[f.Path], f.Content)
			continue
		}
		if _, ok := out[f.Path]; !ok {
			out[f.Path] = nil
		}
	}
	return out
}

// takesOver reports whether an artifact this run materializes writes a path
// id's previous output holds. A standalone path is held whole, so any write to
// it takes it over. A config-merge or inject file is shared by design, and
// what id holds there is its own entry: an inject block and an in-entry tagged
// JSON element are keyed by the artifact ID, so no other artifact writes them,
// while a keyed config-map entry (an mcpServers server) is keyed by the
// artifact's name, so another artifact takes it over when one of its fragments
// records the same entry path in the index.
func (r keepRun) takesOver(id string, entries []LockArtifact, written map[string][][]byte) bool {
	for _, la := range entries {
		frags, ok := written[la.MaterializedPath]
		if !ok {
			continue
		}
		if la.Merge == "" {
			return true
		}
		if la.Merge == "json" && r.entryTakenOver(id, la.MaterializedPath, frags) {
			return true
		}
	}
	return false
}

// entryTakenOver reports whether one of frags, written to the config-merge file
// at rel by another artifact, records the entry path the file's index records
// for id. A file that cannot be read, or whose index records no entry for id,
// holds no keyed entry for id, so nothing in it can be taken over.
func (r keepRun) entryTakenOver(id, rel string, frags [][]byte) bool {
	doc, err := os.ReadFile(filepath.Join(r.target, filepath.FromSlash(rel)))
	if err != nil {
		return false
	}
	held, ok := materialize.IndexedEntries(doc)[id]
	if !ok {
		return false
	}
	for _, frag := range frags {
		for owner, loc := range materialize.IndexedEntries(frag) {
			if owner != id && slices.Equal(loc, held) {
				return true
			}
		}
	}
	return false
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
