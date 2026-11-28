package store

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// The attestation outcomes of a stored version (§4.7.11 Attestation outcome).
const (
	OutcomeVerified     = "verified"
	OutcomeFailed       = "failed"
	OutcomeNotEvaluated = "not_evaluated"
)

// The verdicts of an attestation link (§4.7.11 Attestation storage).
const (
	VerdictVerified   = "verified"
	VerdictUnverified = "unverified"
)

// MaxAttestationLinks is the number of links a version holds at most, which
// is the number of references a consumer accepts.
//
// Spec: §4.7.11 (Attestation storage)
const MaxAttestationLinks = 16

// AttestationFailure is the attestation_failure member of a version whose
// outcome is failed: the §6.10 code, the scope (layer or artifact), the
// constraint where the code carries one, and, for the scope artifact, the
// position of the failed requirement in trust_requirements.
//
// Spec: §4.7.11 (Attestation outcome)
type AttestationFailure struct {
	Code        string `json:"code"`
	Scope       string `json:"scope"`
	Constraint  string `json:"constraint,omitempty"`
	Requirement *int   `json:"requirement,omitempty"`
}

// VersionOutcome is the attestation outcome of a stored version with its
// failure and the time of the evaluation that produced it.
//
// Spec: §4.7.11 (Attestation outcome)
type VersionOutcome struct {
	Outcome     string
	Failure     *AttestationFailure
	EvaluatedAt *time.Time
}

// Equal reports whether two outcomes hold the same outcome and failure. The
// evaluation time is not compared: a cycle writes an unchanged version only
// when the outcome or its failure differs.
func (o VersionOutcome) Equal(p VersionOutcome) bool {
	if o.Outcome != p.Outcome {
		return false
	}
	a, b := o.Failure, p.Failure
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ar, br := -1, -1
	if a.Requirement != nil {
		ar = *a.Requirement
	}
	if b.Requirement != nil {
		br = *b.Requirement
	}
	return a.Code == b.Code && a.Scope == b.Scope && a.Constraint == b.Constraint && ar == br
}

// Outcome returns the attestation outcome a stored row carries.
func (r ManifestRecord) Outcome() VersionOutcome {
	return VersionOutcome{Outcome: r.AttestationOutcome, Failure: r.AttestationFailure, EvaluatedAt: r.AttestationEvaluatedAt}
}

// normalizeOutcome gives a row the outcome every backend stores: an empty
// outcome is not_evaluated, a failure is kept only on a failed row, and the
// evaluation time is held in UTC at the stored precision.
func normalizeOutcome(rec *ManifestRecord) {
	o := normalizedOutcome(rec.Outcome())
	rec.AttestationOutcome, rec.AttestationFailure, rec.AttestationEvaluatedAt = o.Outcome, o.Failure, o.EvaluatedAt
}

// normalizedOutcome returns o as every backend stores it.
func normalizedOutcome(o VersionOutcome) VersionOutcome {
	if o.Outcome == "" {
		o.Outcome = OutcomeNotEvaluated
	}
	if o.Outcome != OutcomeFailed {
		o.Failure = nil
	} else if o.Failure != nil {
		f := *o.Failure
		if f.Requirement != nil {
			r := *f.Requirement
			f.Requirement = &r
		}
		o.Failure = &f
	}
	o.EvaluatedAt = normalizedMarkPtr(o.EvaluatedAt)
	return o
}

// failureColumn encodes a failure for a text column; no failure is the empty
// string.
func failureColumn(f *AttestationFailure) (string, error) {
	if f == nil {
		return "", nil
	}
	b, err := json.Marshal(f)
	if err != nil {
		return "", fmt.Errorf("store: encode attestation failure: %w", err)
	}
	return string(b), nil
}

// parseFailureColumn decodes the failure column.
func parseFailureColumn(col string) (*AttestationFailure, error) {
	if col == "" {
		return nil, nil
	}
	var f AttestationFailure
	if err := json.Unmarshal([]byte(col), &f); err != nil {
		return nil, fmt.Errorf("store: decode attestation failure: %w", err)
	}
	return &f, nil
}

// StoredAttestation is one stored bundle: the exact bytes a cycle verified,
// keyed by tenant and attestation digest, with the values the cycle read from
// its verification.
//
// Spec: §4.7.11 (Attestation storage)
type StoredAttestation struct {
	TenantID string
	// Digest is the attestation digest, sha256:<hex> over Bundle.
	Digest string
	Bundle []byte
	// LayerID and SourceRef are the layer whose cycle first stored the bundle
	// and that cycle's snapshot reference.
	LayerID   string
	SourceRef string
	// PredicateType is the statement's predicate type.
	PredicateType string
	// Instance is the Sigstore instance under which the bundle's signature,
	// chain, and time verified.
	Instance string
	// The certificate fields; empty where the certificate lacks the field.
	SourceRepository      string
	SourceRepositoryOwner string
	SourceRepositoryRef   string
	SignerIssuer          string
	SignerIdentity        string
	// LogIndex is nil when the instance keeps no transparency log.
	LogIndex   *int64
	AttestedAt time.Time
	StoredAt   time.Time
}

// AttestationLink joins one stored version to one stored bundle under a key
// of predicate type and signer.
//
// Spec: §4.7.11 (Attestation storage)
type AttestationLink struct {
	ArtifactID     string
	Version        string
	PredicateType  string
	Instance       string
	SignerIssuer   string
	SignerIdentity string
	// Digest names the stored bundle.
	Digest string
	// Verdict is verified or unverified.
	Verdict string
	// VerifiedUnder is the policy key under which the registry last verified
	// the bundle for this link; empty on an unverified link and on a verified
	// link that a process behind a root record wrote.
	VerifiedUnder string
	LinkedAt      time.Time
	// AttestedAt is the attested time of the linked bundle, which a read
	// fills from the stored bundle; a write does not read it.
	AttestedAt time.Time
}

// LinkKey is the key a version holds one link for: the predicate type and
// the signer, which is the instance with the certificate's issuer and
// identity.
type LinkKey struct {
	PredicateType, Instance, Issuer, Identity string
}

// Key returns the link's key.
func (l AttestationLink) Key() LinkKey {
	return LinkKey{l.PredicateType, l.Instance, l.SignerIssuer, l.SignerIdentity}
}

// LinkFilter selects the links ListAttestationLinks returns. An empty member
// matches every value. LayerID matches the links of versions whose stored
// row, live or soft-deleted, records that layer.
type LinkFilter struct {
	LayerID    string
	ArtifactID string
	Version    string
	Digest     string
}

// VersionAttestations is the attestation write of one version.
type VersionAttestations struct {
	ArtifactID string
	Version    string
	// ReplaceLinks replaces every stored link of the version with Links.
	// Otherwise each of Links replaces the stored link of its key or is
	// added.
	ReplaceLinks bool
	Links        []AttestationLink
	// Updates each replace the stored link of their key only while that
	// link holds the digest, the verdict, and the recorded key the writer
	// read, so a write that read a link changes it only while no other
	// writer changed it.
	Updates []LinkUpdate
	// Outcome, when set, replaces the stored outcome, and only while the
	// stored outcome is OutcomeIf when OutcomeIf is not empty. A version
	// that has no stored row is passed over.
	Outcome   *VersionOutcome
	OutcomeIf string
}

// LinkUpdate is a conditional update of one stored link: Link carries the
// new values, and the update applies only while the stored link of Link's
// key holds Link.Digest, ReadVerdict, and ReadVerifiedUnder.
type LinkUpdate struct {
	Link              AttestationLink
	ReadVerdict       string
	ReadVerifiedUnder string
}

// applies reports whether u applies to the stored link cur.
func (u LinkUpdate) applies(cur AttestationLink) bool {
	return cur.Digest == u.Link.Digest && cur.Verdict == u.ReadVerdict && cur.VerifiedUnder == u.ReadVerifiedUnder
}

// TrustPolicy is the stored trust policy of a layer as a write compares it.
type TrustPolicy struct {
	Mode         string
	Requirements []TrustRequirement
	RequireTypes []string
	LayerRoot    string
}

// PolicyOf returns the trust policy lc stores.
func PolicyOf(lc LayerConfig) TrustPolicy {
	return TrustPolicy{Mode: lc.TrustMode, Requirements: lc.TrustRequirements, RequireTypes: lc.TrustRequireTypes, LayerRoot: lc.TrustLayerRoot}
}

// WithTrustPolicy returns lc holding the trust policy p, so a caller that
// keeps or replaces a layer's policy moves every member of it together.
func (lc LayerConfig) WithTrustPolicy(p TrustPolicy) LayerConfig {
	lc.TrustMode, lc.TrustRequirements, lc.TrustRequireTypes, lc.TrustLayerRoot = p.Mode, p.Requirements, p.RequireTypes, p.LayerRoot
	return lc
}

// AttestationWrites is one store write of bundles, links, and outcomes.
type AttestationWrites struct {
	// Bundles are stored when the tenant holds none under their digest; a
	// stored bundle keeps the layer and reference that first stored it.
	Bundles  []StoredAttestation
	Versions []VersionAttestations
	// Policy, with a claim, makes the write apply only while the live layer
	// stores this trust policy, compared in its stored form, so a cycle in
	// flight across a policy change writes nothing under the earlier policy.
	//
	// Spec: §4.7.11 (Verified links)
	Policy *TrustPolicy
}

// AttestationWriteResult reports the conditional link updates that applied.
type AttestationWriteResult struct {
	Updated []AttestationLink
}

// empty reports whether w writes nothing.
func (w AttestationWrites) empty() bool {
	return len(w.Bundles) == 0 && len(w.Versions) == 0
}

// policyColumns returns the stored form of p for the comparison.
func policyColumns(p TrustPolicy) (mode, reqs, types, root string, err error) {
	reqs, err = trustRequirementsColumn(p.Requirements)
	return p.Mode, reqs, requireTypesColumn(p.RequireTypes), p.LayerRoot, err
}

// RootRecord is the root record of one Sigstore instance: the digest and the
// root time of the trusted root that re-evaluation runs under. An instance
// is configured for the whole deployment, so the record has no tenant.
//
// Spec: §4.7.11 (Root record)
type RootRecord struct {
	Instance   string
	RootDigest string
	RootTime   time.Time
}

// rootRecordEqual reports whether a stored record is the one a conditional
// replacement names; nil names no record.
func rootRecordEqual(stored *RootRecord, want *RootRecord) bool {
	if stored == nil || want == nil {
		return stored == nil && want == nil
	}
	return stored.RootDigest == want.RootDigest && stored.RootTime.Equal(want.RootTime)
}

// normalizeLink gives a link the values every backend stores.
func normalizeLink(l AttestationLink) AttestationLink {
	l.LinkedAt = normalizeMark(l.LinkedAt)
	if l.Verdict != VerdictVerified {
		l.VerifiedUnder = ""
	}
	return l
}

// sortLinks orders links by version and then lists the verified links of a
// version first, each group by key, so every backend lists them alike.
func sortLinks(links []AttestationLink) {
	sort.Slice(links, func(i, j int) bool {
		a, b := links[i], links[j]
		switch {
		case a.ArtifactID != b.ArtifactID:
			return a.ArtifactID < b.ArtifactID
		case a.Version != b.Version:
			return a.Version < b.Version
		case a.Verdict != b.Verdict:
			return a.Verdict == VerdictVerified
		case a.PredicateType != b.PredicateType:
			return a.PredicateType < b.PredicateType
		case a.Instance != b.Instance:
			return a.Instance < b.Instance
		case a.SignerIssuer != b.SignerIssuer:
			return a.SignerIssuer < b.SignerIssuer
		}
		return a.SignerIdentity < b.SignerIdentity
	})
}

// normalizeStoredAttestation gives a bundle row the values every backend
// stores.
func normalizeStoredAttestation(a StoredAttestation) StoredAttestation {
	a.AttestedAt = normalizeMark(a.AttestedAt)
	a.StoredAt = normalizeMark(a.StoredAt)
	a.Bundle = append([]byte(nil), a.Bundle...)
	if a.LogIndex != nil {
		v := *a.LogIndex
		a.LogIndex = &v
	}
	return a
}
