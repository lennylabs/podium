package sign_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/sign"
)

// trustHarness is an in-memory CA + intermediate stack the tests use
// to stand in for the real Sigstore root chain. The harness signs a
// leaf cert against the intermediate when Fulcio is hit, and exposes
// the root as PEM so tests can plug it into SigstoreKeyless.TrustRoot.
type trustHarness struct {
	rootCert     *x509.Certificate
	rootKey      *ecdsa.PrivateKey
	interCert    *x509.Certificate
	interKey     *ecdsa.PrivateKey
	rootPEM      []byte
	interPEM     []byte
	subject      string
	clock        time.Time
	rekorEntries int64
}

func newTrustHarness(t *testing.T, subject string) *trustHarness {
	t.Helper()
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("root key: %v", err)
	}
	rootTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-sigstore-root"},
		NotBefore:             now.Add(-1 * time.Hour),
		NotAfter:              now.Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTmpl, rootTmpl, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	rootCert, _ := x509.ParseCertificate(rootDER)

	interKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("inter key: %v", err)
	}
	interTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "test-fulcio-intermediate"},
		NotBefore:             now.Add(-1 * time.Hour),
		NotAfter:              now.Add(5 * 365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
	}
	interDER, err := x509.CreateCertificate(rand.Reader, interTmpl, rootCert, &interKey.PublicKey, rootKey)
	if err != nil {
		t.Fatalf("create inter: %v", err)
	}
	interCert, _ := x509.ParseCertificate(interDER)

	return &trustHarness{
		rootCert: rootCert, rootKey: rootKey,
		interCert: interCert, interKey: interKey,
		rootPEM:  pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER}),
		interPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: interDER}),
		subject:  subject,
		clock:    now,
	}
}

// signLeaf issues a leaf cert for an ephemeral public key. The cert
// binds the configured subject as a SAN URI so the Sigstore-style
// "code signing" cert shape lines up.
func (h *trustHarness) signLeaf(t *testing.T, pub *ecdsa.PublicKey) []byte {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "ephemeral"},
		NotBefore:    h.clock.Add(-5 * time.Minute),
		NotAfter:     h.clock.Add(15 * time.Minute),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		EmailAddresses: []string{h.subject},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, h.interCert, pub, h.interKey)
	if err != nil {
		t.Fatalf("sign leaf: %v", err)
	}
	return der
}

// fakeFulcioRekor builds an httptest server that responds to both
// /api/v2/signingCert (Fulcio) and /api/v1/log/entries (Rekor) so
// SigstoreKeyless can run end-to-end against it.
func (h *trustHarness) fakeFulcioRekor(t *testing.T, opts ...fakeOpt) *httptest.Server {
	t.Helper()
	cfg := fakeConfig{}
	for _, o := range opts {
		o(&cfg)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v2/signingCert", func(w http.ResponseWriter, r *http.Request) {
		if cfg.fulcioFail {
			http.Error(w, "fulcio offline", http.StatusServiceUnavailable)
			return
		}
		var body struct {
			PublicKeyRequest struct {
				PublicKey struct {
					Content string `json:"content"`
				} `json:"publicKey"`
			} `json:"publicKeyRequest"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		block, _ := pem.Decode([]byte(body.PublicKeyRequest.PublicKey.Content))
		pubAny, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		pub := pubAny.(*ecdsa.PublicKey)
		leafDER := h.signLeaf(t, pub)
		leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
		_ = json.NewEncoder(w).Encode(map[string]any{
			"signedCertificateEmbeddedSct": map[string]any{
				"chain": map[string]any{
					"certificates": []string{
						string(leafPEM),
						string(h.interPEM),
					},
				},
			},
		})
	})
	mux.HandleFunc("/api/v1/log/entries", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			if cfg.rekorFail {
				http.Error(w, "rekor offline", http.StatusServiceUnavailable)
				return
			}
			idx := atomic.AddInt64(&h.rekorEntries, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				fmt.Sprintf("uuid-%d", idx): map[string]any{
					"logID":          "test-log",
					"logIndex":       idx,
					"integratedTime": time.Now().Unix(),
				},
			})
		case http.MethodGet:
			if cfg.rekorMissingIndex {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{})
		}
	})
	return httptest.NewServer(mux)
}

type fakeConfig struct {
	fulcioFail        bool
	rekorFail         bool
	rekorMissingIndex bool
}

type fakeOpt func(*fakeConfig)

func withFulcioFail() fakeOpt        { return func(c *fakeConfig) { c.fulcioFail = true } }
func withRekorFail() fakeOpt         { return func(c *fakeConfig) { c.rekorFail = true } }
func withRekorMissingIndex() fakeOpt { return func(c *fakeConfig) { c.rekorMissingIndex = true } }

// fakeOIDCToken builds a JWT with no signature; only the payload is
// inspected by our oidcSubject helper. Fulcio in production verifies
// the signature; the harness mock does not.
func fakeOIDCToken(t *testing.T, subject string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload := map[string]string{"email": subject, "iss": "https://test"}
	pl, _ := json.Marshal(payload)
	body := base64.RawURLEncoding.EncodeToString(pl)
	return header + "." + body + ".sig"
}

// hashOf returns the canonical content-hash form for body.
func hashOf(body []byte) string {
	h := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(h[:])
}

// Spec: §4.7.9 — SigstoreKeyless Sign and Verify round-trip
// successfully against a CA + Fulcio + Rekor harness. The cert
// chain validates to the configured trust root and the signature
// verifies under the leaf's public key.
// Phase: 1
func TestSigstoreKeyless_RoundTrip(t *testing.T) {
	testharness.RequirePhase(t, 1)
	t.Parallel()
	h := newTrustHarness(t, "alice@example.com")
	srv := h.fakeFulcioRekor(t)
	t.Cleanup(srv.Close)

	provider := sign.SigstoreKeyless{
		FulcioURL: srv.URL,
		RekorURL:  srv.URL,
		OIDCToken: fakeOIDCToken(t, "alice@example.com"),
		TrustRoot: h.rootPEM,
		Client:    srv.Client(),
		Now:       func() time.Time { return h.clock },
	}
	contentHash := hashOf([]byte("podium artifact body"))
	envelopeStr, err := provider.Sign(contentHash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := provider.Verify(contentHash, envelopeStr); err != nil {
		t.Fatalf("Verify round-trip: %v", err)
	}
}

// Spec: §4.7.9 — Sign returns ErrSigstoreUnavailable when the
// Fulcio endpoint is not configured.
// Phase: 1
func TestSigstoreKeyless_UnconfiguredFails(t *testing.T) {
	testharness.RequirePhase(t, 1)
	t.Parallel()
	provider := sign.SigstoreKeyless{}
	_, err := provider.Sign("sha256:" + strings.Repeat("a", 64))
	if !errors.Is(err, sign.ErrSigstoreUnavailable) {
		t.Fatalf("got %v, want ErrSigstoreUnavailable", err)
	}
}

// Spec: §6.10 materialize.signature_invalid — Verify rejects a
// signature whose content hash does not match the envelope.
// Phase: 1
// Matrix: §6.10 (materialize.signature_invalid)
func TestSigstoreKeyless_VerifyDetectsTamperedHash(t *testing.T) {
	testharness.RequirePhase(t, 1)
	t.Parallel()
	h := newTrustHarness(t, "alice@example.com")
	srv := h.fakeFulcioRekor(t)
	t.Cleanup(srv.Close)
	provider := sign.SigstoreKeyless{
		FulcioURL: srv.URL,
		OIDCToken: fakeOIDCToken(t, "alice@example.com"),
		TrustRoot: h.rootPEM,
		Client:    srv.Client(),
		Now:       func() time.Time { return h.clock },
	}
	envelopeStr, err := provider.Sign(hashOf([]byte("original")))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	err = provider.Verify(hashOf([]byte("tampered")), envelopeStr)
	if !errors.Is(err, sign.ErrSignatureInvalid) {
		t.Fatalf("got %v, want ErrSignatureInvalid", err)
	}
}

// Spec: §4.7.9 — Verify rejects when the envelope's cert chain does
// not chain to the configured trust root. A different trust root
// makes the same envelope unverifiable.
// Phase: 1
// Matrix: §6.10 (materialize.signature_invalid)
func TestSigstoreKeyless_VerifyRejectsForeignTrustRoot(t *testing.T) {
	testharness.RequirePhase(t, 1)
	t.Parallel()
	signerHarness := newTrustHarness(t, "alice@example.com")
	srv := signerHarness.fakeFulcioRekor(t)
	t.Cleanup(srv.Close)
	provider := sign.SigstoreKeyless{
		FulcioURL: srv.URL,
		OIDCToken: fakeOIDCToken(t, "alice@example.com"),
		TrustRoot: signerHarness.rootPEM,
		Client:    srv.Client(),
		Now:       func() time.Time { return signerHarness.clock },
	}
	envelopeStr, err := provider.Sign(hashOf([]byte("body")))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	// Now switch trust root to a freshly-minted unrelated CA.
	otherHarness := newTrustHarness(t, "alice@example.com")
	provider.TrustRoot = otherHarness.rootPEM
	err = provider.Verify(hashOf([]byte("body")), envelopeStr)
	if !errors.Is(err, sign.ErrSignatureInvalid) {
		t.Fatalf("got %v, want ErrSignatureInvalid", err)
	}
}

// Spec: §6.10 — Verify rejects when no trust root is configured.
// Phase: 1
func TestSigstoreKeyless_VerifyRejectsMissingTrustRoot(t *testing.T) {
	testharness.RequirePhase(t, 1)
	t.Parallel()
	h := newTrustHarness(t, "alice@example.com")
	srv := h.fakeFulcioRekor(t)
	t.Cleanup(srv.Close)
	signer := sign.SigstoreKeyless{
		FulcioURL: srv.URL,
		OIDCToken: fakeOIDCToken(t, "alice@example.com"),
		TrustRoot: h.rootPEM,
		Client:    srv.Client(),
		Now:       func() time.Time { return h.clock },
	}
	envelopeStr, _ := signer.Sign(hashOf([]byte("body")))
	verifier := signer
	verifier.TrustRoot = nil
	if err := verifier.Verify(hashOf([]byte("body")), envelopeStr); !errors.Is(err, sign.ErrSignatureInvalid) {
		t.Fatalf("got %v, want ErrSignatureInvalid", err)
	}
}

// Spec: §4.7.9 — Sign surfaces a Fulcio outage as a clear error,
// not a swallowed-and-returned-empty signature.
// Phase: 1
func TestSigstoreKeyless_SignFulcioOutage(t *testing.T) {
	testharness.RequirePhase(t, 1)
	t.Parallel()
	h := newTrustHarness(t, "alice@example.com")
	srv := h.fakeFulcioRekor(t, withFulcioFail())
	t.Cleanup(srv.Close)
	provider := sign.SigstoreKeyless{
		FulcioURL: srv.URL,
		OIDCToken: fakeOIDCToken(t, "alice@example.com"),
		TrustRoot: h.rootPEM,
		Client:    srv.Client(),
	}
	_, err := provider.Sign(hashOf([]byte("body")))
	if err == nil {
		t.Fatalf("Sign expected error on Fulcio 503")
	}
	if !strings.Contains(err.Error(), "fulcio") {
		t.Errorf("error %q should mention fulcio", err)
	}
}

// Spec: §8.6 — Verify rejects when RekorURL is configured but the
// log entry the envelope claims does not exist.
// Phase: 1
func TestSigstoreKeyless_VerifyRejectsMissingRekorEntry(t *testing.T) {
	testharness.RequirePhase(t, 1)
	t.Parallel()
	h := newTrustHarness(t, "alice@example.com")
	srv := h.fakeFulcioRekor(t)
	t.Cleanup(srv.Close)
	signer := sign.SigstoreKeyless{
		FulcioURL: srv.URL,
		RekorURL:  srv.URL,
		OIDCToken: fakeOIDCToken(t, "alice@example.com"),
		TrustRoot: h.rootPEM,
		Client:    srv.Client(),
		Now:       func() time.Time { return h.clock },
	}
	envelopeStr, _ := signer.Sign(hashOf([]byte("body")))

	// Now stand up a new server that 404s on the log-fetch path so
	// the verifier sees a missing entry.
	missingSrv := h.fakeFulcioRekor(t, withRekorMissingIndex())
	t.Cleanup(missingSrv.Close)
	verifier := signer
	verifier.RekorURL = missingSrv.URL
	verifier.Client = missingSrv.Client()
	err := verifier.Verify(hashOf([]byte("body")), envelopeStr)
	if !errors.Is(err, sign.ErrSignatureInvalid) {
		t.Fatalf("got %v, want ErrSignatureInvalid", err)
	}
}

// Spec: §4.7.9 — Verify rejects a malformed envelope with
// ErrSignatureInvalid (parse failure must not be swallowed).
// Phase: 1
// Matrix: §6.10 (materialize.signature_invalid)
func TestSigstoreKeyless_VerifyMalformedEnvelope(t *testing.T) {
	testharness.RequirePhase(t, 1)
	t.Parallel()
	h := newTrustHarness(t, "alice@example.com")
	provider := sign.SigstoreKeyless{
		TrustRoot: h.rootPEM,
		Now:       func() time.Time { return h.clock },
	}
	if err := provider.Verify(hashOf([]byte("body")), "not-json"); !errors.Is(err, sign.ErrSignatureInvalid) {
		t.Fatalf("got %v, want ErrSignatureInvalid", err)
	}
	if err := provider.Verify(hashOf([]byte("body")), `{}`); !errors.Is(err, sign.ErrSignatureInvalid) {
		t.Fatalf("got %v, want ErrSignatureInvalid (empty envelope)", err)
	}
}

// Spec: §4.7.9 — RegistryManagedKey Sign + Verify round-trip with
// an Ed25519 keypair.
// Phase: 1
func TestRegistryManagedKey_RoundTrip(t *testing.T) {
	testharness.RequirePhase(t, 1)
	t.Parallel()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	provider := sign.RegistryManagedKey{
		PrivateKey: priv,
		PublicKey:  pub,
		KeyID:      "key-2026q1",
	}
	contentHash := hashOf([]byte("body"))
	envelopeStr, err := provider.Sign(contentHash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := provider.Verify(contentHash, envelopeStr); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// Spec: §4.7.9 — Verify rejects a signature whose KeyID does not
// match the configured key (rotation safety).
// Phase: 1
func TestRegistryManagedKey_RejectsRotatedKey(t *testing.T) {
	testharness.RequirePhase(t, 1)
	t.Parallel()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer := sign.RegistryManagedKey{
		PrivateKey: priv, PublicKey: pub, KeyID: "key-2026q1",
	}
	envelopeStr, _ := signer.Sign(hashOf([]byte("body")))
	verifier := sign.RegistryManagedKey{
		PrivateKey: priv, PublicKey: pub, KeyID: "key-2026q2",
	}
	err := verifier.Verify(hashOf([]byte("body")), envelopeStr)
	if !errors.Is(err, sign.ErrSignatureInvalid) {
		t.Fatalf("got %v, want ErrSignatureInvalid", err)
	}
}

// Spec: §4.7.9 — Sign with no keypair returns
// ErrRegistryManagedUnavailable.
// Phase: 1
func TestRegistryManagedKey_UnconfiguredFails(t *testing.T) {
	testharness.RequirePhase(t, 1)
	t.Parallel()
	if _, err := (sign.RegistryManagedKey{}).Sign(hashOf([]byte("body"))); !errors.Is(err, sign.ErrRegistryManagedUnavailable) {
		t.Fatalf("got %v, want ErrRegistryManagedUnavailable", err)
	}
}
