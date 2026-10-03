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
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/lennylabs/podium/pkg/spi"
)

// SigstoreKeyless implements §4.7.9 Sigstore-keyless signing.
//
// Sign generates an ephemeral ECDSA P-256 key, mints a short-lived
// certificate from Fulcio with the configured OIDC token, signs the
// SHA-256 content hash with the ephemeral key, obtains an RFC 3161
// timestamp over the signature from the timestamp authority, and records
// the entry in a Rekor v2 transparency log. The returned signature is a
// JSON envelope carrying the certificate chain, the signature, the
// timestamp token, and a tlog object holding the log index, the entry
// body, the inclusion-proof hashes, and the signed checkpoint.
//
// Verify is offline. It verifies the timestamp under a timestamp
// authority of the trusted root and takes its time as the attested time,
// verifies the inclusion proof and the checkpoint signature under a
// transparency-log key, binds the entry body to the digest, the
// signature, and the leaf, checks the leaf's chain and code-signing usage
// at the attested time, checks the signature, and matches the leaf
// against the identity policy.
//
// Test code points the three URLs at an httptest fixture and supplies
// Client; production code points them at the live or staging endpoints
// and leaves Client nil.
type SigstoreKeyless struct {
	// FulcioURL is the Fulcio CA endpoint, e.g.
	// "https://fulcio.sigstore.dev". Required for Sign.
	FulcioURL string
	// RekorURL is the base URL of the Rekor v2 transparency log. Required
	// for Sign; Verify does not read it.
	RekorURL string
	// TSAURL is the RFC 3161 timestamp-authority endpoint. Required for
	// Sign; Verify does not read it.
	TSAURL string
	// OIDCToken is the caller's identity token used to mint the
	// short-lived signing cert. Required for Sign. Verify ignores it.
	OIDCToken string
	// TrustRoot holds the bytes of a Sigstore trusted_root.json. Verify
	// parses it on every call; empty means no trust and Verify refuses
	// every envelope. Sign ignores it.
	TrustRoot []byte
	// Identity is the §4.7.9 signer identity Verify accepts. An empty
	// policy accepts no envelope.
	Identity IdentityPolicy
	// Client overrides the HTTP client Sign uses for Fulcio, Rekor, and
	// the timestamp authority. Production leaves it nil to use
	// http.DefaultClient.
	Client *http.Client
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

// post sends req with the configured client and returns the body of a
// 2xx response.
func (s SigstoreKeyless) post(req *http.Request) ([]byte, error) {
	resp, err := s.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}
	return body, nil
}

// maxResponseBytes bounds a Rekor or timestamp-authority response body.
// A genuine response is a few kilobytes.
const maxResponseBytes = 4 << 20

// envelope is the JSON encoding of a Sigstore-keyless signature. Cert is
// the PEM-concatenated chain (leaf first) and Signature the base64 ECDSA
// signature.
type envelope struct {
	Cert      string     `json:"cert"`
	Signature string     `json:"signature"`
	TLog      *tlogEntry `json:"tlog"`
	Timestamp string     `json:"timestamp"` // base64 DER RFC 3161 TimeStampToken (CMS ContentInfo)
}

// tlogEntry is the Rekor v2 entry and inclusion proof a keyless envelope
// carries.
//
// Spec: §4.7.9.
type tlogEntry struct {
	LogIndex   int64    `json:"log_index"`
	Body       string   `json:"body"`       // base64 canonicalized hashedrekord v0.0.2 body
	Hashes     []string `json:"hashes"`     // base64 audit path, leaf to root
	Checkpoint string   `json:"checkpoint"` // signed-note text, as Rekor returns it
}

// ErrSigstoreUnavailable signals that the Sigstore endpoints are not
// configured and the keyless flow cannot proceed. Sign returns it when
// FulcioURL, RekorURL, TSAURL, or OIDCToken is empty, before any network
// call. Structured per §9.3.
var ErrSigstoreUnavailable = &spi.Error{Code: "config.signature_provider_unavailable", Message: "sign: sigstore-keyless not configured"}

// Sign produces a Sigstore-keyless envelope over contentHash, which must
// be of the form "sha256:hex". A malformed or non-SHA-256 hash returns an
// error before the network is touched.
//
// Spec: §4.7.9.
func (s SigstoreKeyless) Sign(ctx context.Context, contentHash string) (string, error) {
	if s.FulcioURL == "" || s.RekorURL == "" || s.TSAURL == "" || s.OIDCToken == "" {
		return "", fmt.Errorf("%w: FulcioURL, RekorURL, TSAURL, and OIDCToken are all required", ErrSigstoreUnavailable)
	}
	digest, err := sha256Digest(contentHash)
	if err != nil {
		return "", err
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", fmt.Errorf("ephemeral key: %w", err)
	}
	leaf, intermediates, err := s.mintCert(ctx, priv)
	if err != nil {
		return "", fmt.Errorf("fulcio: %w", err)
	}
	sig, err := ecdsa.SignASN1(rand.Reader, priv, digest)
	if err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}
	token, err := s.requestTimestamp(ctx, sig)
	if err != nil {
		return "", fmt.Errorf("tsa: %w", err)
	}
	tl, err := s.uploadRekor(ctx, digest, sig, leaf)
	if err != nil {
		return "", fmt.Errorf("rekor: %w", err)
	}
	body, err := json.Marshal(envelope{
		Cert:      pemEncodeCerts(append([]*x509.Certificate{leaf}, intermediates...)),
		Signature: base64.StdEncoding.EncodeToString(sig),
		TLog:      tl,
		Timestamp: base64.StdEncoding.EncodeToString(token),
	})
	if err != nil {
		return "", fmt.Errorf("envelope: %w", err)
	}
	return string(body), nil
}

// Verify validates a Sigstore-keyless envelope offline. Every failure
// wraps ErrSignatureInvalid. ctx is unused: Verify makes no network call.
//
// Spec: §4.7.9.
func (s SigstoreKeyless) Verify(_ context.Context, contentHash, signature string) error {
	if err := s.verify(contentHash, signature); err != nil {
		return fmt.Errorf("%w: %w", ErrSignatureInvalid, err)
	}
	return nil
}

// verify runs the §4.7.9 acceptance conditions in order. The timestamp
// verifies first because its time is the time every later validity check
// runs at.
func (s SigstoreKeyless) verify(contentHash, signature string) error {
	if err := s.Identity.Validate(); err != nil {
		return err
	}
	root, err := parseTrustedRoot(s.TrustRoot)
	if err != nil {
		return err
	}
	env, err := decodeEnvelope(signature)
	if err != nil {
		return err
	}
	attested, err := verifyTimestamp(root, env.token, env.sig)
	if err != nil {
		return err
	}
	if err := verifyInclusion(root, env.TLog, attested); err != nil {
		return err
	}
	leaf, intermediates, err := pemDecodeChain(env.Cert)
	if err != nil {
		return fmt.Errorf("cert chain: %w", err)
	}
	digest, err := sha256Digest(contentHash)
	if err != nil {
		return err
	}
	if err := bindEntry(env.TLog.Body, digest, env.sig, leaf); err != nil {
		return err
	}
	if err := verifyLeafChain(root, leaf, intermediates, attested); err != nil {
		return err
	}
	if err := verifyLeafSignature(leaf, digest, env.sig); err != nil {
		return err
	}
	return s.Identity.match(leaf)
}

// decodedEnvelope is an envelope with its signature and timestamp
// decoded.
type decodedEnvelope struct {
	envelope
	sig   []byte
	token []byte
}

// decodeEnvelope parses the envelope JSON and refuses one that lacks a
// certificate, a signature, a complete log entry, or a timestamp,
// including every envelope minted before the tlog format.
func decodeEnvelope(signature string) (decodedEnvelope, error) {
	var env decodedEnvelope
	if err := json.Unmarshal([]byte(signature), &env.envelope); err != nil {
		return env, fmt.Errorf("parse envelope: %w", err)
	}
	switch {
	case env.Cert == "" || env.Signature == "":
		return env, errors.New("empty envelope")
	case env.TLog == nil:
		return env, errors.New("envelope carries no transparency-log entry")
	case env.TLog.Body == "" || env.TLog.Checkpoint == "":
		return env, errors.New("transparency-log entry is incomplete")
	case env.Timestamp == "":
		return env, errors.New("envelope carries no timestamp")
	}
	var err error
	if env.sig, err = base64.StdEncoding.DecodeString(env.Signature); err != nil {
		return env, fmt.Errorf("signature decode: %w", err)
	}
	if env.token, err = base64.StdEncoding.DecodeString(env.Timestamp); err != nil {
		return env, fmt.Errorf("timestamp does not parse: %w", err)
	}
	return env, nil
}

// verifyLeafChain chains the leaf to a certificate authority valid at the
// attested time t. The pools come from certificateAuthorities alone, so a
// timestamp-authority root never anchors a code-signing leaf.
func verifyLeafChain(root trustedRoot, leaf *x509.Certificate, envIntermediates []*x509.Certificate, t time.Time) error {
	roots, intermediates, ok := authorityPools(root.certificateAuthorities, t, envIntermediates)
	if !ok {
		return fmt.Errorf("no certificate authority valid at timestamp time %s", stamp(t))
	}
	_, err := leaf.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   t,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
	})
	var invalid x509.CertificateInvalidError
	if errors.As(err, &invalid) && invalid.Reason == x509.Expired {
		return fmt.Errorf("certificate not valid at timestamp time %s: %w", stamp(t), err)
	}
	if err != nil {
		return fmt.Errorf("cert chain: %w", err)
	}
	// KeyUsages alone admits a leaf with no extended-key-usage extension or
	// one listing only ExtKeyUsageAny, because crypto/x509 skips the usage
	// check for the first and passes the second, so the leaf's own usage
	// is checked here.
	if !slices.Contains(leaf.ExtKeyUsage, x509.ExtKeyUsageCodeSigning) {
		return errors.New("leaf lacks the code-signing usage")
	}
	return nil
}

// verifyLeafSignature checks the ECDSA signature over the digest under
// the leaf's key.
func verifyLeafSignature(leaf *x509.Certificate, digest, sig []byte) error {
	pub, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return errors.New("leaf is not ECDSA")
	}
	if !ecdsa.VerifyASN1(pub, digest, sig) {
		return errors.New("signature does not verify")
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
// hash bytes.
func decodeContentHash(s string) ([]byte, error) {
	_, hexStr, err := splitContentHash(s)
	if err != nil {
		return nil, err
	}
	return hex.DecodeString(hexStr)
}
