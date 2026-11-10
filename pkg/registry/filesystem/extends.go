package filesystem

import (
	"errors"
	"fmt"
	"sort"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/version"
)

// composeExtends applies the §4.6 collision rule, the §4.6 sandbox-profile
// rule, and the §4.6 executable-field rule to all, which is in layer order,
// and returns one resolved record per canonical ID.
//
// It repeats the composition until it settles. Each round re-runs dedupe
// over the records not yet refused, then validates every deduped record that
// declares extends: against the authored bytes, rewriting nothing. A round
// refuses each record the sandbox-profile rule or the executable-field rule
// rejects and each record whose extends: parent a refusal in an earlier round
// removed. A round that refuses
// nothing settles the composition. Re-running dedupe matters because a
// higher-precedence record that collided only with a refused record is kept,
// as a server that never stored the refused record keeps it, and a reference
// to a refused record's ID falls through to the next-lower record under it.
//
// With opts.OnDrop set, only the settled round's collisions are reported, in
// dedupe order, followed by the refusals sorted byte-wise by canonical ID and,
// under one ID, by layer from lowest to highest precedence. The order is fixed
// because podium sync prints one line per drop, and an order that followed the
// round that refused each record would read as a different result. With
// opts.OnDrop nil, the first refusal fails the walk.
//
// The rewrite pass runs once, on the settled set, so validation never reads a
// record the rewrite pass has already merged.
//
// Spec: §4.6, §13.11.3
func composeExtends(all []ArtifactRecord, opts WalkOptions) ([]ArtifactRecord, error) {
	removed := map[recordKey]bool{}
	refusedIDs := map[string]bool{}
	var refusals []refusal
	for {
		remaining := make([]ArtifactRecord, 0, len(all))
		for _, rec := range all {
			if !removed[keyOf(rec)] {
				remaining = append(remaining, rec)
			}
		}
		deduped, kept, collisions, err := dedupe(remaining, opts)
		if err != nil {
			return nil, err
		}
		refused, err := validateExtends(deduped, kept, refusedIDs)
		if err != nil {
			return nil, err
		}
		if len(refused) == 0 {
			reportDrops(opts, append(collisions, sortedDrops(refusals, all)...))
			if err := resolveExtends(deduped, kept); err != nil {
				return nil, err
			}
			return deduped, nil
		}
		if opts.OnDrop == nil {
			return nil, refused[0]
		}
		for _, r := range refused {
			removed[recordKey{id: r.id, layerID: r.layerID}] = true
			refusedIDs[r.id] = true
			refusals = append(refusals, refusal{key: recordKey{id: r.id, layerID: r.layerID}, drop: r.drop()})
		}
	}
}

// recordKey names one record: a layer contributes at most one record per
// canonical ID.
type recordKey struct {
	id, layerID string
}

func keyOf(rec ArtifactRecord) recordKey {
	return recordKey{id: rec.ID, layerID: rec.Layer.ID}
}

// refusal is one record composeExtends removed, with the drop it reports.
type refusal struct {
	key  recordKey
	drop layer.Drop
}

// sortedDrops returns the drops of refusals sorted byte-wise by canonical ID
// and, under one ID, by the position of the refused record in all, which is
// layer order from lowest to highest precedence.
func sortedDrops(refusals []refusal, all []ArtifactRecord) []layer.Drop {
	pos := make(map[recordKey]int, len(all))
	for i, rec := range all {
		pos[keyOf(rec)] = i
	}
	sorted := append([]refusal(nil), refusals...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].key.id != sorted[j].key.id {
			return sorted[i].key.id < sorted[j].key.id
		}
		return pos[sorted[i].key] < pos[sorted[j].key]
	})
	drops := make([]layer.Drop, 0, len(sorted))
	for _, r := range sorted {
		drops = append(drops, r.drop)
	}
	return drops
}

// validateExtends runs mergeRecord on each deduped record that declares
// extends: and returns every record the round refuses under the §4.6
// sandbox-profile or executable-field rule, without rewriting any record.
// refusedIDs holds the canonical IDs earlier rounds refused: an
// unresolved reference to one of them refuses the referencing record with
// layer.UnresolvedParentReason, and every other resolver error fails the walk.
// A record's refusal can come from an ancestor in its chain, and several
// deduped records can share that ancestor, so each refused record appears
// once.
//
// Spec: §4.6, §13.11.3
func validateExtends(deduped, kept []ArtifactRecord, refusedIDs map[string]bool) ([]*refusedRecordError, error) {
	effective, layered := indexRecords(deduped, kept)
	var refused []*refusedRecordError
	seen := map[recordKey]bool{}
	for _, rec := range deduped {
		if rec.Artifact == nil || rec.Artifact.Extends == "" {
			continue
		}
		_, err := mergeRecord(rec, len(layered[rec.ID])-1, deduped, effective, layered, map[string]bool{})
		if err == nil {
			continue
		}
		r, rerr := refusalOf(err, refusedIDs)
		if rerr != nil {
			return nil, rerr
		}
		key := recordKey{id: r.id, layerID: r.layerID}
		if !seen[key] {
			seen[key] = true
			refused = append(refused, r)
		}
	}
	return refused, nil
}

// refusalOf maps a mergeRecord error to the refusal it carries. An
// executable-field or sandbox-profile refusal passes through. An unresolved reference to an ID an
// earlier round refused becomes a refusal of the referencing record. Any other
// error is returned as the walk's failure.
func refusalOf(err error, refusedIDs map[string]bool) (*refusedRecordError, error) {
	var refused *refusedRecordError
	if errors.As(err, &refused) {
		return refused, nil
	}
	var unresolved *unresolvedParentError
	if errors.As(err, &unresolved) && refusedIDs[unresolved.parentID] {
		return &refusedRecordError{
			id:      unresolved.id,
			layerID: unresolved.layerID,
			reason:  layer.UnresolvedParentReason(unresolved.parentID),
			err:     unresolved,
		}, nil
	}
	return nil, err
}

// refusedRecordError reports a record composeExtends removes: one the §4.6
// sandbox-profile rule refuses, one the §4.6 executable-field rule refuses,
// one whose executable-field comparison cannot be read, or one whose extends:
// parent an earlier refusal removed. With WalkOptions.OnDrop nil it fails the
// walk. errors.As reaches the wrapped *layer.ExecutableInheritance for an
// executable-field refusal, and errors.Is reaches
// layer.ErrSandboxProfileConflict for a sandbox-profile refusal.
//
// Spec: §4.6, §13.11.3
type refusedRecordError struct {
	id      string
	layerID string
	reason  string
	err     error
}

func (e *refusedRecordError) Error() string {
	return fmt.Sprintf("%s: %q in layer %q: %v", layer.InvalidArtifactCode, e.id, e.layerID, e.err)
}

func (e *refusedRecordError) Unwrap() error { return e.err }

// drop returns the drop record the refusal reports.
func (e *refusedRecordError) drop() layer.Drop {
	return layer.Drop{ArtifactID: e.id, Code: layer.InvalidArtifactCode, Reason: e.reason}
}

// unresolvedParentError reports an extends: reference no kept record
// resolves. It carries the referencing record and the parent ID so
// composeExtends can tell a reference a refusal stranded from one that never
// resolved. The message keeps the extends.unresolved text the walk has always
// failed with.
type unresolvedParentError struct {
	id       string
	layerID  string
	parentID string
	msg      string
}

func (e *unresolvedParentError) Error() string { return e.msg }

// indexRecords returns the lookups mergeRecord reads: effective maps a
// canonical ID to its highest-precedence record in deduped, and layered groups
// every record in all by canonical ID in low-to-high precedence order.
func indexRecords(deduped, all []ArtifactRecord) (effective map[string]int, layered map[string][]ArtifactRecord) {
	effective = make(map[string]int, len(deduped))
	for i, r := range deduped {
		effective[r.ID] = i
	}
	layered = map[string][]ArtifactRecord{}
	for _, r := range all {
		layered[r.ID] = append(layered[r.ID], r)
	}
	return effective, layered
}

// resolveExtends is the rewrite pass. It folds the extends: chain of every
// deduped record per the §4.6 field-semantics table, using the shared
// manifest.MergeExtends so a filesystem-source materialization matches the
// registry's load-time assembly (§13.11.3). For each record that declares
// extends:, it resolves the parent chain, merges parent → child, and rewrites
// the record's ArtifactBytes (extends stripped per §4.6 hidden-parent privacy)
// and parsed Artifact in place. AuthoredBytes is left as loaded, because the
// §4.7.6 digest is defined over the authored ARTIFACT.md and the merged
// re-serialization does not reproduce it. composeExtends calls it once, on
// the settled set, after the §4.6 executable-field validation has read every
// record's authored bytes.
//
// The parent is the record this resolver selects, which ignores any version
// pin in the reference:
//
//   - For a different canonical ID, the parent is the effective
//     (highest-precedence) record for that ID.
//   - For the same canonical ID (the §4.6 same-ID overlay exception), the
//     parent is the record immediately below the child in layer order.
//
// That record can differ from the parent version a server pins, as §13.11.3
// states.
//
// all is the full kept record set in layer order (low to high precedence),
// which is the only place the lower-precedence same-ID parents survive after
// dedup.
//
// A record whose merged frontmatter cannot be rewritten into one that names no
// parent fails the walk with manifest.ErrUnhidableParent wrapped, which is the
// condition the server mode reports as registry.invalid_argument. Both modes
// end the sync on that input, because pkg/sync fetches every listed artifact
// and aborts on the first load that fails, so dropping the record here would
// materialize a partial tree where the server mode materializes nothing (§11,
// §2.2).
//
// Spec: §4.6, §13.11.3
func resolveExtends(deduped, all []ArtifactRecord) error {
	effective, layered := indexRecords(deduped, all)

	for i := range deduped {
		rec := deduped[i]
		if rec.Artifact == nil || rec.Artifact.Extends == "" {
			continue
		}
		// The deduped record is the highest-precedence layer for its ID.
		idx := len(layered[rec.ID]) - 1
		merged, err := mergeRecord(rec, idx, deduped, effective, layered, map[string]bool{})
		if err != nil {
			return err
		}
		out := rec
		out.Artifact = merged
		// Re-serialize the merged manifest through the shared helper so every
		// consumer of ArtifactBytes observes the resolved frontmatter with the
		// parent hidden (§4.6), and so this mode and the server mode produce
		// identical bytes for the same artifact (§11). The helper restores the
		// frontmatter keys manifest.Artifact does not declare, which a typed
		// round-trip drops.
		bytes, serr := manifest.SerializeMerged(merged, rec.ID, authoredChain(rec, idx, deduped, effective, layered)...)
		if serr != nil {
			return fmt.Errorf("extends: re-serialize %q: %w", rec.ID, serr)
		}
		out.ArtifactBytes = bytes
		deduped[i] = out
	}
	return nil
}

// mergeRecord returns the merged Artifact for rec, whose own slot in its
// layered same-ID list is layerIdx. When rec declares extends:, the parent
// chain is resolved recursively (parent first) and folded via
// manifest.MergeExtends. seen guards against extends cycles, mirroring the
// server's load-time cycle re-check (§4.6). Every edge is held to the §4.6
// sandbox-profile rule, and an edge whose parent comes from another layer is
// also held to the §4.6 executable-field rule, checked second. A refusal
// anywhere in the chain returns a *refusedRecordError naming the refused
// record. An unresolved reference returns an *unresolvedParentError.
func mergeRecord(rec ArtifactRecord, layerIdx int, deduped []ArtifactRecord, effective map[string]int, layered map[string][]ArtifactRecord, seen map[string]bool) (*manifest.Artifact, error) {
	if rec.Artifact == nil || rec.Artifact.Extends == "" {
		return rec.Artifact, nil
	}
	key := fmt.Sprintf("%s#%d", rec.ID, layerIdx)
	if seen[key] {
		return nil, fmt.Errorf("extends.cycle: extends cycle at %q", rec.ID)
	}
	seen[key] = true

	parentID := version.StripPin(rec.Artifact.Extends)

	var parentRec ArtifactRecord
	var parentIdx int
	if parentID == rec.ID {
		// Same-ID overlay: the parent is the record immediately below this
		// one in layer order.
		if layerIdx-1 < 0 {
			return nil, &unresolvedParentError{id: rec.ID, layerID: rec.Layer.ID, parentID: parentID,
				msg: fmt.Sprintf("extends.unresolved: %q extends its own id %q but no lower-precedence layer provides it", rec.ID, parentID)}
		}
		parentIdx = layerIdx - 1
		parentRec = layered[rec.ID][parentIdx]
	} else {
		di, ok := effective[parentID]
		if !ok {
			return nil, &unresolvedParentError{id: rec.ID, layerID: rec.Layer.ID, parentID: parentID,
				msg: fmt.Sprintf("extends.unresolved: %q extends %q which is not present in the registry", rec.ID, parentID)}
		}
		parentRec = deduped[di]
		parentIdx = len(layered[parentID]) - 1
	}

	parentMerged, err := mergeRecord(parentRec, parentIdx, deduped, effective, layered, seen)
	if err != nil {
		return nil, err
	}
	if parentMerged == nil {
		return nil, fmt.Errorf("extends.unresolved: parent %q of %q has no parseable manifest", parentID, rec.ID)
	}
	// spec: §4.6 — "The child's type: must match the parent's; ingest rejects
	// an extends: chain that crosses types." The server ingest path enforces
	// this; the filesystem-source materialization path must reject it too
	// rather than silently merge an incompatible parent.
	if parentMerged.Type != rec.Artifact.Type {
		return nil, fmt.Errorf("extends.type_mismatch: %q (type %q) extends %q (type %q); a child's type must match the parent's",
			rec.ID, rec.Artifact.Type, parentID, parentMerged.Type)
	}
	// Spec: §4.6 sandbox profile. Every edge, same-layer included, is
	// checked before the cross-layer executable-field check, so a child both
	// rules refuse reports this refusal, as the server does. The rewrite pass
	// needs no exemption: a child already merged under its parent never
	// conflicts with that parent.
	if manifest.SandboxProfilesConflict(parentMerged.SandboxProfile, rec.Artifact.SandboxProfile) {
		return nil, &refusedRecordError{id: rec.ID, layerID: rec.Layer.ID,
			reason: layer.SandboxProfileConflictReason, err: layer.ErrSandboxProfileConflict}
	}
	if parentRec.Layer.ID != rec.Layer.ID {
		chain := authoredChain(parentRec, parentIdx, deduped, effective, layered)
		if err := executableInheritance(rec, *parentMerged, chain); err != nil {
			return nil, err
		}
	}
	merged := manifest.MergeExtends(*parentMerged, *rec.Artifact)
	return &merged, nil
}

// executableInheritance applies the §4.6 executable-field rule to the edge
// from rec to a parent in another layer. parent is the parent's
// chain-resolved artifact and parentChain its blocks, root first. rec's
// ArtifactBytes are its authored bytes while composeExtends validates, which
// is the pass whose result decides the refusal. A comparison that cannot be
// read refuses rec with layer.UnreadableExecutableFieldsReason, the reason the
// server gives, because its cause can name a withheld ancestor.
//
// Spec: §4.6, §13.11.3
func executableInheritance(rec ArtifactRecord, parent manifest.Artifact, parentChain []manifest.MergedBlock) error {
	fields, err := manifest.InheritedExecutableFields(rec.ID, parent, parentChain, rec.ArtifactBytes)
	if err != nil {
		return &refusedRecordError{id: rec.ID, layerID: rec.Layer.ID, reason: layer.UnreadableExecutableFieldsReason, err: err}
	}
	if len(fields) == 0 {
		return nil
	}
	ie := &layer.ExecutableInheritance{ArtifactID: rec.ID, Fields: fields}
	return &refusedRecordError{id: rec.ID, layerID: rec.Layer.ID, reason: ie.Reason(), err: ie}
}

// authoredChain returns the ARTIFACT.md bytes of rec's extends chain paired
// with each member's declared extends reference, parent first, which is the
// order manifest.SerializeMerged wants: a key the child also sets keeps the
// child's value. It walks the same links mergeRecord walks and stops on any it
// cannot follow, because a chain mergeRecord rejects never reaches the
// serializer.
//
// The reference comes from the parsed Artifact, which resolveExtends leaves
// naming the member's own parent even after it has replaced that member's
// ArtifactBytes with the merged block. Reading it back out of the bytes would
// make the chain the §4.6 strip checks depend on which records this loop has
// already processed.
func authoredChain(rec ArtifactRecord, layerIdx int, deduped []ArtifactRecord, effective map[string]int, layered map[string][]ArtifactRecord) []manifest.MergedBlock {
	var out []manifest.MergedBlock
	seen := map[string]bool{}
	cur, curIdx := rec, layerIdx
	for {
		key := fmt.Sprintf("%s#%d", cur.ID, curIdx)
		if seen[key] || cur.Artifact == nil || cur.Artifact.Extends == "" {
			break
		}
		seen[key] = true
		parentID := version.StripPin(cur.Artifact.Extends)
		if parentID == cur.ID {
			if curIdx-1 < 0 {
				break
			}
			curIdx--
			cur = layered[cur.ID][curIdx]
		} else {
			di, ok := effective[parentID]
			if !ok {
				break
			}
			cur = deduped[di]
			curIdx = len(layered[parentID]) - 1
		}
		out = append(out, blockOf(cur))
	}
	// Reverse into parent-first order, then append the child's own block last
	// so its value wins for a key both declare.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return append(out, blockOf(rec))
}

// blockOf pairs a record's manifest bytes with the extends reference its
// parsed manifest declares.
func blockOf(rec ArtifactRecord) manifest.MergedBlock {
	block := manifest.MergedBlock{Frontmatter: rec.ArtifactBytes}
	if rec.Artifact != nil {
		block.Extends = rec.Artifact.Extends
	}
	return block
}
