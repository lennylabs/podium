package store

import (
	"fmt"
	"sort"
	"time"

	"github.com/lennylabs/podium/pkg/spi"
)

// ErrInvalidOverlapRecord is returned by ReplaceOverlapRecords when a record
// does not name the computed artifact on exactly one side, which a correct
// computation never produces.
var ErrInvalidOverlapRecord = &spi.Error{Code: "registry.invalid_argument", Message: "store: invalid overlap record"}

// OverlapRecord is one §7.3.1 semantic overlap record: the cosine similarity
// of two artifacts' embedding projections at two versions, under one embedding
// model, at the time it was computed. A record is keyed by the unordered pair
// of identifiers; a store returns it with ArtifactA ordered before ArtifactB.
//
// Spec: §7.3.1
type OverlapRecord struct {
	TenantID   string
	ArtifactA  string
	VersionA   string
	ArtifactB  string
	VersionB   string
	Similarity float64
	// ModelID is the embedding model identifier the similarity was computed
	// under. A read reports the record only while it is the configured model.
	ModelID    string
	ComputedAt time.Time
}

// Names reports whether the record names artifactID on either side.
func (r OverlapRecord) Names(artifactID string) bool {
	return r.ArtifactA == artifactID || r.ArtifactB == artifactID
}

// normalizeOverlapRecords validates the records one computation of artifactID
// stores and returns them in canonical form: TenantID set to tenantID, the
// pair ordered so ArtifactA < ArtifactB, the time in UTC, and one record per
// pair, the last given winning. Every backend applies it, so the stored key is
// the same whichever side computed the pair.
func normalizeOverlapRecords(tenantID, artifactID string, recs []OverlapRecord) ([]OverlapRecord, error) {
	byPair := map[[2]string]OverlapRecord{}
	for _, r := range recs {
		if !r.Names(artifactID) || r.ArtifactA == r.ArtifactB {
			return nil, fmt.Errorf("%w: %s and %s from a computation of %s", ErrInvalidOverlapRecord, r.ArtifactA, r.ArtifactB, artifactID)
		}
		if r.ArtifactB < r.ArtifactA {
			r.ArtifactA, r.ArtifactB = r.ArtifactB, r.ArtifactA
			r.VersionA, r.VersionB = r.VersionB, r.VersionA
		}
		r.TenantID = tenantID
		r.ComputedAt = r.ComputedAt.UTC()
		byPair[[2]string{r.ArtifactA, r.ArtifactB}] = r
	}
	out := make([]OverlapRecord, 0, len(byPair))
	for _, r := range byPair {
		out = append(out, r)
	}
	sortOverlapRecords(out)
	return out, nil
}

// sortOverlapRecords orders records by pair, the order ListOverlapRecords
// returns on every backend.
func sortOverlapRecords(recs []OverlapRecord) {
	sort.Slice(recs, func(i, j int) bool {
		if recs[i].ArtifactA != recs[j].ArtifactA {
			return recs[i].ArtifactA < recs[j].ArtifactA
		}
		return recs[i].ArtifactB < recs[j].ArtifactB
	})
}
