package ingest

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/podium/internal/clock"
	"github.com/lennylabs/podium/pkg/store"
)

// bundleReadStore answers every bundle read with err.
type bundleReadStore struct {
	store.Store
	err error
}

func (s bundleReadStore) GetAttestation(context.Context, string, string) (store.StoredAttestation, error) {
	return store.StoredAttestation{}, s.err
}

// Spec: §4.7.11 (Re-evaluation) — a remaining link whose bundle read fails
// fails the re-evaluation, which then writes nothing, rather than count the
// link for every requirement; a bundle the store no longer holds counts for
// no requirement.
func TestReevaluation_RemainingLinkReadFailsClosed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	reqs := []store.TrustRequirement{{CoversLayer: true}, {}}
	kept := store.AttestationLink{ArtifactID: "a", Version: "1.0.0", PredicateType: "p", Instance: "github",
		SignerIssuer: "i", SignerIdentity: "build", Digest: "sha256:kept", Verdict: store.VerdictVerified, VerifiedUnder: "k"}
	lost := kept
	lost.PredicateType, lost.SignerIdentity, lost.Digest, lost.VerifiedUnder = "scan", "scanner", "sha256:lost", "old"
	r := &reevaluation{st: bundleReadStore{Store: store.NewMemory(), err: errors.New("disk")}, layer: store.LayerConfig{TrustRequirements: reqs},
		policy: policyState{mode: "require", key: "k", holds: true}, clock: clock.Real{}}
	if _, err := r.remainingRequirements(ctx, kept, nil); err == nil {
		t.Fatal("a failed bundle read counted the link")
	}
	checks := map[checkKey]linkCheck{keyOf(lost): {decided: true}}
	if _, err := r.writes(ctx, []store.AttestationLink{lost, kept}, []store.AttestationLink{lost}, checks); err == nil {
		t.Fatal("the downgrade wrote over a failed bundle read")
	}
	r.st = bundleReadStore{Store: store.NewMemory(), err: store.ErrNotFound}
	if got, err := r.remainingRequirements(ctx, kept, nil); err != nil || len(got) != 0 {
		t.Fatalf("a bundle the store no longer holds: %v, %v", got, err)
	}
	w, err := r.writes(ctx, []store.AttestationLink{lost, kept}, []store.AttestationLink{lost}, checks)
	if err != nil || len(w.Versions) != 1 || w.Versions[0].Outcome == nil || *w.Versions[0].Outcome.Failure.Requirement != 0 {
		t.Fatalf("writes = %+v, %v", w, err)
	}
}
