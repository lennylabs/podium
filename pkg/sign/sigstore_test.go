package sign_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

// mutate rewrites one member of an envelope's JSON.
func mutate(t *testing.T, env string, edit func(map[string]any)) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(env), &m); err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	edit(m)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("encode envelope: %v", err)
	}
	return string(out)
}

// leafDER returns the DER of an envelope's leaf certificate.
func leafDER(t *testing.T, env string) []byte {
	t.Helper()
	var e struct {
		Cert string `json:"cert"`
	}
	if err := json.Unmarshal([]byte(env), &e); err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	block, _ := pem.Decode([]byte(e.Cert))
	if block == nil {
		t.Fatal("envelope cert carries no PEM block")
	}
	return block.Bytes
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

// Spec: §4.7.9 — an envelope whose timestamp, inclusion proof, entry
// binding, chain, signature, and identity all hold verifies, under an
// identity list with whitespace and empty entries, through a URI SAN when
// the email SAN is outside the list, and under an Ed25519 log key.
func TestSigstoreKeyless_VerifyAcceptsBoundEnvelope(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	hash := hashOf([]byte("acme artifact"))
	const workflow = "https://github.com/acme/repo/.github/workflows/release.yml@refs/heads/main"
	cases := []struct {
		name   string
		env    string
		root   []sh.RootOpt
		policy sign.IdentityPolicy
	}{
		{"default", h.Envelope(t, hash), nil, defaultPolicy()},
		{"list with whitespace and empty entries", h.Envelope(t, hash), nil,
			sign.NewIdentityPolicy(" bob@acme.com, ,alice@acme.com ", sh.DefaultIssuer)},
		{"URI SAN in the list", h.Envelope(t, hash, sh.WithSAN("carol@acme.com"), sh.WithURISAN(workflow)), nil,
			sign.NewIdentityPolicy(workflow, " "+sh.DefaultIssuer+" ")},
		{"Ed25519 checkpoint", h.Envelope(t, hash, sh.WithEd25519Checkpoint()), []sh.RootOpt{sh.WithEd25519LogKey()}, defaultPolicy()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			v := sign.SigstoreKeyless{TrustRoot: h.TrustedRootJSON(c.root...), Identity: c.policy}
			if err := v.Verify(context.Background(), hash, c.env); err != nil {
				t.Fatalf("Verify: %v", err)
			}
		})
	}
}

// Spec: §4.7.9 — validity is evaluated at the timestamp time: the leaf's
// NotAfter (2026-01-15T12:15Z) is in the past on the wall clock and the
// envelope still verifies with no clock injection, while a timestamp
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
	if err := v.Verify(context.Background(), hash, h.Envelope(t, hash)); err != nil {
		t.Fatalf("Verify expired leaf with a valid timestamp: %v", err)
	}
	late := h.Envelope(t, hash, sh.WithTimestampTime(h.Clock().Add(time.Hour)))
	wantRefused(t, v.Verify(context.Background(), hash, late), "certificate not valid at timestamp time")
}

// Spec: §4.7.9, §6.2 — every acceptance condition refuses on its own, and
// each trusted-root role anchors only its own certificates: a leaf
// chains to a certificateAuthorities entry and a timestamp token to a
// timestampAuthorities entry.
// Matrix: §6.10 (materialize.signature_invalid)
func TestSigstoreKeyless_VerifyRefusals(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	hash := hashOf([]byte("acme artifact"))
	sum512 := sha512.Sum512([]byte("acme artifact"))
	otherLeaf := leafDER(t, h.Envelope(t, hashOf([]byte("acme other"))))
	empty := sign.IdentityPolicy{}
	noIssuer := sign.NewIdentityPolicy(sh.DefaultSAN, " ")
	tsaStart, tsaEnd := h.Clock().Add(-2*time.Hour), h.Clock().Add(-time.Second)
	malformedV2 := []byte{0x0c, 0x05, 'a'}
	rootPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: h.FulcioRoot().Raw})
	valid := h.Envelope(t, hash)
	cases := []struct {
		name     string
		env      []sh.EnvOpt
		root     []sh.RootOpt
		rawRoot  []byte // replaces the generated trusted root when set
		noRoot   bool
		policy   *sign.IdentityPolicy
		hash     string // verifies against this content hash when set
		envelope string // verifies this envelope when set
		want     string
	}{
		{name: "SAN outside the list", env: []sh.EnvOpt{sh.WithSAN("bob@acme.com")}, want: "certificate identity mismatch"},
		{name: "SAN differs in case", env: []sh.EnvOpt{sh.WithSAN("Alice@acme.com")}, want: "certificate identity mismatch"},
		{name: "email and URI SAN outside the list", env: []sh.EnvOpt{sh.WithSAN("carol@acme.com"), sh.WithURISAN("https://github.com/acme/other")}, want: "certificate identity mismatch"},
		{name: "issuer mismatch", env: []sh.EnvOpt{sh.WithIssuerExt(sh.OIDIssuerV2, sh.IssuerValue("https://evil.example"))}, want: "OIDC issuer mismatch"},
		{name: "otherName SAN only", env: []sh.EnvOpt{sh.WithOtherNameSANOnly()}, want: "cert chain"},
		{name: "no issuer extension", env: []sh.EnvOpt{sh.WithoutIssuer()}, want: "no OIDC issuer extension"},
		{name: "malformed .1.8 beside a matching .1.1", env: []sh.EnvOpt{sh.WithIssuerExt(sh.OIDIssuerV2, malformedV2), sh.WithIssuerExt(sh.OIDIssuerV1, []byte(sh.DefaultIssuer))}, want: "malformed OIDC issuer extension"},
		{name: "no tlog", env: []sh.EnvOpt{sh.WithoutTLog()}, want: "no transparency-log entry"},
		{name: "entry digest", env: []sh.EnvOpt{sh.WithEntryDigest(hashOf([]byte("acme other")))}, want: "does not bind the digest"},
		{name: "entry signature", env: []sh.EnvOpt{sh.WithEntrySignature([]byte("acme other signature"))}, want: "does not bind the signature"},
		{name: "entry certificate", env: []sh.EnvOpt{sh.WithEntryCert(otherLeaf)}, want: "does not bind the certificate"},
		{name: "hashedrekord v0.0.1", env: []sh.EnvOpt{sh.WithEntryKind("hashedrekord", "0.0.1")}, want: "not a hashedrekord v0.0.2 entry"},
		{name: "tampered content hash", hash: hashOf([]byte("acme tampered")), want: "does not bind the digest"},
		{name: "content hash without alg", hash: hex.EncodeToString([]byte("acme")), want: "content hash"},
		{name: "sha512 content hash", hash: "sha512:" + hex.EncodeToString(sum512[:]), want: "is not sha256"},
		{name: "foreign signature", env: []sh.EnvOpt{sh.WithForeignSignature()}, want: "signature does not verify"},
		{name: "Ed25519 leaf", env: []sh.EnvOpt{sh.WithEd25519Leaf()}, want: "leaf is not ECDSA"},
		{name: "server-auth leaf", env: []sh.EnvOpt{sh.WithoutCodeSigning()}, want: "cert chain"},
		{name: "leaf without EKU", env: []sh.EnvOpt{sh.WithLeafWithoutEKU()}, want: "leaf lacks the code-signing usage"},
		{name: "any-purpose leaf", env: []sh.EnvOpt{sh.WithLeafAnyUsage()}, want: "leaf lacks the code-signing usage"},
		{name: "foreign certificate authority", root: []sh.RootOpt{sh.WithForeignCA()}, want: "cert chain"},
		{name: "leaf issued by the TSA root", env: []sh.EnvOpt{sh.WithLeafIssuedByTSARoot()}, want: "cert chain"},
		{name: "token signed under the Fulcio root", env: []sh.EnvOpt{sh.WithTimestampSignedByFulcioCA()}, want: "timestamp authority not trusted"},
		{name: "empty identity policy", policy: &empty, want: "PODIUM_SIGSTORE_CERT_IDENTITY"},
		{name: "empty issuer", policy: &noIssuer, want: "PODIUM_SIGSTORE_CERT_OIDC_ISSUER"},
		{name: "no certificate authority", root: []sh.RootOpt{sh.WithoutCAs()}, want: "no certificate authority"},
		{name: "no log key", root: []sh.RootOpt{sh.WithoutTLogs()}, want: "no usable transparency-log key"},
		{name: "only an unsupported log key", root: []sh.RootOpt{sh.WithoutTLogs(), sh.WithUnsupportedLogKey()}, want: "no usable transparency-log key"},
		{name: "no timestamp authority", root: []sh.RootOpt{sh.WithoutTSAs()}, want: "no timestamp authority"},
		{name: "timestamp authority expired", root: []sh.RootOpt{sh.WithTSAValidFor(&tsaStart, &tsaEnd)}, want: "timestamp authority not trusted"},
		{name: "garbage trust-root certificate", root: []sh.RootOpt{sh.WithGarbageCert()}, want: "trust root certificate"},
		{name: "malformed trust root", root: []sh.RootOpt{sh.WithMalformedJSON()}, want: "trust root does not parse"},
		{name: "PEM trust root", rawRoot: rootPEM, want: "trust root does not parse"},
		{name: "empty trust root", noRoot: true, want: "no trust root configured"},
		{name: "malformed envelope", envelope: "not-json", want: "parse envelope"},
		{name: "empty envelope", envelope: `{}`, want: "empty envelope"},
		{name: "signature not base64", envelope: mutate(t, valid, func(m map[string]any) { m["signature"] = "%%%" }), want: "signature decode"},
		{name: "timestamp not base64", envelope: mutate(t, valid, func(m map[string]any) { m["timestamp"] = "%%%" }), want: "timestamp does not parse"},
		{name: "cert carries no certificate", envelope: mutate(t, valid, func(m map[string]any) { m["cert"] = "acme" }), want: "cert chain"},
		{name: "no timestamp", env: []sh.EnvOpt{sh.WithoutTimestamp()}, want: "no timestamp"},
		{name: "timestamp over another signature", env: []sh.EnvOpt{sh.WithTimestampOver([]byte("acme other signature"))}, want: "does not cover the signature"},
		{name: "untrusted timestamp authority", env: []sh.EnvOpt{sh.WithUntrustedTSA()}, want: "timestamp authority not trusted"},
		{name: "TSA signer without EKU", env: []sh.EnvOpt{sh.WithTSALeafWithoutTimeStamping()}, want: "signer certificate lacks the time-stamping usage"},
		{name: "any-purpose TSA signer", env: []sh.EnvOpt{sh.WithTSALeafAnyUsage()}, want: "signer certificate lacks the time-stamping usage"},
		{name: "flipped timestamp signature", env: []sh.EnvOpt{sh.WithFlippedTimestampSignature()}, want: "timestamp signature does not verify"},
		{name: "no inclusion proof", env: []sh.EnvOpt{sh.WithoutInclusionProof()}, want: "incomplete"},
		{name: "flipped proof hash", env: []sh.EnvOpt{sh.WithFlippedProofHash()}, want: "inclusion proof does not verify"},
		{name: "wrong log index", env: []sh.EnvOpt{sh.WithLogIndex(4)}, want: "inclusion proof does not verify"},
		{name: "log index at the tree size", env: []sh.EnvOpt{sh.WithLogIndex(7)}, want: "inclusion proof does not verify"},
		{name: "negative log index", env: []sh.EnvOpt{sh.WithLogIndex(-1)}, want: "inclusion proof does not verify"},
		{name: "checkpoint signed by a foreign key", env: []sh.EnvOpt{sh.WithCheckpointSignedByForeignKey()}, want: "checkpoint signature does not verify"},
		{name: "malformed checkpoint", env: []sh.EnvOpt{sh.WithMalformedCheckpoint()}, want: "checkpoint does not parse"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
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
			env := c.envelope
			if env == "" {
				env = h.Envelope(t, hash, c.env...)
			}
			verifyHash := hash
			if c.hash != "" {
				verifyHash = c.hash
			}
			wantRefused(t, v.Verify(context.Background(), verifyHash, env), c.want)
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
			if err := v.Verify(context.Background(), hash, h.Envelope(t, hash, sh.WithTreeSize(n, i))); err != nil {
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
	if err := v.Verify(context.Background(), hash, h.Envelope(t, hash)); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// Spec: §4.7.9, §6.2 — a trusted-root entry is used only when its
// validFor window contains the timestamp time T. Both ends are included,
// and a key listed more than once is usable when any listing's window
// contains T.
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
		{"log window ending before T", sh.WithLogValidFor(sh.Window{Start: at(-time.Hour), End: at(-time.Second)}), "checkpoint signature does not verify under a trusted log key"},
		{"log window starting after T", sh.WithLogValidFor(sh.Window{Start: at(time.Second)}), "checkpoint signature does not verify under a trusted log key"},
		{"CA window starting at T", sh.WithCAValidFor(at(0), nil), ""},
		{"CA window ending before T", sh.WithCAValidFor(at(-2*time.Hour), at(-time.Second)), "no certificate authority valid at timestamp time"},
		{"TSA window ending before T", sh.WithTSAValidFor(at(-2*time.Hour), at(-time.Second)), "timestamp authority not trusted"},
		{"key listed twice", sh.WithLogValidFor(
			sh.Window{Start: at(-2 * time.Hour), End: at(-time.Hour)},
			sh.Window{Start: at(-time.Second)}), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			err := harnessVerifier(h, c.root).Verify(context.Background(), hash, h.Envelope(t, hash))
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
// an unsupported log key, another log's key, and CT logs, as Sigstore's
// own file does, still verifies the default envelope.
func TestSigstoreKeyless_VerifyIgnoresUnusedTrustedRootEntries(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	hash := hashOf([]byte("acme artifact"))
	v := harnessVerifier(h, sh.WithEd25519LogKey(), sh.WithUnsupportedLogKey(), sh.WithExtraTLog(), sh.WithCTLogs())
	if err := v.Verify(context.Background(), hash, h.Envelope(t, hash)); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// Spec: §4.7.9 — Sign against Fulcio, a timestamp authority, and Rekor v2
// produces an envelope carrying the tlog object and the timestamp, and
// Verify accepts it.
func TestSigstoreKeyless_RoundTrip(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	provider := signer(t, h, h.FakeServer(t))
	hash := hashOf([]byte("podium artifact body"))
	env, err := provider.Sign(context.Background(), hash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	var got struct {
		TLog *struct {
			LogIndex   int64    `json:"log_index"`
			Body       string   `json:"body"`
			Hashes     []string `json:"hashes"`
			Checkpoint string   `json:"checkpoint"`
		} `json:"tlog"`
		Timestamp string `json:"timestamp"`
	}
	if err := json.Unmarshal([]byte(env), &got); err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if got.TLog == nil || got.TLog.LogIndex != 5 || got.TLog.Body == "" || len(got.TLog.Hashes) == 0 || got.TLog.Checkpoint == "" || got.Timestamp == "" {
		t.Fatalf("envelope lacks the tlog or timestamp members: %s", env)
	}
	if err := provider.Verify(context.Background(), hash, env); err != nil {
		t.Fatalf("Verify round-trip: %v", err)
	}
}

// Spec: §4.7.9 — protojson omits a zero logIndex, so an absent value is
// recorded as log_index 0 and the envelope still verifies.
func TestSigstoreKeyless_SignRecordsAbsentLogIndexAsZero(t *testing.T) {
	t.Parallel()
	h := sh.New(t)
	provider := signer(t, h, h.FakeServer(t, sh.WithRekorLogIndexOmitted()))
	hash := hashOf([]byte("podium artifact body"))
	env, err := provider.Sign(context.Background(), hash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	var got struct {
		TLog *struct {
			LogIndex *int64 `json:"log_index"`
		} `json:"tlog"`
	}
	if err := json.Unmarshal([]byte(env), &got); err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if got.TLog == nil || got.TLog.LogIndex == nil || *got.TLog.LogIndex != 0 {
		t.Fatalf("tlog.log_index is not a present 0: %s", env)
	}
	if err := provider.Verify(context.Background(), hash, env); err != nil {
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
// or answers without the member the envelope needs.
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
			env, err := provider.Sign(context.Background(), hashOf([]byte("body")))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Sign = (%q, %v), want an error containing %q", env, err, c.want)
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

// Spec: §4.7.9, §4.7.10 — a registry-managed delivery envelope carries no
// certificate chain, so the Sigstore-keyless verifier refuses it.
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
	wantRefused(t, harnessVerifier(h).Verify(context.Background(), hash, env), "empty envelope")
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
