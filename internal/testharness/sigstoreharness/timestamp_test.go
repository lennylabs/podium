package sigstoreharness

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"testing"
)

// Spec: §4.7.9. Each TSOpt changes the one property of the token it
// names and keeps the token decodable.
func TestTimestampToken_Options(t *testing.T) {
	t.Parallel()
	h := New(t)
	sig := []byte("acme signature")
	otherOID := asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	ecPublicKey := asn1.ObjectIdentifier{1, 2, 840, 10045, 2, 1}
	cases := []struct {
		name  string
		opts  []TSOpt
		check func(t *testing.T, tok parsedToken)
	}{
		{"two signers", []TSOpt{WithTwoSigners()}, func(t *testing.T, tok parsedToken) {
			if len(tok.signers) != 2 {
				t.Fatalf("signers = %d", len(tok.signers))
			}
		}},
		{"without signed attributes", []TSOpt{WithoutSignedAttrs()}, func(t *testing.T, tok parsedToken) {
			if tok.signedSET != nil {
				t.Fatal("token carries signed attributes")
			}
			if err := h.TSALeaf().CheckSignature(x509.ECDSAWithSHA256, tok.eContent, tok.signers[0].Signature); err != nil {
				t.Fatalf("signature over TSTInfo: %v", err)
			}
		}},
		{"message digest mismatch", []TSOpt{WithMessageDigestMismatch()}, func(t *testing.T, tok parsedToken) {
			md := sha256.Sum256(tok.eContent)
			if bytes.Equal(tok.messageDigest(t), md[:]) {
				t.Fatal("messageDigest matches")
			}
			if err := tok.attrsVerifyUnder(h.TSALeaf()); err != nil {
				t.Fatalf("attributes do not verify: %v", err)
			}
		}},
		{"content type", []TSOpt{WithTSTContentType(otherOID)}, func(t *testing.T, tok parsedToken) {
			var attr asn1.ObjectIdentifier
			if _, err := asn1.Unmarshal(tok.attrs[oidAttrContentType.String()], &attr); err != nil {
				t.Fatal(err)
			}
			if !tok.eContentType.Equal(otherOID) || !attr.Equal(otherOID) {
				t.Fatalf("eContentType %v, attribute %v", tok.eContentType, attr)
			}
		}},
		{"signature algorithm", []TSOpt{WithSignatureAlgorithm(ecPublicKey)}, func(t *testing.T, tok parsedToken) {
			if !tok.signers[0].SignatureAlgorithm.Algorithm.Equal(ecPublicKey) {
				t.Fatalf("algorithm = %v", tok.signers[0].SignatureAlgorithm.Algorithm)
			}
			if err := tok.attrsVerifyUnder(h.TSALeaf()); err != nil {
				t.Fatalf("attributes do not verify: %v", err)
			}
		}},
		{"without embedded certs", []TSOpt{WithoutEmbeddedCerts()}, func(t *testing.T, tok parsedToken) {
			if len(tok.embedded) != 0 {
				t.Fatalf("embedded = %d", len(tok.embedded))
			}
		}},
		{"subject key identifier sid", []TSOpt{WithSubjectKeyIdentifierSID()}, func(t *testing.T, tok parsedToken) {
			si := tok.signers[0]
			if si.Version != 3 || si.SID.Class != asn1.ClassContextSpecific || si.SID.Tag != 0 {
				t.Fatalf("version %d sid class %d tag %d", si.Version, si.SID.Class, si.SID.Tag)
			}
			equalBytes(t, "sid", si.SID.Bytes, h.TSALeaf().SubjectKeyId)
		}},
		{"issuer and serial sid", nil, func(t *testing.T, tok parsedToken) {
			var ias struct {
				Issuer asn1.RawValue
				Serial asn1.RawValue
			}
			if _, err := asn1.Unmarshal(tok.signers[0].SID.FullBytes, &ias); err != nil {
				t.Fatal(err)
			}
			equalBytes(t, "sid issuer", ias.Issuer.FullBytes, h.TSALeaf().RawIssuer)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.check(t, parseToken(t, h.TimestampToken(sig, tc.opts...)))
		})
	}
}

// Spec: §4.7.9. The token builder reports an encoding failure, such as
// an object identifier encoding/asn1 cannot marshal.
func TestBuildToken_ReportsEncodingFailure(t *testing.T) {
	t.Parallel()
	h := New(t)
	cfg := h.defaultTSConfig(make([]byte, sha256.Size))
	cfg.contentType = asn1.ObjectIdentifier{7}
	if _, err := buildToken(cfg); err == nil {
		t.Fatal("invalid OID accepted")
	}
}

// Spec: §4.7.9. The fake authority decodes a SHA-256 TimeStampReq and
// refuses one with trailing bytes, another digest, or no DER.
func TestParseTimeStampReq(t *testing.T) {
	t.Parallel()
	imprint := sha256.Sum256([]byte("acme"))
	good := timeStampRequest(t, oidSHA256, imprint[:])
	if got, err := parseTimeStampReq(good); err != nil || !bytes.Equal(got, imprint[:]) {
		t.Fatalf("got %x, %v", got, err)
	}
	sha384 := asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 2}
	for name, der := range map[string][]byte{
		"trailing": append(append([]byte(nil), good...), 0x00),
		"sha384":   timeStampRequest(t, sha384, make([]byte, 48)),
		"garbage":  []byte("acme"),
	} {
		if _, err := parseTimeStampReq(der); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

// timeStampRequest encodes a TimeStampReq with certReq set.
func timeStampRequest(t *testing.T, alg asn1.ObjectIdentifier, imprint []byte) []byte {
	t.Helper()
	var req timeStampReq
	req.Version = 1
	req.MessageImprint.HashAlgorithm.Algorithm = alg
	req.MessageImprint.HashedMessage = imprint
	req.CertReq = true
	der, err := asn1.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return der
}
