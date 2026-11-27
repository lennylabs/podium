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
	// fillRemoved: another writer removed the row between the read and the
	// write, so it needs no digest.
	fillRemoved
	// fillFailed: the write failed, or the row's content hash changed again
	// after the fill read it a second time, which holds the completion record
	// back.
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
// A row whose content hash another writer changed after the read, such as
// through a second replica's content-hash rewrite, is read again and
// computed from the bytes it then holds, because the bytes decide the
// digest. A row whose write fails, or whose hash changes again, holds the
// completion record back, so the next start runs the fill again; the start
// continues, because a row without a digest is served without the
// package_digest member. A row another writer removed, and a row whose files
// give no entry list, do not hold the record back. A failure to list the rows
// or to read or write the record fails the start.
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
	log.Printf("package digest fill: %d written, %d already current, %d without an entry list, %d removed by another writer, %d in error",
		counts[fillWritten], counts[fillCurrent], counts[fillNoEntryList], counts[fillRemoved], counts[fillFailed])
	if counts[fillFailed] > 0 {
		log.Printf("package digest fill: %d row(s) failed; the next start runs the fill again", counts[fillFailed])
		return nil
	}
	if err := st.SetDataMigrationApplied(ctx, store.DataMigrationPackageDigest, true); err != nil {
		return fmt.Errorf("package digest fill: record completion: %w", err)
	}
	return nil
}

// fillRow fills one row. A write that finds the row's content hash changed
// reads the row again and fills it once more from its current bytes, and a
// second change holds the completion record back.
func fillRow(ctx context.Context, st store.Store, rec store.ManifestRecord) fillOutcome {
	outcome, err := fillOnce(ctx, st, rec)
	if !errors.Is(err, store.ErrImmutableViolation) {
		return settle(rec, outcome, err)
	}
	current, found, err := rereadRow(ctx, st, rec)
	if err != nil {
		log.Printf("package digest fill: %s: read again: %v", rowKey(rec), err)
		return fillFailed
	}
	if !found {
		return fillRemoved
	}
	outcome, err = fillOnce(ctx, st, current)
	return settle(current, outcome, err)
}

// fillOnce computes and, when it differs from the stored value, writes the
// package digest of one row under the row's stored content hash. A row whose
// files give no entry list is written an empty digest, so no load serves a
// digest its bytes do not give. It returns the write's error for settle or
// fillRow to map.
func fillOnce(ctx context.Context, st store.Store, rec store.ManifestRecord) (fillOutcome, error) {
	outcome := fillWritten
	digest, err := storedRowDigest(rec)
	if err != nil {
		log.Printf("package digest fill: %s keeps no package digest: %v", rowKey(rec), err)
		outcome = fillNoEntryList
	}
	if digest == rec.PackageDigest {
		if outcome == fillNoEntryList {
			return outcome, nil
		}
		return fillCurrent, nil
	}
	return outcome, st.SetPackageDigest(ctx, rec.TenantID, rec.ArtifactID, rec.Version, rec.ContentHash, digest)
}

// settle maps the write error of a row's last attempt to its outcome. A
// removed row needs no digest. Any other error, a changed hash included,
// holds the completion record back.
func settle(rec store.ManifestRecord, outcome fillOutcome, err error) fillOutcome {
	switch {
	case err == nil:
		return outcome
	case errors.Is(err, store.ErrNotFound):
		return fillRemoved
	default:
		log.Printf("package digest fill: %s: %v", rowKey(rec), err)
		return fillFailed
	}
}

// rereadRow returns the row rec names as the store now holds it, a
// soft-deleted row included, and false when the row is gone. The store reads
// a soft-deleted row only through the tenant's full listing, and the fill
// reaches this path only for a row another writer changed.
func rereadRow(ctx context.Context, st store.Store, rec store.ManifestRecord) (store.ManifestRecord, bool, error) {
	recs, err := st.ListManifestsIncludingDeleted(ctx, rec.TenantID)
	if err != nil {
		return store.ManifestRecord{}, false, err
	}
	for _, r := range recs {
		if r.ArtifactID == rec.ArtifactID && r.Version == rec.Version {
			return r, true, nil
		}
	}
	return store.ManifestRecord{}, false, nil
}

// rowKey names a row in a log line by its coordinates.
func rowKey(rec store.ManifestRecord) string {
	return fmt.Sprintf("%s/%s@%s", rec.TenantID, rec.ArtifactID, rec.Version)
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
