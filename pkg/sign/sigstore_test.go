package sign_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	protobundle "github.com/sigstore/protobuf-specs/gen/pb-go/bundle/v1"
	"google.golang.org/protobuf/encoding/protojson"

	sh "github.com/lennylabs/podium/internal/testharness/sigstoreharness"
	"github.com/lennylabs/podium/pkg/sign"
)

// fakeOIDCToken builds a JWT with no signature; only the payload is
// inspected by the oidcSubject helper. Fulcio in production verifies
// the signature; the harness fake does not.
func fakeOIDCToken(t *testing.T, subject string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	pl, err := json.Marshal(map[string]string{"email": subject, "iss": sh.DefaultIssuer})
	if err != nil {
		t.Fatalf("token payload: %v", err)
	}
	return header + "." + base64.RawURLEncoding.EncodeToString(pl) + ".sig"
}

// hashOf returns the canonical content-hash form for body.
func hashOf(body []byte) string {
	h := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(h[:])
}

// defaultPolicy is the identity policy every default harness leaf matches.
func defaultPolicy() sign.IdentityPolicy {
	return sign.NewIdentityPolicy(sh.DefaultSAN, sh.DefaultIssuer)
}

// harnessVerifier returns a verifier over the harness trusted root built
// with opts and the default identity policy.
func harnessVerifier(h *sh.Harness, opts ...sh.RootOpt) sign.SigstoreKeyless {
	return sign.SigstoreKeyless{TrustRoot: h.TrustedRootJSON(opts...), Identity: defaultPolicy()}
}

// signer returns a SigstoreKeyless whose endpoints all point at srv.
func signer(t *testing.T, h *sh.Harness, srv *httptest.Server) sign.SigstoreKeyless {
	t.Helper()
	return sign.SigstoreKeyless{
		FulcioURL: srv.URL,
		RekorURL:  srv.URL,
		TSAURL:    srv.URL + "/api/v1/timestamp",
		OIDCToken: fakeOIDCToken(t, sh.DefaultSAN),
		TrustRoot: h.TrustedRootJSON(),
		Identity:  defaultPolicy(),
		Client:    srv.Client(),
	}
}

// mutate rewrites a bundle's JSON through edit.
func mutate(t *testing.T, bundle string, edit func(map[string]any)) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(bundle), &m); err != nil {
		t.Fatalf("parse bundle: %v", err)
	}
	edit(m)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("encode bundle: %v", err)
	}
	return string(out)
}

// member returns the object at the dotted path of m.
func member(m map[string]any, path ...string) map[string]any {
	for _, p := range path {
		m = m[p].(map[string]any)
	}
	return m
}

// parseBundle decodes a bundle with protojson.
func parseBundle(t *testing.T, bundle string) *protobundle.Bundle {
	t.Helper()
	pb := new(protobundle.Bundle)
	if err := protojson.Unmarshal([]byte(bundle), pb); err != nil {
		t.Fatalf("parse bundle: %v", err)
	}
	return pb
}

// wantRefused fails the test unless err wraps ErrSignatureInvalid and
// names want.
func wantRefused(t *testing.T, err error, want string) {
	t.Helper()
	if !errors.Is(err, sign.ErrSignatureInvalid) {
		t.Fatalf("err = %v, want ErrSignatureInvalid", err)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %q, want it to contain %q", err, want)
	}
}

// Spec: §4.7.9 — a bundle whose timestamp, log entry, chain, signature,
// and identity all hold verifies: under an identity list with whitespace
// and empty entries, for a leaf whose one name is a URI, under an Ed25519
// log key, with the older spelling of the version 0.3 media type, and
// with an SCT when the trusted root lists a certificate transparency log.
func TestSigstoreKeyless_VerifyAcceptsBoundBundle(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	hash := hashOf([]byte("acme artifact"))
	const workflow = "https://github.com/acme/repo/.github/workflows/release.yml@refs/heads/main"
	cases := []struct {
		name   string
		bundle string
		root   []sh.RootOpt
		policy sign.IdentityPolicy
	}{
		{"default", h.KeylessBundle(t, hash), nil, defaultPolicy()},
		{"list with whitespace and empty entries", h.KeylessBundle(t, hash), nil,
			sign.NewIdentityPolicy(" bob@acme.com, ,alice@acme.com ", sh.DefaultIssuer)},
		{"URI SAN in the list", h.KeylessBundle(t, hash, sh.WithSAN(""), sh.WithURISAN(workflow)), nil,
			sign.NewIdentityPolicy(workflow, " "+sh.DefaultIssuer+" ")},
		{"Ed25519 checkpoint", h.KeylessBundle(t, hash, sh.WithEd25519Checkpoint()), []sh.RootOpt{sh.WithEd25519LogKey()}, defaultPolicy()},
		{"older media type spelling", h.KeylessBundle(t, hash, sh.WithKeylessMediaType(sh.BundleMediaTypeV03Legacy)), nil, defaultPolicy()},
		{"SCT under a listed CT log", h.KeylessBundle(t, hash, sh.WithKeylessSCT()), []sh.RootOpt{sh.WithCTLogKey()}, defaultPolicy()},
		{"two timestamp authorities", h.KeylessBundle(t, hash, sh.WithExtraTimestamp(sh.TSASecond)), []sh.RootOpt{sh.WithTimestampAuthorities(sh.TSASecond)}, defaultPolicy()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			v := sign.SigstoreKeyless{TrustRoot: h.TrustedRootJSON(c.root...), Identity: c.policy}
			if err := v.Verify(context.Background(), hash, c.bundle); err != nil {
				t.Fatalf("Verify: %v", err)
			}
		})
	}
}

// Spec: §4.7.9 — validity is evaluated at the verified time: the leaf's
// NotAfter (2026-01-15T12:15Z) is in the past on the wall clock and the
// bundle still verifies with no clock injection, while a timestamp
// outside the leaf's window is refused.
// Matrix: §6.10 (materialize.signature_invalid)
func TestSigstoreKeyless_VerifyAtTimestampTime(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	hash := hashOf([]byte("acme artifact"))
	if !time.Now().After(h.Clock().Add(15 * time.Minute)) {
		t.Fatal("the harness leaf must have expired on the wall clock for this test to mean anything")
	}
	v := harnessVerifier(h)
	if err := v.Verify(context.Background(), hash, h.KeylessBundle(t, hash)); err != nil {
		t.Fatalf("Verify expired leaf with a valid timestamp: %v", err)
	}
	late := h.KeylessBundle(t, hash, sh.WithTimestampTime(h.Clock().Add(time.Hour)))
	wantRefused(t, v.Verify(context.Background(), hash, late), "not valid at a verified time")
}

// keylessRefusal is one §4.7.9 refusal case: a bundle built with env, or
// the raw bundle, verified under a root built with root, or the raw root.
type keylessRefusal struct {
	name    string
	env     []sh.EnvOpt
	root    []sh.RootOpt
	rawRoot []byte // replaces the generated trusted root when set
	noRoot  bool
	policy  *sign.IdentityPolicy
	hash    string // verifies against this content hash when set
	bundle  string // verifies this bundle when set
	want    string
}

// run verifies the case and requires the refusal it names.
func (c keylessRefusal) run(t *testing.T, h *sh.Harness, hash string) {
	t.Helper()
	v := harnessVerifier(h, c.root...)
	switch {
	case c.noRoot:
		v.TrustRoot = nil
	case c.rawRoot != nil:
		v.TrustRoot = c.rawRoot
	}
	if c.policy != nil {
		v.Identity = *c.policy
	}
	bundle := c.bundle
	if bundle == "" {
		bundle = h.KeylessBundle(t, hash, c.env...)
	}
	verifyHash := hash
	if c.hash != "" {
		verifyHash = c.hash
	}
	wantRefused(t, v.Verify(context.Background(), verifyHash, bundle), c.want)
}

// Spec: §4.7.9 — the identity conditions: the leaf's one email or URI
// name equals an entry of the configured list and its Issuer (V2)
// extension equals the configured issuer, both byte for byte, and a leaf
// with several names, an OtherName, no Issuer (V2) extension, or a
// malformed field is refused.
// Matrix: §6.10 (materialize.signature_invalid)
func TestSigstoreKeyless_VerifyIdentityRefusals(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	hash := hashOf([]byte("acme artifact"))
	empty := sign.IdentityPolicy{}
	noIssuer := sign.NewIdentityPolicy(sh.DefaultSAN, " ")
	malformedV2 := []byte{0x0c, 0x05, 'a'}
	cases := []keylessRefusal{
		{name: "SAN outside the list", env: []sh.EnvOpt{sh.WithSAN("bob@acme.com")}, want: "certificate identity mismatch"},
		{name: "SAN differs in case", env: []sh.EnvOpt{sh.WithSAN("Alice@acme.com")}, want: "certificate identity mismatch"},
		{name: "email and URI SAN outside the list", env: []sh.EnvOpt{sh.WithSAN("carol@acme.com"), sh.WithURISAN("https://github.com/acme/other")}, want: "more than one name"},
		{name: "listed email beside a URI SAN", env: []sh.EnvOpt{sh.WithURISAN("https://github.com/acme/other")}, want: "more than one name"},
		{name: "issuer mismatch", env: []sh.EnvOpt{sh.WithIssuerExt(sh.OIDIssuerV2, sh.IssuerValue("https://evil.example"))}, want: "OIDC issuer mismatch"},
		{name: "otherName SAN only", env: []sh.EnvOpt{sh.WithOtherNameSANOnly(), sh.WithNonCriticalSAN()}, want: "neither an email address nor a URI"},
		{name: "critical otherName SAN only", env: []sh.EnvOpt{sh.WithOtherNameSANOnly()}, want: "neither an email address nor a URI"},
		{name: "no issuer extension", env: []sh.EnvOpt{sh.WithoutIssuer()}, want: "no Issuer (V2) extension"},
		{name: "older issuer extension only", env: []sh.EnvOpt{sh.WithoutIssuer(), sh.WithIssuerExt(sh.OIDIssuerV1, []byte(sh.DefaultIssuer))}, want: "no Issuer (V2) extension"},
		{name: "malformed .1.8 beside a matching .1.1", env: []sh.EnvOpt{sh.WithIssuerExt(sh.OIDIssuerV2, malformedV2), sh.WithIssuerExt(sh.OIDIssuerV1, []byte(sh.DefaultIssuer))}, want: "issuer extension is malformed"},
		{name: "empty identity policy", policy: &empty, want: "PODIUM_SIGSTORE_CERT_IDENTITY"},
		{name: "empty issuer", policy: &noIssuer, want: "PODIUM_SIGSTORE_CERT_OIDC_ISSUER"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t, h, hash)
		})
	}
}

// Spec: §4.7.9, §6.2 — the bundle, signature, chain, and trusted-root
// conditions each refuse on their own, and each trusted-root role anchors
// only its own certificates: a leaf chains to a certificateAuthorities
// entry and a timestamp to a timestampAuthorities entry.
// Matrix: §6.10 (materialize.signature_invalid)
func TestSigstoreKeyless_VerifyRefusals(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	hash := hashOf([]byte("acme artifact"))
	sum512 := sha512.Sum512([]byte("acme artifact"))
	tsaStart, tsaEnd := h.Clock().Add(-2*time.Hour), h.Clock().Add(-time.Second)
	rootPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: h.FulcioRoot().Raw})
	valid := h.KeylessBundle(t, hash)
	dsse := string(h.DSSEBundle(sh.Statement(t, "https://acme.test/predicate", sh.Subject{Name: "acme", SHA256: strings.TrimPrefix(hash, "sha256:")}),
		sh.WithTimestamp(sh.TSAPrimary)))
	cases := []keylessRefusal{
		{name: "tampered content hash", hash: hashOf([]byte("acme tampered")), want: "message digest is not the digest being verified"},
		{name: "content hash without alg", hash: hex.EncodeToString([]byte("acme")), want: "content hash"},
		{name: "sha512 content hash", hash: "sha512:" + hex.EncodeToString(sum512[:]), want: "is not sha256"},
		{name: "SHA-384 message digest", env: []sh.EnvOpt{sh.WithMessageDigestAlgorithm("SHA2_384")}, want: "message digest is not SHA-256"},
		{name: "foreign signature", env: []sh.EnvOpt{sh.WithForeignSignature()}, want: "signature does not verify"},
		{name: "Ed25519 leaf", env: []sh.EnvOpt{sh.WithEd25519Leaf()}, want: "not an ECDSA P-256 key"},
		{name: "P-384 leaf", env: []sh.EnvOpt{sh.WithP384Leaf()}, want: "not an ECDSA P-256 key"},
		{name: "server-auth leaf", env: []sh.EnvOpt{sh.WithoutCodeSigning()}, want: "does not chain"},
		{name: "leaf without EKU", env: []sh.EnvOpt{sh.WithLeafWithoutEKU()}, want: "does not list code signing"},
		{name: "any-purpose leaf", env: []sh.EnvOpt{sh.WithLeafAnyUsage()}, want: "does not list code signing"},
		{name: "foreign certificate authority", root: []sh.RootOpt{sh.WithForeignCA()}, want: "does not chain"},
		{name: "leaf issued by the TSA root", env: []sh.EnvOpt{sh.WithLeafIssuedByTSARoot()}, want: "does not chain"},
		{name: "token signed under the Fulcio root", env: []sh.EnvOpt{sh.WithTimestampSignedByFulcioCA()}, want: "verifies under no authority"},
		{name: "no certificate authority", root: []sh.RootOpt{sh.WithoutCAs()}, want: "does not chain"},
		{name: "no log key", root: []sh.RootOpt{sh.WithoutTLogs()}, want: "names no transparency log"},
		{name: "only another log's key", root: []sh.RootOpt{sh.WithoutTLogs(), sh.WithUnsupportedLogKey(), sh.WithExtraTLog()}, want: "names no transparency log"},
		{name: "no timestamp authority", root: []sh.RootOpt{sh.WithoutTSAs()}, want: "verifies under no authority"},
		{name: "timestamp authority expired", root: []sh.RootOpt{sh.WithTSAValidFor(&tsaStart, &tsaEnd)}, want: "verifies under no authority"},
		{name: "CT log listed and no SCT", root: []sh.RootOpt{sh.WithCTLogKey()}, want: "no signed certificate timestamp"},
		{name: "CT log listed and a foreign SCT", env: []sh.EnvOpt{sh.WithKeylessForeignSCT()}, root: []sh.RootOpt{sh.WithCTLogKey()}, want: "no signed certificate timestamp"},
		{name: "garbage trust-root certificate", root: []sh.RootOpt{sh.WithGarbageCert()}, want: "trust root does not load"},
		{name: "garbage CT log key", root: []sh.RootOpt{sh.WithCTLogs()}, want: "trust root does not load"},
		{name: "timestamp authority entry that begins with a CA", root: []sh.RootOpt{sh.WithCAFirstTSA()}, want: "trust root does not load"},
		{name: "malformed trust root", root: []sh.RootOpt{sh.WithMalformedJSON()}, want: "trust root does not load"},
		{name: "PEM trust root", rawRoot: rootPEM, want: "trust root does not load"},
		{name: "empty trust root", noRoot: true, want: "no trust root configured"},
		{name: "not JSON", bundle: "not-json", want: "fails the JSON rule"},
		{name: "empty object", bundle: `{}`, want: "not a version 0.3 Sigstore bundle"},
		{name: "version 0.1 media type", env: []sh.EnvOpt{sh.WithKeylessMediaType(sh.BundleMediaTypeV01)}, want: "not a version 0.3 Sigstore bundle"},
		{name: "DSSE bundle", bundle: dsse, want: "carries no message signature"},
		{name: "signature not base64", bundle: mutate(t, valid, func(m map[string]any) {
			member(m, "messageSignature")["signature"] = "%%%"
		}), want: "does not parse"},
		{name: "timestamp not base64", bundle: mutate(t, valid, func(m map[string]any) {
			ts := member(m, "verificationMaterial", "timestampVerificationData")["rfc3161Timestamps"].([]any)
			ts[0].(map[string]any)["signedTimestamp"] = "%%%"
		}), want: "does not parse"},
		{name: "certificate is not DER", bundle: mutate(t, valid, func(m map[string]any) {
			member(m, "verificationMaterial", "certificate")["rawBytes"] = base64.StdEncoding.EncodeToString([]byte("acme"))
		}), want: "(parse)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t, h, hash)
		})
	}
}

// Spec: §4.7.9 — the timestamp conditions: the bundle carries a timestamp,
// each timestamp verifies over the bundle's signature under a timestamp
// authority whose signer carries the time-stamping usage, and no two come
// from one authority.
// Matrix: §6.10 (materialize.signature_invalid)
func TestSigstoreKeyless_VerifyTimestampRefusals(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	hash := hashOf([]byte("acme artifact"))
	cases := []keylessRefusal{
		{name: "no timestamp", env: []sh.EnvOpt{sh.WithoutTimestamp()}, want: "carries no verified time"},
		{name: "timestamp over another signature", env: []sh.EnvOpt{sh.WithTimestampOver([]byte("acme other signature"))}, want: "verifies under no authority"},
		{name: "untrusted timestamp authority", env: []sh.EnvOpt{sh.WithUntrustedTSA()}, want: "verifies under no authority"},
		{name: "TSA signer without EKU", env: []sh.EnvOpt{sh.WithTSALeafWithoutTimeStamping()}, want: "verifies under no authority"},
		{name: "any-purpose TSA signer", env: []sh.EnvOpt{sh.WithTSALeafAnyUsage()}, want: "verifies under no authority"},
		{name: "flipped timestamp signature", env: []sh.EnvOpt{sh.WithFlippedTimestampSignature()}, want: "verifies under no authority"},
		{name: "two timestamps from one authority", env: []sh.EnvOpt{sh.WithExtraTimestamp(sh.TSAPrimary)}, want: "two timestamps come from one authority"},
		{name: "two signers", env: []sh.EnvOpt{sh.WithTimestampOptions(sh.WithTwoSigners())}, want: "timestamp does not parse"},
		{name: "subject key identifier sid", env: []sh.EnvOpt{sh.WithTimestampOptions(sh.WithSubjectKeyIdentifierSID())}, want: "verifies under no authority"},
		{name: "message digest mismatch", env: []sh.EnvOpt{sh.WithTimestampOptions(sh.WithMessageDigestMismatch())}, want: "verifies under no authority"},
		{name: "content type", env: []sh.EnvOpt{sh.WithTimestampOptions(sh.WithTSTContentType(asn1.ObjectIdentifier{1, 2, 3}))}, want: "timestamp does not parse"},
		{name: "RSA algorithm over an ECDSA signer", env: []sh.EnvOpt{sh.WithTimestampOptions(sh.WithSignatureAlgorithm(asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 11}))}, want: "verifies under no authority"},
		{name: "unknown algorithm", env: []sh.EnvOpt{sh.WithTimestampOptions(sh.WithSignatureAlgorithm(asn1.ObjectIdentifier{1, 2, 3}))}, want: "verifies under no authority"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t, h, hash)
		})
	}
}

// Spec: §4.7.9 — a token whose signer the timestamp authority's chain in
// the trusted root supplies verifies, and so does a token whose signer
// signs the TSTInfo directly, with no signed attributes, which sigstore-go
// accepts: the signature still binds the time to the authority's key.
func TestSigstoreKeyless_VerifyTimestampForms(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	hash := hashOf([]byte("acme artifact"))
	v := harnessVerifier(h)
	for name, opt := range map[string]sh.TSOpt{
		"signer found in the trusted root": sh.WithoutEmbeddedCerts(),
		"no signed attributes":             sh.WithoutSignedAttrs(),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := v.Verify(context.Background(), hash, h.KeylessBundle(t, hash, sh.WithTimestampOptions(opt))); err != nil {
				t.Fatalf("Verify: %v", err)
			}
		})
	}
}

// Spec: §4.7.9 — the log entry conditions: the bundle carries one Rekor v2
// hashedrekord 0.0.2 entry of a log the trusted root lists, whose
// inclusion proof leads from the entry the bundle's digest, signature, and
// leaf make to a checkpoint that log's key signed.
// Matrix: §6.10 (materialize.signature_invalid)
func TestSigstoreKeyless_VerifyLogEntryRefusals(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	hash := hashOf([]byte("acme artifact"))
	otherLeaf := parseBundle(t, h.KeylessBundle(t, hashOf([]byte("acme other")))).GetVerificationMaterial().GetCertificate().GetRawBytes()
	valid := h.KeylessBundle(t, hash)
	cases := []keylessRefusal{
		{name: "no log entry", env: []sh.EnvOpt{sh.WithoutTLog()}, want: "carries no log entry"},
		{name: "entry digest", env: []sh.EnvOpt{sh.WithEntryDigest(hashOf([]byte("acme other")))}, want: "log entry does not verify"},
		{name: "entry signature", env: []sh.EnvOpt{sh.WithEntrySignature([]byte("acme other signature"))}, want: "log entry does not verify"},
		{name: "entry certificate", env: []sh.EnvOpt{sh.WithEntryCert(otherLeaf)}, want: "log entry does not verify"},
		{name: "hashedrekord v0.0.1 body", env: []sh.EnvOpt{sh.WithEntryKind("hashedrekord", "0.0.1")}, want: "(parse)"},
		{name: "kind version 0.0.1", bundle: mutate(t, valid, func(m map[string]any) {
			entries := member(m, "verificationMaterial")["tlogEntries"].([]any)
			member(entries[0].(map[string]any), "kindVersion")["version"] = "0.0.1"
		}), want: "not a hashedrekord 0.0.2 entry"},
		{name: "log ID of an unlisted key", env: []sh.EnvOpt{sh.WithForeignLogID()}, want: "names no transparency log"},
		{name: "no inclusion proof", env: []sh.EnvOpt{sh.WithoutInclusionProof()}, want: "(parse)"},
		{name: "flipped proof hash", env: []sh.EnvOpt{sh.WithFlippedProofHash()}, want: "log entry does not verify"},
		{name: "wrong log index", env: []sh.EnvOpt{sh.WithLogIndex(4)}, want: "log entry does not verify"},
		{name: "log index at the tree size", env: []sh.EnvOpt{sh.WithLogIndex(7)}, want: "log entry does not verify"},
		{name: "negative log index", env: []sh.EnvOpt{sh.WithLogIndex(-1)}, want: "(parse)"},
		{name: "checkpoint signed by a foreign key", env: []sh.EnvOpt{sh.WithCheckpointSignedByForeignKey()}, want: "log entry does not verify"},
		{name: "malformed checkpoint", env: []sh.EnvOpt{sh.WithMalformedCheckpoint()}, want: "log entry does not verify"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t, h, hash)
		})
	}
}

// Spec: §4.7.9 — the RFC 9162 root recomputation accepts an entry at
// every position of trees of 1 through 9 leaves.
func TestSigstoreKeyless_VerifyRecomputesEveryTreePosition(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	hash := hashOf([]byte("acme artifact"))
	v := harnessVerifier(h)
	for n := 1; n <= 9; n++ {
		for i := 0; i < n; i++ {
			if err := v.Verify(context.Background(), hash, h.KeylessBundle(t, hash, sh.WithTreeSize(n, i))); err != nil {
				t.Errorf("tree size %d, index %d: %v", n, i, err)
			}
		}
	}
}

// failingTransport fails the test on any request.
type failingTransport struct{ t *testing.T }

func (f failingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.t.Errorf("Verify sent a request to %s", r.URL)
	return nil, fmt.Errorf("no network in Verify")
}

// Spec: §4.7.9 — Verify makes no network call, even with the signing
// endpoints and a client configured.
func TestSigstoreKeyless_VerifyMakesNoNetworkCall(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	hash := hashOf([]byte("acme artifact"))
	v := harnessVerifier(h)
	v.RekorURL, v.TSAURL = "https://rekor.acme.test", "https://tsa.acme.test"
	v.Client = &http.Client{Transport: failingTransport{t}}
	if err := v.Verify(context.Background(), hash, h.KeylessBundle(t, hash)); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// Spec: §4.7.9, §6.2 — a trusted-root entry is used only when its
// validFor window contains the verified time T, both ends included. A log
// key listed twice under one log ID is used with its last listing's
// window.
// Matrix: §6.10 (materialize.signature_invalid)
func TestSigstoreKeyless_VerifyTrustedRootWindows(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	hash := hashOf([]byte("acme artifact"))
	at := func(d time.Duration) *time.Time { v := h.Clock().Add(d); return &v }
	cases := []struct {
		name string
		root sh.RootOpt
		want string // empty means accepted
	}{
		{"log window ending at T", sh.WithLogValidFor(sh.Window{Start: at(-time.Hour), End: at(0)}), ""},
		{"log window ending before T", sh.WithLogValidFor(sh.Window{Start: at(-time.Hour), End: at(-time.Second)}), "window excludes a verified time"},
		{"log window starting after T", sh.WithLogValidFor(sh.Window{Start: at(time.Second)}), "window excludes a verified time"},
		{"CA window starting at T", sh.WithCAValidFor(at(0), nil), ""},
		{"CA window ending before T", sh.WithCAValidFor(at(-2*time.Hour), at(-time.Second)), "does not chain"},
		{"TSA window ending before T", sh.WithTSAValidFor(at(-2*time.Hour), at(-time.Second)), "verifies under no authority"},
		{"key listed twice, the last listing covers T", sh.WithLogValidFor(
			sh.Window{Start: at(-2 * time.Hour), End: at(-time.Hour)},
			sh.Window{Start: at(-time.Second)}), ""},
		{"key listed twice, the last listing excludes T", sh.WithLogValidFor(
			sh.Window{Start: at(-time.Second)},
			sh.Window{Start: at(-2 * time.Hour), End: at(-time.Hour)}), "window excludes a verified time"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			err := harnessVerifier(h, c.root).Verify(context.Background(), hash, h.KeylessBundle(t, hash))
			if c.want == "" {
				if err != nil {
					t.Fatalf("Verify: %v", err)
				}
				return
			}
			wantRefused(t, err, c.want)
		})
	}
}

// Spec: §4.7.9, §6.2 — a trusted root that also lists an Ed25519 log key,
// an RSA log key, and another log's key, as Sigstore's own file lists
// several logs, still verifies the default bundle.
func TestSigstoreKeyless_VerifyIgnoresUnusedTrustedRootEntries(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	hash := hashOf([]byte("acme artifact"))
	v := harnessVerifier(h, sh.WithEd25519LogKey(), sh.WithUnsupportedLogKey(), sh.WithExtraTLog())
	if err := v.Verify(context.Background(), hash, h.KeylessBundle(t, hash)); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// Spec: §4.7.9 — Sign against Fulcio, a timestamp authority, and Rekor v2
// produces a version 0.3 bundle carrying the leaf, the log entry with its
// inclusion proof and checkpoint, the timestamp response, and the message
// signature over the digest, and Verify accepts it.
func TestSigstoreKeyless_RoundTrip(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	provider := signer(t, h, h.FakeServer(t))
	hash := hashOf([]byte("podium artifact body"))
	bundle, err := provider.Sign(context.Background(), hash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	pb := parseBundle(t, bundle)
	vm := pb.GetVerificationMaterial()
	entries := vm.GetTlogEntries()
	if pb.GetMediaType() != sh.BundleMediaTypeV03 || len(vm.GetCertificate().GetRawBytes()) == 0 ||
		len(entries) != 1 || entries[0].GetLogIndex() != 5 || entries[0].GetInclusionProof().GetCheckpoint().GetEnvelope() == "" ||
		len(vm.GetTimestampVerificationData().GetRfc3161Timestamps()) != 1 || len(pb.GetMessageSignature().GetSignature()) == 0 {
		t.Fatalf("bundle lacks a member: %s", bundle)
	}
	digest, _ := hex.DecodeString(strings.TrimPrefix(hash, "sha256:"))
	if got := pb.GetMessageSignature().GetMessageDigest().GetDigest(); string(got) != string(digest) {
		t.Fatalf("message digest = %x, want %x", got, digest)
	}
	if err := provider.Verify(context.Background(), hash, bundle); err != nil {
		t.Fatalf("Verify round-trip: %v", err)
	}
}

// Spec: §4.7.9 — protojson omits a zero logIndex, so an absent value is
// recorded as log index 0 and the bundle still verifies.
func TestSigstoreKeyless_SignRecordsAbsentLogIndexAsZero(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	provider := signer(t, h, h.FakeServer(t, sh.WithRekorLogIndexOmitted()))
	hash := hashOf([]byte("podium artifact body"))
	bundle, err := provider.Sign(context.Background(), hash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if entries := parseBundle(t, bundle).GetVerificationMaterial().GetTlogEntries(); len(entries) != 1 || entries[0].GetLogIndex() != 0 {
		t.Fatalf("log index is not 0: %s", bundle)
	}
	if err := provider.Verify(context.Background(), hash, bundle); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// Spec: §4.7.9 — Sign returns ErrSigstoreUnavailable when any of
// FulcioURL, RekorURL, TSAURL, or OIDCToken is empty, before any network
// call.
func TestSigstoreKeyless_UnconfiguredFails(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	var count atomic.Int64
	srv := h.FakeServer(t, sh.WithRequestCounter(&count))
	clears := map[string]func(*sign.SigstoreKeyless){
		"FulcioURL": func(s *sign.SigstoreKeyless) { s.FulcioURL = "" },
		"RekorURL":  func(s *sign.SigstoreKeyless) { s.RekorURL = "" },
		"TSAURL":    func(s *sign.SigstoreKeyless) { s.TSAURL = "" },
		"OIDCToken": func(s *sign.SigstoreKeyless) { s.OIDCToken = "" },
	}
	for field, clear := range clears {
		provider := signer(t, h, srv)
		clear(&provider)
		_, err := provider.Sign(context.Background(), hashOf([]byte("body")))
		if !errors.Is(err, sign.ErrSigstoreUnavailable) {
			t.Errorf("%s empty: got %v, want ErrSigstoreUnavailable", field, err)
		}
	}
	if n := count.Load(); n != 0 {
		t.Fatalf("server saw %d requests, want 0", n)
	}
}

// Spec: §4.7.9 — Sign fails, naming the service, when a service is down
// or answers without the member the bundle needs.
func TestSigstoreKeyless_SignRefusesIncompleteResponses(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	cases := []struct {
		name string
		opt  sh.ServerOpt
		want string
	}{
		{"fulcio outage", sh.WithFulcioFailure(), "fulcio"},
		{"rekor outage", sh.WithRekorFailure(), "rekor: HTTP 503"},
		{"no body", sh.WithRekorBodyOmitted(), "rekor: response carries no canonicalized body"},
		{"no inclusion proof", sh.WithRekorProofOmitted(), "rekor: response carries no inclusion proof"},
		{"no checkpoint", sh.WithRekorCheckpointOmitted(), "rekor: response carries no checkpoint"},
		{"timestamp rejection", sh.WithTimestampStatus(2), "tsa: status 2"},
		{"timestamp without token", sh.WithTimestampTokenOmitted(), "tsa: response carries no token"},
		{"timestamp authority outage", sh.WithTSAFailure(), "tsa: HTTP 503"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			provider := signer(t, h, h.FakeServer(t, c.opt))
			bundle, err := provider.Sign(context.Background(), hashOf([]byte("body")))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Sign = (%q, %v), want an error containing %q", bundle, err, c.want)
			}
		})
	}
}

// Spec: §6.2 — a log that serves no /api/v2 path fails the signing: a
// Rekor v1 URL answers 404 and its v1 handler sees no request.
func TestSigstoreKeyless_SignRefusesRekorV1(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	var v1Hits atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/log/entries", func(w http.ResponseWriter, _ *http.Request) {
		v1Hits.Add(1)
		w.WriteHeader(http.StatusCreated)
	})
	v1 := httptest.NewServer(mux)
	t.Cleanup(v1.Close)
	provider := signer(t, h, h.FakeServer(t))
	provider.RekorURL = v1.URL
	_, err := provider.Sign(context.Background(), hashOf([]byte("body")))
	if err == nil || !strings.Contains(err.Error(), "rekor: HTTP 404") {
		t.Fatalf("Sign err = %v, want rekor: HTTP 404", err)
	}
	if n := v1Hits.Load(); n != 0 {
		t.Fatalf("v1 handler saw %d requests, want 0", n)
	}
}

// Spec: §4.7.9 — Sign refuses a content hash other than SHA-256 before any
// network call.
func TestSigstoreKeyless_SignRefusesNonSHA256(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	var count atomic.Int64
	provider := signer(t, h, h.FakeServer(t, sh.WithRequestCounter(&count)))
	sum := sha512.Sum512([]byte("body"))
	_, err := provider.Sign(context.Background(), "sha512:"+hex.EncodeToString(sum[:]))
	if err == nil || !strings.Contains(err.Error(), "is not sha256") {
		t.Fatalf("Sign err = %v, want is not sha256", err)
	}
	if n := count.Load(); n != 0 {
		t.Fatalf("server saw %d requests, want 0", n)
	}
}

// Spec: §4.7.9, §4.7.10 — a registry-managed delivery envelope is no
// Sigstore bundle, so the Sigstore-keyless verifier refuses it.
// Matrix: §6.10 (materialize.signature_invalid)
func TestSigstoreKeyless_VerifyRefusesADeliveryEnvelope(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	hash := hashOf([]byte("acme delivery record"))
	env, err := sign.RegistryManagedKey{PrivateKey: priv}.Sign(context.Background(), hash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	wantRefused(t, harnessVerifier(h).Verify(context.Background(), hash, env), "not a version 0.3 Sigstore bundle")
}

// Spec: §4.7.9 — a keyless envelope in the format of 0.7.0, which carried
// cert, signature, tlog, and timestamp members, is refused, and the
// refusal names the format change.
// Matrix: §6.10 (materialize.signature_invalid)
func TestSigstoreKeyless_VerifyRefusesA070Envelope(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	files := h.WriteFiles(t, t.TempDir())
	raw, err := os.ReadFile(files.Envelopes[sh.EnvelopeLegacy])
	if err != nil {
		t.Fatalf("read legacy envelope: %v", err)
	}
	wantRefused(t, harnessVerifier(h).Verify(context.Background(), files.ContentHash, string(raw)), "keyless envelope from 0.7.0 or earlier")
}

// Spec: §4.7.9 — RegistryManagedKey Sign + Verify round-trip with
// an Ed25519 keypair.
func TestRegistryManagedKey_RoundTrip(t *testing.T) {
	t.Parallel()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	provider := sign.RegistryManagedKey{
		PrivateKey: priv,
		PublicKey:  pub,
	}
	contentHash := hashOf([]byte("body"))
	envelopeStr, err := provider.Sign(context.Background(), contentHash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := provider.Verify(context.Background(), contentHash, envelopeStr); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// Spec: §4.7.9 — an envelope signed by a key outside the verifier's set is
// refused, whether its key_id names that outside key or names a key inside the
// set, because the key_id selects only the order in which keys are tried.
func TestRegistryManagedKey_RefusesKeyOutsideSet(t *testing.T) {
	t.Parallel()
	insidePub, _, _ := ed25519.GenerateKey(rand.Reader)
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	_, outsidePriv, _ := ed25519.GenerateKey(rand.Reader)
	hash := hashOf([]byte("body"))
	envelope, err := sign.RegistryManagedKey{PrivateKey: outsidePriv}.Sign(context.Background(), hash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	verifier := sign.RegistryManagedKey{Trusted: []ed25519.PublicKey{insidePub, otherPub}}
	if err := verifier.Verify(context.Background(), hash, envelope); !errors.Is(err, sign.ErrSignatureInvalid) {
		t.Errorf("outside key naming itself: got %v, want ErrSignatureInvalid", err)
	}
	var env map[string]string
	if err := json.Unmarshal([]byte(envelope), &env); err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	env["key_id"] = sign.KeyIDFor(insidePub)
	forged, _ := json.Marshal(env)
	if err := verifier.Verify(context.Background(), hash, string(forged)); !errors.Is(err, sign.ErrSignatureInvalid) {
		t.Errorf("outside key naming an inside key: got %v, want ErrSignatureInvalid", err)
	}
}

// Spec: §4.7.9 — Sign with no keypair returns
// ErrRegistryManagedUnavailable.
func TestRegistryManagedKey_UnconfiguredFails(t *testing.T) {
	t.Parallel()
	if _, err := (sign.RegistryManagedKey{}).Sign(context.Background(), hashOf([]byte("body"))); !errors.Is(err, sign.ErrRegistryManagedUnavailable) {
		t.Fatalf("got %v, want ErrRegistryManagedUnavailable", err)
	}
}
