package store

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/lennylabs/podium/pkg/spi"
)

// ErrInvalidOverlapRecord is returned by ReplaceOverlapRecords when the
// computation entry names no artifact or a record is not owned by the
// computed artifact or names it as its own peer, which a correct computation
// never produces.
var ErrInvalidOverlapRecord = &spi.Error{Code: "registry.invalid_argument", Message: "store: invalid overlap record"}

// ErrOverlapSuperseded is returned by ReplaceOverlapRecords when the stored
// computation entry of the artifact is for another version and was computed
// after the given one. The call changes nothing. It is never reported to a
// client: the registry treats it as a computation that stored nothing.
var ErrOverlapSuperseded = errors.New("store: a later computation of another version is stored")

// supersededBy reports whether the stored entry prev supersedes comp: it is
// for another version and was computed after comp. The registry stamps a
// computation before it confirms its version is still served, so a
// computation of a newer version that stored after that confirmation carries
// the later time. The same version is never superseded, so two computations
// of one version both store, one after the other.
//
// Spec: §7.3.1
func supersededBy(prev, comp OverlapComputation) bool {
	return prev.Version != comp.Version && prev.ComputedAt.After(comp.ComputedAt)
}

// OverlapRecord is one §7.3.1 semantic overlap record: the cosine similarity
// of the owner's embedding projection at OwnerVersion and the peer's at
// PeerVersion, under one embedding model, at the time it was computed. The
// owner is the artifact whose computation stored the record, and a record is
// keyed by (Owner, Peer), so computing one artifact never replaces a record
// its peer owns.
//
// Spec: §7.3.1
type OverlapRecord struct {
	TenantID     string
	Owner        string
	OwnerVersion string
	Peer         string
	PeerVersion  string
	Similarity   float64
	// ModelID is the embedding model identifier the similarity was computed
	// under. A read reports the record only while it is the configured model.
	ModelID    string
	ComputedAt time.Time
}

// OverlapComputation is the §7.3.1 computation entry of one artifact: the
// version and the model of its last successful computation. The sweep reads
// it to tell a current artifact whose computation found no peer, and so owns
// no record, from one that was never computed.
//
// Spec: §7.3.1
type OverlapComputation struct {
	TenantID   string
	ArtifactID string
	Version    string
	ModelID    string
	ComputedAt time.Time
}

// normalizeOverlapComputation validates one computation's entry and records
// and returns them in canonical form: TenantID set to tenantID, times in UTC,
// and one record per peer, the last given winning, ordered by peer. Every
// backend applies it, so the backends store the same rows.
func normalizeOverlapComputation(tenantID string, comp OverlapComputation, recs []OverlapRecord) (OverlapComputation, []OverlapRecord, error) {
	if comp.ArtifactID == "" {
		return comp, nil, fmt.Errorf("%w: a computation entry names no artifact", ErrInvalidOverlapRecord)
	}
	comp.TenantID = tenantID
	comp.ComputedAt = comp.ComputedAt.UTC()
	byPeer := map[string]OverlapRecord{}
	for _, r := range recs {
		if r.Owner != comp.ArtifactID || r.Peer == "" || r.Peer == r.Owner {
			return comp, nil, fmt.Errorf("%w: owner %s and peer %s from a computation of %s", ErrInvalidOverlapRecord, r.Owner, r.Peer, comp.ArtifactID)
		}
		r.TenantID = tenantID
		r.ComputedAt = r.ComputedAt.UTC()
		byPeer[r.Peer] = r
	}
	out := make([]OverlapRecord, 0, len(byPeer))
	for _, r := range byPeer {
		out = append(out, r)
	}
	sortOverlapRecords(out)
	return comp, out, nil
}

// sortOverlapRecords orders records by owner and then peer, the order
// ListOverlapRecords returns on every backend.
func sortOverlapRecords(recs []OverlapRecord) {
	sort.Slice(recs, func(i, j int) bool {
		if recs[i].Owner != recs[j].Owner {
			return recs[i].Owner < recs[j].Owner
		}
		return recs[i].Peer < recs[j].Peer
	})
}

// sortOverlapComputations orders entries by artifact, the order
// ListOverlapComputations returns on every backend.
func sortOverlapComputations(entries []OverlapComputation) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].ArtifactID < entries[j].ArtifactID })
}
