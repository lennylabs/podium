package filesystem

import (
	"fmt"
	"strings"

	"github.com/lennylabs/podium/pkg/manifest"
)

// resolveExtends folds the extends: chain of every deduped record per the
// §4.6 field-semantics table, using the shared manifest.MergeExtends so a
// filesystem-source materialization matches the registry's load-time
// assembly (§13.11.3). For each record that declares extends:, it resolves
// the parent chain, merges parent → child, and rewrites the record's
// ArtifactBytes (extends stripped per §4.6 hidden-parent privacy) and parsed
// Artifact in place.
//
// Two parent forms are supported, matching the server resolver:
//
//   - A different canonical ID names a separate artifact; the parent is the
//     effective (highest-precedence) record for that ID.
//   - The same canonical ID names a lower-precedence layer's artifact (the
//     §4.6 same-ID overlay exception); the parent is the record immediately
//     below the child in layer order.
//
// all is the full pre-dedup record set in layer order (low to high
// precedence), which is the only place the lower-precedence same-ID parents
// survive after dedup.
func resolveExtends(deduped, all []ArtifactRecord) error {
	// effective maps a canonical ID to its highest-precedence record, used to
	// resolve a different-ID parent reference.
	effective := make(map[string]int, len(deduped))
	for i, r := range deduped {
		effective[r.ID] = i
	}
	// layered groups every record by canonical ID in low-to-high precedence
	// order, used to resolve a same-ID extends to the next-lower layer.
	layered := map[string][]ArtifactRecord{}
	for _, r := range all {
		layered[r.ID] = append(layered[r.ID], r)
	}

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
		// Re-serialize the merged manifest with extends stripped so every
		// consumer of ArtifactBytes observes the resolved frontmatter (§4.6).
		stripped := *merged
		stripped.Extends = ""
		bytes, serr := manifest.SerializeArtifact(&stripped)
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
// server's load-time cycle re-check (§4.6).
func mergeRecord(rec ArtifactRecord, layerIdx int, deduped []ArtifactRecord, effective map[string]int, layered map[string][]ArtifactRecord, seen map[string]bool) (*manifest.Artifact, error) {
	if rec.Artifact == nil || rec.Artifact.Extends == "" {
		return rec.Artifact, nil
	}
	key := fmt.Sprintf("%s#%d", rec.ID, layerIdx)
	if seen[key] {
		return nil, fmt.Errorf("extends.cycle: extends cycle at %q", rec.ID)
	}
	seen[key] = true

	parentID := stripPin(rec.Artifact.Extends)

	var parentRec ArtifactRecord
	var parentIdx int
	if parentID == rec.ID {
		// Same-ID overlay: the parent is the record immediately below this
		// one in layer order.
		if layerIdx-1 < 0 {
			return nil, fmt.Errorf("extends.unresolved: %q extends its own id %q but no lower-precedence layer provides it", rec.ID, parentID)
		}
		parentIdx = layerIdx - 1
		parentRec = layered[rec.ID][parentIdx]
	} else {
		di, ok := effective[parentID]
		if !ok {
			return nil, fmt.Errorf("extends.unresolved: %q extends %q which is not present in the registry", rec.ID, parentID)
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
	merged := manifest.MergeExtends(*parentMerged, *rec.Artifact)
	return &merged, nil
}

// stripPin returns the canonical ID portion of an extends reference,
// dropping any "@version" or "@sha256:..." pin. Filesystem layers are the
// versioning mechanism, so parent selection is by canonical ID and layer
// precedence rather than by pin resolution.
func stripPin(ref string) string {
	if i := strings.Index(ref, "@"); i >= 0 {
		return ref[:i]
	}
	return ref
}
