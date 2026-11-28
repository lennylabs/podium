package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/lennylabs/podium/internal/clock"
	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/store"
)

// The causes layer.attestation_links_downgraded records.
const (
	reevalCauseCycle        = "cycle"
	reevalCausePolicyChange = "policy_change"
	reevalCauseStart        = "start"
)

// ConstraintLinkDowngraded is the constraint of the outcome a re-evaluation
// sets on a version whose link it changed to unverified.
const ConstraintLinkDowngraded = "link_downgraded"

// policyState is the policy key of a layer and whether this process holds
// the recorded root of every instance the policy names. A layer under none
// has the empty key.
//
// Spec: §4.7.11 (Policy key, Root record)
type policyState struct {
	mode  string
	key   string
	holds bool
}

// verifiedUnder is the key a link this process writes as verified records:
// the policy key, or the empty key when the process is behind a root
// record, so the next re-evaluation by a process that holds the recorded
// roots selects the link.
func (p policyState) verifiedUnder() string {
	if !p.holds {
		return ""
	}
	return p.key
}

// policyKeyDoc is the JSON document the policy key digests.
type policyKeyDoc struct {
	Requirements []store.TrustRequirement `json:"trust_requirements"`
	Instances    []policyKeyInstance      `json:"instances"`
}

// policyKeyInstance is one instance of the policy key document.
type policyKeyInstance struct {
	Name                    string `json:"name"`
	TransparencyLog         bool   `json:"transparency_log"`
	CertificateTransparency bool   `json:"certificate_transparency"`
	RootDigest              string `json:"root_digest"`
}

// policyState derives the policy key of lc. Before it reads the root records
// it replaces each record this process is ahead of, for the instances the
// policy names: an instance with no record, or a record whose digest differs
// from the root this process uses and whose root time is earlier. A process
// whose root is past its maximum age, or cannot be read, writes no record
// and is behind it. The replacement depends on no ingest claim.
//
// Spec: §4.7.11 (Policy key, Root record)
func (c *AttestationConfig) policyState(ctx context.Context, st store.Store, lc store.LayerConfig, mode string) (policyState, error) {
	if mode == layer.TrustModeNone {
		return policyState{mode: mode, holds: true}, nil
	}
	records, err := rootRecords(ctx, st)
	if err != nil {
		return policyState{}, err
	}
	doc := policyKeyDoc{Requirements: lc.TrustRequirements}
	holds := true
	for _, name := range policyInstances(lc) {
		inst := c.Instances[name]
		status, usable, serr := sign.RootStatus(ctx, inst)
		rec, has := records[name]
		if serr == nil && usable && (!has || status.Digest != rec.RootDigest && status.Time.After(rec.RootTime)) {
			if rec, err = replaceRoot(ctx, st, records, name, status); err != nil {
				return policyState{}, err
			}
			has = rec.RootDigest != ""
		}
		holds = holds && serr == nil && usable && has && status.Digest == rec.RootDigest
		doc.Instances = append(doc.Instances, policyKeyInstance{Name: name, TransparencyLog: inst.TransparencyLog,
			CertificateTransparency: inst.CertificateTransparency, RootDigest: rec.RootDigest})
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return policyState{}, fmt.Errorf("ingest: policy key: %w", err)
	}
	sum := sha256.Sum256(raw)
	return policyState{mode: mode, key: "sha256:" + hex.EncodeToString(sum[:]), holds: holds}, nil
}

// rootRecords reads the root records by instance.
func rootRecords(ctx context.Context, st store.Store) (map[string]store.RootRecord, error) {
	list, err := st.ListRootRecords(ctx)
	if err != nil {
		return nil, fmt.Errorf("ingest: read the root records: %w", err)
	}
	out := make(map[string]store.RootRecord, len(list))
	for _, r := range list {
		out[r.Instance] = r
	}
	return out, nil
}

// replaceRoot replaces the root record of name with status, conditional on
// the record read, and returns the record in force afterwards: the new one,
// or the one another process wrote in between.
func replaceRoot(ctx context.Context, st store.Store, records map[string]store.RootRecord, name string, status sign.AttestationRootStatus) (store.RootRecord, error) {
	next := store.RootRecord{Instance: name, RootDigest: status.Digest, RootTime: status.Time}
	var prev *store.RootRecord
	if r, ok := records[name]; ok {
		prev = &r
	}
	wrote, err := st.ReplaceRootRecord(ctx, prev, next)
	if err != nil {
		return store.RootRecord{}, fmt.Errorf("ingest: replace the root record of instance %s: %w", name, err)
	}
	if wrote {
		records[name] = next
		return next, nil
	}
	fresh, err := rootRecords(ctx, st)
	if err != nil {
		return store.RootRecord{}, err
	}
	for k, v := range fresh {
		records[k] = v
	}
	return records[name], nil
}

// policyInstances names, sorted, each instance a signer of lc names, with an
// empty name read as public-good.
func policyInstances(lc store.LayerConfig) []string {
	seen := map[string]bool{}
	var out []string
	for _, req := range lc.TrustRequirements {
		for _, s := range req.Signers {
			name := s.Instance
			if name == "" {
				name = sign.PublicGoodInstance
			}
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out
}

// reevaluate runs the §4.7.11 re-evaluation of the cycle's layer after its
// claim. Its writes apply only while the claim is the latest; superseded
// reports one that a later claim refused.
//
// Spec: §4.7.11 (Re-evaluation)
func (a *AttestationCycle) reevaluate(ctx context.Context, st store.Store, onWrite func()) (superseded bool, err error) {
	r := reevaluation{cfg: a.cfg, st: st, layer: a.layer, mode: a.mode, claim: a.claim, clock: a.clock, audit: a.audit,
		cause: reevalCauseCycle, onWrite: onWrite}
	ps, err := r.run(ctx)
	if errors.Is(err, store.ErrIngestSuperseded) {
		return true, nil
	}
	a.policy = ps
	return false, err
}

// Reevaluate runs the §4.7.11 re-evaluation of lc after a register or an
// update changed its trust policy. It holds no ingest claim: each of its
// writes applies to a link only while the link holds the bundle the
// re-evaluation read, and a downgraded outcome only while it is verified.
// A layer whose policy is invalid is not re-evaluated.
//
// Spec: §4.7.11 (Re-evaluation), §7.3.1 (Layer trust policy)
func (c *AttestationConfig) Reevaluate(ctx context.Context, st store.Store, lc store.LayerConfig, clk clock.Clock, emit AuditEmitterFunc) error {
	return c.reevaluateClaimless(ctx, st, lc, clk, emit, reevalCausePolicyChange, nil)
}

// ReevaluateStoredLayers runs the §4.7.11 re-evaluation of every live layer
// of tenantID that a start finds, after it seeded the declared and bootstrap
// layers and loaded the trusted roots, so a policy that a declaration, the
// trust default, or a replaced root changed between two starts is
// re-evaluated before the registry serves. It reads the tenant's versions
// once for every layer, and re-evaluates each layer even when another fails,
// returning the first failure.
//
// Spec: §4.7.11 (Re-evaluation)
func (c *AttestationConfig) ReevaluateStoredLayers(ctx context.Context, st store.Store, tenantID string, clk clock.Clock, emit AuditEmitterFunc) error {
	layers, err := st.ListLayerConfigs(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("ingest: read the layers of tenant %s: %w", tenantID, err)
	}
	rows, err := st.ListManifestsIncludingDeleted(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("ingest: read the versions of tenant %s: %w", tenantID, err)
	}
	var first error
	for _, lc := range layers {
		if err := c.reevaluateClaimless(ctx, st, lc, clk, emit, reevalCauseStart, rows); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// reevaluateClaimless runs a re-evaluation that holds no ingest claim, with
// the tenant's versions rows when the caller read them.
func (c *AttestationConfig) reevaluateClaimless(ctx context.Context, st store.Store, lc store.LayerConfig, clk clock.Clock, emit AuditEmitterFunc, cause string, rows []store.ManifestRecord) error {
	if c == nil {
		c = &AttestationConfig{}
	}
	cyc := c.Cycle(lc, "")
	if cyc == nil {
		return nil
	}
	if err := cyc.readFloor(ctx); err != nil {
		return err
	}
	if cyc.policyFailure() != nil {
		return nil
	}
	if clk == nil {
		clk = clock.Real{}
	}
	r := reevaluation{cfg: c, st: st, layer: lc, mode: cyc.mode, clock: clk, audit: emit, cause: cause, clearAll: true, rows: rows}
	_, err := r.run(ctx)
	return err
}

// reevaluation is one re-evaluation of the links of a layer.
type reevaluation struct {
	cfg   *AttestationConfig
	st    store.Store
	layer store.LayerConfig
	// mode is the layer's resolved trust mode under the tenant floor and the
	// trust default.
	mode    string
	claim   string
	clock   clock.Clock
	audit   AuditEmitterFunc
	cause   string
	onWrite func()
	// policy is the layer's policy state, and cycle the layer's
	// verification, which a process that holds the recorded roots runs on
	// each selected bundle.
	policy policyState
	cycle  *AttestationCycle
	// clearAll makes a re-evaluation under none set every version of the
	// layer to not_evaluated, which a claim-less re-evaluation does. A cycle
	// leaves that to its own write, which reads the versions anyway. rows
	// holds the tenant's versions when the caller read them.
	clearAll bool
	rows     []store.ManifestRecord
}

// checkKey names a decision about one stored bundle under the instance a
// link keys it on: a requirement accepts a link only through a signer of
// that instance.
type checkKey struct{ digest, instance string }

// keyOf returns the check key of a link.
func keyOf(l store.AttestationLink) checkKey { return checkKey{l.Digest, l.Instance} }

// linkCheck is what a re-evaluation decided about one stored bundle: the
// requirements that accept it, empty when none does, and whether the
// decision was made by this process, which a process behind a root record
// only makes for a bundle no signer matches.
type linkCheck struct {
	accepted []int
	decided  bool
}

// run selects the layer's verified links whose recorded key differs from
// the current key, and under none every verified link, and decides each
// distinct bundle once. It writes the changed links and outcomes in one
// store write and records layer.attestation_links_downgraded for the
// downgrades.
func (r *reevaluation) run(ctx context.Context) (policyState, error) {
	ps, err := r.cfg.policyState(ctx, r.st, r.layer, r.mode)
	if err != nil {
		return policyState{}, err
	}
	r.policy = ps
	r.cycle = &AttestationCycle{cfg: r.cfg, layer: r.layer, mode: ps.mode, clock: r.clock}
	links, err := r.st.ListAttestationLinks(ctx, r.layer.TenantID, store.LinkFilter{LayerID: r.layer.ID})
	if err != nil {
		return ps, fmt.Errorf("ingest: read the links of layer %s: %w", r.layer.ID, err)
	}
	selected := r.selectLinks(links)
	clear := ps.mode == layer.TrustModeNone && r.clearAll
	if len(selected) == 0 && !clear {
		return ps, nil
	}
	checks, err := r.checkBundles(ctx, selected)
	if err != nil {
		return ps, err
	}
	w, err := r.writes(ctx, links, selected, checks)
	if err != nil {
		return ps, err
	}
	if clear {
		if err := r.clearOutcomes(ctx, &w); err != nil {
			return ps, err
		}
	}
	if len(w.Versions) == 0 {
		return ps, nil
	}
	if r.claim != "" {
		// Spec: §4.7.11 (Verified links) — the writes of a cycle apply only
		// under the policy it claimed under.
		p := store.PolicyOf(r.layer)
		w.Policy = &p
	}
	if r.onWrite != nil {
		r.onWrite()
	}
	res, err := r.st.WriteAttestations(ctx, r.layer.TenantID, r.layer.ID, r.claim, w)
	if err != nil {
		if errors.Is(err, store.ErrIngestSuperseded) {
			return ps, err
		}
		return ps, fmt.Errorf("ingest: write the re-evaluation of layer %s: %w", r.layer.ID, err)
	}
	r.emit(downgradedDigests(res.Updated))
	return ps, nil
}

// downgradedDigests returns, sorted, the digests of the applied updates that
// changed a link to unverified.
func downgradedDigests(updated []store.AttestationLink) []string {
	set := map[string]bool{}
	for _, l := range updated {
		if l.Verdict == store.VerdictUnverified {
			set[l.Digest] = true
		}
	}
	return sortedKeys(set)
}

// selectLinks returns the verified links the re-evaluation decides.
func (r *reevaluation) selectLinks(links []store.AttestationLink) []store.AttestationLink {
	var out []store.AttestationLink
	for _, l := range links {
		if l.Verdict != store.VerdictVerified {
			continue
		}
		if r.policy.mode == layer.TrustModeNone || l.VerifiedUnder != r.policy.key {
			out = append(out, l)
		}
	}
	return out
}

// checkBundles decides each distinct bundle of the selected links once.
// Under none no requirement applies. A process that holds the recorded roots
// verifies the stored bytes against the current policy; a process behind a
// root record compares the certificate with the signers alone and decides
// only a bundle that no signer matches. A bundle the store no longer holds
// is accepted by no requirement.
func (r *reevaluation) checkBundles(ctx context.Context, selected []store.AttestationLink) (map[checkKey]linkCheck, error) {
	out := map[checkKey]linkCheck{}
	raws := map[string]*snapshotBundle{}
	for _, l := range selected {
		k := keyOf(l)
		if _, done := out[k]; done {
			continue
		}
		if r.policy.mode == layer.TrustModeNone {
			out[k] = linkCheck{decided: true}
			continue
		}
		b, ok := raws[l.Digest]
		if !ok {
			a, err := r.st.GetAttestation(ctx, r.layer.TenantID, l.Digest)
			switch {
			case errors.Is(err, store.ErrNotFound):
				out[k] = linkCheck{decided: true}
				continue
			case err != nil:
				return nil, fmt.Errorf("ingest: read attestation %s: %w", l.Digest, err)
			}
			b = &snapshotBundle{raw: a.Bundle, verified: map[string]*sign.VerifiedAttestation{}, refusal: map[string]sign.AttestationReason{}}
			raws[l.Digest] = b
		}
		c, err := r.check(ctx, b, l.Instance)
		if err != nil {
			return nil, err
		}
		out[k] = c
	}
	return out, nil
}

// check decides one stored bundle under the instance a link keys it on: a
// process that holds the recorded roots verifies it under that instance for
// each signer that names it, and a process behind a root record compares the
// certificate with those signers alone.
func (r *reevaluation) check(ctx context.Context, b *snapshotBundle, instance string) (linkCheck, error) {
	if !r.policy.holds {
		reqs := matchingRequirements(b.raw, r.layer.TrustRequirements, instance)
		return linkCheck{accepted: reqs, decided: len(reqs) == 0}, nil
	}
	var accepted []int
	for i, req := range r.layer.TrustRequirements {
		for _, s := range req.Signers {
			if signerInstance(s) != instance {
				continue
			}
			v, _, err := r.cycle.verifyUnder(ctx, b, s.Instance)
			if err != nil {
				return linkCheck{}, err
			}
			if v != nil && signerReason(v, s, req) == "" {
				accepted = append(accepted, i)
				break
			}
		}
	}
	return linkCheck{accepted: accepted, decided: true}, nil
}

// signerInstance is the instance a signer names, public-good for none.
func signerInstance(s store.TrustSigner) string {
	if s.Instance == "" {
		return sign.PublicGoodInstance
	}
	return s.Instance
}

// matchingRequirements returns the requirements that have a signer of
// instance whose issuer, identity, source binding, and ref binding the
// certificate of raw matches and whose predicate type, where the
// requirement names one, equals the bundle's. It verifies nothing, so it
// reads only a bundle the registry verified when it stored it.
func matchingRequirements(raw []byte, reqs []store.TrustRequirement, instance string) []int {
	sum, err := sign.InspectAttestationBundle(raw)
	if err != nil {
		return nil
	}
	var out []int
	for i, req := range reqs {
		if req.PredicateType != "" && sum.Statement.PredicateType != req.PredicateType {
			continue
		}
		for _, s := range req.Signers {
			if signerInstance(s) == instance && sum.MatchesSigner(layer.VerifierSigner(s)) == nil {
				out = append(out, i)
				break
			}
		}
	}
	return out
}

// writes builds the link updates and the outcome downgrades of the decided
// bundles. Each update applies only while the link holds the digest, the
// verdict, and the key the re-evaluation read. A bundle the outcome
// downgrade cannot read fails the re-evaluation, which then writes nothing.
func (r *reevaluation) writes(ctx context.Context, links, selected []store.AttestationLink, checks map[checkKey]linkCheck) (store.AttestationWrites, error) {
	now := r.clock.Now().UTC()
	byVersion := map[[2]string]*store.VersionAttestations{}
	var order [][2]string
	version := func(l store.AttestationLink) *store.VersionAttestations {
		k := [2]string{l.ArtifactID, l.Version}
		v, ok := byVersion[k]
		if !ok {
			v = &store.VersionAttestations{ArtifactID: l.ArtifactID, Version: l.Version}
			byVersion[k] = v
			order = append(order, k)
		}
		return v
	}
	downgradedLinks := map[[2]string]map[store.LinkKey]bool{}
	for _, l := range selected {
		read := l
		c := checks[keyOf(l)]
		switch {
		case len(c.accepted) > 0 && r.policy.holds:
			l.VerifiedUnder = r.policy.key
		case len(c.accepted) > 0, !c.decided:
			continue
		default:
			l.Verdict, l.VerifiedUnder = store.VerdictUnverified, ""
			k := [2]string{l.ArtifactID, l.Version}
			if downgradedLinks[k] == nil {
				downgradedLinks[k] = map[store.LinkKey]bool{}
			}
			downgradedLinks[k][l.Key()] = true
		}
		l.LinkedAt = now
		v := version(l)
		v.Updates = append(v.Updates, store.LinkUpdate{Link: l, ReadVerdict: read.Verdict, ReadVerifiedUnder: read.VerifiedUnder})
	}
	if r.policy.mode != layer.TrustModeNone {
		if err := r.downgradeOutcomes(ctx, links, checks, downgradedLinks, version, now); err != nil {
			return store.AttestationWrites{}, err
		}
	}
	w := store.AttestationWrites{}
	for _, k := range order {
		w.Versions = append(w.Versions, *byVersion[k])
	}
	return w, nil
}

// downgradeOutcomes sets the outcome failed on each version that lost a
// verified link, with the first requirement that is not optional and that
// accepts the bundle of none of its remaining verified links. A version
// whose requirements that are not optional each still accept a remaining
// link keeps its outcome, so a lost link of an optional requirement changes
// no outcome. A remaining link that the
// re-evaluation did not decide is credited to the requirements whose signers
// its certificate matches.
func (r *reevaluation) downgradeOutcomes(ctx context.Context, links []store.AttestationLink, checks map[checkKey]linkCheck,
	downgraded map[[2]string]map[store.LinkKey]bool, version func(store.AttestationLink) *store.VersionAttestations, now time.Time) error {
	handled := map[[2]string]bool{}
	for _, l := range links {
		k := [2]string{l.ArtifactID, l.Version}
		lost := downgraded[k]
		if lost == nil || handled[k] {
			continue
		}
		handled[k] = true
		covered := map[int]bool{}
		for _, other := range links {
			if other.ArtifactID != l.ArtifactID || other.Version != l.Version || other.Verdict != store.VerdictVerified || lost[other.Key()] {
				continue
			}
			reqs, err := r.remainingRequirements(ctx, other, checks)
			if err != nil {
				return err
			}
			for _, req := range reqs {
				covered[req] = true
			}
		}
		for req, tr := range r.layer.TrustRequirements {
			if tr.Optional || covered[req] {
				continue
			}
			n := req
			v := version(l)
			v.Outcome = &store.VersionOutcome{Outcome: store.OutcomeFailed, EvaluatedAt: &now, Failure: &store.AttestationFailure{
				Code: CodeAttestationMissing, Scope: "artifact", Constraint: ConstraintLinkDowngraded, Requirement: &n}}
			v.OutcomeIf = store.OutcomeVerified
			break
		}
	}
	return nil
}

// remainingRequirements returns the requirements a verified link that keeps
// its verdict counts for. A bundle the store no longer holds counts for no
// requirement, as checkBundles decides it. A read that fails otherwise fails
// the re-evaluation, so a read failure never keeps an outcome verified; the
// next cycle or start runs the re-evaluation again.
//
// Spec: §4.7.11 (Re-evaluation)
func (r *reevaluation) remainingRequirements(ctx context.Context, l store.AttestationLink, checks map[checkKey]linkCheck) ([]int, error) {
	if c, ok := checks[keyOf(l)]; ok && c.decided {
		return c.accepted, nil
	}
	a, err := r.st.GetAttestation(ctx, r.layer.TenantID, l.Digest)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("ingest: read attestation %s: %w", l.Digest, err)
	}
	return matchingRequirements(a.Bundle, r.layer.TrustRequirements, l.Instance), nil
}

// clearOutcomes sets the outcome not_evaluated on every version of a layer
// under none whose outcome is another, which a change of the mode to none
// requires.
func (r *reevaluation) clearOutcomes(ctx context.Context, w *store.AttestationWrites) error {
	rows := r.rows
	if rows == nil {
		var err error
		if rows, err = r.st.ListManifestsIncludingDeleted(ctx, r.layer.TenantID); err != nil {
			return fmt.Errorf("ingest: read the versions of layer %s: %w", r.layer.ID, err)
		}
	}
	now := r.clock.Now().UTC()
	idx := map[[2]string]int{}
	for i, v := range w.Versions {
		idx[[2]string{v.ArtifactID, v.Version}] = i
	}
	for _, row := range rows {
		if row.Layer != r.layer.ID || row.AttestationOutcome == store.OutcomeNotEvaluated {
			continue
		}
		o := &store.VersionOutcome{Outcome: store.OutcomeNotEvaluated, EvaluatedAt: &now}
		if i, ok := idx[[2]string{row.ArtifactID, row.Version}]; ok {
			w.Versions[i].Outcome = o
			continue
		}
		w.Versions = append(w.Versions, store.VersionAttestations{ArtifactID: row.ArtifactID, Version: row.Version, Outcome: o})
	}
	return nil
}

// emit records layer.attestation_links_downgraded for the digests whose
// links changed to unverified.
//
// Spec: §8.1, §4.7.11 (Re-evaluation)
func (r *reevaluation) emit(digests []string) {
	if len(digests) == 0 {
		return
	}
	if r.audit == nil {
		log.Printf("attestation: layer %s: %d attestations changed to unverified (%s)", r.layer.ID, len(digests), r.cause)
		return
	}
	r.audit(string(audit.EventLayerAttestationLinksDowngraded), r.layer.ID, map[string]string{
		"cause":   r.cause,
		"digests": jsonText(digests),
	})
}
