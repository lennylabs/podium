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
	res := Result{Resolution: Resolution{ID: d.Request.ID, Mode: ModeNone, UserMode: ModeNone, key: "default"}}
	if c.Policy != nil {
		res.Resolution = c.Policy.Resolve(d.Request.ID)
	}
	if !res.Resolution.Mode.AboveNone() {
		return res, nil
	}
	res.Checked = true
	if err := c.check(ctx, d, &res); err != nil {
		if res.Resolution.Mode == ModeRequire {
			return Result{}, err
		}
		res.Warnings = append(res.Warnings, asFailure(err))
	}
	return res, nil
}

// asFailure returns err as a *Failure, wrapping an error of another kind
// as an invalid attestation so that no error escapes without a code.
func asFailure(err error) *Failure {
	if f, ok := FailureOf(err); ok {
		return f
	}
	return fail(CodeAttestationInvalid, "", "the attestation check failed", err)
}

// check runs the attestation check steps. Under require the first failure
// is returned. Under warn a failure that stops the check is returned, and
// the failure of one rule among several is added as a warning while the
// check goes on to the next rule.
func (c *Checker) check(ctx context.Context, d Delivery, res *Result) error {
	r := res.Resolution
	mode := r.Mode
	rules, err := c.selectRules(r, res)
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
	for _, m := range rules {
		accepted, err := c.meetRule(ctx, m, bundles, res.SubjectName, res.Candidates)
		if err != nil {
			if mode == ModeRequire {
				return err
			}
			res.Warnings = append(res.Warnings, asFailure(err))
			continue
		}
		for _, a := range accepted {
			if res.PackageDigest == "" {
				res.PackageDigest = a.digest
			}
			res.Accepted = append(res.Accepted, a.AcceptedBundle)
		}
	}
	return nil
}

// selectRules applies step 1: the rules of the ID's mode that hold a
// requirement, after the user mode rule. A user mode of warn that no
// user-source rule meets adds a warning and the check goes on.
func (c *Checker) selectRules(r Resolution, res *Result) ([]RuleMatch, error) {
	if r.UserMode.AboveNone() && !r.userRuleMet() {
		f := fail(CodeAttestationUntrusted, ConstraintUserRule, "no rule of a user-source file with a requirement matches the ID", nil)
		if r.UserMode == ModeRequire {
			return nil, f
		}
		res.Warnings = append(res.Warnings, f)
	}
	var rules []RuleMatch
	for _, m := range r.metRules(r.Mode) {
		if len(m.Rule().Requirements) > 0 {
			rules = append(rules, m)
		}
	}
	switch {
	case len(r.Matches) == 0:
		return nil, fail(CodeAttestationUntrusted, ConstraintNoRule, "no rule matches the ID, and the default mode is above none", nil)
	case len(rules) == 0:
		return nil, fail(CodeAttestationUntrusted, ConstraintNoRequirements, "the rules of the ID's mode hold no requirement", nil)
	}
	return rules, nil
}

// fetched is one bundle of step 8 with its digest.
type fetched struct {
	digest string
	raw    []byte
}

// fetch applies steps 7 and 8: it reads the usable references and fetches
// each bundle, checking its size and its digest.
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
	for _, d := range refs {
		b, err := c.Bundles.Bundle(ctx, d)
		switch {
		case errors.Is(err, ErrBundleNotFound):
			continue
		case err != nil:
			if f, ok := FailureOf(err); ok {
				return nil, f
			}
			return nil, fail(CodeRegistryUnreachable, "", "the bundle fetch did not reach the registry", err)
		case int64(len(b)) > limit:
			return nil, fail(CodeAttestationInvalid, ConstraintLimits, "a bundle exceeds the size limit", nil)
		case AttestationDigest(b) != d:
			return nil, fail(CodeAttestationInvalid, ConstraintFetch, "a fetched bundle's digest differs from its reference", nil)
		}
		out = append(out, fetched{digest: d, raw: b})
	}
	if len(out) == 0 {
		return nil, fail(CodeAttestationMissing, "", "the registry holds no referenced bundle", nil)
	}
	return out, nil
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

// closeness ranks how near a bundle came to meeting a requirement, which
// chooses the code of a failed requirement.
type closeness int

const (
	closeInvalid closeness = iota
	closeUntrusted
	closePredicate
	closeSubject
)

// meetRequirement applies step 10 to one requirement and reports a failed
// requirement by the bundle that came closest to meeting it: an accepted
// bundle with no matching subject, then one of another predicate type, then
// a bundle that verifies under a named instance and that no signer accepts,
// and otherwise the latest refusal reason in the §4.7.11 order.
func (c *Checker) meetRequirement(ctx context.Context, f *PolicyFile, req PolicyRequirement,
	bundles []fetched, name string, candidates []string) (acceptedFor, error) {
	best := closeInvalid
	var reason sign.AttestationReason
	for _, b := range bundles {
		for _, s := range req.Signers {
			v, err := c.verifyFor(ctx, f, s, b)
			if err != nil {
				if r, ok := sign.AttestationReasonOf(err); ok {
					reason = sign.LaterAttestationReason(reason, r)
					continue
				}
				if errors.Is(err, sign.ErrAttestationRootStale) {
					return acceptedFor{}, fail(CodeAttestationRootStale, "", "the public-good root is past its maximum age", err)
				}
				return acceptedFor{}, fail(CodeAttestationInvalid, "", "a trusted root cannot verify the bundle", err)
			}
			if v.AcceptsSigner(s.Signer) != nil {
				best = max(best, closeUntrusted)
				continue
			}
			if req.PredicateType != "" && v.Statement.PredicateType != req.PredicateType {
				best = max(best, closePredicate)
				continue
			}
			if d, ok := listedCandidate(v.Statement, name, candidates); ok {
				return acceptedFor{digest: d, AcceptedBundle: AcceptedBundle{
					Digest: b.digest, PredicateType: v.Statement.PredicateType,
					Instance: v.Instance, Issuer: v.SignerIssuer(), Identity: v.SignerIdentity(),
					AttestedTime: v.AttestedTime,
				}}, nil
			}
			best = closeSubject
		}
	}
	switch best {
	case closeSubject:
		return acceptedFor{}, fail(CodeAttestationMismatch, ConstraintSubject, "no accepted bundle lists the expected subject with a candidate package digest", nil)
	case closePredicate:
		return acceptedFor{}, fail(CodeAttestationMismatch, ConstraintPredicateType, "an accepted bundle carries another predicate type", nil)
	case closeUntrusted:
		return acceptedFor{}, fail(CodeAttestationUntrusted, ConstraintSigner, "no bundle is accepted for a signer of the requirement", nil)
	}
	if reason == "" {
		reason = sign.ReasonSignature
	}
	return acceptedFor{}, fail(CodeAttestationInvalid, string(reason), "no bundle verifies for a signer of the requirement", nil)
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
