package sigstoreharness

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"fmt"
	"math/big"
	"time"
)

var (
	oidSignedData        = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	oidTSTInfo           = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 1, 4}
	oidAttrContentType   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 3}
	oidAttrMessageDigest = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 4}
	oidSHA256            = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidECDSAWithSHA256   = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 2}
	oidTSAPolicy         = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 2, 1}
)

// TSOpt adjusts one RFC 3161 token built by TimestampToken.
type TSOpt func(*tsConfig)

// tsConfig describes one RFC 3161 TimeStampToken.
type tsConfig struct {
	genTime        time.Time
	imprint        []byte // SHA-256 of the timestamped bytes
	signer         authority
	embed          []*x509.Certificate
	contentType    asn1.ObjectIdentifier
	sigAlg         asn1.ObjectIdentifier
	twoSigners     bool
	noSignedAttrs  bool
	digestMismatch bool
	flipSignature  bool
	skid           bool
}

// WithTwoSigners writes the SignerInfo twice.
func WithTwoSigners() TSOpt { return func(c *tsConfig) { c.twoSigners = true } }

// WithoutSignedAttrs omits the signed attributes and signs SHA-256 of the
// TSTInfo directly.
func WithoutSignedAttrs() TSOpt { return func(c *tsConfig) { c.noSignedAttrs = true } }

// WithMessageDigestMismatch writes a messageDigest attribute that differs
// from SHA-256 of the TSTInfo. The signature still covers the attributes.
func WithMessageDigestMismatch() TSOpt { return func(c *tsConfig) { c.digestMismatch = true } }

// WithTSTContentType sets both the encapsulated eContentType and the
// signed contentType attribute to oid.
func WithTSTContentType(oid asn1.ObjectIdentifier) TSOpt {
	return func(c *tsConfig) { c.contentType = oid }
}

// WithSignatureAlgorithm writes oid as the SignerInfo signature algorithm.
// The signature itself stays ECDSA with SHA-256.
func WithSignatureAlgorithm(oid asn1.ObjectIdentifier) TSOpt {
	return func(c *tsConfig) { c.sigAlg = oid }
}

// WithoutEmbeddedCerts omits the SignedData certificates, so a verifier
// finds the signer in the trusted root's timestamp-authority chain.
func WithoutEmbeddedCerts() TSOpt { return func(c *tsConfig) { c.embed = nil } }

// WithSubjectKeyIdentifierSID identifies the signer by its subject key
// identifier (SignerInfo version 3) instead of issuer and serial number.
func WithSubjectKeyIdentifierSID() TSOpt { return func(c *tsConfig) { c.skid = true } }

// defaultTSConfig stamps imprint at the harness clock under the
// timestamp-authority signer, which it embeds.
func (h *Harness) defaultTSConfig(imprint []byte) tsConfig {
	return tsConfig{
		genTime:     h.clock,
		imprint:     imprint,
		signer:      h.tsaLeaf,
		embed:       []*x509.Certificate{h.tsaLeaf.cert},
		contentType: oidTSTInfo,
		sigAlg:      oidECDSAWithSHA256,
	}
}

// TimestampToken returns a DER RFC 3161 TimeStampToken (a CMS ContentInfo)
// over SHA-256(sig) at the harness clock, signed by the
// timestamp-authority signer.
//
// Spec: §4.7.9.
func (h *Harness) TimestampToken(sig []byte, opts ...TSOpt) []byte {
	h.tb.Helper()
	imprint := sha256.Sum256(sig)
	cfg := h.defaultTSConfig(imprint[:])
	for _, o := range opts {
		o(&cfg)
	}
	token, err := buildToken(cfg)
	must(h.tb, err)
	return token
}

// buildToken encodes the ContentInfo holding the SignedData over a TSTInfo.
func buildToken(c tsConfig) ([]byte, error) {
	var w derWriter
	tst := tstInfo(&w, c)
	eci := seq(w.marshal(c.contentType, ""), tlv(tagContext0, octets(tst)))
	si, err := signerInfo(&w, c, tst)
	if err != nil {
		return nil, err
	}
	infos := [][]byte{si}
	if c.twoSigners {
		infos = append(infos, si)
	}
	parts := [][]byte{w.marshal(3, ""), setOf(seq(w.marshal(oidSHA256, ""))), eci}
	if len(c.embed) > 0 {
		var raw []byte
		for _, cert := range c.embed {
			raw = append(raw, cert.Raw...)
		}
		parts = append(parts, tlv(tagContext0, raw))
	}
	parts = append(parts, setOf(infos...))
	token := seq(w.marshal(oidSignedData, ""), tlv(tagContext0, seq(parts...)))
	if w.err != nil {
		return nil, fmt.Errorf("timestamp token: %w", w.err)
	}
	return token, nil
}

// tstInfo encodes TSTInfo from version through genTime.
func tstInfo(w *derWriter, c tsConfig) []byte {
	imprint := seq(seq(w.marshal(oidSHA256, "")), octets(c.imprint))
	return seq(
		w.marshal(1, ""),
		w.marshal(oidTSAPolicy, ""),
		imprint,
		w.marshal(big.NewInt(c.genTime.UnixNano()), ""),
		w.marshal(c.genTime.UTC(), "generalized"),
	)
}

// signerInfo encodes the SignerInfo. CMS (RFC 5652 §5.4) signs the DER of
// the signed attributes with their [0] IMPLICIT tag replaced by the SET
// tag, so the signature covers the SET form while the token carries the
// [0] form.
func signerInfo(w *derWriter, c tsConfig, tst []byte) ([]byte, error) {
	md := sha256.Sum256(tst)
	if c.digestMismatch {
		md[0] ^= 0xff
	}
	attrs := setOf(
		seq(w.marshal(oidAttrContentType, ""), setOf(w.marshal(c.contentType, ""))),
		seq(w.marshal(oidAttrMessageDigest, ""), setOf(octets(md[:]))),
	)
	signed := attrs
	if c.noSignedAttrs {
		signed = tst
	}
	digest := sha256.Sum256(signed)
	sig, err := c.signer.key.Sign(rand.Reader, digest[:], crypto.SHA256)
	if err != nil {
		return nil, fmt.Errorf("sign timestamp: %w", err)
	}
	if c.flipSignature {
		sig[len(sig)-1] ^= 0x01
	}
	version, sid := 1, seq(c.signer.cert.RawIssuer, w.marshal(c.signer.cert.SerialNumber, ""))
	if c.skid {
		version, sid = 3, tlv(tagContext0Raw, c.signer.cert.SubjectKeyId)
	}
	parts := [][]byte{w.marshal(version, ""), sid, seq(w.marshal(oidSHA256, ""))}
	if !c.noSignedAttrs {
		parts = append(parts, append([]byte{tagContext0}, attrs[1:]...))
	}
	parts = append(parts, seq(w.marshal(c.sigAlg, "")), octets(sig))
	return seq(parts...), nil
}

// timeStampResp encodes a TimeStampResp with status and, when token is
// non-empty, the token.
func timeStampResp(status int, token []byte) ([]byte, error) {
	var w derWriter
	resp := seq(seq(w.marshal(status, "")), token)
	return resp, w.err
}

// timeStampReq is the RFC 3161 §2.4.1 request the fake authority decodes.
type timeStampReq struct {
	Version        int
	MessageImprint struct {
		HashAlgorithm struct {
			Algorithm  asn1.ObjectIdentifier
			Parameters asn1.RawValue `asn1:"optional"`
		}
		HashedMessage []byte
	}
	ReqPolicy asn1.ObjectIdentifier `asn1:"optional"`
	Nonce     *big.Int              `asn1:"optional"`
	CertReq   bool                  `asn1:"optional"`
}

// parseTimeStampReq returns the SHA-256 imprint of a DER TimeStampReq.
func parseTimeStampReq(der []byte) ([]byte, error) {
	var req timeStampReq
	rest, err := asn1.Unmarshal(der, &req)
	if err != nil {
		return nil, fmt.Errorf("timestamp request: %w", err)
	}
	if len(rest) > 0 {
		return nil, fmt.Errorf("timestamp request: %d trailing bytes", len(rest))
	}
	if !req.MessageImprint.HashAlgorithm.Algorithm.Equal(oidSHA256) || len(req.MessageImprint.HashedMessage) != sha256.Size {
		return nil, fmt.Errorf("timestamp request: imprint is not SHA-256")
	}
	return bytes.Clone(req.MessageImprint.HashedMessage), nil
}
