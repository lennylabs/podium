package sigstoreharness

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"fmt"
	"math/big"
	"net/url"
	"time"
)

var (
	oidExtKeyUsage       = asn1.ObjectIdentifier{2, 5, 29, 37}
	oidSubjectAltName    = asn1.ObjectIdentifier{2, 5, 29, 17}
	oidTimeStampingUsage = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 3, 8}
	// oidFulcioUsername is Fulcio's otherName SAN type for a username identity.
	oidFulcioUsername = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 7}
)

// authority is a certificate together with the key that signs what it issues.
type authority struct {
	cert *x509.Certificate
	key  crypto.Signer
}

// certSpec describes one certificate the harness issues.
type certSpec struct {
	cn        string
	isCA      bool
	notBefore time.Time
	notAfter  time.Time
	keyUsage  x509.KeyUsage
	eku       []x509.ExtKeyUsage
	extra     []pkix.Extension
	emails    []string
	uris      []*url.URL
}

// rootSpec describes a self-signed root. Sigstore's roots carry no
// extended key usage, so neither does this one, and a chain that ends
// at it is never refused by the root's usages.
func rootSpec(cn string, clock time.Time) certSpec {
	return certSpec{
		cn: cn, isCA: true,
		notBefore: clock.Add(-24 * time.Hour), notAfter: clock.Add(10 * 365 * 24 * time.Hour),
		keyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
}

// timestampSignerSpec describes a timestamp-authority signing certificate.
// usage is the extended-key-usage material: the critical time-stamping
// extension by default, none, or the any-purpose usage.
func timestampSignerSpec(cn string, clock time.Time, usage tsaUsage) (certSpec, error) {
	spec := certSpec{
		cn:        cn,
		notBefore: clock.Add(-24 * time.Hour), notAfter: clock.Add(5 * 365 * 24 * time.Hour),
		keyUsage: x509.KeyUsageDigitalSignature,
	}
	switch usage {
	case tsaUsageAny:
		spec.eku = []x509.ExtKeyUsage{x509.ExtKeyUsageAny}
	case tsaUsageCriticalTimeStamping:
		// crypto/x509 writes the extended key usage non-critical, and
		// RFC 3161 §2.3 requires it critical, so the extension is written by hand.
		var w derWriter
		value := w.marshal([]asn1.ObjectIdentifier{oidTimeStampingUsage}, "")
		if w.err != nil {
			return certSpec{}, w.err
		}
		spec.extra = []pkix.Extension{{Id: oidExtKeyUsage, Critical: true, Value: value}}
	}
	return spec, nil
}

// tsaUsage selects the extended key usage of a timestamp signer.
type tsaUsage int

const (
	tsaUsageCriticalTimeStamping tsaUsage = iota
	tsaUsageNone
	tsaUsageAny
)

// template turns a spec into an x509 template for pub.
func (s certSpec) template(pub crypto.PublicKey) (*x509.Certificate, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		return nil, fmt.Errorf("serial: %w", err)
	}
	pkixPub, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("marshal public key: %w", err)
	}
	ski := sha256.Sum256(pkixPub)
	return &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: s.cn},
		NotBefore:             s.notBefore,
		NotAfter:              s.notAfter,
		IsCA:                  s.isCA,
		BasicConstraintsValid: true,
		KeyUsage:              s.keyUsage,
		ExtKeyUsage:           s.eku,
		ExtraExtensions:       s.extra,
		EmailAddresses:        s.emails,
		URIs:                  s.uris,
		SubjectKeyId:          ski[:20],
	}, nil
}

// The constructors below return the errors of key generation, certificate
// creation, and parsing rather than failing the test. With harness-built
// templates and P-256 keys those branches do not run; they propagate so a
// fake-server handler answers 500 instead of calling FailNow from a
// goroutine that is not the test's.

// newECKey generates an ECDSA P-256 key.
func newECKey() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}

// selfSigned creates a self-signed authority over a fresh P-256 key.
func selfSigned(spec certSpec) (authority, error) {
	key, err := newECKey()
	if err != nil {
		return authority{}, err
	}
	tmpl, err := spec.template(key.Public())
	if err != nil {
		return authority{}, err
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return authority{}, fmt.Errorf("create %s: %w", spec.cn, err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return authority{}, fmt.Errorf("parse %s: %w", spec.cn, err)
	}
	return authority{cert: cert, key: key}, nil
}

// issue signs a certificate for pub under a.
func (a authority) issue(spec certSpec, pub crypto.PublicKey) (*x509.Certificate, error) {
	tmpl, err := spec.template(pub)
	if err != nil {
		return nil, err
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.cert, pub, a.key)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", spec.cn, err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", spec.cn, err)
	}
	return cert, nil
}

// child issues a certificate over a fresh P-256 key and returns it as an
// authority that can sign in turn.
func (a authority) child(spec certSpec) (authority, error) {
	key, err := newECKey()
	if err != nil {
		return authority{}, err
	}
	cert, err := a.issue(spec, key.Public())
	if err != nil {
		return authority{}, err
	}
	return authority{cert: cert, key: key}, nil
}

// newFulcio builds a Fulcio-style root and a code-signing intermediate.
func newFulcio(name string, clock time.Time) (root, inter authority, err error) {
	root, err = selfSigned(rootSpec(name+"-root", clock))
	if err != nil {
		return authority{}, authority{}, err
	}
	interSpec := rootSpec(name+"-intermediate", clock)
	interSpec.keyUsage = x509.KeyUsageCertSign
	interSpec.eku = []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning}
	inter, err = root.child(interSpec)
	return root, inter, err
}

// newTimestampAuthority builds a timestamp-authority root and its signer.
func newTimestampAuthority(name string, clock time.Time) (root, leaf authority, err error) {
	root, err = selfSigned(rootSpec(name+"-root", clock))
	if err != nil {
		return authority{}, authority{}, err
	}
	leaf, err = root.timestampSigner(name+"-signer", clock, tsaUsageCriticalTimeStamping)
	return root, leaf, err
}

// timestampSigner issues a timestamp signer under a with the given usage.
func (a authority) timestampSigner(cn string, clock time.Time, usage tsaUsage) (authority, error) {
	spec, err := timestampSignerSpec(cn, clock, usage)
	if err != nil {
		return authority{}, err
	}
	return a.child(spec)
}

// otherNameSAN returns a subject-alternative-name extension whose only
// entry is a Fulcio username otherName, so the leaf carries no email or
// URI SAN. Fulcio marks the extension critical because the leaf subject is
// empty (RFC 5280 §4.2.1.6), and critical is false only for a test that
// needs the leaf to pass the chain check.
func otherNameSAN(username string, critical bool) (pkix.Extension, error) {
	var w derWriter
	oid := w.marshal(oidFulcioUsername, "")
	if w.err != nil {
		return pkix.Extension{}, w.err
	}
	// otherName is [0] IMPLICIT OtherName; its value is [0] EXPLICIT.
	name := tlv(tagContext0, append(oid, tlv(tagContext0, utf8String(username))...))
	return pkix.Extension{Id: oidSubjectAltName, Critical: critical, Value: seq(name)}, nil
}
