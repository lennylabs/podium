package sign_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/sign"
)

// TestVerifyAttestation_IdentityComparison covers the §4.7.11 identity
// comparison: byte equality on the raw extensions of the verified leaf,
// with each malformed or ambiguous certificate refused.
//
// Spec: §4.7.11
func TestVerifyAttestation_IdentityComparison(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	gh := githubInstance(t, h)
	printable := []byte{0x13, 0x03, '1', '0', '1'} // PrintableString "101"
	twoStrings := append(sh.UTF8Value("101"), sh.UTF8Value("102")...)
	for _, tc := range []struct {
		name   string
		leaf   []sh.LeafOpt
		signer sign.AttestationSigner
		want   sign.AttestationReason
	}{
		{"name that extends the identity", []sh.LeafOpt{sh.LeafEmail("alice@acme.com.attacker.net")}, emailSigner(), sign.ReasonIdentity},
		{"name that differs at a dot", []sh.LeafOpt{sh.LeafEmail("alice@acmeXcom")}, emailSigner(), sign.ReasonIdentity},
		{"name that is a proper prefix", []sh.LeafOpt{sh.LeafEmail("alice@acme.co")}, emailSigner(), sign.ReasonIdentity},
		{"name in another letter case", []sh.LeafOpt{sh.LeafEmail("Alice@acme.com")}, emailSigner(), sign.ReasonIdentity},
		{"older Issuer extension only", []sh.LeafOpt{sh.LeafIssuerV1Only(sh.DefaultIssuer)}, emailSigner(), sign.ReasonIssuer},
		{"issuer that differs", []sh.LeafOpt{sh.LeafIssuer("https://accounts.acme.com/")}, emailSigner(), sign.ReasonIssuer},
		{"issuer that is not a UTF8String", []sh.LeafOpt{sh.LeafIssuerRaw([]byte{0x16, 0x01, 'x'})}, emailSigner(), sign.ReasonIssuer},
		{"OtherName", []sh.LeafOpt{sh.LeafSAN(sh.SANValue(sh.OtherName(sh.DefaultSAN)))}, emailSigner(), sign.ReasonIdentity},
		{"critical OtherName", []sh.LeafOpt{sh.LeafOtherNameOnly()}, emailSigner(), sign.ReasonIdentity},
		{"no subject alternative name", []sh.LeafOpt{sh.LeafEmail("")}, emailSigner(), sign.ReasonIdentity},
		{"no issuer extension", []sh.LeafOpt{sh.LeafWithoutIssuer()}, emailSigner(), sign.ReasonIssuer},
		{"URI and email", []sh.LeafOpt{sh.LeafSAN(sh.SANValue(sh.EmailName(sh.DefaultSAN), sh.URIName("https://acme.com/alice")))}, emailSigner(), sign.ReasonIdentity},
		{"two emails", []sh.LeafOpt{sh.LeafSAN(sh.SANValue(sh.EmailName(sh.DefaultSAN), sh.EmailName("bob@acme.com")))}, emailSigner(), sign.ReasonIdentity},
		{"binding value that is a PrintableString", []sh.LeafOpt{sh.LeafExtension(sh.OIDSourceRepositoryID, printable)}, emailSigner(), sign.ReasonSourceBinding},
		{"binding value of two UTF8Strings", []sh.LeafOpt{sh.LeafExtension(sh.OIDSourceRepositoryOwnerID, twoStrings)}, emailSigner(), sign.ReasonSourceBinding},
		{"source commit that is not a UTF8String", []sh.LeafOpt{sh.LeafExtension(sh.OIDSourceRepositoryDigest, printable)}, emailSigner(), sign.ReasonSourceBinding},
		{"ref that is not a UTF8String", []sh.LeafOpt{sh.LeafExtension(sh.OIDSourceRepositoryRef, printable)}, emailSigner(), sign.ReasonRefBinding},
		{"extension that occurs twice", []sh.LeafOpt{sh.LeafDuplicateExtension(sh.OIDSourceRepository, skillsRepo)}, emailSigner(), sign.ReasonParse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := ghBundle(t, h, sh.WithLeaf(tc.leaf...))
			_, err := verify(t, gh, raw, tc.signer)
			wantReason(t, err, tc.want)
		})
	}
}

// TestVerifyAttestation_Bindings covers the §4.7.11 source and ref
// bindings against GitHub Actions leaves.
//
// Spec: §4.7.11
func TestVerifyAttestation_Bindings(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	gh := githubInstance(t, h)
	withSource := func(edit func(*sh.Source)) []byte {
		s := skillsSource()
		edit(&s)
		return ghBundle(t, h, sh.WithLeaf(sh.GitHubActionsLeaf(sharedWorkflow, s)))
	}
	byOwnerName := sign.AttestationSigner{Issuer: sh.GitHubActionsIssuer, Identity: sharedWorkflow,
		SourceRepositoryOwner: acmeOwner, SourceRepositoryRef: mainRef}
	byOwnerID := sign.AttestationSigner{Issuer: sh.GitHubActionsIssuer, Identity: sharedWorkflow,
		SourceRepositoryOwnerID: acmeOwnerID, SourceRepositoryRef: mainRef}
	byRepo := sign.AttestationSigner{Issuer: sh.GitHubActionsIssuer, Identity: sharedWorkflow,
		SourceRepository: skillsRepo, SourceRepositoryID: skillsRepoID, SourceRepositoryRef: mainRef}
	anyRef := actionsSigner()
	anyRef.SourceRepositoryRef, anyRef.AnyRef = "", true
	noRef := actionsSigner()
	noRef.SourceRepositoryRef = ""
	unbound := sign.AttestationSigner{Issuer: sh.GitHubActionsIssuer, Identity: sharedWorkflow}
	anySource := unbound
	anySource.AnySource = true
	noSourceFields := ghBundle(t, h, sh.WithLeaf(sh.LeafURI(sharedWorkflow), sh.LeafIssuer(sh.GitHubActionsIssuer)))
	feature := withSource(func(s *sh.Source) { s.Ref = "refs/heads/feature" })
	for _, tc := range []struct {
		name   string
		raw    []byte
		signer sign.AttestationSigner
		want   sign.AttestationReason
	}{
		{"bound signer on main", withSource(func(*sh.Source) {}), byRepo, ""},
		{"branch through the shared workflow", feature, actionsSigner(), sign.ReasonRefBinding},
		{"branch under any_ref", feature, anyRef, ""},
		{"tag ref", withSource(func(s *sh.Source) { s.Ref = "refs/tags/v1.0.0" }), actionsSigner(), sign.ReasonRefBinding},
		{"bound signer with no ref binding", withSource(func(*sh.Source) {}), noRef, sign.ReasonRefBinding},
		{"leaf without a ref", withSource(func(s *sh.Source) { s.Ref = "" }), actionsSigner(), sign.ReasonRefBinding},
		{"repository outside the binding", withSource(func(s *sh.Source) {
			s.Repository, s.ID = "https://github.com/acme/other", "999"
		}), byRepo, sign.ReasonSourceBinding},
		{"re-registered owner name under a name binding", withSource(func(s *sh.Source) { s.OwnerID = "999" }), byOwnerName, ""},
		{"re-registered owner name under an identifier binding", withSource(func(s *sh.Source) { s.OwnerID = "999" }), byOwnerID, sign.ReasonSourceBinding},
		{"renamed owner under a name binding", withSource(func(s *sh.Source) { s.Owner = "https://github.com/acme-corp" }), byOwnerName, sign.ReasonSourceBinding},
		{"renamed owner under an identifier binding", withSource(func(s *sh.Source) { s.Owner = "https://github.com/acme-corp" }), byOwnerID, ""},
		{"leaf without a named member", withSource(func(s *sh.Source) { s.ID = "" }), byRepo, sign.ReasonSourceBinding},
		{"URI identity with no binding", withSource(func(*sh.Source) {}), unbound, sign.ReasonSourceBinding},
		{"any_source on a leaf with no source fields", noSourceFields, anySource, ""},
		{"email identity with no binding", ghBundle(t, h), emailSigner(), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := verify(t, gh, tc.raw, tc.signer)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("verify: %v", err)
				}
				return
			}
			wantReason(t, err, tc.want)
		})
	}
}

// TestAttestationSigner_Validate covers the §4.7.11 load rules of a signer.
//
// Spec: §4.7.11
func TestAttestationSigner_Validate(t *testing.T) {
	t.Parallel()
	anyRef := actionsSigner()
	anyRef.SourceRepositoryRef, anyRef.AnyRef = "", true
	for _, tc := range []struct {
		name  string
		s     sign.AttestationSigner
		valid bool
	}{
		{"email signer", emailSigner(), true},
		{"bound URI signer", actionsSigner(), true},
		{"bound URI signer with any_ref", anyRef, true},
		{"URI signer with any_source", sign.AttestationSigner{Issuer: "i", Identity: sharedWorkflow, AnySource: true}, true},
		{"no issuer", sign.AttestationSigner{Identity: sh.DefaultSAN}, false},
		{"no identity", sign.AttestationSigner{Issuer: sh.DefaultIssuer}, false},
		{"URI signer with no binding", sign.AttestationSigner{Issuer: "i", Identity: sharedWorkflow}, false},
		{"bound signer with no ref", sign.AttestationSigner{Issuer: "i", Identity: sharedWorkflow, SourceRepositoryID: "1"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.s.Validate()
			if tc.valid != (err == nil) {
				t.Fatalf("Validate() = %v, want valid %v", err, tc.valid)
			}
			if err != nil && !errors.Is(err, sign.ErrAttestationSignerInvalid) {
				t.Fatalf("error does not wrap ErrAttestationSignerInvalid: %v", err)
			}
		})
	}
}

// TestVerifyAttestation_Statement covers the statement format of §4.7.11:
// the JSON rule, exact member names, and the subject structure.
//
// Spec: §4.7.11
func TestVerifyAttestation_Statement(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	gh := githubInstance(t, h)
	const head = `{"_type":"https://in-toto.io/Statement/v1","predicateType":"p",`
	for _, tc := range []struct {
		name    string
		payload string
		ok      bool
	}{
		{"minimal", head + `"subject":[{"name":"a","digest":{"sha256":"00"}}]}`, true},
		{"byte order mark", "\ufeff" + head + `"subject":[{"name":"a","digest":{}}]}`, false},
		{"lone surrogate escape", head + `"subject":[{"name":"\ud800","digest":{}}]}`, false},
		{"array at the top", `[1]`, false},
		{"other statement type", `{"_type":"https://in-toto.io/Statement/v0.1","predicateType":"p","subject":[{"name":"a","digest":{}}]}`, false},
		{"type in another case", `{"_TYPE":"https://in-toto.io/Statement/v1","predicateType":"p","subject":[{"name":"a","digest":{}}]}`, false},
		{"no predicate type", `{"_type":"https://in-toto.io/Statement/v1","subject":[{"name":"a","digest":{}}]}`, false},
		{"empty predicate type", `{"_type":"https://in-toto.io/Statement/v1","predicateType":"","subject":[{"name":"a","digest":{}}]}`, false},
		{"no subject", head + `"subject":null}`, false},
		{"empty subject", head + `"subject":[]}`, false},
		{"subject that is not an object", head + `"subject":["a"]}`, false},
		{"subject without a name", head + `"subject":[{"uri":"pkg:acme/x","digest":{"sha256":"00"}}]}`, true},
		{"subject with a null name", head + `"subject":[{"name":null,"digest":{"sha256":"00"}}]}`, true},
		{"subject without a digest", head + `"subject":[{"name":"a"}]}`, false},
		{"subject with an empty digest", head + `"subject":[{"name":"a","digest":{}}]}`, false},
		{"name of another type", head + `"subject":[{"name":1,"digest":{"sha256":"00"}}]}`, false},
		{"digest that is not an object", head + `"subject":[{"name":"a","digest":"00"}]}`, false},
		{"digest value of another type", head + `"subject":[{"name":"a","digest":{"sha256":1}}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := h.DSSEBundle([]byte(tc.payload), sh.WithTimestamp(sh.TSAPrimary))
			_, err := verify(t, gh, raw, emailSigner())
			if tc.ok {
				if err != nil {
					t.Fatalf("verify: %v", err)
				}
				return
			}
			wantReason(t, err, sign.ReasonPayload)
		})
	}
}

// TestAttestationStatement_Lists covers the subject matching rules of
// §4.7.11: exact names, lowercase digests, and the character rule.
//
// Spec: §4.7.11
func TestAttestationStatement_Lists(t *testing.T) {
	t.Parallel()
	upper := strings.ToUpper(digestA)
	st := sign.AttestationStatement{Subjects: []sign.AttestationSubject{
		{Name: "finance/close-books@1.4.0", Digest: map[string]string{"sha256": digestA}},
		{Name: "finance/upper@1.0.0", Digest: map[string]string{"sha256": upper}},
		{Name: "finance/line\nfeed@1.0.0", Digest: map[string]string{"sha256": digestA}},
		{Name: "finance/\ufeffbom@1.0.0", Digest: map[string]string{"sha256": digestA}},
		{Name: `finance\back@1.0.0`, Digest: map[string]string{"sha256": digestA}},
		{Name: "finance/sha512-only@1.0.0", Digest: map[string]string{"sha512": digestA}},
	}}
	for _, tc := range []struct {
		name, digest string
		want         bool
	}{
		{"finance/close-books@1.4.0", digestA, true},
		{"finance/close-books@1.4.1", digestA, false},
		{"finance/close-books@1.4.0", digestA[:63] + "2", false},
		{"finance/close-books@1.4.0", upper, false},
		{"finance/upper@1.0.0", digestA, false},
		{"finance/upper@1.0.0", upper, false},
		{"finance/line\nfeed@1.0.0", digestA, false},
		{"finance/\ufeffbom@1.0.0", digestA, false},
		{`finance\back@1.0.0`, digestA, false},
		{"finance/sha512-only@1.0.0", digestA, false},
		{"finance/close-books@1.4.0", digestA[:62], false},
		{"", digestA, false},
		{"finance/\xff@1.0.0", digestA, false},
	} {
		if got := st.Lists(tc.name, tc.digest); got != tc.want {
			t.Errorf("Lists(%q, %q) = %v, want %v", tc.name, tc.digest, got, tc.want)
		}
	}
	for _, r := range []rune{0x00, 0x1f, ' ', 0x7f, 0x9f, 0xa0, 0x1680, 0x2000, 0x200a, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff, '\\'} {
		name := "a" + string(r) + "b"
		st := sign.AttestationStatement{Subjects: []sign.AttestationSubject{{Name: name, Digest: map[string]string{"sha256": digestA}}}}
		if st.Lists(name, digestA) {
			t.Errorf("a name with U+%04X matched", r)
		}
	}
}

// TestAttestationReasons covers the §4.7.11 reason order and the refusal
// error type.
//
// Spec: §4.7.11
func TestAttestationReasons(t *testing.T) {
	t.Parallel()
	if got := sign.LaterAttestationReason(sign.ReasonIssuer, sign.ReasonChain); got != sign.ReasonIssuer {
		t.Errorf("Later(issuer, chain) = %s", got)
	}
	if got := sign.LaterAttestationReason(sign.ReasonParse, sign.ReasonPredicateType); got != sign.ReasonPredicateType {
		t.Errorf("Later(parse, predicate_type) = %s", got)
	}
	if got := sign.LaterAttestationReason("", sign.ReasonParse); got != sign.ReasonParse {
		t.Errorf("Later(\"\", parse) = %s", got)
	}
	if _, ok := sign.AttestationReasonOf(errors.New("plain")); ok {
		t.Error("AttestationReasonOf(plain) reported a reason")
	}
	h := sh.New(t)
	_, err := sign.VerifyAttestationBundle(context.Background(), ghBundle(t, h, sh.WithBadSignature()), githubInstance(t, h))
	if !strings.HasPrefix(err.Error(), "attestation bundle refused: signature: ") {
		t.Errorf("refusal message = %q", err)
	}
}

// failingSource is a root source that cannot supply a root.
type failingSource struct{ err error }

func (f failingSource) AttestationRoot(context.Context) (*sign.AttestationRoot, error) {
	return nil, f.err
}

// TestAttestationRoot_Loading covers loading a trusted root from a file and
// from the caller, and the errors of a root that cannot be used.
//
// Spec: §4.7.11
func TestAttestationRoot_Loading(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "trusted_root.json")
	if err := os.WriteFile(path, h.TrustedRootJSON(sh.GitHubStyleRoot()...), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := sign.LoadAttestationRootFile(path)
	if err != nil {
		t.Fatalf("LoadAttestationRootFile: %v", err)
	}
	if _, err := verify(t, sign.AttestationInstance{Name: "github", Root: r}, ghBundle(t, h), emailSigner()); err != nil {
		t.Fatalf("verify under the file root: %v", err)
	}
	if _, err := sign.LoadAttestationRootFile(filepath.Join(dir, "absent.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("absent file: %v", err)
	}
	if _, err := sign.ParseAttestationRoot(h.TrustedRootJSON(sh.WithMalformedJSON())); !errors.Is(err, sign.ErrAttestationRootInvalid) {
		t.Errorf("malformed root: %v", err)
	}
	if _, err := sign.ParseAttestationRoot([]byte(`{"mediaType":"application/vnd.dev.sigstore.trustedroot+json;version=0.2"}`)); !errors.Is(err, sign.ErrAttestationRootInvalid) {
		t.Errorf("unknown root media type: %v", err)
	}
	if _, err := sign.ParseAttestationRoot(h.TrustedRootJSON(sh.WithGarbageTSACert())); !errors.Is(err, sign.ErrAttestationRootInvalid) {
		t.Errorf("garbage timestamp authority: %v", err)
	}
	stale := errors.New("acme root unavailable")
	_, err = sign.VerifyAttestationBundle(context.Background(), ghBundle(t, h), sign.AttestationInstance{Name: "github", Root: failingSource{stale}})
	if !errors.Is(err, stale) {
		t.Errorf("failing source: %v", err)
	}
	if _, ok := sign.AttestationReasonOf(err); ok {
		t.Error("a root that cannot be supplied is reported as a bundle refusal")
	}
	if _, err := sign.VerifyAttestationBundle(context.Background(), ghBundle(t, h), sign.AttestationInstance{Name: "none"}); err == nil {
		t.Error("an instance with no root verified a bundle")
	}
}
