package ingest

import (
	"context"
	"fmt"
	"log"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/version"
)

// autoVersionDuplicateConstraint is the details.constraint value of the
// ingest.invalid_artifact rejection of a skill whose registry-assigned version
// would give content a stored row already holds a second version.
//
// Spec: §7.3.1
const autoVersionDuplicateConstraint = "auto_version_duplicate_content"

// autoVersionDuplicateReason is the reason of that rejection. It names no
// version and no layer, because the matching row can belong to another layer
// or be soft-deleted.
//
// Spec: §7.3.1
const autoVersionDuplicateReason = "content matches an earlier stored version of this skill, " +
	"whose version the registry assigns; set metadata.version in SKILL.md or change the content"

// autoVersionAttempts bounds the assignments one ingest call makes for a
// record whose version the registry assigns. Each lost attempt means another
// ingest committed a row for the same artifact between this call's listing
// and its write, so the bound is a guard against a livelock rather than a
// deployment setting, and it is not operator-tunable. An exhausted bound
// reports the §7.3.1 conflict, and a later ingest assigns the version.
const autoVersionAttempts = 3

// listManifestRows reads every manifest row of the tenant, soft-deleted rows
// included, and groups the rows by artifact ID.
//
// Spec: §7.3.1
func listManifestRows(ctx context.Context, st store.Store, tenantID string) (map[string][]store.ManifestRecord, error) {
	all, err := st.ListManifestsIncludingDeleted(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("ingest: list manifests: %w", err)
	}
	byID := map[string][]store.ManifestRecord{}
	for _, m := range all {
		byID[m.ArtifactID] = append(byID[m.ArtifactID], m)
	}
	return byID, nil
}

// crossLayerRows returns the non-deleted rows of rowsByID that a layer other
// than layerID contributed, grouped by artifact ID. A soft-deleted row is
// served by no read path, so it is not a §4.6 collision.
//
// Spec: §4.6
func crossLayerRows(rowsByID map[string][]store.ManifestRecord, layerID string) map[string][]store.ManifestRecord {
	out := map[string][]store.ManifestRecord{}
	for id, rows := range rowsByID {
		for _, m := range rows {
			if m.Layer != layerID && m.DeletedAt == nil {
				out[id] = append(out[id], m)
			}
		}
	}
	return out
}

// assignVersion picks the registry-assigned version for a skill that declares
// none, from rows (every stored row of the skill's artifact ID, soft-deleted
// rows included), the ingesting layer, and the incoming content hash:
//
//  1. When the layer's own row, the most recently ingested non-deleted row the
//     layer contributed at any version (ordered as `latest` orders
//     candidates), carries hash, its version is reused. That version is always
//     registry-assigned, because a declared version lies in the hashed bytes.
//  2. Otherwise, when any row carries hash, duplicate is true, so no two rows
//     of one artifact ID hold one content hash under different versions.
//  3. Otherwise the version is 0.0.0-auto.<N> with N one more than the highest
//     N any row carries, or 0 when no row carries the form.
//
// Soft-deleted rows join the duplicate set and the N derivation, because
// restoring their layer (§8.4) serves them again and their keys still block
// an insert. They never supply the reused version: GetManifest hides a
// tombstoned row and PutManifest leaves its key tombstoned, so reusing one
// would report an accepted skill that is not served. Comparing against any
// earlier row of the layer, rather than its latest one, would map a revert
// back to an older row while `latest` serves the newer one.
//
// assignVersion reads no store and no clock.
//
// Spec: §7.3.1, §4.7.6
func assignVersion(rows []store.ManifestRecord, layerID, hash string) (v string, duplicate bool) {
	var own []version.Candidate
	ownHash := map[string]string{}
	maxN := -1
	for _, r := range rows {
		if r.Layer == layerID && r.DeletedAt == nil {
			own = append(own, version.Candidate{Version: r.Version, IngestedAt: r.IngestedAt})
			ownHash[r.Version] = r.ContentHash
		}
		if r.ContentHash == hash {
			duplicate = true
		}
		if n, ok := manifest.IsAutoVersion(r.Version); ok && n > maxN {
			maxN = n
		}
	}
	if latest, err := version.ResolveLatest(own); err == nil && ownHash[latest] == hash {
		return latest, false
	}
	if duplicate {
		return "", true
	}
	return manifest.AutoVersion(maxN + 1), false
}

// autoVersionDuplicateRejection builds the §7.3.1 rejection of a skill whose
// assigned version would duplicate stored content, and logs identifiers only.
//
// Spec: §7.3.1
func autoVersionDuplicateRejection(tenantID, layerID, id string) *RejectedArtifact {
	log.Printf("ingest: tenant=%s layer=%s artifact=%s rejected: %s", tenantID, layerID, id, autoVersionDuplicateConstraint)
	return &RejectedArtifact{
		ArtifactID: id,
		Reason:     autoVersionDuplicateReason,
		Code:       layer.InvalidArtifactCode,
		Details:    map[string]any{"constraint": autoVersionDuplicateConstraint},
	}
}
