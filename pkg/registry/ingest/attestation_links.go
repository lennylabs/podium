package ingest

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"path"
	"sort"
	"time"

	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
)

// linkCandidate is one bundle a version can link: the bundle, the
// verification its link key and bundle row come from, and the requirement
// that accepted it, -1 for an unverified link.
type linkCandidate struct {
	bundle *snapshotBundle
	v      *sign.VerifiedAttestation
}

// key returns the link key of the candidate: the statement's predicate type
// and the instance, issuer, and identity of its verification.
func (c linkCandidate) key() store.LinkKey {
	return store.LinkKey{PredicateType: c.v.Statement.PredicateType, Instance: c.v.Instance,
		Issuer: c.v.Certificate.Issuer.Value, Identity: c.v.Certificate.Identity.Value}
}

// sortCandidates orders candidates by descending attested time and then by
// ascending attestation digest.
func sortCandidates(cs []linkCandidate) {
	sort.SliceStable(cs, func(i, j int) bool {
		ti, tj := cs[i].v.AttestedTime, cs[j].v.AttestedTime
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return cs[i].bundle.digest < cs[j].bundle.digest
	})
}

// linkPlan collects the attestation writes of one cycle: the links of each
// version it stores or classifies as unchanged, the bundles those links
// reference, and the outcomes of its unchanged versions. The cycle writes it
// in one store write after its artifacts and before its removals.
//
// Spec: §4.7.11 (Verified links, Unverified links, Attestation outcome)
type linkPlan struct {
	cycle    *AttestationCycle
	stored   map[[2]string][]store.AttestationLink
	bundles  map[string]store.StoredAttestation
	versions []store.VersionAttestations
	// unverified holds the bundles of a cycle under none, and present
	// whether its snapshot holds an attestation directory within the limits.
	unverified []linkCandidate
	present    bool
	// evaluated are the bundles layer step 2 evaluated in a cycle under warn
	// or require, nil when the step evaluated none, which changes no link.
	evaluated []*snapshotBundle
	now       time.Time
}

// newLinkPlan reads the stored links of the layer the cycle links.
func newLinkPlan(ctx context.Context, st store.Store, a *AttestationCycle, now time.Time) (*linkPlan, error) {
	if a == nil {
		return nil, nil
	}
	links, err := st.ListAttestationLinks(ctx, a.layer.TenantID, store.LinkFilter{LayerID: a.layer.ID})
	if err != nil {
		return nil, fmt.Errorf("ingest: read the links of layer %s: %w", a.layer.ID, err)
	}
	p := &linkPlan{cycle: a, stored: map[[2]string][]store.AttestationLink{}, bundles: map[string]store.StoredAttestation{}, now: now}
	for _, l := range links {
		k := [2]string{l.ArtifactID, l.Version}
		p.stored[k] = append(p.stored[k], l)
	}
	return p, nil
}

// version adds the writes of one version that the cycle stores (stored) or
// classifies as unchanged, from the snapshot record rec. row is the stored
// row of an unchanged version, whose outcome the plan rewrites when the
// cycle's outcome differs.
func (p *linkPlan) version(rec *filesystem.ArtifactRecord, id, version string, outcome *store.VersionOutcome, row *store.ManifestRecord) {
	if p == nil {
		return
	}
	w := store.VersionAttestations{ArtifactID: id, Version: version}
	if links, changed := p.links(rec, p.stored[[2]string{id, version}]); changed {
		w.ReplaceLinks, w.Links = true, links
	}
	if row != nil && outcome != nil && !row.Outcome().Equal(*outcome) {
		w.Outcome = outcome
	}
	if w.ReplaceLinks || w.Outcome != nil {
		p.versions = append(p.versions, w)
	}
}

// outcome adds the outcome of a stored version the cycle evaluated and
// stores no version for, which is an unchanged version of a dropped
// artifact.
func (p *linkPlan) outcome(row store.ManifestRecord, o *store.VersionOutcome) {
	if p == nil || o == nil || row.Outcome().Equal(*o) {
		return
	}
	p.versions = append(p.versions, store.VersionAttestations{ArtifactID: row.ArtifactID, Version: row.Version, Outcome: o})
}

// links returns the links rec's version holds after the cycle, and whether
// they differ from stored.
func (p *linkPlan) links(rec *filesystem.ArtifactRecord, stored []store.AttestationLink) ([]store.AttestationLink, bool) {
	name, err := rec.SubjectName()
	if err != nil {
		return nil, false
	}
	var out []store.AttestationLink
	switch {
	case p.cycle.verifies():
		if p.evaluated == nil {
			return nil, false
		}
		out = p.verifiedLinks(name, rec.PackageDigest, stored)
	case p.present:
		out = p.unverifiedLinks(name, rec.PackageDigest)
	default:
		return nil, false
	}
	return out, !sameLinks(out, stored)
}

// verifiedLinks applies the §4.7.11 verified link rule: in requirement order,
// each key of the accepted bundles that list the subject keeps a stored
// verified link and otherwise links its bundle with the latest attested
// time; a stored verified link of a key no accepted bundle lists is kept,
// and an unverified link is kept while the limit leaves room.
//
// Spec: §4.7.11 (Verified links)
func (p *linkPlan) verifiedLinks(name, digest string, stored []store.AttestationLink) []store.AttestationLink {
	byKey := map[store.LinkKey]store.AttestationLink{}
	for _, l := range stored {
		byKey[l.Key()] = l
	}
	taken := map[store.LinkKey]bool{}
	var out []store.AttestationLink
	for r := range p.cycle.layer.TrustRequirements {
		var cs []linkCandidate
		for _, b := range p.evaluated {
			if v := b.accepted[r]; v != nil && lists(v, name, digest) {
				cs = append(cs, linkCandidate{bundle: b, v: v})
			}
		}
		sortCandidates(cs)
		for _, c := range cs {
			k := c.key()
			if taken[k] {
				continue
			}
			taken[k] = true
			if s, ok := byKey[k]; ok && s.Verdict == store.VerdictVerified {
				if s.Digest == c.bundle.digest && p.cycle.policy.holds {
					s.VerifiedUnder = p.cycle.policy.key
				}
				out = append(out, s)
				continue
			}
			out = append(out, p.link(c, store.VerdictVerified))
		}
	}
	for _, verdict := range []string{store.VerdictVerified, store.VerdictUnverified} {
		for _, s := range stored {
			if s.Verdict == verdict && !taken[s.Key()] {
				taken[s.Key()] = true
				out = append(out, s)
			}
		}
	}
	return capLinks(out)
}

// unverifiedLinks applies the §4.7.11 unverified link rule of a cycle under
// none: one link per key to the bundle with the latest attested time, and
// the keys of the latest attested times where they exceed the limit.
//
// Spec: §4.7.11 (Unverified links)
func (p *linkPlan) unverifiedLinks(name, digest string) []store.AttestationLink {
	var cs []linkCandidate
	for _, c := range p.unverified {
		if lists(c.v, name, digest) {
			cs = append(cs, c)
		}
	}
	sortCandidates(cs)
	taken := map[store.LinkKey]bool{}
	var out []store.AttestationLink
	for _, c := range cs {
		if k := c.key(); !taken[k] {
			taken[k] = true
			out = append(out, p.link(c, store.VerdictUnverified))
		}
	}
	return capLinks(out)
}

// link returns a new link to the candidate's bundle and adds the bundle to
// the plan.
func (p *linkPlan) link(c linkCandidate, verdict string) store.AttestationLink {
	k := c.key()
	l := store.AttestationLink{PredicateType: k.PredicateType, Instance: k.Instance, SignerIssuer: k.Issuer,
		SignerIdentity: k.Identity, Digest: c.bundle.digest, Verdict: verdict, LinkedAt: p.now}
	if verdict == store.VerdictVerified {
		l.VerifiedUnder = p.cycle.policy.verifiedUnder()
	}
	if _, ok := p.bundles[c.bundle.digest]; !ok {
		p.bundles[c.bundle.digest] = p.bundleRow(c)
	}
	return l
}

// bundleRow is the stored bundle of a candidate.
func (p *linkPlan) bundleRow(c linkCandidate) store.StoredAttestation {
	f := c.v.Certificate
	row := store.StoredAttestation{
		Digest: c.bundle.digest, Bundle: c.bundle.raw, LayerID: p.cycle.layer.ID, SourceRef: p.cycle.reference,
		PredicateType: c.v.Statement.PredicateType, Instance: c.v.Instance,
		SourceRepository: f.SourceRepository.Value, SourceRepositoryOwner: f.SourceRepositoryOwner.Value,
		SourceRepositoryRef: f.SourceRepositoryRef.Value, SignerIssuer: f.Issuer.Value, SignerIdentity: f.Identity.Value,
		AttestedAt: c.v.AttestedTime, StoredAt: p.now,
	}
	if n := logIndex(c.bundle.raw); n >= 0 {
		row.LogIndex = &n
	}
	return row
}

// capLinks keeps the first MaxAttestationLinks links.
func capLinks(links []store.AttestationLink) []store.AttestationLink {
	if len(links) > store.MaxAttestationLinks {
		return links[:store.MaxAttestationLinks]
	}
	return links
}

// sameLinks reports whether two link sets hold the same keys with the same
// digest, verdict, and recorded key.
func sameLinks(a, b []store.AttestationLink) bool {
	if len(a) != len(b) {
		return false
	}
	m := make(map[store.LinkKey]store.AttestationLink, len(a))
	for _, l := range a {
		m[l.Key()] = l
	}
	for _, l := range b {
		o, ok := m[l.Key()]
		if !ok || o.Digest != l.Digest || o.Verdict != l.Verdict || o.VerifiedUnder != l.VerifiedUnder {
			return false
		}
	}
	return true
}

// write stores the plan in one store write under the cycle's claim. A plan
// that writes nothing makes no write. A later claim refuses it with
// store.ErrIngestSuperseded, which the removal check that follows reports.
func (p *linkPlan) write(ctx context.Context, st store.Store, claim string) error {
	if p == nil || len(p.versions) == 0 {
		return nil
	}
	w := store.AttestationWrites{Versions: p.versions}
	for _, d := range sortedKeys(p.bundles) {
		w.Bundles = append(w.Bundles, p.bundles[d])
	}
	err := st.WriteAttestations(ctx, p.cycle.layer.TenantID, p.cycle.layer.ID, claim, w)
	if err != nil && !errors.Is(err, store.ErrIngestSuperseded) {
		return fmt.Errorf("ingest: write the links of layer %s: %w", p.cycle.layer.ID, err)
	}
	return nil
}

// readUnverified reads the attestation directory of a cycle under none and
// keeps each bundle whose signature, chain, and time verify under one of the
// configured instances, in name order, without the identity comparison and
// the bindings. A directory with more bundle files than the limit links no
// bundle, and changes no link; a file over the size limit or one the cycle
// cannot read is passed over. A failure that is no refusal of the bundle,
// such as a trusted root past its maximum age, passes the bundle over and is
// logged, because no policy applies under none.
//
// Spec: §4.7.11 (Unverified links)
func (p *linkPlan) readUnverified(ctx context.Context, files fs.FS) {
	if p == nil || p.cycle.verifies() {
		return
	}
	entries, err := fs.ReadDir(files, attest.AttestationDir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if attest.IsBundleFileName(e.Name()) {
			names = append(names, e.Name())
		}
	}
	maxBundles, maxBytes := p.cycle.limits()
	if len(names) > maxBundles {
		log.Printf("attestation: layer %s: the attestation directory holds %d bundle files, more than PODIUM_ATTESTATION_MAX_BUNDLES (%d); no bundle is linked", p.cycle.layer.ID, len(names), maxBundles)
		return
	}
	p.present = true
	sort.Strings(names)
	for _, name := range names {
		raw, ok := readBundleFile(files, path.Join(attest.AttestationDir, name), maxBytes)
		if !ok {
			continue
		}
		b := &snapshotBundle{raw: raw, digest: attest.AttestationDigest(raw)}
		if v := p.verifyAnyInstance(ctx, b); v != nil {
			p.unverified = append(p.unverified, linkCandidate{bundle: b, v: v})
		}
	}
}

// readBundleFile reads a regular bundle file of at most maxBytes bytes.
func readBundleFile(files fs.FS, p string, maxBytes int64) ([]byte, bool) {
	info, err := fs.Stat(files, p)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxBytes {
		return nil, false
	}
	raw, err := fs.ReadFile(files, p)
	if err != nil || int64(len(raw)) > maxBytes {
		return nil, false
	}
	return raw, true
}

// verifyAnyInstance verifies b under each configured instance in name order
// and returns the first verification, nil when none verifies.
func (p *linkPlan) verifyAnyInstance(ctx context.Context, b *snapshotBundle) *sign.VerifiedAttestation {
	names := make([]string, 0, len(p.cycle.cfg.Instances))
	for n := range p.cycle.cfg.Instances {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		v, err := sign.VerifyAttestationBundle(ctx, b.raw, p.cycle.cfg.Instances[n])
		if err == nil {
			return v
		}
		// A refusal of the bundle, and a public-good root past its maximum age,
		// which no policy under none starts the refresh of, are no failures
		// worth a line per bundle and cycle.
		if _, refused := sign.AttestationReasonOf(err); !refused && !errors.Is(err, sign.ErrAttestationRootStale) {
			log.Printf("attestation: layer %s: bundle %s under instance %s: %v", p.cycle.layer.ID, b.digest, n, err)
		}
	}
	return nil
}

// versionOutcome returns the outcome a cycle sets on a version of id that it
// stores, or that it classifies as unchanged when unchanged is true, from
// the cycle's verdict. Nil leaves the stored outcome: a cycle with no
// attestation configuration, and an unchanged version after a failed layer
// step under warn, which evaluated none of its subjects.
//
// Spec: §4.7.11 (Attestation outcome)
func (a *AttestationCycle) versionOutcome(v *attestationVerdict, id string, unchanged bool, now time.Time) *store.VersionOutcome {
	switch {
	case a == nil:
		return nil
	case !a.verifies():
		return &store.VersionOutcome{Outcome: store.OutcomeNotEvaluated, EvaluatedAt: &now}
	case v == nil, unchanged && v.failure != nil:
		return nil
	}
	o, ok := v.outcomes[id]
	if !ok {
		return nil
	}
	out := &store.VersionOutcome{Outcome: o.Outcome, EvaluatedAt: &now}
	if o.Outcome == OutcomeFailed {
		f := &store.AttestationFailure{Code: o.Code, Scope: o.Scope, Constraint: o.Constraint}
		if o.Scope == "artifact" {
			r := o.Requirement
			f.Requirement = &r
		}
		out.Failure = f
	}
	return out
}

// storedRow returns the stored row of id at version from the listing the
// cycle read before its writes, nil when it holds none.
func storedRow(rowsByID map[string][]store.ManifestRecord, id, version string) *store.ManifestRecord {
	for i := range rowsByID[id] {
		if rowsByID[id][i].Version == version {
			return &rowsByID[id][i]
		}
	}
	return nil
}

// unchangedRow returns the live stored row of rec's layer that holds rec's
// content hash, at rec's version unless the registry assigns the version,
// which is the version a dropped artifact is unchanged from.
func unchangedRow(rec filesystem.ArtifactRecord, req Request, rowsByID map[string][]store.ManifestRecord, now time.Time) (store.ManifestRecord, bool) {
	mr, err := manifestRecordFor(rec, req.TenantID, req.LayerID, now)
	if err != nil {
		return store.ManifestRecord{}, false
	}
	for _, row := range rowsByID[rec.ID] {
		if row.Layer == req.LayerID && row.DeletedAt == nil && row.ContentHash == mr.ContentHash &&
			(rec.VersionAssigned || row.Version == mr.Version) {
			return row, true
		}
	}
	return store.ManifestRecord{}, false
}
