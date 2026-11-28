package attest_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/attest"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/version"
)

// agentPlainMD is a signed agent with no lifecycle fields.
const agentPlainMD = "---\ntype: agent\nversion: 2.0.0\ndescription: Pay an invoice\nsensitivity: low\n---\n\nPay.\n"

// mapSource is a BundleSource over a map; a digest it lacks is not found.
type mapSource struct {
	bundles map[string][]byte
	err     error
	calls   atomic.Int64
}

// Bundle returns the mapped bytes.
func (m *mapSource) Bundle(_ context.Context, digest string) ([]byte, error) {
	m.calls.Add(1)
	if m.err != nil {
		return nil, m.err
	}
	b, ok := m.bundles[digest]
	if !ok {
		return nil, attest.ErrBundleNotFound
	}
	return b, nil
}

// consumerFixture is a served agent record, the bundle the harness signed
// over its package digest, and a checker under one policy file.
type consumerFixture struct {
	h      *sh.Harness
	served version.Served
	bundle []byte
	src    *mapSource
	digest string
}

// newConsumerFixture builds the record of finance/pay and a bundle that
// lists it under the harness's default email signer.
func newConsumerFixture(t *testing.T) *consumerFixture {
	t.Helper()
	h := sh.New(t)
	f := &consumerFixture{h: h, src: &mapSource{bundles: map[string][]byte{}}}
	f.digest = signerDigest(t, map[string][]byte{"ARTIFACT.md": []byte(agentPlainMD)})
	f.bundle = f.sign(t, "finance/pay@2.0.0", f.digest)
	f.served = version.Served{
		Record: version.DeliveryRecord{ID: "finance/pay", Version: "2.0.0", Type: "agent", Sensitivity: "low",
			Frontmatter: agentPlainMD, ManifestBody: "Pay.\n"},
	}
	f.setReferences(t, f.bundle)
	return f
}

// sign returns a bundle of the default signer that lists name with digest,
// and makes the source serve it.
func (f *consumerFixture) sign(t *testing.T, name, digest string, opts ...sh.BundleOpt) []byte {
	t.Helper()
	return f.signAs(t, slsaType, name, digest, opts...)
}

// signAs is sign with a predicate type.
func (f *consumerFixture) signAs(t *testing.T, predicate, name, digest string, opts ...sh.BundleOpt) []byte {
	t.Helper()
	opts = append([]sh.BundleOpt{sh.WithTimestamp(sh.TSAPrimary, sh.WithGenTime(f.h.Clock().Add(time.Minute)))}, opts...)
	b := f.h.DSSEBundle(sh.Statement(t, predicate, sh.Subject{Name: name, SHA256: strings.TrimPrefix(digest, "sha256:")}), opts...)
	f.src.bundles[attest.AttestationDigest(b)] = b
	return b
}

// setReferences sets the attestations member to the bundles' digests.
func (f *consumerFixture) setReferences(t *testing.T, bundles ...[]byte) {
	t.Helper()
	var refs []map[string]string
	for _, b := range bundles {
		refs = append(refs, map[string]string{"digest": attest.AttestationDigest(b), "verdict": "verified"})
	}
	f.setMember(t, "attestations", refs)
}

// setMember sets a top-level member of the served response.
func (f *consumerFixture) setMember(t *testing.T, name string, v any) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if f.served.Members == nil {
		f.served.Members = map[string]json.RawMessage{}
	}
	f.served.Members[name] = raw
}

// checker returns a checker under one user-source file of rules.
func (f *consumerFixture) checker(t *testing.T, rules ...string) *attest.Checker {
	t.Helper()
	return f.checkerWith(t, attest.ModeNone, sign.AttestationInstance{}, rules...)
}

// checkerWith returns a checker under one user-source file of rules, with a
// default mode and a public-good instance.
func (f *consumerFixture) checkerWith(t *testing.T, def attest.Mode, pg sign.AttestationInstance, rules ...string) *attest.Checker {
	t.Helper()
	file := writePolicy(t, f.h, policyJSON(rules...), pg)
	p, err := attest.NewPolicy(attest.Source{Class: attest.UserSource, DefaultMode: def, Files: []*attest.PolicyFile{file}})
	if err != nil {
		t.Fatal(err)
	}
	return &attest.Checker{Policy: p, Bundles: f.src}
}

// verify runs the checker on the fixture's record requested as id.
func (f *consumerFixture) verify(c *attest.Checker, id, pin string) (attest.Result, error) {
	return c.Verify(context.Background(), attest.Delivery{Request: attest.Request{ID: id, Pin: pin}, Served: f.served})
}

// Spec: §4.7.11 (Attestation check) — a record whose bundle a signer of its
// require rule accepts, with the expected subject and a candidate digest,
// passes and reports the accepted bundle with the signer the consumer
// verified.
func TestChecker_RequirePasses(t *testing.T) {
	t.Parallel()
	f := newConsumerFixture(t)
	c := f.checker(t, requireRule("require", "finance/**"))
	res, err := f.verify(c, "finance/pay", "2.x")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Checked || res.PackageDigest != f.digest || res.SubjectName != "finance/pay@2.0.0" || len(res.Accepted) != 1 || len(res.Warnings) != 0 {
		t.Fatalf("result = %+v", res)
	}
	a := res.Accepted[0]
	if a.Digest != attest.AttestationDigest(f.bundle) || a.Instance != "github" || a.Issuer != sh.DefaultIssuer ||
		a.Identity != sh.DefaultSAN || a.PredicateType != slsaType || a.AttestedTime.IsZero() {
		t.Fatalf("accepted = %+v", a)
	}
	// A second delivery verifies from the memo and fetches again from the
	// source, which a CachedBundles would answer.
	if _, err := f.verify(c, "finance/pay", ""); err != nil {
		t.Fatal(err)
	}
}

// Spec: §4.7.11 (Attestation check, Request binding) — an ID under none
// runs no check and fetches nothing, a nil policy is none, and the request
// binding refuses under none.
func TestChecker_NoneRunsBindingOnly(t *testing.T) {
	t.Parallel()
	f := newConsumerFixture(t)
	c := f.checker(t, `{"ids":["finance/**"],"mode":"none"}`)
	res, err := f.verify(c, "finance/pay", "")
	if err != nil || res.Checked || f.src.calls.Load() != 0 {
		t.Fatalf("none: %+v, %v, %d fetches", res, err, f.src.calls.Load())
	}
	_, err = f.verify(c, "personal/x", "")
	wantFailure(t, err, attest.CodeRequestMismatch, attest.ConstraintID)
	_, err = f.verify(&attest.Checker{}, "finance/pay", "3.0.0")
	wantFailure(t, err, attest.CodeRequestMismatch, attest.ConstraintVersion)
	_, err = f.verify(&attest.Checker{}, "GIT~1", "")
	wantFailure(t, err, attest.CodeRequestMismatch, attest.ConstraintIDForm)
	g := newConsumerFixture(t)
	g.served.Record.Resources = map[string]string{"../x": attest.FileDigest(nil)}
	_, err = g.verify(&attest.Checker{}, "finance/pay", "")
	wantFailure(t, err, attest.CodeRequestMismatch, attest.ConstraintResourcePath)
	if res, err := f.verify(&attest.Checker{}, "finance/pay", ""); err != nil || res.Checked || res.Resolution.RuleKey() != "default" {
		t.Fatalf("nil policy: %+v, %v", res, err)
	}
}

// Spec: §4.7.11 (Attestation check) — under warn a failure is a warning
// whose text names the requested ID, the code, and the constraint, and the
// record is delivered.
func TestChecker_WarnDelivers(t *testing.T) {
	t.Parallel()
	f := newConsumerFixture(t)
	other := f.sign(t, "finance/pay@2.0.0", "sha256:"+strings.Repeat("0", 64))
	f.setReferences(t, other)
	res, err := f.verify(f.checker(t, requireRule("warn", "finance/**")), "finance/pay", "")
	if err != nil || len(res.Warnings) != 1 {
		t.Fatalf("warn: %+v, %v", res, err)
	}
	if got := res.Warnings[0].WarningText("finance/pay"); got != "finance/pay: materialize.attestation_mismatch (constraint subject)" {
		t.Fatalf("warning = %q", got)
	}
	f.setReferences(t)
	res, err = f.verify(f.checker(t, requireRule("warn", "finance/**")), "finance/pay", "")
	if err != nil || len(res.Warnings) != 1 || res.Warnings[0].Code != attest.CodeAttestationMissing {
		t.Fatalf("warn missing: %+v, %v", res, err)
	}
}

// Spec: §4.7.11 (Attestation check, steps 7 and 8) — no usable reference
// and only not-found references are missing; more than 16 references, a
// member that is no array, a bundle over the size limit, and a digest
// mismatch are invalid; a source that cannot reach the registry is
// network.registry_unreachable.
// Matrix: §6.10 (materialize.attestation_missing)
// Matrix: §6.10 (materialize.attestation_invalid)
func TestChecker_ReferencesAndFetch(t *testing.T) {
	t.Parallel()
	rule := requireRule("require", "finance/**")
	f := newConsumerFixture(t)
	c := f.checker(t, rule)

	f.setMember(t, "attestations", []map[string]any{{"digest": "sha256:ABC"}, {"digest": 1}, {}})
	_, err := f.verify(c, "finance/pay", "")
	wantFailure(t, err, attest.CodeAttestationMissing, "")

	f.setMember(t, "attestations", []map[string]string{{"digest": "sha256:" + strings.Repeat("a", 64)}})
	_, err = f.verify(c, "finance/pay", "")
	wantFailure(t, err, attest.CodeAttestationMissing, "")

	many := make([]map[string]string, 17)
	for i := range many {
		many[i] = map[string]string{"digest": attest.AttestationDigest(f.bundle)}
	}
	f.setMember(t, "attestations", many)
	_, err = f.verify(c, "finance/pay", "")
	wantFailure(t, err, attest.CodeAttestationInvalid, attest.ConstraintReferences)

	f.setMember(t, "attestations", "sha256:x")
	_, err = f.verify(c, "finance/pay", "")
	wantFailure(t, err, attest.CodeAttestationInvalid, attest.ConstraintReferences)

	f.setReferences(t, f.bundle)
	small := &attest.Checker{Policy: c.Policy, Bundles: f.src, MaxBytes: 16}
	_, err = f.verify(small, "finance/pay", "")
	wantFailure(t, err, attest.CodeAttestationInvalid, attest.ConstraintLimits)

	swapped := &mapSource{bundles: map[string][]byte{attest.AttestationDigest(f.bundle): append([]byte(" "), f.bundle...)}}
	_, err = f.verify(&attest.Checker{Policy: c.Policy, Bundles: swapped}, "finance/pay", "")
	wantFailure(t, err, attest.CodeAttestationInvalid, attest.ConstraintFetch)

	down := &mapSource{err: errors.New("dial tcp: connection refused")}
	_, err = f.verify(&attest.Checker{Policy: c.Policy, Bundles: down}, "finance/pay", "")
	wantFailure(t, err, attest.CodeRegistryUnreachable, "")

	coded := &mapSource{err: &attest.Failure{Code: attest.CodeAttestationMissing}}
	_, err = f.verify(&attest.Checker{Policy: c.Policy, Bundles: coded}, "finance/pay", "")
	wantFailure(t, err, attest.CodeAttestationMissing, "")

	_, err = f.verify(&attest.Checker{Policy: c.Policy}, "finance/pay", "")
	wantFailure(t, err, attest.CodeAttestationMissing, "")
}

// Spec: §4.7.11 (Attestation check, step 10) — a failed requirement is
// reported by the bundle that came closest: an accepted bundle of another
// predicate type is a mismatch, a bundle that verifies under the instance
// and that no signer accepts is untrusted, and a bundle that fails
// verification is invalid with its refusal reason; an accepted bundle with
// no matching subject outranks the others.
// Matrix: §6.10 (materialize.attestation_mismatch)
// Matrix: §6.10 (materialize.attestation_untrusted)
func TestChecker_RequirementFailures(t *testing.T) {
	t.Parallel()
	f := newConsumerFixture(t)
	c := f.checker(t, requireRule("require", "finance/**"))
	cases := []struct {
		name             string
		bundles          func() [][]byte
		code, constraint string
	}{
		{"predicate type", func() [][]byte {
			return [][]byte{f.signAs(t, "https://example.com/scan", "finance/pay@2.0.0", f.digest)}
		},
			attest.CodeAttestationMismatch, attest.ConstraintPredicateType},
		{"other subject", func() [][]byte { return [][]byte{f.sign(t, "finance/pay@2.0.1", f.digest)} },
			attest.CodeAttestationMismatch, attest.ConstraintSubject},
		{"other signer", func() [][]byte {
			return [][]byte{f.sign(t, "finance/pay@2.0.0", f.digest, sh.WithLeaf(sh.LeafEmail("bob@acme.com")))}
		}, attest.CodeAttestationUntrusted, attest.ConstraintSigner},
		{"bad signature", func() [][]byte { return [][]byte{f.sign(t, "finance/pay@2.0.0", f.digest, sh.WithBadSignature())} },
			attest.CodeAttestationInvalid, string(sign.ReasonSignature)},
		{"subject outranks signer", func() [][]byte {
			return [][]byte{
				f.sign(t, "finance/pay@2.0.0", f.digest, sh.WithLeaf(sh.LeafEmail("bob@acme.com"))),
				f.sign(t, "finance/pay@9.0.0", f.digest),
			}
		}, attest.CodeAttestationMismatch, attest.ConstraintSubject},
		{"untrusted outranks invalid", func() [][]byte {
			return [][]byte{
				f.sign(t, "finance/pay@2.0.0", f.digest, sh.WithBadSignature()),
				f.sign(t, "finance/pay@2.0.0", f.digest, sh.WithLeaf(sh.LeafEmail("bob@acme.com"))),
			}
		}, attest.CodeAttestationUntrusted, attest.ConstraintSigner},
	}
	for _, tc := range cases {
		f.setReferences(t, tc.bundles()...)
		_, err := f.verify(c, "finance/pay", "")
		if f, ok := attest.FailureOf(err); !ok || f.Code != tc.code || f.Constraint != tc.constraint {
			t.Errorf("%s: err = %v, want %s (%s)", tc.name, err, tc.code, tc.constraint)
		}
	}
	// A valid bundle beside failing ones meets the requirement.
	f.setReferences(t, f.sign(t, "finance/pay@2.0.0", f.digest, sh.WithBadSignature()), f.bundle)
	if _, err := f.verify(c, "finance/pay", ""); err != nil {
		t.Fatalf("valid beside invalid: %v", err)
	}
}

// Spec: §4.7.11 (Attestation check, steps 2, 3, and 6) — the record checks
// run before any fetch and refuse under require.
func TestChecker_RecordChecks(t *testing.T) {
	t.Parallel()
	rule := requireRule("require", "finance/**")
	for name, tc := range map[string]struct {
		mutate     func(*consumerFixture, *testing.T)
		constraint string
	}{
		"document slot": {func(f *consumerFixture, _ *testing.T) {
			f.served.Record.Resources = map[string]string{"ARTIFACT.md": attest.FileDigest(nil)}
		}, attest.ConstraintDocumentSlot},
		"manifest body":    {func(f *consumerFixture, _ *testing.T) { f.served.Record.ManifestBody = "other" }, attest.ConstraintDerivedField},
		"deprecated type":  {func(f *consumerFixture, t *testing.T) { f.setMember(t, "deprecated", "yes") }, attest.ConstraintDerivedField},
		"assigned version": {func(f *consumerFixture, _ *testing.T) { f.served.Record.Version = "0.0.0-auto.2" }, attest.ConstraintAssignedVersion},
		"resource hash": {func(f *consumerFixture, _ *testing.T) {
			f.served.Record.Resources = map[string]string{"x.txt": "sha256:Z"}
		}, attest.ConstraintSubject},
	} {
		f := newConsumerFixture(t)
		tc.mutate(f, t)
		_, err := f.verify(f.checker(t, rule), "finance/pay", "")
		if fl, ok := attest.FailureOf(err); !ok || fl.Code != attest.CodeAttestationMismatch || fl.Constraint != tc.constraint {
			t.Errorf("%s: err = %v", name, err)
		}
		if f.src.calls.Load() != 0 {
			t.Errorf("%s: fetched before the record checks", name)
		}
	}
}

// Spec: §4.7.11 (Attestation check, step 1; Policy sources) — under a
// default of require an ID that no rule matches has no trusted signer, a
// rule of that mode with no requirement has none either, and a user mode of
// require that only a repository-source rule could meet is user_rule.
func TestChecker_RuleSelection(t *testing.T) {
	t.Parallel()
	f := newConsumerFixture(t)
	c := f.checkerWith(t, attest.ModeRequire, sign.AttestationInstance{}, requireRule("require", "other/**"))
	_, err := f.verify(c, "finance/pay", "")
	wantFailure(t, err, attest.CodeAttestationUntrusted, attest.ConstraintUserRule)

	// The same default from a repository source: no rule is no_rule.
	repo := writePolicy(t, f.h, policyJSON(requireRule("require", "other/**")), sign.AttestationInstance{})
	p, _ := attest.NewPolicy(attest.Source{Class: attest.RepositorySource, DefaultMode: attest.ModeRequire, Files: []*attest.PolicyFile{repo}})
	_, err = f.verify(&attest.Checker{Policy: p, Bundles: f.src}, "finance/pay", "")
	wantFailure(t, err, attest.CodeAttestationUntrusted, attest.ConstraintNoRule)

	// A rule without a mode or requirements under the default.
	noreq := writePolicy(t, f.h, policyJSON(`{"ids":["finance/**"]}`), sign.AttestationInstance{})
	p, _ = attest.NewPolicy(attest.Source{Class: attest.RepositorySource, DefaultMode: attest.ModeRequire, Files: []*attest.PolicyFile{noreq}})
	_, err = f.verify(&attest.Checker{Policy: p, Bundles: f.src}, "finance/pay", "")
	wantFailure(t, err, attest.CodeAttestationUntrusted, attest.ConstraintNoRequirements)

	// A user default of require, met by a repository rule with the right
	// signer alone, is still user_rule; adding a user rule meets it.
	userEmpty := writePolicy(t, f.h, policyJSON(), sign.AttestationInstance{})
	p, _ = attest.NewPolicy(
		attest.Source{Class: attest.UserSource, DefaultMode: attest.ModeRequire, Files: []*attest.PolicyFile{userEmpty}},
		attest.Source{Class: attest.RepositorySource, Files: []*attest.PolicyFile{writePolicy(t, f.h, policyJSON(requireRule("", "finance/**")), sign.AttestationInstance{})}},
	)
	_, err = f.verify(&attest.Checker{Policy: p, Bundles: f.src}, "finance/pay", "")
	wantFailure(t, err, attest.CodeAttestationUntrusted, attest.ConstraintUserRule)
	userRule := writePolicy(t, f.h, policyJSON(requireRule("", "finance/**")), sign.AttestationInstance{})
	p, _ = attest.NewPolicy(attest.Source{Class: attest.UserSource, DefaultMode: attest.ModeRequire, Files: []*attest.PolicyFile{userRule}})
	if _, err := f.verify(&attest.Checker{Policy: p, Bundles: f.src}, "finance/pay", ""); err != nil {
		t.Fatalf("user rule: %v", err)
	}

	// A user default of warn with no user rule warns and checks the
	// repository's require rule.
	p, _ = attest.NewPolicy(
		attest.Source{Class: attest.UserSource, DefaultMode: attest.ModeWarn},
		attest.Source{Class: attest.RepositorySource, Files: []*attest.PolicyFile{writePolicy(t, f.h, policyJSON(requireRule("require", "finance/**")), sign.AttestationInstance{})}},
	)
	res, err := f.verify(&attest.Checker{Policy: p, Bundles: f.src}, "finance/pay", "")
	if err != nil || len(res.Warnings) != 1 || res.Warnings[0].Constraint != attest.ConstraintUserRule || len(res.Accepted) != 1 {
		t.Fatalf("warn user mode: %+v, %v", res, err)
	}
}

// Spec: §4.7.11 (Policy sources) — under warn each failing rule of several
// files is reported, and under require the requirements of every require
// rule combine.
func TestChecker_SeveralFiles(t *testing.T) {
	t.Parallel()
	f := newConsumerFixture(t)
	bob := `{"ids":["**"],"mode":"%s","requirements":[{"signers":[{"issuer":"` + sh.DefaultIssuer + `","identity":"bob@acme.com","instance":"github"}]}]}`
	a := writePolicy(t, f.h, policyJSON(requireRule("warn", "finance/**")), sign.AttestationInstance{})
	b := writePolicy(t, f.h, policyJSON(fmt.Sprintf(bob, "warn")), sign.AttestationInstance{})
	p, _ := attest.NewPolicy(attest.Source{Class: attest.UserSource, Files: []*attest.PolicyFile{a, b}})
	res, err := f.verify(&attest.Checker{Policy: p, Bundles: f.src}, "finance/pay", "")
	if err != nil || len(res.Warnings) != 1 || res.Warnings[0].Code != attest.CodeAttestationUntrusted || len(res.Accepted) != 1 {
		t.Fatalf("warn: %+v, %v", res, err)
	}
	c := writePolicy(t, f.h, policyJSON(fmt.Sprintf(bob, "require")), sign.AttestationInstance{})
	p, _ = attest.NewPolicy(attest.Source{Class: attest.UserSource, Files: []*attest.PolicyFile{writePolicy(t, f.h, policyJSON(requireRule("require", "finance/**")), sign.AttestationInstance{}), c}})
	_, err = f.verify(&attest.Checker{Policy: p, Bundles: f.src}, "finance/pay", "")
	wantFailure(t, err, attest.CodeAttestationUntrusted, attest.ConstraintSigner)
}

// staleRoot is a public-good root source past its maximum age.
type staleRoot struct{}

// AttestationRoot reports the stale root.
func (staleRoot) AttestationRoot(context.Context) (*sign.AttestationRoot, error) {
	return nil, fmt.Errorf("%w: test", sign.ErrAttestationRootStale)
}

// Spec: §4.7.11 (Attestation check, step 9) — a rule whose signer names
// public-good fails with root_stale while its public-good root is past its
// maximum age, and a rule on another instance is unaffected.
// Matrix: §6.10 (materialize.attestation_root_stale)
func TestChecker_RootStale(t *testing.T) {
	t.Parallel()
	f := newConsumerFixture(t)
	pgRule := `{"ids":["finance/**"],"mode":"require","requirements":[{"signers":[{"issuer":"i","identity":"alice@acme.com"}]}]}`
	c := f.checkerWith(t, attest.ModeNone, sign.AttestationInstance{Root: staleRoot{}}, pgRule)
	_, err := f.verify(c, "finance/pay", "")
	wantFailure(t, err, attest.CodeAttestationRootStale, "")
	c = f.checkerWith(t, attest.ModeNone, sign.AttestationInstance{Root: staleRoot{}}, requireRule("require", "finance/**"))
	if _, err := f.verify(c, "finance/pay", ""); err != nil {
		t.Fatalf("github rule under a stale public-good root: %v", err)
	}
}

// Spec: §4.7.11 (Key-based signers, Attestation check) — a key-based
// signer accepts a bundle signed with its key, reported with an empty
// instance and issuer and the key's identifier; a bundle of another key is
// invalid.
func TestChecker_KeySigner(t *testing.T) {
	t.Parallel()
	f := newConsumerFixture(t)
	key := sh.NewSigningKey(t)
	pem, _ := json.Marshal(sh.PublicKeyPEM(t, &key.PublicKey))
	rule := `{"ids":["finance/**"],"mode":"require","requirements":[{"signers":[{"public_key":` + string(pem) + `}]}]}`
	b := f.h.DSSEBundle(sh.Statement(t, slsaType, sh.Subject{Name: "finance/pay@2.0.0", SHA256: strings.TrimPrefix(f.digest, "sha256:")}), sh.WithSigningKey(key))
	f.src.bundles[attest.AttestationDigest(b)] = b
	f.setReferences(t, b)
	c := f.checker(t, rule)
	res, err := f.verify(c, "finance/pay", "")
	if err != nil {
		t.Fatal(err)
	}
	if a := res.Accepted[0]; a.Instance != "" || a.Issuer != "" || a.Identity != sh.KeyIdentifier(t, &key.PublicKey) {
		t.Fatalf("accepted = %+v", a)
	}
	f.setReferences(t, f.bundle)
	_, err = f.verify(c, "finance/pay", "")
	wantFailure(t, err, attest.CodeAttestationInvalid, string(sign.ReasonSignature))
}

// Spec: §4.7.11 (Attestation check) — concurrent deliveries share the
// verification memo without a race.
func TestChecker_Concurrent(t *testing.T) {
	t.Parallel()
	f := newConsumerFixture(t)
	c := f.checker(t, requireRule("require", "finance/**"))
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.verify(c, "finance/pay", ""); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// Spec: §4.7.11 (Well-formed requests and records), §4.7.6 — a requested
// ID that holds @ is refused before any request, so it cannot spell the
// versioned subject name of another artifact and be served as a skill under
// a registry-assigned version, whose expected subject is the bare ID.
func TestChecker_AtSignIDCannotTakeAnotherSubject(t *testing.T) {
	t.Parallel()
	f := newConsumerFixture(t)
	digest := signerDigest(t, map[string][]byte{"SKILL.md": []byte(plainSkillMD), "scripts/run.sh": []byte("echo\n"), "empty.txt": nil})
	b := f.sign(t, "finance/close-books@1.2.0", digest)
	doc, err := manifest.SynthesizedSkillDocument("0.0.0-auto.3")
	if err != nil {
		t.Fatal(err)
	}
	rec := servedPlainSkill(t)
	rec.ID, rec.Version, rec.Frontmatter = "finance/close-books@1.2.0", "0.0.0-auto.3", string(doc)
	f.served.Record = rec
	f.setReferences(t, b)
	c := f.checker(t, keyRule(t, "require", "finance/close-books"), requireRule("require", "**"))
	_, err = f.verify(c, "finance/close-books@1.2.0", "")
	wantFailure(t, err, attest.CodeRequestMismatch, attest.ConstraintIDForm)
	if _, err := attest.SubjectName("finance/close-books@1.2.0", ""); !errors.Is(err, attest.ErrNoSubjectName) {
		t.Fatalf("SubjectName of an ID with @: %v", err)
	}
	_, err = attest.ExpectedSubjectName("finance/close-books@1.2.0", rec)
	wantFailure(t, err, attest.CodeAttestationMismatch, attest.ConstraintSubject)
}

// Spec: §4.7.11 (Policy sources) — a repository-source require rule that
// its own signer meets does not silence a user-source warn rule: the user's
// rule is still checked and its failure is a warning.
func TestChecker_RepositoryRequireKeepsUserWarn(t *testing.T) {
	t.Parallel()
	f := newConsumerFixture(t)
	user := fileOf(t, `{"version":1,"rules":[`+keyRule(t, "warn", "**")+`]}`)
	repo := writePolicy(t, f.h, policyJSON(requireRule("require", "**")), sign.AttestationInstance{})
	p, err := attest.NewPolicy(
		attest.Source{Class: attest.UserSource, Name: "home", Files: []*attest.PolicyFile{user}},
		attest.Source{Class: attest.RepositorySource, Name: "workspace", Files: []*attest.PolicyFile{repo}},
	)
	if err != nil {
		t.Fatal(err)
	}
	res, err := f.verify(&attest.Checker{Policy: p, Bundles: f.src}, "finance/pay", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Resolution.Mode != attest.ModeRequire || len(res.Accepted) != 1 || len(res.Warnings) != 1 ||
		res.Warnings[0].Code != attest.CodeAttestationInvalid {
		t.Fatalf("result = %+v", res)
	}
}

// Spec: §4.7.11 (Attestation check, step 8) — a reference that is not
// cached under offline-only, not found, answered with a 5xx status, or not
// reachable is unusable, and a bundle of another reference still meets the
// rule; with no bundle at all the outcome is missing, or
// network.registry_unreachable when a fetch could not reach the registry.
func TestChecker_UnusableReferencesDoNotAbort(t *testing.T) {
	t.Parallel()
	f := newConsumerFixture(t)
	other := f.sign(t, "finance/other@1.0.0", "sha256:"+strings.Repeat("1", 64))
	rule := requireRule("require", "finance/**")
	cache := &attest.MemoryCache{}
	_ = cache.Put(attest.AttestationDigest(f.bundle), f.bundle)
	f.setReferences(t, other, f.bundle)
	off := f.checker(t, rule)
	off.Bundles = attest.CachedBundles{Cache: cache, OfflineOnly: true}
	if _, err := f.verify(off, "finance/pay", ""); err != nil {
		t.Fatalf("offline with one cached bundle: %v", err)
	}
	for name, failing := range map[string]error{
		"not found":   attest.ErrBundleNotFound,
		"unavailable": attest.ErrBundleUnavailable,
		"unreachable": errors.New("dial tcp: refused"),
	} {
		src := &perDigestSource{fail: map[string]error{attest.AttestationDigest(other): failing}, inner: f.src}
		c := f.checker(t, rule)
		c.Bundles = src
		if _, err := f.verify(c, "finance/pay", ""); err != nil {
			t.Errorf("%s beside a valid bundle: %v", name, err)
		}
	}
	f.setReferences(t, other)
	src := &perDigestSource{fail: map[string]error{attest.AttestationDigest(other): attest.ErrBundleUnavailable}, inner: f.src}
	c := f.checker(t, rule)
	c.Bundles = src
	_, err := f.verify(c, "finance/pay", "")
	wantFailure(t, err, attest.CodeAttestationMissing, "")
	_, err = f.verify(off, "finance/pay", "")
	wantFailure(t, err, attest.CodeAttestationMissing, "")
}

// perDigestSource fails the digests in fail and serves the rest from inner.
type perDigestSource struct {
	fail  map[string]error
	inner attest.BundleSource
}

// Bundle fails or delegates.
func (s *perDigestSource) Bundle(ctx context.Context, digest string) ([]byte, error) {
	if err, ok := s.fail[digest]; ok {
		return nil, err
	}
	return s.inner.Bundle(ctx, digest)
}
