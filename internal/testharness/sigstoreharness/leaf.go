package sigstoreharness

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"fmt"
	"net/url"
	"time"
)

var (
	// OIDIssuerV2 is the Fulcio OIDC-issuer extension, a DER UTF8String.
	OIDIssuerV2 = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 8}
	// OIDIssuerV1 is the deprecated Fulcio OIDC-issuer extension, raw bytes.
	OIDIssuerV1 = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 1}
)

// Key-details names the hashedrekord v0.0.2 verifier carries.
const (
	keyDetailsP256    = "PKIX_ECDSA_P256_SHA_256"
	keyDetailsEd25519 = "PKIX_ED25519"
)

// leafConfig describes the identity and usages of a Fulcio-style leaf.
type leafConfig struct {
	email         string
	uris          []string
	otherNameOnly bool
	issuerExts    []pkix.Extension
	eku           []x509.ExtKeyUsage
}

// defaultLeafConfig is the leaf Fulcio issues for alice@acme.com.
func defaultLeafConfig() leafConfig {
	return leafConfig{
		email:      DefaultSAN,
		issuerExts: []pkix.Extension{{Id: OIDIssuerV2, Value: IssuerValue(DefaultIssuer)}},
		eku:        []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
	}
}

// setIssuerExt replaces the extension with oid, or appends it.
func (c *leafConfig) setIssuerExt(oid asn1.ObjectIdentifier, raw []byte) {
	for i, e := range c.issuerExts {
		if e.Id.Equal(oid) {
			c.issuerExts[i].Value = raw
			return
		}
	}
	c.issuerExts = append(c.issuerExts, pkix.Extension{Id: oid, Value: raw})
}

// spec turns the leaf configuration into a certificate spec valid from
// the clock minus 5 minutes to the clock plus 15 minutes.
func (c leafConfig) spec(clock time.Time) (certSpec, error) {
	spec := certSpec{
		notBefore: clock.Add(-5 * time.Minute),
		notAfter:  clock.Add(15 * time.Minute),
		keyUsage:  x509.KeyUsageDigitalSignature,
		eku:       c.eku,
		extra:     append([]pkix.Extension(nil), c.issuerExts...),
	}
	if c.otherNameOnly {
		san, err := otherNameSAN(DefaultSAN)
		if err != nil {
			return certSpec{}, err
		}
		spec.extra = append(spec.extra, san)
		return spec, nil
	}
	if c.email != "" {
		spec.emails = []string{c.email}
	}
	for _, raw := range c.uris {
		u, err := url.Parse(raw)
		if err != nil {
			return certSpec{}, fmt.Errorf("uri SAN %q: %w", raw, err)
		}
		spec.uris = append(spec.uris, u)
	}
	return spec, nil
}

// signedLeaf is a leaf certificate, the chain the envelope carries after
// it, and the signature the leaf key made over the digest.
type signedLeaf struct {
	cert          *x509.Certificate
	intermediates []*x509.Certificate
	sig           []byte
	keyDetails    string
}

// issueSignedLeaf issues a leaf under issuer over a fresh key and signs
// msg with that key: Ed25519 when ed is set, ECDSA P-256 otherwise.
func (h *Harness) issueSignedLeaf(cfg leafConfig, issuer authority, ed bool, msg []byte) (signedLeaf, error) {
	spec, err := cfg.spec(h.clock)
	if err != nil {
		return signedLeaf{}, err
	}
	var (
		pub        crypto.PublicKey
		sign       func() ([]byte, error)
		keyDetails = keyDetailsP256
	)
	if ed {
		edPub, edPriv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return signedLeaf{}, err
		}
		pub, keyDetails = edPub, keyDetailsEd25519
		sign = func() ([]byte, error) { return ed25519.Sign(edPriv, msg), nil }
	} else {
		key, err := newECKey()
		if err != nil {
			return signedLeaf{}, err
		}
		pub = key.Public()
		sign = func() ([]byte, error) { return ecdsa.SignASN1(rand.Reader, key, msg) }
	}
	cert, err := issuer.issue(spec, pub)
	if err != nil {
		return signedLeaf{}, err
	}
	sig, err := sign()
	if err != nil {
		return signedLeaf{}, fmt.Errorf("sign digest: %w", err)
	}
	return signedLeaf{cert: cert, sig: sig, keyDetails: keyDetails}, nil
}
