package sign

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// SigstoreKeyless implements §4.7.9 Sigstore-keyless signing.
//
// Sign generates an ephemeral ECDSA P-256 key, mints a short-lived
// cert from Fulcio using the configured OIDC token, signs the
// content hash with the ephemeral key, and (when RekorURL is set)
// records the signature in the Rekor transparency log. The returned
// signature is a compact JSON envelope carrying the cert chain, the
// signature bytes, and the Rekor log index.
//
// Verify decodes the envelope, validates the cert chain against the
// configured trust root, and verifies the signature over the content
// hash. When RekorURL is set, it also confirms the entry exists in
// the transparency log.
//
// SigstoreKeyless is safe to use as a Provider directly. Test code
// supplies Client + TrustRoot + Now to drive the implementation
// against an httptest fixture; production code points FulcioURL +
// RekorURL at the live (or staging) endpoints and leaves Client nil.
type SigstoreKeyless struct {
	// FulcioURL is the Fulcio CA endpoint, e.g.
	// "https://fulcio.sigstore.dev". Required for Sign.
	FulcioURL string
	// RekorURL is the Rekor transparency-log endpoint. When empty,
	// signatures still validate locally but are not anchored in
	// any external log.
	RekorURL string
	// OIDCToken is the caller's identity token used to mint the
	// short-lived signing cert. Required for Sign. Verify ignores it.
	OIDCToken string
	// TrustRoot is the PEM-encoded set of certificates that anchor
	// the cert chain during Verify. Empty means "no implicit trust"
	// and Verify will fail; production deployments load the Sigstore
	// public-good root from disk and assign it here.
	TrustRoot []byte
	// Client overrides the HTTP client used for Fulcio + Rekor calls.
	// Tests inject httptest-backed clients; production leaves it nil
	// to use http.DefaultClient.
	Client *http.Client
	// Now overrides the clock used during cert chain validation.
	// Tests pass a fixed time so vendored fixtures with expired
	// certs continue to verify.
	Now func() time.Time
}

// ID returns "sigstore-keyless".
func (SigstoreKeyless) ID() string { return "sigstore-keyless" }

// httpClient returns the configured HTTP client, defaulting to
// http.DefaultClient.
func (s SigstoreKeyless) httpClient() *http.Client {
	if s.Client != nil {
		return s.Client
	}
	return http.DefaultClient
}

// now returns the configured clock, defaulting to time.Now.
func (s SigstoreKeyless) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// envelope is the JSON encoding of a Sigstore-keyless signature.
// Cert is the PEM-concatenated cert chain (leaf first); Signature is
// the base64-encoded ECDSA signature; LogIndex is the Rekor log
// index, or -1 when no log entry was created.
type envelope struct {
	Cert      string `json:"cert"`
	Signature string `json:"signature"`
	LogIndex  int64  `json:"log_index"`
}

// ErrSigstoreUnavailable signals that the Sigstore endpoints are
// not configured and the keyless flow cannot proceed. Sign returns
// this when FulcioURL or OIDCToken is empty.
var ErrSigstoreUnavailable = errors.New("sign: sigstore-keyless not configured")

// Sign produces a Sigstore-keyless envelope over contentHash.
//
// contentHash must be of the form "alg:hex" (e.g. "sha256:abc...").
// The hex must decode cleanly; invalid forms return an error before
// the network is touched.
func (s SigstoreKeyless) Sign(contentHash string) (string, error) {
	if s.FulcioURL == "" || s.OIDCToken == "" {
		return "", ErrSigstoreUnavailable
	}
	hashBytes, err := decodeContentHash(contentHash)
	if err != nil {
		return "", err
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", fmt.Errorf("ephemeral key: %w", err)
	}

	ctx := context.Background()
	leaf, intermediates, err := s.mintCert(ctx, priv)
	if err != nil {
		return "", fmt.Errorf("fulcio: %w", err)
	}

	sig, err := ecdsa.SignASN1(rand.Reader, priv, hashBytes)
	if err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}

	logIndex := int64(-1)
	if s.RekorURL != "" {
		idx, err := s.uploadRekor(ctx, contentHash, sig, leaf)
		if err != nil {
			return "", fmt.Errorf("rekor: %w", err)
		}
		logIndex = idx
	}

	chain := append([]*x509.Certificate{leaf}, intermediates...)
	body, err := json.Marshal(envelope{
		Cert:      pemEncodeCerts(chain),
		Signature: base64.StdEncoding.EncodeToString(sig),
		LogIndex:  logIndex,
	})
	if err != nil {
		return "", fmt.Errorf("envelope: %w", err)
	}
	return string(body), nil
}

// Verify validates a Sigstore-keyless envelope.
//
// The verification chain: parse envelope → walk cert chain to the
// configured trust root → verify signature with the leaf cert's
// public key against contentHash. When RekorURL is set, also
// confirm the log entry exists.
func (s SigstoreKeyless) Verify(contentHash, signature string) error {
	var env envelope
	if err := json.Unmarshal([]byte(signature), &env); err != nil {
		return fmt.Errorf("%w: parse envelope: %v", ErrSignatureInvalid, err)
	}
	if env.Cert == "" || env.Signature == "" {
		return fmt.Errorf("%w: empty envelope", ErrSignatureInvalid)
	}

	leaf, intermediates, err := pemDecodeChain(env.Cert)
	if err != nil {
		return fmt.Errorf("%w: cert chain: %v", ErrSignatureInvalid, err)
	}
	if len(s.TrustRoot) == 0 {
		return fmt.Errorf("%w: no trust root configured", ErrSignatureInvalid)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(s.TrustRoot) {
		return fmt.Errorf("%w: invalid trust root", ErrSignatureInvalid)
	}
	intPool := x509.NewCertPool()
	for _, c := range intermediates {
		intPool.AddCert(c)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intPool,
		CurrentTime:   s.now(),
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
	}); err != nil {
		return fmt.Errorf("%w: cert chain: %v", ErrSignatureInvalid, err)
	}

	pub, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return fmt.Errorf("%w: leaf is not ECDSA", ErrSignatureInvalid)
	}
	sigBytes, err := base64.StdEncoding.DecodeString(env.Signature)
	if err != nil {
		return fmt.Errorf("%w: signature decode: %v", ErrSignatureInvalid, err)
	}
	hashBytes, err := decodeContentHash(contentHash)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSignatureInvalid, err)
	}
	if !ecdsa.VerifyASN1(pub, hashBytes, sigBytes) {
		return fmt.Errorf("%w: signature does not verify", ErrSignatureInvalid)
	}

	if s.RekorURL != "" && env.LogIndex >= 0 {
		if err := s.fetchRekor(context.Background(), env.LogIndex); err != nil {
			return fmt.Errorf("%w: rekor: %v", ErrSignatureInvalid, err)
		}
	}
	return nil
}

// pemEncodeCerts concatenates a sequence of certs into a single PEM
// block stream. The chain is leaf-first, intermediates after.
func pemEncodeCerts(chain []*x509.Certificate) string {
	out := []byte{}
	for _, c := range chain {
		out = append(out, pem.EncodeToMemory(&pem.Block{
			Type:  "CERTIFICATE",
			Bytes: c.Raw,
		})...)
	}
	return string(out)
}

// pemDecodeChain decodes a PEM-concatenated cert stream produced by
// pemEncodeCerts. Returns (leaf, intermediates, err).
func pemDecodeChain(s string) (*x509.Certificate, []*x509.Certificate, error) {
	rest := []byte(s)
	var certs []*x509.Certificate
	for {
		block, next := pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			c, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return nil, nil, fmt.Errorf("parse cert: %w", err)
			}
			certs = append(certs, c)
		}
		rest = next
	}
	if len(certs) == 0 {
		return nil, nil, fmt.Errorf("no certs in chain")
	}
	return certs[0], certs[1:], nil
}

// decodeContentHash parses an "alg:hex" string and returns the raw
// hash bytes. The algorithm string is informational; the bytes are
// what ECDSA signs over.
func decodeContentHash(s string) ([]byte, error) {
	_, hexStr, err := splitContentHash(s)
	if err != nil {
		return nil, err
	}
	return hex.DecodeString(hexStr)
}
