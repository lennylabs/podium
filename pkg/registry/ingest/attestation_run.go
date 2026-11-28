package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/store"
)

// run applies the attestation steps to the snapshot files: the layer step
// over all, the discovery walk before any refusal, then the artifact step
// over kept, the artifacts the pre-lint rules keep, and the record write. It
// runs before the cycle writes any domain record, artifact, removal, or
// link. superseded reports a record write that a later claim refused.
//
// Under require a layer-step failure refuses the cycle: run returns the
// failure, wrapping ErrAttestationRefused, after the record write. Under
// warn the cycle continues with the failure in the verdict. An error outside
// the attestation steps, such as a lineage query the source cannot answer,
// is returned with no record, apart from a Git fetch that reached its
// deadline.
//
// Spec: §4.7.11 (Ingest verification, Attestation record)
func (a *AttestationCycle) run(ctx context.Context, st store.Store, files fs.FS, all, kept []filesystem.ArtifactRecord, onWrite func()) (*attestationVerdict, bool, error) {
	if err := a.readVisibility(files); err != nil {
		return nil, false, err
	}
	res, failure, err := a.layerStep(ctx, files, all)
	if err != nil {
		if _, rerr := a.RecordSourceFailure(ctx, st, err, onWrite); rerr != nil {
			return nil, false, rerr
		}
		return nil, false, err
	}
	if failure != nil {
		return a.failedLayerStep(ctx, st, failure, kept, onWrite)
	}
	v := &attestationVerdict{layerTime: res.time, commit: res.commit, bundles: res.bundles}
	v.drops, v.outcomes = a.artifactStep(res.bundles, kept)
	rec := a.layer.AttestationRecord
	superseded, err := a.write(ctx, st, store.AttestationWrite{
		Status: store.AttestationStatusOK, Dropped: len(v.drops),
		Advance: true, Mark: res.time, Commit: res.commit, ReadMark: rec.Mark, ReadCommit: rec.Commit,
		Visibility: a.recordVisibility(true),
	}, onWrite)
	if superseded || err != nil {
		return nil, superseded, err
	}
	a.emitVerified(res, v)
	return v, false, nil
}

// failedLayerStep writes the record of a failed layer step and refuses the
// cycle under require. Under warn each version the cycle stores carries the
// outcome failed with the layer step's code and the scope layer, apart from
// an artifact of a type trust_require_types lists, which the cycle withholds
// as an artifact-step failure, with the layer step's code and details, and
// counts among the dropped artifacts of the record.
//
// Spec: §4.7.11 (Layer step, Attestation outcome, Attestation record)
func (a *AttestationCycle) failedLayerStep(ctx context.Context, st store.Store, f *AttestationError, kept []filesystem.ArtifactRecord, onWrite func()) (*attestationVerdict, bool, error) {
	f.Refusal = a.mode == layer.TrustModeRequire
	v := &attestationVerdict{failure: f, outcomes: map[string]AttestationOutcome{}, drops: map[string]*AttestationError{},
		bundles: a.evaluated, requireTypes: a.layer.TrustRequireTypes}
	if !f.Refusal {
		for i := range kept {
			rec := &kept[i]
			if a.requiredType(rec) {
				v.drops[rec.ID] = &AttestationError{Code: f.Code, Details: f.Details,
					Message: fmt.Sprintf("%s: the layer step failed, and trust_require_types lists the type of %s", f.Code, attest.ReportString(rec.ID))}
				continue
			}
			v.outcomes[rec.ID] = AttestationOutcome{Outcome: OutcomeFailed, Code: f.Code, Scope: "layer", Constraint: f.Constraint()}
		}
	}
	w := store.AttestationWrite{Status: f.Code, Details: f.Details, Dropped: len(v.drops), Visibility: a.recordVisibility(false)}
	if superseded, err := a.write(ctx, st, w, onWrite); superseded || err != nil {
		return nil, superseded, err
	}
	a.emitFailed(f, a.reference)
	if f.Refusal {
		return nil, false, f
	}
	return v, false, nil
}

// requiredType reports whether trust_require_types lists the type of rec,
// read from its parsed manifest and from the resolved field the package
// digest frames, so a record whose two readings differ is held to the
// listed type.
//
// Spec: §4.7.11 (Layer trust policy)
func (a *AttestationCycle) requiredType(rec *filesystem.ArtifactRecord) bool {
	types := a.layer.TrustRequireTypes
	if rec.Artifact != nil && slices.Contains(types, string(rec.Artifact.Type)) {
		return true
	}
	return slices.Contains(types, string(rec.Fields.Type))
}

// artifactStep applies the §4.7.11 artifact step to each artifact the
// pre-lint rules kept, after a passed layer step: for each requirement that
// is not optional, a subject with the artifact's subject name and package
// digest in a bundle the requirement accepts. An optional requirement fails
// nothing; the cycle links what it accepts. An artifact that fails a
// requirement is dropped under require and when trust_require_types lists
// its type, and is stored with the outcome failed under warn otherwise. The
// layer step's completeness condition already places every artifact under
// the covering requirement, so a failure reports an additional requirement,
// the first in policy order that the artifact fails.
//
// Spec: §4.7.11 (Artifact step, Attestation outcome)
func (a *AttestationCycle) artifactStep(bundles []*snapshotBundle, kept []filesystem.ArtifactRecord) (map[string]*AttestationError, map[string]AttestationOutcome) {
	drops := map[string]*AttestationError{}
	outcomes := map[string]AttestationOutcome{}
	for i := range kept {
		rec := &kept[i]
		name, _ := rec.SubjectName()
		out := AttestationOutcome{Outcome: OutcomeVerified}
		for r, req := range a.layer.TrustRequirements {
			if req.Optional || coveredBy(bundles, r, name, rec.PackageDigest) {
				continue
			}
			f := artifactFailure(bundles, r, rec.ID, name)
			out = AttestationOutcome{Outcome: OutcomeFailed, Code: f.Code, Scope: "artifact", Constraint: f.Constraint(), Requirement: r}
			if a.mode == layer.TrustModeRequire || a.requiredType(rec) {
				drops[rec.ID] = f
			}
			break
		}
		outcomes[rec.ID] = out
	}
	return drops, outcomes
}

// artifactFailure is the artifact step's failure of requirement r for the
// artifact id with subject name name: ingest.attestation_mismatch when an
// accepted bundle lists the ID with another digest (digest) or another
// version (version), and ingest.attestation_missing with the scope artifact
// when no accepted bundle lists the ID.
func artifactFailure(bundles []*snapshotBundle, r int, id, name string) *AttestationError {
	constraint := ""
	for _, b := range bundles {
		v := b.accepted[r]
		if v == nil {
			continue
		}
		for _, s := range v.Statement.Subjects {
			sid, _ := attest.ParseSubjectName(s.Name)
			switch {
			case s.Name == name:
				constraint = "digest"
			case sid == id && constraint == "":
				constraint = "version"
			}
		}
	}
	if constraint == "" {
		return attestFailure(CodeAttestationMissing, "", fmt.Sprintf("no accepted bundle of requirement %d lists %s", r, attest.ReportString(id)),
			"scope", "artifact", "requirement", r)
	}
	return attestFailure(CodeAttestationMismatch, constraint, fmt.Sprintf("the subject of %s in requirement %d has another %s", attest.ReportString(id), r, constraint),
		"scope", "artifact", "requirement", r)
}

// emitVerified records layer.attestation_verified: the snapshot reference,
// the layer attested time, each accepted bundle, the counts of matched,
// mismatched, and missing artifacts, and the bundles no requirement accepted
// with the reason for each. No event carries bundle bytes.
//
// Spec: §8.1, §4.7.11 (Layer step)
func (a *AttestationCycle) emitVerified(res *layerStepResult, v *attestationVerdict) {
	if a.audit == nil {
		return
	}
	var accepted []map[string]any
	for _, b := range res.bundles {
		for r, va := range b.accepted {
			if va == nil {
				continue
			}
			accepted = append(accepted, map[string]any{
				"digest": b.digest, "requirement": r, "predicate_type": attest.ReportString(va.Statement.PredicateType),
				"instance": va.Instance, "issuer": attest.ReportString(va.Certificate.Issuer.Value),
				"identity": attest.ReportString(va.Certificate.Identity.Value), "log_index": logIndex(b.raw),
				"attested_at": va.AttestedTime.UTC().Format(time.RFC3339Nano), "rsa_timestamp_without_sha256": va.RSATimestampWithoutSHA256,
			})
		}
	}
	counts := map[string]int{}
	for _, o := range v.outcomes {
		switch {
		case o.Outcome == OutcomeVerified:
			counts["matched"]++
		case o.Code == CodeAttestationMismatch:
			counts["mismatched"]++
		default:
			counts["missing"]++
		}
	}
	a.audit(string(audit.EventLayerAttestationVerified), a.layer.ID, map[string]string{
		"mode":              a.mode,
		"reference":         a.reference,
		"layer_attested_at": res.time.UTC().Format(time.RFC3339Nano),
		"attested_commit":   attest.ReportString(res.commit),
		"bundles":           jsonText(accepted),
		"matched":           strconv.Itoa(counts["matched"]),
		"mismatched":        strconv.Itoa(counts["mismatched"]),
		"missing":           strconv.Itoa(counts["missing"]),
		"refused":           jsonText(unacceptedBundles(res.bundles)),
	})
}

// emitFailed records layer.attestation_failed with the §6.10 code, the
// constraint, the mode in force, and the snapshot reference where known.
//
// Spec: §8.1
func (a *AttestationCycle) emitFailed(f *AttestationError, reference string) {
	if a.audit == nil {
		return
	}
	a.audit(string(audit.EventLayerAttestationFailed), a.layer.ID, map[string]string{
		"mode":       a.mode,
		"reference":  reference,
		"code":       f.Code,
		"constraint": f.Constraint(),
		"details":    jsonText(f.Details),
	})
}

// unacceptedBundles lists every bundle no requirement accepted, with its
// reason.
func unacceptedBundles(bundles []*snapshotBundle) []map[string]string {
	out := []map[string]string{}
	for _, b := range bundles {
		if !b.acceptedByAny() {
			out = append(out, map[string]string{"digest": b.digest, "reason": string(b.reason)})
		}
	}
	return out
}

// jsonText encodes v for a string member of an audit event.
func jsonText(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return ""
	}
	return strings.TrimSuffix(b.String(), "\n")
}
