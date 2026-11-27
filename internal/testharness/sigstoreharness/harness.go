// Package sigstoreharness generates Sigstore trust material, signed
// bundles, tamper variants, and fake Fulcio, Rekor v2, and
// timestamp-authority services in-process: version 0.3 message-signature
// bundles with Rekor v2 entries for the §4.7.9 keyless tests, and version
// 0.3 DSSE bundles with Rekor v1 entries, RFC 3161 timestamps, embedded
// SCTs, and Fulcio source extensions for the §4.7.11 tests.
//
// The package imports the standard library and internal/testharness
// only. It never imports pkg/sign: the package-sign tests import this
// harness, so the reverse import would be a cycle, and building the
// bundles from independent types catches format drift between the
// generator and the verifier.
//
// Spec: §4.7.9, §4.7.11, §6.2.
package sigstoreharness

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"testing"
	"time"
)

// Identity values every default leaf carries.
const (
	// DefaultSAN is the email subject alternative name of a default leaf.
	DefaultSAN = "alice@acme.com"
	// DefaultIssuer is the OIDC issuer a default leaf carries in the
	// Fulcio 1.3.6.1.4.1.57264.1.8 extension.
	DefaultIssuer = "https://accounts.acme.com"
	// checkpointOrigin is the origin line of every generated checkpoint.
	checkpointOrigin = "log.acme.test"
)

// Harness holds the trust material one test generates bundles from.
// The keys are fresh per Harness, so two harnesses never trust each
// other's output.
type Harness struct {
	tb          testing.TB
	clock       time.Time
	fulcioRoot  authority
	fulcioInter authority
	tsaRoot     authority
	tsaLeaf     authority
	logKey      *ecdsa.PrivateKey
	edLogKey    ed25519.PrivateKey
	// ctKey is the certificate-transparency log key that signs the
	// embedded SCT of an attestation leaf.
	ctKey *ecdsa.PrivateKey
	// tsas caches the timestamp authorities other than TSAPrimary, which
	// timestampAuthority builds on first use.
	tsas map[TSA]tsaAuthority
}

// New builds a Fulcio root and code-signing intermediate, a
// timestamp-authority root and a signer with the critical time-stamping
// usage, an ECDSA P-256 and an Ed25519 transparency-log key, an ECDSA
// P-256 certificate-transparency log key, and a fixed clock at
// 2026-01-15T12:00:00Z.
func New(t testing.TB) *Harness {
	t.Helper()
	clock := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	fRoot, fInter, err := newFulcio("acme-fulcio", clock)
	must(t, err)
	tRoot, tLeaf, err := newTimestampAuthority("acme-tsa", clock)
	must(t, err)
	logKey, err := newECKey()
	must(t, err)
	_, edLogKey, err := ed25519.GenerateKey(rand.Reader)
	must(t, err)
	ctKey, err := newECKey()
	must(t, err)
	return &Harness{
		tb: t, clock: clock,
		fulcioRoot: fRoot, fulcioInter: fInter,
		tsaRoot: tRoot, tsaLeaf: tLeaf,
		logKey: logKey, edLogKey: edLogKey, ctKey: ctKey,
	}
}

// Clock returns the harness time: the timestamp time of every default
// token and the centre of every default leaf's validity window.
func (h *Harness) Clock() time.Time { return h.clock }

// LogPublicKey returns the ECDSA P-256 transparency-log key that signs
// default checkpoints.
func (h *Harness) LogPublicKey() *ecdsa.PublicKey { return &h.logKey.PublicKey }

// Ed25519LogPublicKey returns the Ed25519 transparency-log key that
// signs checkpoints built with WithEd25519Checkpoint.
func (h *Harness) Ed25519LogPublicKey() ed25519.PublicKey {
	return h.edLogKey.Public().(ed25519.PublicKey)
}

// TSALeaf returns the timestamp-authority signer certificate.
func (h *Harness) TSALeaf() *x509.Certificate { return h.tsaLeaf.cert }

// TSARoot returns the timestamp-authority root certificate.
func (h *Harness) TSARoot() *x509.Certificate { return h.tsaRoot.cert }

// FulcioRoot returns the Fulcio root certificate.
func (h *Harness) FulcioRoot() *x509.Certificate { return h.fulcioRoot.cert }

// FulcioIntermediate returns the Fulcio code-signing intermediate.
func (h *Harness) FulcioIntermediate() *x509.Certificate { return h.fulcioInter.cert }

// IssuerValue returns the DER UTF8String a Fulcio 1.3.6.1.4.1.57264.1.8
// extension carries for issuer, for use with WithIssuerExt.
func IssuerValue(issuer string) []byte { return utf8String(issuer) }

// must fails the test on a harness construction error. Generation
// errors come from crypto/rand or encoding and do not depend on the
// test's input, so the harness reports them as a fatal setup failure.
func must(tb testing.TB, err error) {
	tb.Helper()
	if err != nil {
		tb.Fatalf("sigstoreharness: %v", err)
	}
}
