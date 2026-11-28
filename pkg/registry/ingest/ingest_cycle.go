package ingest

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// supersededResult is the return of a cycle that stopped at a check: the
// result of a superseded cycle, which removed nothing, or the error.
func supersededResult(superseded bool, err error) (*Result, error) {
	if err != nil {
		return nil, err
	}
	return &Result{Removed: []string{}, Superseded: superseded}, nil
}

// checkClaim is the claim check of a cycle that runs no attestation step: a
// cycle that a later cycle of the layer has already superseded stores
// nothing, because its puts would restore versions the later cycle removed.
// It runs OnFirstWrite once the check passes, because every return after it
// follows a store write or an attempted one.
//
// Spec: §7.3.1 (Removed artifacts)
func checkClaim(ctx context.Context, st store.Store, req Request, now time.Time) (superseded bool, err error) {
	if req.IngestClaim != "" {
		err := st.SoftDeleteArtifacts(ctx, req.TenantID, req.LayerID, nil, now, req.IngestClaim)
		switch {
		case errors.Is(err, store.ErrIngestSuperseded):
			return true, nil
		case err != nil:
			return false, fmt.Errorf("ingest: check the ingest claim: %w", err)
		}
	}
	if req.OnFirstWrite != nil {
		req.OnFirstWrite()
	}
	return false, nil
}

// persistDomains stores every DOMAIN.md record of the snapshot and returns
// the newly-unlisted advisories. A DOMAIN.md that is newly added, or whose
// source changed since the previous ingest, emits domain.published.
//
// Spec: §4.5.1, §8.1, §12
func persistDomains(ctx context.Context, st store.Store, req Request, domainRecs []store.DomainRecord) ([]lint.Diagnostic, error) {
	prevDomains := map[string]string{}
	if existing, lerr := st.ListDomains(ctx, req.TenantID); lerr == nil {
		for _, d := range existing {
			if d.Layer == req.LayerID {
				prevDomains[d.Path] = string(d.Raw)
			}
		}
	}
	var domainAdvisories []lint.Diagnostic
	for _, dr := range domainRecs {
		prev, seen := prevDomains[dr.Path]
		if err := st.PutDomain(ctx, dr); err != nil {
			return nil, err
		}
		if !seen || prev != string(dr.Raw) {
			// §7.3.2 outbound webhook + §8.1 audit: a DOMAIN.md that was
			// added or whose source changed emits domain.published. Both
			// the change-event seam and the audit sink fire so receivers
			// and SIEM pipelines see one event per real change.
			if req.PublishEvent != nil {
				scope := core.EventScope{TenantID: dr.TenantID, Layers: []string{dr.Layer}, Path: dr.Path}
				req.PublishEvent(ctx, scope, string(audit.EventDomainPublished), map[string]any{
					"domain": dr.Path,
					"layer":  dr.Layer,
					"tenant": dr.TenantID,
				})
			}
			if req.AuditEmit != nil {
				req.AuditEmit(string(audit.EventDomainPublished), dr.Path,
					map[string]string{"layer": dr.Layer})
			}
		}
		// spec: §12 — "ingest-time lint flags newly-set unlisted: true for
		// review". An unlisted: true DOMAIN.md hides its whole
		// subtree from enumeration (§4.5.3), so a freshly-set value is
		// surfaced as an advisory. A domain that was already unlisted in the
		// prior ingest draws no advisory (the flag is not newly-set), so a
		// steady-state reingest stays quiet.
		if adv := newlyUnlistedAdvisory(dr, prev, seen); adv != nil {
			domainAdvisories = append(domainAdvisories, *adv)
		}
	}
	return domainAdvisories, nil
}

// drop reports whether the artifact step dropped id, with the rejection the
// cycle reports for it. A nil verdict drops nothing.
//
// Spec: §4.7.11 (Artifact step)
func (v *attestationVerdict) drop(id string) (RejectedArtifact, bool) {
	if v == nil {
		return RejectedArtifact{}, false
	}
	f, ok := v.drops[id]
	if !ok {
		return RejectedArtifact{}, false
	}
	return RejectedArtifact{ArtifactID: id, Reason: f.Message, Code: f.Code, Details: f.Details}, true
}

// report is the Result member of the verdict of a cycle that ran the
// attestation steps, nil for one that ran none.
func (v *attestationVerdict) report(a *AttestationCycle) *AttestationReport {
	if v == nil || !a.verifies() {
		return nil
	}
	r := &AttestationReport{Mode: a.mode, Status: store.AttestationStatusOK, Outcomes: v.outcomes,
		LayerAttestedTime: v.layerTime, AttestedCommit: v.commit}
	if v.failure != nil {
		r.Status, r.Details = v.failure.Code, v.failure.Details
	}
	return r
}
