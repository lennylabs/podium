package sigstoreharness

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"fmt"
	"time"
)

// TSA names one timestamp authority of a Harness. TSAPrimary is built by
// New; the others are built on first use, so a test that names none of
// them pays for no extra key generation.
type TSA int

const (
	// TSAPrimary is the ECDSA P-256 authority at https://tsa.acme.test
	// that signs the §4.7.9 keyless bundle timestamps.
	TSAPrimary TSA = iota
	// TSASecond is a second ECDSA P-256 authority at
	// https://tsa2.acme.test.
	TSASecond
	// TSASameURI is an authority with its own keys that the trusted root
	// lists under the primary's URI, so a verifier counts the two entries
	// as one authority.
	TSASameURI
	// TSARSA is an RSA authority whose tokens carry an RSA signature with
	// SHA-384, at https://tsa-rsa.acme.test. Its chain carries SHA-256
	// signatures.
	TSARSA
	// TSAExpired is an authority that was rotated out: its certificates
	// and its trusted-root window end a year before the harness clock.
	TSAExpired
	// TSASHA1 is an RSA authority whose root certificate carries a SHA-1
	// signature, at https://tsa-sha1.acme.test.
	TSASHA1
)

// tsaAuthority is one timestamp authority: its root, its signer, the URI
// the trusted root lists it under, and its trusted-root window.
type tsaAuthority struct {
	name   string
	uri    string
	root   authority
	signer authority
	window Window
	// hash is the digest the authority signs its tokens with.
	hash crypto.Hash
	// sigAlg is the SignerInfo signature algorithm of its tokens.
	sigAlg asn1.ObjectIdentifier
}

// timestampAuthority returns the authority name, building it on first use.
func (h *Harness) timestampAuthority(name TSA) (tsaAuthority, error) {
	if a, ok := h.tsas[name]; ok {
		return a, nil
	}
	a, err := h.newTimestampAuthority(name)
	if err != nil {
		return tsaAuthority{}, err
	}
	if h.tsas == nil {
		h.tsas = map[TSA]tsaAuthority{}
	}
	h.tsas[name] = a
	return a, nil
}

// newTimestampAuthority builds the keys and certificates of name.
func (h *Harness) newTimestampAuthority(name TSA) (tsaAuthority, error) {
	start := h.clock.Add(-time.Hour)
	open := Window{Start: &start}
	switch name {
	case TSAPrimary:
		return tsaAuthority{
			name: "acme-tsa", uri: "https://tsa.acme.test", root: h.tsaRoot, signer: h.tsaLeaf,
			window: open, hash: crypto.SHA256, sigAlg: oidECDSAWithSHA256,
		}, nil
	case TSASecond, TSASameURI:
		cn, uri := "acme-tsa2", "https://tsa2.acme.test"
		if name == TSASameURI {
			cn, uri = "acme-tsa-same-uri", "https://tsa.acme.test"
		}
		root, signer, err := newTimestampAuthority(cn, h.clock)
		if err != nil {
			return tsaAuthority{}, err
		}
		return tsaAuthority{
			name: cn, uri: uri, root: root, signer: signer,
			window: open, hash: crypto.SHA256, sigAlg: oidECDSAWithSHA256,
		}, nil
	case TSARSA:
		root, signer, err := newRSATimestampAuthority("acme-tsa-rsa", h.clock)
		if err != nil {
			return tsaAuthority{}, err
		}
		return tsaAuthority{
			name: "acme-tsa-rsa", uri: "https://tsa-rsa.acme.test", root: root, signer: signer,
			window: open, hash: crypto.SHA384, sigAlg: oidSHA384WithRSA,
		}, nil
	case TSAExpired:
		// The authority's life ended a year before the clock: its
		// certificates and its window cover the two years before that.
		then := h.clock.Add(-3 * 365 * 24 * time.Hour)
		end := h.clock.Add(-365 * 24 * time.Hour)
		root, signer, err := newExpiredTimestampAuthority("acme-tsa-retired", then, end)
		if err != nil {
			return tsaAuthority{}, err
		}
		return tsaAuthority{
			name: "acme-tsa-retired", uri: "https://tsa-retired.acme.test", root: root, signer: signer,
			window: Window{Start: &then, End: &end}, hash: crypto.SHA256, sigAlg: oidECDSAWithSHA256,
		}, nil
	case TSASHA1:
		root, signer, err := newRSATimestampAuthority("acme-tsa-sha1", h.clock)
		if err != nil {
			return tsaAuthority{}, err
		}
		if root.cert, err = resignSHA1(root); err != nil {
			return tsaAuthority{}, err
		}
		return tsaAuthority{
			name: "acme-tsa-sha1", uri: "https://tsa-sha1.acme.test", root: root, signer: signer,
			window: open, hash: crypto.SHA256, sigAlg: oidSHA256WithRSA,
		}, nil
	default:
		return tsaAuthority{}, fmt.Errorf("unknown timestamp authority %d", name)
	}
}

// PKCS #1 signature identifiers of the RSA authorities.
var (
	oidSHA1WithRSA   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 5}
	oidSHA256WithRSA = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 11}
	oidSHA384WithRSA = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 12}
)

// resignSHA1 returns a's self-signed certificate signed again with SHA-1
// and RSA. crypto/x509 creates no SHA-1 certificate, so the identifier in
// the TBS is rewritten to sha1WithRSAEncryption, whose encoding has the
// length of sha256WithRSAEncryption, and the TBS is signed by hand.
func resignSHA1(a authority) (*x509.Certificate, error) {
	var cert rawCertificate
	if _, err := asn1.Unmarshal(a.cert.Raw, &cert); err != nil {
		return nil, err
	}
	from, err := asn1.Marshal(oidSHA256WithRSA)
	if err != nil {
		return nil, err
	}
	to, err := asn1.Marshal(oidSHA1WithRSA)
	if err != nil {
		return nil, err
	}
	tbs := bytes.Replace(cert.TBS.FullBytes, from, to, 1)
	digest := sha1.Sum(tbs)
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.key.(*rsa.PrivateKey), crypto.SHA1, digest[:])
	if err != nil {
		return nil, err
	}
	var w derWriter
	alg := w.marshal(pkix.AlgorithmIdentifier{Algorithm: oidSHA1WithRSA, Parameters: asn1.NullRawValue}, "")
	bits := w.marshal(asn1.BitString{Bytes: sig, BitLength: len(sig) * 8}, "")
	if w.err != nil {
		return nil, w.err
	}
	return x509.ParseCertificate(seq(tbs, alg, bits))
}

// newRSATimestampAuthority builds an RSA root and an RSA signer with the
// critical time-stamping usage. crypto/x509 signs both certificates with
// SHA-256, so the chain carries no SHA-1 signature.
func newRSATimestampAuthority(name string, clock time.Time) (root, signer authority, err error) {
	rootKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return authority{}, authority{}, err
	}
	root, err = selfSignedWith(rootSpec(name+"-root", clock), rootKey)
	if err != nil {
		return authority{}, authority{}, err
	}
	signerKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return authority{}, authority{}, err
	}
	spec, err := timestampSignerSpec(name+"-signer", clock, tsaUsageCriticalTimeStamping)
	if err != nil {
		return authority{}, authority{}, err
	}
	cert, err := root.issue(spec, signerKey.Public())
	if err != nil {
		return authority{}, authority{}, err
	}
	return root, authority{cert: cert, key: signerKey}, nil
}

// newExpiredTimestampAuthority builds an ECDSA authority whose root and
// signer are valid from start to end.
func newExpiredTimestampAuthority(name string, start, end time.Time) (root, signer authority, err error) {
	spec := rootSpec(name+"-root", start)
	spec.notBefore, spec.notAfter = start, end
	root, err = selfSigned(spec)
	if err != nil {
		return authority{}, authority{}, err
	}
	signerSpec, err := timestampSignerSpec(name+"-signer", start, tsaUsageCriticalTimeStamping)
	if err != nil {
		return authority{}, authority{}, err
	}
	signerSpec.notBefore, signerSpec.notAfter = start, end
	signer, err = root.child(signerSpec)
	return root, signer, err
}

// selfSignedWith creates a self-signed authority over key.
func selfSignedWith(spec certSpec, key crypto.Signer) (authority, error) {
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

// entry writes the trusted-root entry of a: its signer, then its root.
func (a tsaAuthority) entry() authorityDoc {
	return authorityEntry(a.name, a.uri, a.window, a.signer.cert, a.root.cert)
}
