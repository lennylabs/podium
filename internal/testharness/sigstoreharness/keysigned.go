package sigstoreharness

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"strconv"
	"testing"
	"time"
)

// SignedAtPrefix is the §4.7.11 prefix of the signed-time subject of a
// key-signed statement.
const SignedAtPrefix = ".podium/signed-at/"

// WithSigningKey signs the envelope with key and writes a public key hint
// in place of the leaf certificate, which is the §4.7.11 key-signed bundle
// that `cosign attest-blob --key` writes. The other options still apply,
// so a test can add a timestamp or a log entry to a key-signed bundle.
func WithSigningKey(key *ecdsa.PrivateKey) BundleOpt {
	return func(c *bundleConfig) { c.signingKey, c.noCert = key, true }
}

// WithKeyID sets the keyid member of the envelope's signature, which lies
// outside the signed bytes.
func WithKeyID(id string) BundleOpt { return func(c *bundleConfig) { c.keyID = id } }

// WithKeyHint sets the public key hint of a bundle without a certificate,
// which lies outside the signed bytes.
func WithKeyHint(hint string) BundleOpt { return func(c *bundleConfig) { c.hint = hint } }

// NewSigningKey returns a fresh ECDSA P-256 key, the algorithm a §4.7.11
// key-based signer takes.
func NewSigningKey(tb testing.TB) *ecdsa.PrivateKey {
	tb.Helper()
	key, err := newECKey()
	must(tb, err)
	return key
}

// PublicKeyPEM returns pub as one PEM block of type PUBLIC KEY, as
// `cosign generate-key-pair` writes it to cosign.pub: the §4.7.11
// public_key of a key-based signer.
func PublicKeyPEM(tb testing.TB, pub crypto.PublicKey) string {
	tb.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	must(tb, err)
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// KeyIdentifier returns the §4.7.11 identifier of a key-based signer:
// sha256: and the hexadecimal SHA-256 digest of its DER
// SubjectPublicKeyInfo encoding.
func KeyIdentifier(tb testing.TB, pub crypto.PublicKey) string {
	tb.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	must(tb, err)
	sum := sha256.Sum256(der)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// SignedTimeSubject returns the signed-time subject of t whose digest is
// the layer digest layerHex, as `podium attest --signed-time` writes it.
func SignedTimeSubject(t time.Time, layerHex string) Subject {
	return Subject{Name: SignedAtPrefix + strconv.FormatInt(t.Unix(), 10), SHA256: layerHex}
}
