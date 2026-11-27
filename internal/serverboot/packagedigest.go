package serverboot

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/store"
)

// fillOutcome is what the package digest fill did with one stored row.
type fillOutcome int

const (
	// fillCurrent: the row already stores the digest its bytes give.
	fillCurrent fillOutcome = iota
	// fillWritten: the fill stored the row's digest.
	fillWritten
	// fillNoEntryList: the row's stored files give no entry list, so it
	// keeps no digest. Ingest never stores such a row.
	fillNoEntryList
	// fillSuperseded: another writer changed the row's content hash or
	// removed the row between the read and the write.
	fillSuperseded
	// fillFailed: the write failed, which holds the completion record back.
	fillFailed
)

// fillCounts tallies the outcomes of one fill, for its summary line.
type fillCounts [fillFailed + 1]int

// fillPackageDigests stores the §4.7.6 package digest of every stored row,
// once per store. It runs on a start whose listener bound, before the
// bootstrap ingest and before anything is served, and returns at once when
// the completion record is present. Each row's digest comes from its stored
// manifest, SKILL.md, and resource content hashes through
// attest.StoredPackageDigest, which resolves the fields with the function
// §13.4 stored-row admission applies, so the stored digest frames the values
// a load of the row serves. It reads no object store.
//
// A row whose write fails holds the completion record back, so the next
// start runs the fill again; the start continues, because a row without a
// digest is served without the package_digest member. A row another writer
// changed or removed, and a row whose files give no entry list, do not hold
// the record back. A failure to list the rows or to read or write the record
// fails the start.
//
// Spec: §13.4 — the package digest fill.
func fillPackageDigests(ctx context.Context, st store.Store) error {
	applied, err := st.DataMigrationApplied(ctx, store.DataMigrationPackageDigest)
	if err != nil {
		return fmt.Errorf("package digest fill: read data-migration marker: %w", err)
	}
	if applied {
		return nil
	}
	tenants, err := st.ListTenants(ctx)
	if err != nil {
		return fmt.Errorf("package digest fill: list tenants: %w", err)
	}
	var counts fillCounts
	for _, t := range tenants {
		recs, err := st.ListManifestsIncludingDeleted(ctx, t.ID)
		if err != nil {
			return fmt.Errorf("package digest fill: list manifests for tenant %s: %w", t.ID, err)
		}
		for _, rec := range recs {
			counts[fillRow(ctx, st, rec)]++
		}
	}
	log.Printf("package digest fill: %d written, %d already current, %d without an entry list, %d changed by another writer, %d in error",
		counts[fillWritten], counts[fillCurrent], counts[fillNoEntryList], counts[fillSuperseded], counts[fillFailed])
	if counts[fillFailed] > 0 {
		log.Printf("package digest fill: %d row(s) failed; the next start runs the fill again", counts[fillFailed])
		return nil
	}
	if err := st.SetDataMigrationApplied(ctx, store.DataMigrationPackageDigest, true); err != nil {
		return fmt.Errorf("package digest fill: record completion: %w", err)
	}
	return nil
}

// fillRow computes and, when it differs from the stored value, writes the
// package digest of one row under the row's stored content hash. A row whose
// files give no entry list is written an empty digest, so no load serves a
// digest its bytes do not give.
func fillRow(ctx context.Context, st store.Store, rec store.ManifestRecord) fillOutcome {
	key := fmt.Sprintf("%s/%s@%s", rec.TenantID, rec.ArtifactID, rec.Version)
	outcome := fillWritten
	digest, err := storedRowDigest(rec)
	if err != nil {
		log.Printf("package digest fill: %s keeps no package digest: %v", key, err)
		outcome = fillNoEntryList
	}
	if digest == rec.PackageDigest {
		if outcome == fillNoEntryList {
			return outcome
		}
		return fillCurrent
	}
	err = st.SetPackageDigest(ctx, rec.TenantID, rec.ArtifactID, rec.Version, rec.ContentHash, digest)
	switch {
	case err == nil:
		return outcome
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrImmutableViolation):
		return fillSuperseded
	default:
		log.Printf("package digest fill: %s: %v", key, err)
		return fillFailed
	}
}

// storedRowDigest is the §4.7.6 package digest of a stored row, from its
// stored documents and the path and content hash of each resource.
func storedRowDigest(rec store.ManifestRecord) (string, error) {
	resources := make([]attest.StoredResource, 0, len(rec.Resources))
	for _, ref := range rec.Resources {
		resources = append(resources, attest.StoredResource{Path: ref.Path, ContentHash: ref.ContentHash})
	}
	return attest.StoredPackageDigest(rec.Frontmatter, rec.SkillRaw, resources)
}
