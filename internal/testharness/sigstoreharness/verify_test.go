package sigstoreharness

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The helpers in this file re-derive what the harness wrote from the
// RFCs rather than from the harness code, so a test that passes shows
// the generated material is well-formed for an independent verifier.

// rootFromProof recomputes a tree root by the RFC 9162 §2.1.3.2
// algorithm. ok is false when the proof does not fit the tree.
func rootFromProof(index, size int64, leaf []byte, proof [][]byte) ([]byte, bool) {
	if index < 0 || index >= size {
		return nil, false
	}
	fn, sn, r := index, size-1, leaf
	for _, p := range proof {
		if sn == 0 {
			return nil, false
		}
		if fn&1 == 1 || fn == sn {
			r = nodeHash(p, r)
			for fn&1 == 0 && fn != 0 {
				fn >>= 1
				sn >>= 1
			}
		} else {
			r = nodeHash(r, p)
		}
		fn >>= 1
		sn >>= 1
	}
	return r, sn == 0
}

// note is a parsed signed-note checkpoint.
type note struct {
	body string
	size int64
	root []byte
	sigs [][]byte // signature bytes after the 4-byte key hint
}

// parseNote parses checkpoint text. ok is false when it is malformed.
func parseNote(text string) (note, bool) {
	body, sigText, found := strings.Cut(text, "\n\n")
	if !found {
		return note{}, false
	}
	lines := strings.Split(body, "\n")
	if len(lines) < 3 {
		return note{}, false
	}
	size, err := strconv.ParseInt(lines[1], 10, 64)
	if err != nil {
		return note{}, false
	}
	root, err := base64.StdEncoding.DecodeString(lines[2])
	if err != nil || len(root) != sha256.Size {
		return note{}, false
	}
	n := note{body: body + "\n", size: size, root: root}
	for _, line := range strings.Split(strings.TrimSuffix(sigText, "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[0] != "—" {
			return note{}, false
		}
		raw, err := base64.StdEncoding.DecodeString(fields[2])
		if err != nil || len(raw) <= 4 {
			return note{}, false
		}
		n.sigs = append(n.sigs, raw[4:])
	}
	return n, len(n.sigs) > 0
}

// verifiesUnder reports whether a signature line verifies under pub.
func (n note) verifiesUnder(pub crypto.PublicKey) bool {
	for _, sig := range n.sigs {
		switch k := pub.(type) {
		case *ecdsa.PublicKey:
			digest := sha256.Sum256([]byte(n.body))
			if ecdsa.VerifyASN1(k, digest[:], sig) {
				return true
			}
		case ed25519.PublicKey:
			if ed25519.Verify(k, []byte(n.body), sig) {
				return true
			}
		}
	}
	return false
}

// CMS and RFC 3161 structures, decoded with encoding/asn1.
type cmsContentInfo struct {
	ContentType asn1.ObjectIdentifier
	Content     asn1.RawValue `asn1:"explicit,tag:0"`
}

type cmsSignedData struct {
	Version          int
	DigestAlgorithms asn1.RawValue
	Encap            cmsEncap
	Certificates     asn1.RawValue   `asn1:"optional,tag:0"`
	SignerInfos      []cmsSignerInfo `asn1:"set"`
}

type cmsEncap struct {
	EContentType asn1.ObjectIdentifier
	EContent     []byte `asn1:"explicit,tag:0"`
}

type cmsSignerInfo struct {
	Version            int
	SID                asn1.RawValue
	DigestAlgorithm    pkix.AlgorithmIdentifier
	SignedAttrs        asn1.RawValue `asn1:"optional,tag:0"`
	SignatureAlgorithm pkix.AlgorithmIdentifier
	Signature          []byte
}

type cmsAttribute struct {
	Type   asn1.ObjectIdentifier
	Values asn1.RawValue
}

type tstInfoMsg struct {
	Version        int
	Policy         asn1.ObjectIdentifier
	MessageImprint struct {
		HashAlgorithm pkix.AlgorithmIdentifier
		HashedMessage []byte
	}
	SerialNumber *big.Int
	GenTime      time.Time `asn1:"generalized"`
}

// parsedToken is the decoded content of a TimeStampToken.
type parsedToken struct {
	eContentType asn1.ObjectIdentifier
	eContent     []byte
	embedded     []*x509.Certificate
	signers      []cmsSignerInfo
	signedSET    []byte // signed attributes re-tagged as a SET
	attrs        map[string][]byte
	tst          tstInfoMsg
}

// parseToken decodes a DER TimeStampToken and fails the test when it
// does not parse.
func parseToken(t *testing.T, der []byte) parsedToken {
	t.Helper()
	var ci cmsContentInfo
	if _, err := asn1.Unmarshal(der, &ci); err != nil {
		t.Fatalf("content info: %v", err)
	}
	if !ci.ContentType.Equal(oidSignedData) {
		t.Fatalf("content type = %v", ci.ContentType)
	}
	var sd cmsSignedData
	if _, err := asn1.Unmarshal(ci.Content.Bytes, &sd); err != nil {
		t.Fatalf("signed data: %v", err)
	}
	out := parsedToken{eContentType: sd.Encap.EContentType, eContent: sd.Encap.EContent, signers: sd.SignerInfos}
	if len(sd.Certificates.Bytes) > 0 {
		certs, err := x509.ParseCertificates(sd.Certificates.Bytes)
		if err != nil {
			t.Fatalf("embedded certificates: %v", err)
		}
		out.embedded = certs
	}
	if _, err := asn1.Unmarshal(sd.Encap.EContent, &out.tst); err != nil {
		t.Fatalf("tst info: %v", err)
	}
	if len(sd.SignerInfos) > 0 && len(sd.SignerInfos[0].SignedAttrs.FullBytes) > 0 {
		out.signedSET = append([]byte{tagSet}, sd.SignerInfos[0].SignedAttrs.FullBytes[1:]...)
		out.attrs = parseAttrs(t, out.signedSET)
	}
	return out
}

// parseAttrs maps each signed attribute's OID to its single value's DER.
func parseAttrs(t *testing.T, set []byte) map[string][]byte {
	t.Helper()
	var attrs []cmsAttribute
	if _, err := asn1.UnmarshalWithParams(set, &attrs, "set"); err != nil {
		t.Fatalf("signed attributes: %v", err)
	}
	out := map[string][]byte{}
	for _, a := range attrs {
		out[a.Type.String()] = a.Values.Bytes
	}
	return out
}

// messageDigest returns the messageDigest attribute value.
func (p parsedToken) messageDigest(t *testing.T) []byte {
	t.Helper()
	var md []byte
	if _, err := asn1.Unmarshal(p.attrs[oidAttrMessageDigest.String()], &md); err != nil {
		t.Fatalf("message digest attribute: %v", err)
	}
	return md
}

// attrsVerifyUnder checks the first signer's signature over the re-tagged
// signed attributes with cert.
func (p parsedToken) attrsVerifyUnder(cert *x509.Certificate) error {
	return cert.CheckSignature(x509.ECDSAWithSHA256, p.signedSET, p.signers[0].Signature)
}

// inspection is a decoded envelope.
type inspection struct {
	env           envelopeJSON
	leaf          *x509.Certificate
	intermediates []*x509.Certificate
	sig           []byte
	body          hashedRekordEntry
	bodyRaw       []byte
	hashes        [][]byte
	token         *parsedToken
}

// inspect decodes an envelope and fails the test when any part does not
// decode.
func inspect(t *testing.T, raw string) inspection {
	t.Helper()
	var in inspection
	if err := json.Unmarshal([]byte(raw), &in.env); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	certs := decodePEMChain(t, in.env.Cert)
	in.leaf, in.intermediates = certs[0], certs[1:]
	in.sig = mustB64(t, in.env.Signature)
	if in.env.TLog != nil {
		in.bodyRaw = mustB64(t, in.env.TLog.Body)
		if err := json.Unmarshal(in.bodyRaw, &in.body); err != nil {
			t.Fatalf("entry body: %v", err)
		}
		for _, h := range in.env.TLog.Hashes {
			in.hashes = append(in.hashes, mustB64(t, h))
		}
	}
	if in.env.Timestamp != "" {
		tok := parseToken(t, mustB64(t, in.env.Timestamp))
		in.token = &tok
	}
	return in
}

// proofRoot recomputes the root the envelope's proof leads to.
func (in inspection) proofRoot(index, size int64) ([]byte, bool) {
	return rootFromProof(index, size, leafHash(in.bodyRaw), in.hashes)
}

func decodePEMChain(t *testing.T, s string) []*x509.Certificate {
	t.Helper()
	var out []*x509.Certificate
	rest := []byte(s)
	for {
		block, next := pem.Decode(rest)
		if block == nil {
			break
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatalf("chain certificate: %v", err)
		}
		out = append(out, c)
		rest = next
	}
	if len(out) == 0 {
		t.Fatal("envelope cert carries no certificate")
	}
	return out
}

func mustB64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("base64 %q: %v", s, err)
	}
	return b
}

// testContentHash returns the "sha256:hex" content hash of data and its
// digest bytes.
func testContentHash(data string) (string, []byte) {
	sum := sha256.Sum256([]byte(data))
	return "sha256:" + hex.EncodeToString(sum[:]), sum[:]
}

// pkixBytes marshals a public key, failing the test on error.
func pkixBytes(t *testing.T, pub crypto.PublicKey) []byte {
	t.Helper()
	raw, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return raw
}

// equalBytes fails the test when got differs from want.
func equalBytes(t *testing.T, what string, got, want []byte) {
	t.Helper()
	if !bytes.Equal(got, want) {
		t.Fatalf("%s = %x, want %x", what, got, want)
	}
}
