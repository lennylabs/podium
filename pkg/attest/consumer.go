package attest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/version"
)

// Checker runs the §4.7.11 consumer procedure on registry-served records:
// the well-formed rule, the request binding, and the attestation check. It
// is safe for concurrent use.
type Checker struct {
	// Policy is the consumer trust policy; nil resolves every ID to none.
	Policy *Policy
	// Bundles supplies bundles by digest, normally a CachedBundles.
	Bundles BundleSource
	// MaxBytes is the consumer's PODIUM_ATTESTATION_MAX_BYTES; zero is the
	// §13.12 default.
	MaxBytes int64

	// memo keeps the verification result of a bundle under one trusted
	// root or one key, so a record delivered again, or another record
	// attested by the same bundle, does not repeat the verification.
	memo verifyMemo
}

// Delivery is one record a consumer received: the request it sent, and the
// response the §4.7.10 procedure verified. Served.Members carries the
// deprecated, replaced_by, and attestations members by exact name; a
// record delivered from a cache carries the members cached with it.
type Delivery struct {
	Request Request
	Served  version.Served
}

// Result is the outcome of a delivery that the procedure did not refuse.
type Result struct {
	// Resolution is the policy of the requested ID.
	Resolution Resolution
	// Checked reports whether the attestation check ran, which it does for
	// an ID whose mode is above none.
	Checked bool
	// Candidates are the package digests of step 5, when the check ran.
	Candidates []string
	// SubjectName is the expected subject name of step 6.
	SubjectName string
	// PackageDigest is the candidate that the bundle meeting the first
	// requirement lists, empty when no requirement was met.
	PackageDigest string
	// Accepted lists, for each requirement met, the bundle that met it.
	Accepted []AcceptedBundle
	// Warnings lists the failures that the mode warn reports.
	Warnings []*Failure
}

// AcceptedBundle is a bundle that met a requirement, with the signer the
// consumer verified: the instance name its own configuration gives the
// trusted root, and the issuer and identity of the certificate, or, for a
// key-based signer, an empty instance and issuer and the key's identifier.
type AcceptedBundle struct {
	Digest        string
	PredicateType string
	Instance      string
	Issuer        string
	Identity      string
	// AttestedTime is the bundle's attested time, zero when it has none.
	AttestedTime time.Time
}

// Verify runs the §4.7.11 consumer procedure on one delivery, after the
// §4.7.10 procedure and before the consumer reads any policy input out of
// the record, caches it, returns it, or writes any byte. The well-formed
// rule and the request binding run under every mode and fail with
// materialize.request_mismatch. The attestation check runs when the
// requested ID resolves to a mode above none: under require a failure is
// returned as the error, and under warn it is a warning in the result. Every
// error is a *Failure.
//
// Spec: §4.7.11 (Well-formed requests and records, Request binding,
// Attestation check)
func (c *Checker) Verify(ctx context.Context, d Delivery) (Result, error) {
	rec := d.Served.Record
	if err := CheckRequestedID(d.Request.ID); err != nil {
		return Result{}, err
	}
	if err := CheckRecordPaths(rec); err != nil {
		return Result{}, err
	}
	if err := BindRequest(d.Request, rec); err != nil {
		return Result{}, err
	}
	res := c.resolve(d.Request.ID)
	if !res.Resolution.Mode.AboveNone() {
		return res, nil
	}
	res.Checked = true
	return res.settle(c.check(ctx, d, &res))
}

// resolve returns the result of an ID before the check: its resolution
// under the policy, or none when the checker holds no policy.
func (c *Checker) resolve(id string) Result {
	res := Result{Resolution: Resolution{ID: id, Mode: ModeNone, UserMode: ModeNone, key: "default"}}
	if c.Policy != nil {
		res.Resolution = c.Policy.Resolve(id)
	}
	return res
}

// settle applies the mode to the error of the check: under require it
// refuses, and under warn it becomes a warning of the result.
func (r Result) settle(err error) (Result, error) {
	if err == nil {
		return r, nil
	}
	if r.Resolution.Mode == ModeRequire {
		return Result{}, err
	}
	r.Warnings = append(r.Warnings, asFailure(err))
	return r, nil
}

// runPlan applies a rule check to the rules of a plan: a failure of an
// enforced rule is returned, and a failure of a reported rule is added as a
// warning, and the check goes on to the next rule.
func (c *Checker) runPlan(plan CheckPlan, res *Result, apply func(RuleMatch) error) error {
	for _, m := range plan.Enforced {
		if err := apply(m); err != nil {
			return err
		}
	}
	for _, m := range plan.Reported {
		if err := apply(m); err != nil {
			res.Warnings = append(res.Warnings, asFailure(err))
		}
	}
	return nil
}

// asFailure returns err as a *Failure, wrapping an error of another kind
// as an invalid attestation so that no error escapes without a code.
func asFailure(err error) *Failure {
	if f, ok := FailureOf(err); ok {
		return f
	}
	return fail(CodeAttestationInvalid, "", "the attestation check failed", err)
}

// check runs the attestation check steps. A failure of steps 1 to 8, and
// of a rule the record meets under require, is returned: Verify refuses it
// under require and reports it as a warning under warn. The failure of a
// rule whose failures are reported is added as a warning and the check
// goes on to the next rule.
func (c *Checker) check(ctx context.Context, d Delivery, res *Result) error {
	plan, err := res.Resolution.Plan()
	res.Warnings = append(res.Warnings, plan.Warnings...)
	if err != nil {
		return err
	}
	rec := d.Served.Record
	if err := CheckDocumentSlots(rec); err != nil {
		return err
	}
	if err := CheckManifestBody(rec); err != nil {
		return err
	}
	life, err := ReadLifecycle(d.Served.Members)
	if err != nil {
		return err
	}
	if res.Candidates, err = PackageDigestCandidates(rec, life); err != nil {
		return err
	}
	if res.SubjectName, err = ExpectedSubjectName(d.Request.ID, rec); err != nil {
		return err
	}
	bundles, err := c.fetch(ctx, d.Served)
	if err != nil {
		return err
	}
	return c.runPlan(plan, res, func(m RuleMatch) error {
		return c.applyRule(ctx, m, bundles, res)
	})
}

// applyRule runs steps 9 and 10 for one rule and records the bundles that
// met its requirements.
func (c *Checker) applyRule(ctx context.Context, m RuleMatch, bundles []fetched, res *Result) error {
	accepted, err := c.meetRule(ctx, m, bundles, res.SubjectName, res.Candidates)
	if err != nil {
		return err
	}
	for _, a := range accepted {
		if res.PackageDigest == "" {
			res.PackageDigest = a.digest
		}
		res.Accepted = append(res.Accepted, a.AcceptedBundle)
	}
	return nil
}

// fetched is one bundle of step 8 with its digest.
type fetched struct {
	digest string
	raw    []byte
}

// fetch applies steps 7 and 8: it reads the usable references and fetches
// each bundle, checking its size and its digest. A reference whose bundle is
// not found, unavailable, unreachable, or not cached offline is unusable,
// and the others are still fetched, so one such reference does not hide a
// bundle that meets the rule.
func (c *Checker) fetch(ctx context.Context, served version.Served) ([]fetched, error) {
	refs, err := ReadReferences(served.Members)
	if err != nil {
		return nil, err
	}
	if len(refs) == 0 {
		return nil, fail(CodeAttestationMissing, "", "the record carries no usable attestation reference", nil)
	}
	if c.Bundles == nil {
		return nil, fail(CodeAttestationMissing, "", "the consumer has no bundle source", nil)
	}
	limit := maxBytesOr(c.MaxBytes)
	var out []fetched
	var unreachable error
	for _, d := range refs {
		b, err := c.Bundles.Bundle(ctx, d)
		if err != nil {
			usable, reached := classifyFetchError(err)
			if usable != nil {
				return nil, usable
			}
			if !reached {
				unreachable = err
			}
			continue
		}
		switch {
		case int64(len(b)) > limit:
			return nil, fail(CodeAttestationInvalid, ConstraintLimits, "a bundle exceeds the size limit", nil)
		case AttestationDigest(b) != d:
			return nil, fail(CodeAttestationInvalid, ConstraintFetch, "a fetched bundle's digest differs from its reference", nil)
		}
		out = append(out, fetched{digest: d, raw: b})
	}
	switch {
	case len(out) > 0:
		return out, nil
	case unreachable != nil:
		return nil, fail(CodeRegistryUnreachable, "", "no referenced bundle could be fetched, and the registry could not be reached", unreachable)
	}
	return nil, fail(CodeAttestationMissing, "", "no referenced bundle can be read", nil)
}

// classifyFetchError sorts the error of one bundle fetch. A refusal that
// stops the check (a body over the size limit, a malformed response) is
// returned as stop. Every other error makes the reference unusable, and
// reached is false when the fetch did not reach the registry.
func classifyFetchError(err error) (stop error, reached bool) {
	switch {
	case errors.Is(err, ErrBundleNotFound), errors.Is(err, ErrBundleUnavailable):
		return nil, true
	}
	f, ok := FailureOf(err)
	switch {
	case !ok, f.Code == CodeRegistryUnreachable:
		return nil, false
	case f.Code == CodeAttestationMissing:
		return nil, true
	}
	return f, true
}

// acceptedFor is an accepted bundle with the candidate its subject lists.
type acceptedFor struct {
	AcceptedBundle
	digest string
}

// meetRule applies steps 9 and 10 to one rule: the public-good root of the
// rule's file is not past its maximum age when a signer names it, and each
// requirement is met by a fetched bundle.
func (c *Checker) meetRule(ctx context.Context, m RuleMatch, bundles []fetched, name string, candidates []string) ([]acceptedFor, error) {
	rule := m.Rule()
	if err := c.checkRootAge(ctx, m.File, rule); err != nil {
		return nil, err
	}
	var out []acceptedFor
	for _, req := range rule.Requirements {
		a, err := c.meetRequirement(ctx, m.File, req, bundles, name, candidates)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// checkRootAge applies step 9.
func (c *Checker) checkRootAge(ctx context.Context, f *PolicyFile, rule PolicyRule) error {
	for _, req := range rule.Requirements {
		for _, s := range req.Signers {
			if s.Instance != sign.PublicGoodInstance {
				continue
			}
			inst, ok := f.Instance(sign.PublicGoodInstance)
			if !ok {
				// NewPolicyFile refuses a file whose signer names
				// public-good with no root, so a loaded file reaches no
				// such signer.
				return fail(CodeAttestationRootStale, "", "no public-good root is available", nil)
			}
			if _, err := inst.Root.AttestationRoot(ctx); errors.Is(err, sign.ErrAttestationRootStale) {
				return fail(CodeAttestationRootStale, "", "the public-good root is past its maximum age", err)
			}
			return nil
		}
	}
	return nil
}

// meetRequirement applies step 10 to one requirement. Each pair of a
// fetched bundle and a signer of the requirement gives an observation, and
// the first observation that meets the requirement wins; otherwise
// RequirementFailure chooses the code.
func (c *Checker) meetRequirement(ctx context.Context, f *PolicyFile, req PolicyRequirement,
	bundles []fetched, name string, candidates []string) (acceptedFor, error) {
	var obs []SignerObservation
	for _, b := range bundles {
		for _, s := range req.Signers {
			v, err := c.verifyFor(ctx, f, s, b)
			if err != nil {
				r, ok := sign.AttestationReasonOf(err)
				switch {
				case ok:
					obs = append(obs, SignerObservation{Reason: r})
					continue
				case errors.Is(err, sign.ErrAttestationRootStale):
					return acceptedFor{}, fail(CodeAttestationRootStale, "", "the public-good root is past its maximum age", err)
				}
				return acceptedFor{}, fail(CodeAttestationInvalid, "", "a trusted root cannot verify the bundle", err)
			}
			o := SignerObservation{Verified: true, Accepted: v.AcceptsSigner(s.Signer) == nil}
			o.PredicateType = o.Accepted && (req.PredicateType == "" || v.Statement.PredicateType == req.PredicateType)
			d, listed := listedCandidate(v.Statement, name, candidates)
			o.Subject = o.PredicateType && listed
			if o.Subject {
				return acceptedFor{digest: d, AcceptedBundle: AcceptedBundle{
					Digest: b.digest, PredicateType: v.Statement.PredicateType,
					Instance: v.Instance, Issuer: v.SignerIssuer(), Identity: v.SignerIdentity(),
					AttestedTime: v.AttestedTime,
				}}, nil
			}
			obs = append(obs, o)
		}
	}
	return acceptedFor{}, RequirementFailure(obs)
}

// SignerObservation is what one fetched bundle showed for one signer of a
// requirement in step 10 of the §4.7.11 attestation check. Each flag holds
// only when the ones before it hold.
type SignerObservation struct {
	// Verified reports that the bundle verified under the signer's
	// instance or key; Reason is the refusal reason otherwise.
	Verified bool
	Reason   sign.AttestationReason
	// Accepted reports that the signer accepts the verified bundle.
	Accepted bool
	// PredicateType reports that the accepted bundle carries the
	// requirement's predicate type, or that the requirement names none.
	PredicateType bool
	// Subject reports that the statement lists the expected subject name
	// with a candidate package digest.
	Subject bool
}

// RequirementFailure chooses the outcome of a requirement from the
// observations of step 10: nil when one observation meets it, and
// otherwise the failure of the observation that came closest: an accepted
// bundle of the predicate type with no matching subject is
// materialize.attestation_mismatch (subject), an accepted bundle of
// another predicate type is materialize.attestation_mismatch
// (predicate_type), a verified bundle that no signer accepts is
// materialize.attestation_untrusted (signer), and otherwise the failure is
// materialize.attestation_invalid with the latest refusal reason in the
// §4.7.11 order, signature when there is none.
//
// Spec: §4.7.11 (Attestation check, step 10)
func RequirementFailure(obs []SignerObservation) error {
	best := 0
	var reason sign.AttestationReason
	for _, o := range obs {
		switch {
		case o.Subject:
			return nil
		case o.PredicateType:
			best = max(best, 3)
		case o.Accepted:
			best = max(best, 2)
		case o.Verified:
			best = max(best, 1)
		default:
			reason = sign.LaterAttestationReason(reason, o.Reason)
		}
	}
	switch best {
	case 3:
		return fail(CodeAttestationMismatch, ConstraintSubject, "no accepted bundle lists the expected subject with a candidate package digest", nil)
	case 2:
		return fail(CodeAttestationMismatch, ConstraintPredicateType, "an accepted bundle carries another predicate type", nil)
	case 1:
		return fail(CodeAttestationUntrusted, ConstraintSigner, "no bundle is accepted for a signer of the requirement", nil)
	}
	if reason == "" {
		reason = sign.ReasonSignature
	}
	return fail(CodeAttestationInvalid, string(reason), "no bundle verifies for a signer of the requirement", nil)
}

// listedCandidate returns the candidate that the statement lists under
// name.
func listedCandidate(st sign.AttestationStatement, name string, candidates []string) (string, bool) {
	for _, d := range candidates {
		if st.Lists(name, strings.TrimPrefix(d, digestPrefix)) {
			return d, true
		}
	}
	return "", false
}

// verifyFor verifies a bundle for one signer: under the signer's key for a
// key-based signer, and under the instance it names in its own file for a
// certificate signer. The result is memoized by the bundle's digest and the
// root digest or key identifier, because neither the result nor its
// refusal depends on anything else.
func (c *Checker) verifyFor(ctx context.Context, f *PolicyFile, s PolicySigner, b fetched) (*sign.VerifiedAttestation, error) {
	if s.Signer.PublicKey != "" {
		key, err := sign.ParseAttestationPublicKey(s.Signer.PublicKey)
		if err != nil {
			// The policy load parsed the key, so this is unreachable; the
			// signer then accepts nothing.
			return nil, err
		}
		return c.memo.do(b.digest+"|key|"+key.ID(), func() (*sign.VerifiedAttestation, error) {
			return sign.VerifyKeySignedAttestation(b.raw, key)
		})
	}
	inst, ok := f.Instance(s.Instance)
	if !ok {
		return nil, fail(CodeAttestationInvalid, "", "the signer's instance has no trusted root", nil)
	}
	root, err := inst.Root.AttestationRoot(ctx)
	if err != nil {
		return nil, err
	}
	fixed := inst
	fixed.Root = root
	key := b.digest + "|" + inst.Name + "|" + root.Digest() + "|" + tlogKey(inst)
	return c.memo.do(key, func() (*sign.VerifiedAttestation, error) {
		return sign.VerifyAttestationBundle(ctx, b.raw, fixed)
	})
}

// tlogKey renders an instance's log requirements for a memo key.
func tlogKey(inst sign.AttestationInstance) string {
	return fmt.Sprintf("%t,%t", inst.TransparencyLog, inst.CertificateTransparency)
}

// verifyMemo memoizes verification results. Its size is bounded, and
// reaching the bound empties it.
type verifyMemo struct {
	// mu guards m.
	mu sync.Mutex
	m  map[string]memoEntry
}

// memoEntry is one memoized result.
type memoEntry struct {
	v   *sign.VerifiedAttestation
	err error
}

// maxMemo bounds the memo.
const maxMemo = 1024

// do returns the memoized result of key, or runs fn and keeps a result that
// is a verification or a refusal. Another error, such as a cancelled
// context, is not kept.
func (m *verifyMemo) do(key string, fn func() (*sign.VerifiedAttestation, error)) (*sign.VerifiedAttestation, error) {
	m.mu.Lock()
	e, ok := m.m[key]
	m.mu.Unlock()
	if ok {
		return e.v, e.err
	}
	v, err := fn()
	if _, refused := sign.AttestationReasonOf(err); err == nil || refused {
		m.mu.Lock()
		if m.m == nil || len(m.m) >= maxMemo {
			m.m = map[string]memoEntry{}
		}
		m.m[key] = memoEntry{v: v, err: err}
		m.mu.Unlock()
	}
	return v, err
}
