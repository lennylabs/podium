package sign

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"net/http"
)

// Object identifiers of the CMS and RFC 3161 structures a timestamp
// token uses, and of the signature algorithms the TypeScript-limit check
// of the §4.7.11 timestamp condition reads.
var (
	oidSignedData    = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	oidTSTInfo       = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 1, 4}
	oidSHA256        = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidRSAEncryption = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
	oidSHA256WithRSA = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 11}
	oidSHA384WithRSA = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 12}
	oidSHA512WithRSA = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 13}
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
// TSAURL and returns the DER TimeStampResp, which is what a bundle's
// signedTimestamp member holds.
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
	rest, err := asn1.Unmarshal(body, &resp)
	if err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if st := resp.Status.Status; st != 0 && st != 1 {
		return nil, fmt.Errorf("status %d", st)
	}
	if len(resp.TimeStampToken.FullBytes) == 0 {
		return nil, errors.New("response carries no token")
	}
	return body[:len(body)-len(rest)], nil
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

// parsedToken is a decoded TimeStampToken: the certificates it embeds and
// its single signer. The library verifies the token; the timestamp
// condition reads these members only.
type parsedToken struct {
	embedded []*x509.Certificate
	signer   cmsSignerInfo
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
	tok := parsedToken{signer: sd.SignerInfos[0]}
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
