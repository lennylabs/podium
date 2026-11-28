package ingest

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"time"

	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/sign"
)

// Bounds of the lists a details object carries, which keep the
// attestation_details of a layer below 32 KiB.
const (
	detailsBundles  = 5
	detailsNames    = 5
	detailsSubjects = 20
	detailsFields   = 5
)

// layerStepResult is a layer step that passed: the attested time and commit
// of the layer bundle, and the evaluated bundles the artifact step reads.
type layerStepResult struct {
	time    time.Time
	commit  string
	bundles []*snapshotBundle
}

// layerStep runs the §4.7.11 layer step over the snapshot files, whose
// discovery walk before any refusal is all. It returns the passed step, or a
// failure, or an error that fails the cycle outside the attestation steps,
// such as a lineage query the source cannot answer.
//
// Spec: §4.7.11 (Layer step)
func (a *AttestationCycle) layerStep(ctx context.Context, files fs.FS, all []filesystem.ArtifactRecord) (*layerStepResult, *AttestationError, error) {
	if f, err := a.checkRoots(ctx); f != nil || err != nil {
		return nil, f, err
	}
	covering := layer.CoveringRequirement(a.layer.TrustRequirements)
	if covering < 0 {
		// Unreachable: the policy check (CheckPolicy) refuses a policy with
		// no covering requirement before the snapshot is read. A cycle that
		// skipped it fails rather than verify against no requirement.
		return nil, nil, fmt.Errorf("ingest: the trust policy of layer %s has no covering requirement", a.layer.ID)
	}
	bundles, f, err := a.readBundles(files)
	if f != nil || err != nil {
		return nil, f, err
	}
	if f := subjectNameFailure(all); f != nil {
		return nil, f, nil
	}
	for _, b := range bundles {
		if err := a.evaluate(ctx, b, covering); err != nil {
			return nil, nil, err
		}
	}
	a.evaluated = bundles
	name, err := a.layerSubjectName()
	if err != nil {
		return nil, attestFailure(CodeAttestationInvalid, "subject_name",
			"the layer root holds a character a subject name excludes", "root", attest.ReportString(err.Error())), nil
	}
	input, err := filesystem.LayerDigestInput(all, files)
	if err != nil {
		return nil, nil, fmt.Errorf("ingest: layer digest: %w", err)
	}
	digest := attest.LayerDigest(input)
	candidates, f := a.selectCandidates(bundles, covering, name, digest, input, all)
	if f != nil {
		return nil, f, nil
	}
	// Spec: §4.7.11 (Layer step) — the registry reads its clock once in a
	// cycle for the signed-time bound.
	layerBundle, f, err := a.passingCandidate(ctx, candidates, a.clock.Now().UTC())
	if f != nil || err != nil {
		return nil, f, err
	}
	if f := completeness(bundles, covering, input.Subjects); f != nil {
		return nil, f, nil
	}
	v := layerBundle.covering
	res := &layerStepResult{time: v.AttestedTime, bundles: bundles}
	// A value longer than any commit name is no commit, and recording it
	// would make every later lineage query of the layer invalid.
	if c := v.Certificate.SourceRepositoryDigest; c.Present && len(c.Value) <= source.MaxLineageStringBytes {
		res.commit = c.Value
	}
	return res, nil, nil
}

// checkRoots fails the layer step with ingest.attestation_root_stale when a
// signer of the policy names the public-good instance and that instance's
// trusted root is past its maximum age. It starts the refresh of that root
// first, through OnPublicGood.
//
// Spec: §4.7.11 (Maximum age, Ingest verification)
func (a *AttestationCycle) checkRoots(ctx context.Context) (*AttestationError, error) {
	if !a.namesPublicGood() {
		return nil, nil
	}
	if a.cfg.OnPublicGood != nil {
		a.cfg.OnPublicGood()
	}
	inst, _ := a.instance(sign.PublicGoodInstance)
	if inst.Root == nil {
		return nil, nil
	}
	_, err := inst.Root.AttestationRoot(ctx)
	switch {
	case errors.Is(err, sign.ErrAttestationRootStale):
		return attestFailure(CodeAttestationRootStale, "",
			"the trusted root of the public-good instance is past PODIUM_SIGSTORE_ROOT_MAX_AGE", "instance", sign.PublicGoodInstance), nil
	case err != nil:
		return nil, fmt.Errorf("ingest: trusted root of instance %s: %w", sign.PublicGoodInstance, err)
	}
	return nil, nil
}

// namesPublicGood reports whether a certificate signer of the policy names
// the public-good instance, which an empty instance names. A key-based
// signer names no instance.
func (a *AttestationCycle) namesPublicGood() bool {
	for _, req := range a.layer.TrustRequirements {
		for _, s := range req.Signers {
			if layer.SignerInstance(s) == sign.PublicGoodInstance {
				return true
			}
		}
	}
	return false
}

// layerSubjectName is the name of the layer subject the cycle expects: from
// the root of a git layer, and from trust_layer_root on a local layer.
//
// Spec: §4.7.11 (Local layers and the standalone bootstrap)
func (a *AttestationCycle) layerSubjectName() (string, error) {
	if a.layer.SourceType == "git" {
		return attest.LayerSubjectName(a.layer.Root)
	}
	return attest.LayerSubjectName(a.layer.TrustLayerRoot)
}

// subjectNameFailure is layer step 1's refusal of a snapshot that holds a
// package with no subject name, listing up to 5 of their IDs.
func subjectNameFailure(all []filesystem.ArtifactRecord) *AttestationError {
	var bad []string
	for i := range all {
		if _, err := all[i].SubjectName(); err != nil {
			bad = append(bad, all[i].ID)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	return attestFailure(CodeAttestationInvalid, "subject_name",
		fmt.Sprintf("%d packages have IDs that no subject can name", len(bad)),
		"ids", attest.ReportStrings(bad, detailsNames), "count", len(bad))
}

// selectCandidates applies layer step 3: among the bundles the covering
// requirement, at position covering, accepts, those listing the layer
// subject name; among them, the candidates, which list it with the
// snapshot's layer digest.
func (a *AttestationCycle) selectCandidates(bundles []*snapshotBundle, covering int, name, digest string, input attest.Layer, all []filesystem.ArtifactRecord) ([]*snapshotBundle, *AttestationError) {
	var accepted, named, candidates []*snapshotBundle
	for _, b := range bundles {
		v := b.covering
		if v == nil {
			continue
		}
		accepted = append(accepted, b)
		if !namesSubject(v, name) {
			continue
		}
		named = append(named, b)
		if lists(v, name, digest) {
			candidates = append(candidates, b)
		}
	}
	switch {
	case len(bundles) == 0:
		return nil, attestFailure(CodeAttestationMissing, "no_bundle", "the attestation directory holds no bundle", "layer_subject", attest.ReportString(name))
	case len(accepted) == 0:
		return nil, attestFailure(CodeAttestationMissing, "no_accepted_bundle",
			"no bundle of the attestation directory is accepted by a signer of the layer's covering requirement",
			"bundles", refusedBundles(bundles, covering, a))
	case len(named) == 0:
		return nil, attestFailure(CodeAttestationMissing, "no_layer_subject",
			"the accepted bundles list no layer subject named "+attest.ReportString(name), "layer_subject", attest.ReportString(name))
	case len(candidates) == 0:
		return nil, layerDigestFailure(name, digest, input, accepted, all)
	}
	return candidates, nil
}

// namesSubject reports whether the statement lists a subject named name.
func namesSubject(v *sign.VerifiedAttestation, name string) bool {
	for _, s := range v.Statement.Subjects {
		if s.Name == name {
			return true
		}
	}
	return false
}

// passingCandidate applies layer steps 4 and 5: a candidate passes when its
// attested time is not earlier than the mark, a key-signed candidate's
// signed time is not later than now plus the signed-time allowance, and,
// where the source answers lineage queries and its certificate records a
// source commit, that commit is in the lineage of the snapshot. It returns
// the passing candidate with the latest attested time, or the failure of
// the candidate with the latest attested time when none passes.
//
// Spec: §4.7.11 (Layer step, Key-based signers)
func (a *AttestationCycle) passingCandidate(ctx context.Context, candidates []*snapshotBundle, now time.Time) (*snapshotBundle, *AttestationError, error) {
	sortByAttestedTime(candidates)
	mark := a.layer.Mark
	bound := now.Add(a.signedTimeAllowance())
	var pastMark []*snapshotBundle
	for _, c := range candidates {
		if (mark == nil || !c.covering.AttestedTime.Before(*mark)) && !signedTimeAhead(c, bound) {
			pastMark = append(pastMark, c)
		}
	}
	answers, err := a.lineageAnswers(ctx, pastMark)
	if err != nil {
		return nil, nil, err
	}
	for _, c := range pastMark {
		if ans, ok := answers[attestedCommit(c)]; !ok || ans == source.InLineage {
			return c, nil, nil
		}
	}
	latest := candidates[0]
	if signedTimeAhead(latest, bound) {
		return nil, attestFailure(CodeAttestationStale, "signed_time_ahead",
			"the signed time of the key-signed layer subject is later than the registry's clock plus PODIUM_ATTESTATION_SIGNED_TIME_ALLOWANCE",
			"signed_at", latest.covering.AttestedTime.UTC().Format(time.RFC3339Nano),
			"registry_time", now.Format(time.RFC3339Nano), "allowance", a.signedTimeAllowance().String(),
			"digest", latest.digest), nil
	}
	if mark != nil && latest.covering.AttestedTime.Before(*mark) {
		return nil, attestFailure(CodeAttestationStale, "mark",
			"the newest layer subject is older than the layer's attestation mark",
			"attested_at", latest.covering.AttestedTime.UTC().Format(time.RFC3339Nano),
			"attestation_mark", mark.UTC().Format(time.RFC3339Nano), "digest", latest.digest), nil
	}
	commit := attestedCommit(latest)
	return nil, attestFailure(CodeAttestationStale, string(answers[commit]),
		"the attested commit of the layer subject is not in the lineage of the snapshot",
		"attested_commit", attest.ReportString(commit), "recorded_commit", attest.ReportString(a.layer.Commit),
		"digest", latest.digest), nil
}

// signedTimeAhead reports whether c is a key-signed candidate whose signed
// time is later than bound. A certificate-signed candidate's time comes from
// its instance and is never bounded this way.
func signedTimeAhead(c *snapshotBundle, bound time.Time) bool {
	return c.covering.Key != "" && c.covering.AttestedTime.After(bound)
}

// signedTimeAllowance is PODIUM_ATTESTATION_SIGNED_TIME_ALLOWANCE. A
// configuration that sets none allows no time ahead, which fails closed.
func (a *AttestationCycle) signedTimeAllowance() time.Duration {
	if a.cfg.SignedTimeAllowance < 0 {
		return 0
	}
	return a.cfg.SignedTimeAllowance
}

// sortByAttestedTime orders bundles by the attested time under the covering
// requirement, latest first, and by ascending attestation digest on a tie.
func sortByAttestedTime(bundles []*snapshotBundle) {
	sort.SliceStable(bundles, func(i, j int) bool {
		ti := bundles[i].covering.AttestedTime
		tj := bundles[j].covering.AttestedTime
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return bundles[i].digest < bundles[j].digest
	})
}

// attestedCommit is the source commit the leaf certificate of a candidate
// records, empty when it records none.
func attestedCommit(b *snapshotBundle) string {
	c := b.covering.Certificate.SourceRepositoryDigest
	if !c.Present {
		return ""
	}
	return c.Value
}

// lineageAnswers sends the lineage query of layer step 5 for the attested
// commits of the candidates that passed the mark, and returns the answer of
// each commit. A cycle whose source answers no lineage query, and a
// candidate whose certificate records no commit, have no answer, and step 5
// does not apply to them. The query names the recorded commit as its base
// and carries at most source.MaxLineageCommits commits, so a longer list is
// sent in several queries.
//
// Spec: §4.7.11 (Layer step), §9.1 (Lineage query)
func (a *AttestationCycle) lineageAnswers(ctx context.Context, candidates []*snapshotBundle) (map[string]source.LineageAnswer, error) {
	answers := map[string]source.LineageAnswer{}
	if a.lineage == nil {
		return answers, nil
	}
	var commits []string
	seen := map[string]bool{}
	for _, c := range candidates {
		commit := attestedCommit(c)
		switch {
		case commit == "" || seen[commit]:
		case len(commit) > source.MaxLineageStringBytes:
			// No commit name is this long, so the commit is outside the
			// history, and the query, which refuses such a member, is not
			// sent for it.
			answers[commit] = source.NotInHistory
		default:
			commits = append(commits, commit)
		}
		seen[commit] = true
	}
	for start := 0; start < len(commits); start += source.MaxLineageCommits {
		batch := commits[start:min(start+source.MaxLineageCommits, len(commits))]
		got, err := a.lineage(ctx, a.layer.Commit, batch)
		if err != nil {
			return nil, err
		}
		if err := source.CheckLineageAnswers(batch, got); err != nil {
			return nil, err
		}
		for i, commit := range batch {
			answers[commit] = got[i]
		}
	}
	return answers, nil
}

// completeness applies layer step 6: every artifact of the snapshot's
// artifact list has a subject with its name and package digest in a bundle
// the covering requirement, at position covering, accepts, whatever that
// bundle's attested time. An additional requirement is decided per artifact
// by the artifact step, because a second signer can lag or decline.
func completeness(bundles []*snapshotBundle, covering int, subjects []attest.Subject) *AttestationError {
	var missing []string
	for _, s := range subjects {
		if !coveredBy(bundles, covering, s.Name, s.Digest) {
			missing = append(missing, s.Name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return attestFailure(CodeAttestationMissing, "incomplete",
		fmt.Sprintf("%d artifacts have no subject from the covering signer", len(missing)),
		"artifacts", attest.ReportStrings(missing, detailsSubjects), "count", len(missing))
}

// coveredBy reports whether a bundle requirement r accepts lists name with
// digest.
func coveredBy(bundles []*snapshotBundle, r int, name, digest string) bool {
	for _, b := range bundles {
		if v := b.accepted[r]; v != nil && lists(v, name, digest) {
			return true
		}
	}
	return false
}
