package sign

import (
	"bytes"
	"context"
	"crypto"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"time"

	// Register the SHA-384 and SHA-512 implementations crypto.Hash.New
	// uses for a token whose signer digests with them.
	_ "crypto/sha512"
)

// Object identifiers of the CMS and RFC 3161 structures a timestamp
// token uses.
var (
	oidSignedData        = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	oidTSTInfo           = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 1, 4}
	oidAttrContentType   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 3}
	oidAttrMessageDigest = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 4}
	oidSHA256            = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidSHA384            = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 2}
	oidSHA512            = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 3}
	oidECPublicKey       = asn1.ObjectIdentifier{1, 2, 840, 10045, 2, 1}
	oidECDSAWithSHA256   = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 2}
	oidECDSAWithSHA384   = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 3}
	oidECDSAWithSHA512   = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 4}
	oidRSAEncryption     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
	oidSHA256WithRSA     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 11}
	oidSHA384WithRSA     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 12}
	oidSHA512WithRSA     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 13}
)

// messageImprint is the RFC 3161 MessageImprint.
type messageImprint struct {
	HashAlgorithm pkix.AlgorithmIdentifier
	HashedMessage []byte
}

// timeStampReq is the RFC 3161 §2.4.1 TimeStampReq. It carries no nonce,
// because nothing at verification time could check one; the imprint
// already binds the token to a fresh signature.
type timeStampReq struct {
	Version        int
	MessageImprint messageImprint
	CertReq        bool `asn1:"optional"`
}

// timeStampResp is the RFC 3161 §2.4.2 TimeStampResp. encoding/asn1
// accepts trailing members of a SEQUENCE, so PKIStatusInfo declares only
// the status.
type timeStampResp struct {
	Status struct {
		Status int
	}
	TimeStampToken asn1.RawValue `asn1:"optional"`
}

// requestTimestamp obtains an RFC 3161 token over SHA-256(sig) from
// TSAURL and returns its DER.
//
// Spec: §4.7.9.
func (s SigstoreKeyless) requestTimestamp(ctx context.Context, sig []byte) ([]byte, error) {
	imprint := sha256.Sum256(sig)
	der, err := asn1.Marshal(timeStampReq{
		Version: 1,
		MessageImprint: messageImprint{
			HashAlgorithm: pkix.AlgorithmIdentifier{Algorithm: oidSHA256, Parameters: asn1.NullRawValue},
			HashedMessage: imprint[:],
		},
		CertReq: true,
	})
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.TSAURL, bytes.NewReader(der))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/timestamp-query")
	req.Header.Set("Accept", "application/timestamp-reply")
	body, err := s.post(req)
	if err != nil {
		return nil, err
	}
	var resp timeStampResp
	if _, err := asn1.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if st := resp.Status.Status; st != 0 && st != 1 {
		return nil, fmt.Errorf("status %d", st)
	}
	if len(resp.TimeStampToken.FullBytes) == 0 {
		return nil, errors.New("response carries no token")
	}
	return resp.TimeStampToken.FullBytes, nil
}

// CMS structures (RFC 5652) a TimeStampToken carries.
type contentInfo struct {
	ContentType asn1.ObjectIdentifier
	Content     asn1.RawValue `asn1:"explicit,tag:0"`
}

type signedData struct {
	Version          int
	DigestAlgorithms asn1.RawValue
	EncapContentInfo struct {
		EContentType asn1.ObjectIdentifier
		EContent     []byte `asn1:"explicit,tag:0"`
	}
	Certificates asn1.RawValue   `asn1:"optional,tag:0"`
	CRLs         asn1.RawValue   `asn1:"optional,tag:1"`
	SignerInfos  []cmsSignerInfo `asn1:"set"`
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

type issuerAndSerial struct {
	Issuer asn1.RawValue
	Serial *big.Int
}

// tstInfo declares the TSTInfo members from version through genTime; the
// later optional members are not read. genTime is decoded by hand
// because encoding/asn1 refuses the fractional seconds RFC 3161 allows.
type tstInfo struct {
	Version        int
	Policy         asn1.ObjectIdentifier
	MessageImprint messageImprint
	SerialNumber   *big.Int
	GenTime        asn1.RawValue
}

// parsedToken is a decoded TimeStampToken with its single signer.
type parsedToken struct {
	eContent []byte
	embedded []*x509.Certificate
	signer   cmsSignerInfo
}

// verifyTimestamp verifies an RFC 3161 token over sig under the
// trusted root's timestamp authorities and returns its genTime, the
// attested time. The ESS signing-certificate attribute is not read: the
// chain check below already binds the signer key to a trusted authority.
//
// Spec: §4.7.9.
func verifyTimestamp(root trustedRoot, tokenDER, sig []byte) (time.Time, error) {
	tok, err := parseToken(tokenDER)
	if err != nil {
		return time.Time{}, err
	}
	signerCert, err := tok.verifySignature(root)
	if err != nil {
		return time.Time{}, err
	}
	var info tstInfo
	if _, err := asn1.Unmarshal(tok.eContent, &info); err != nil {
		return time.Time{}, fmt.Errorf("timestamp does not parse: %w", err)
	}
	genTime, err := parseGeneralizedTime(info.GenTime)
	if err != nil {
		return time.Time{}, fmt.Errorf("timestamp does not parse: %w", err)
	}
	want := sha256.Sum256(sig)
	if !info.MessageImprint.HashAlgorithm.Algorithm.Equal(oidSHA256) || !bytes.Equal(info.MessageImprint.HashedMessage, want[:]) {
		return time.Time{}, errors.New("timestamp does not cover the signature")
	}
	if err := verifyTimestampSigner(root, signerCert, tok.embedded, genTime); err != nil {
		return time.Time{}, fmt.Errorf("timestamp authority not trusted at %s: %w", stamp(genTime), err)
	}
	return genTime, nil
}

// parseToken decodes the ContentInfo, the SignedData, and the embedded
// certificates, and requires exactly one signer.
func parseToken(der []byte) (parsedToken, error) {
	var ci contentInfo
	if rest, err := asn1.Unmarshal(der, &ci); err != nil || len(rest) > 0 {
		return parsedToken{}, fmt.Errorf("timestamp does not parse: content info: %v", errOrTrailing(err))
	}
	if !ci.ContentType.Equal(oidSignedData) {
		return parsedToken{}, fmt.Errorf("timestamp does not parse: content type %v is not signed data", ci.ContentType)
	}
	var sd signedData
	if _, err := asn1.Unmarshal(ci.Content.Bytes, &sd); err != nil {
		return parsedToken{}, fmt.Errorf("timestamp does not parse: signed data: %w", err)
	}
	if !sd.EncapContentInfo.EContentType.Equal(oidTSTInfo) {
		return parsedToken{}, fmt.Errorf("timestamp does not parse: content type %v is not TSTInfo", sd.EncapContentInfo.EContentType)
	}
	if n := len(sd.SignerInfos); n != 1 {
		return parsedToken{}, fmt.Errorf("timestamp carries %d signers; want 1", n)
	}
	tok := parsedToken{eContent: sd.EncapContentInfo.EContent, signer: sd.SignerInfos[0]}
	if len(sd.Certificates.Bytes) > 0 {
		certs, err := x509.ParseCertificates(sd.Certificates.Bytes)
		if err != nil {
			return parsedToken{}, fmt.Errorf("timestamp does not parse: certificates: %w", err)
		}
		tok.embedded = certs
	}
	return tok, nil
}

// errOrTrailing names trailing bytes when the decode itself succeeded.
func errOrTrailing(err error) error {
	if err != nil {
		return err
	}
	return errors.New("trailing bytes")
}

// verifySignature checks the signed attributes and the signer's signature
// over them, and returns the signer certificate.
func (tok parsedToken) verifySignature(root trustedRoot) (*x509.Certificate, error) {
	si := tok.signer
	if len(si.SignedAttrs.FullBytes) == 0 {
		return nil, errors.New("timestamp does not parse: signer carries no signed attributes")
	}
	hash, ok := digestHash(si.DigestAlgorithm.Algorithm)
	if !ok {
		return nil, fmt.Errorf("timestamp signature does not verify: unsupported digest algorithm %v", si.DigestAlgorithm.Algorithm)
	}
	// CMS (RFC 5652 §5.4) signs the DER of the signed attributes with
	// their [0] IMPLICIT tag replaced by the SET tag, so hashing the bytes
	// as they appear in the token would fail every genuine timestamp.
	signedSET := append([]byte{0x31}, si.SignedAttrs.FullBytes[1:]...)
	if err := checkSignedAttrs(signedSET, hash, tok.eContent); err != nil {
		return nil, err
	}
	cert := findSigner(si.SID, append(slices.Clone(tok.embedded), root.timestampCerts()...))
	if cert == nil {
		return nil, errors.New("timestamp signature does not verify: signer certificate not found")
	}
	alg, ok := signatureAlgorithm(si.SignatureAlgorithm.Algorithm, hash)
	if !ok {
		return nil, fmt.Errorf("timestamp signature does not verify: unsupported signature algorithm %v", si.SignatureAlgorithm.Algorithm)
	}
	if err := cert.CheckSignature(alg, signedSET, si.Signature); err != nil {
		return nil, errors.New("timestamp signature does not verify")
	}
	return cert, nil
}

// checkSignedAttrs requires a contentType attribute naming TSTInfo and a
// messageDigest attribute equal to hash(eContent).
func checkSignedAttrs(signedSET []byte, hash crypto.Hash, eContent []byte) error {
	var attrs []cmsAttribute
	if _, err := asn1.UnmarshalWithParams(signedSET, &attrs, "set"); err != nil {
		return fmt.Errorf("timestamp does not parse: signed attributes: %w", err)
	}
	var contentType asn1.ObjectIdentifier
	var digest []byte
	for _, a := range attrs {
		switch {
		case a.Type.Equal(oidAttrContentType):
			if _, err := asn1.Unmarshal(a.Values.Bytes, &contentType); err != nil {
				return fmt.Errorf("timestamp does not parse: content-type attribute: %w", err)
			}
		case a.Type.Equal(oidAttrMessageDigest):
			if _, err := asn1.Unmarshal(a.Values.Bytes, &digest); err != nil {
				return fmt.Errorf("timestamp does not parse: message-digest attribute: %w", err)
			}
		}
	}
	if !contentType.Equal(oidTSTInfo) {
		return errors.New("timestamp signature does not verify: content-type attribute does not name TSTInfo")
	}
	h := hash.New()
	h.Write(eContent)
	if !bytes.Equal(digest, h.Sum(nil)) {
		return errors.New("timestamp signature does not verify: message-digest attribute does not match the content")
	}
	return nil
}

// findSigner selects the certificate a SignerIdentifier names: an
// IssuerAndSerialNumber SEQUENCE, or a [0] SubjectKeyIdentifier.
func findSigner(sid asn1.RawValue, candidates []*x509.Certificate) *x509.Certificate {
	var ias issuerAndSerial
	bySerial := sid.Class == asn1.ClassUniversal && sid.Tag == asn1.TagSequence
	if bySerial {
		if _, err := asn1.Unmarshal(sid.FullBytes, &ias); err != nil || ias.Serial == nil {
			return nil
		}
	} else if sid.Class != asn1.ClassContextSpecific || sid.Tag != 0 {
		return nil
	}
	for _, c := range candidates {
		if bySerial && bytes.Equal(c.RawIssuer, ias.Issuer.FullBytes) && c.SerialNumber.Cmp(ias.Serial) == 0 {
			return c
		}
		if !bySerial && len(c.SubjectKeyId) > 0 && bytes.Equal(c.SubjectKeyId, sid.Bytes) {
			return c
		}
	}
	return nil
}

// timestampCerts returns every certificate of every timestamp-authority
// chain, as signer candidates for a token that embeds none.
func (r trustedRoot) timestampCerts() []*x509.Certificate {
	var out []*x509.Certificate
	for _, a := range r.timestampAuthorities {
		out = append(out, a.intermediates...)
		out = append(out, a.anchor)
	}
	return out
}

// digestHash maps a SignerInfo digest algorithm to its hash.
func digestHash(oid asn1.ObjectIdentifier) (crypto.Hash, bool) {
	switch {
	case oid.Equal(oidSHA256):
		return crypto.SHA256, true
	case oid.Equal(oidSHA384):
		return crypto.SHA384, true
	case oid.Equal(oidSHA512):
		return crypto.SHA512, true
	default:
		return 0, false
	}
}

// signatureAlgorithms maps the SignerInfo signature algorithms that name
// their hash.
var signatureAlgorithms = []struct {
	oid asn1.ObjectIdentifier
	alg x509.SignatureAlgorithm
}{
	{oidECDSAWithSHA256, x509.ECDSAWithSHA256},
	{oidECDSAWithSHA384, x509.ECDSAWithSHA384},
	{oidECDSAWithSHA512, x509.ECDSAWithSHA512},
	{oidSHA256WithRSA, x509.SHA256WithRSA},
	{oidSHA384WithRSA, x509.SHA384WithRSA},
	{oidSHA512WithRSA, x509.SHA512WithRSA},
}

// keyAlgorithms maps a bare key algorithm (id-ecPublicKey, rsaEncryption)
// combined with the SignerInfo digest algorithm.
var keyAlgorithms = map[string]map[crypto.Hash]x509.SignatureAlgorithm{
	oidECPublicKey.String(): {
		crypto.SHA256: x509.ECDSAWithSHA256, crypto.SHA384: x509.ECDSAWithSHA384, crypto.SHA512: x509.ECDSAWithSHA512,
	},
	oidRSAEncryption.String(): {
		crypto.SHA256: x509.SHA256WithRSA, crypto.SHA384: x509.SHA384WithRSA, crypto.SHA512: x509.SHA512WithRSA,
	},
}

// signatureAlgorithm maps a SignerInfo signature algorithm, and for a
// bare key algorithm the digest hash, to the x509 algorithm.
func signatureAlgorithm(oid asn1.ObjectIdentifier, hash crypto.Hash) (x509.SignatureAlgorithm, bool) {
	for _, a := range signatureAlgorithms {
		if oid.Equal(a.oid) {
			return a.alg, true
		}
	}
	alg, ok := keyAlgorithms[oid.String()][hash]
	return alg, ok
}

// parseGeneralizedTime decodes an RFC 3161 genTime: a GeneralizedTime in
// UTC, with optional fractional seconds.
func parseGeneralizedTime(raw asn1.RawValue) (time.Time, error) {
	if raw.Class != asn1.ClassUniversal || raw.Tag != asn1.TagGeneralizedTime {
		return time.Time{}, errors.New("genTime is not a GeneralizedTime")
	}
	t, err := time.Parse("20060102150405Z", string(raw.Bytes))
	if err != nil {
		return time.Time{}, fmt.Errorf("genTime: %w", err)
	}
	return t, nil
}

// verifyTimestampSigner chains the signer to a timestamp authority valid
// at genTime. The pools come from timestampAuthorities alone, so a
// certificate-authority root never anchors a timestamp signer.
func verifyTimestampSigner(root trustedRoot, signer *x509.Certificate, embedded []*x509.Certificate, genTime time.Time) error {
	roots, intermediates, ok := authorityPools(root.timestampAuthorities, genTime, embedded)
	if !ok {
		return errors.New("no timestamp authority valid at that time")
	}
	if _, err := signer.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   genTime,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping},
	}); err != nil {
		return err
	}
	// KeyUsages alone does not enforce the usage: crypto/x509 skips the
	// extended-key-usage check for a certificate with no such extension
	// and passes one that lists ExtKeyUsageAny (checkChainForKeyUsage in
	// crypto/x509/verify.go). The signer's own usage is therefore checked
	// here. Whether the extension is critical is not checked.
	if !slices.Contains(signer.ExtKeyUsage, x509.ExtKeyUsageTimeStamping) {
		return errors.New("signer certificate lacks the time-stamping usage")
	}
	return nil
}
