package sign

import (
	"context"
	"crypto"
	"crypto/x509"
	"encoding/asn1"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness/sigstoreharness"
)

// Spec: §4.7.9 — an RFC 3161 token verifies when it has one signer whose
// signature over the re-tagged signed attributes holds, whose attributes
// name TSTInfo and digest the content, whose imprint is SHA-256 of the
// signature, and whose signer chains to a timestamp authority at genTime.
func TestVerifyTimestamp(t *testing.T) {
	t.Parallel()
	h := sigstoreharness.New(t)
	root, err := parseTrustedRoot(h.TrustedRootJSON())
	if err != nil {
		t.Fatalf("trusted root: %v", err)
	}
	sig := []byte("acme signature")
	cases := []struct {
		name  string
		token []byte
		sig   []byte
		want  string // empty means accepted
	}{
		{"default", h.TimestampToken(sig), sig, ""},
		{"signer found in the trusted root", h.TimestampToken(sig, sigstoreharness.WithoutEmbeddedCerts()), sig, ""},
		{"subject key identifier sid", h.TimestampToken(sig, sigstoreharness.WithSubjectKeyIdentifierSID()), sig, ""},
		{"bare key algorithm", h.TimestampToken(sig, sigstoreharness.WithSignatureAlgorithm(oidECPublicKey)), sig, ""},
		{"two signers", h.TimestampToken(sig, sigstoreharness.WithTwoSigners()), sig, "timestamp carries 2 signers; want 1"},
		{"no signed attributes", h.TimestampToken(sig, sigstoreharness.WithoutSignedAttrs()), sig, "signer carries no signed attributes"},
		{"message digest mismatch", h.TimestampToken(sig, sigstoreharness.WithMessageDigestMismatch()), sig, "timestamp signature does not verify"},
		{"content type", h.TimestampToken(sig, sigstoreharness.WithTSTContentType(asn1.ObjectIdentifier{1, 2, 3})), sig, "timestamp does not parse"},
		{"RSA algorithm over an ECDSA signer", h.TimestampToken(sig, sigstoreharness.WithSignatureAlgorithm(oidSHA256WithRSA)), sig, "timestamp signature does not verify"},
		{"unknown algorithm", h.TimestampToken(sig, sigstoreharness.WithSignatureAlgorithm(asn1.ObjectIdentifier{1, 2, 3})), sig, "timestamp signature does not verify"},
		{"other signature", h.TimestampToken(sig), []byte("acme other"), "timestamp does not cover the signature"},
		{"not DER", []byte("acme"), sig, "timestamp does not parse"},
		{"trailing bytes", append(h.TimestampToken(sig), 0x00), sig, "timestamp does not parse"},
		{"not signed data", contentInfoDER(t, oidTSTInfo), sig, "is not signed data"},
		{"signed data does not parse", contentInfoDER(t, oidSignedData), sig, "signed data"},
	}
	for _, c := range cases {
		got, err := verifyTimestamp(root, c.token, c.sig)
		if c.want == "" {
			if err != nil || !got.Equal(h.Clock()) {
				t.Errorf("%s: (%v, %v), want (%v, nil)", c.name, got, err, h.Clock())
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}

// contentInfoDER encodes a ContentInfo of contentType whose content is a
// NULL.
func contentInfoDER(t *testing.T, contentType asn1.ObjectIdentifier) []byte {
	t.Helper()
	b, err := asn1.Marshal(struct {
		ContentType asn1.ObjectIdentifier
		Content     asn1.RawValue
	}{contentType, asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: []byte{0x05, 0x00}}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// Spec: §4.7.9 — genTime is a UTC GeneralizedTime with optional
// fractional seconds.
func TestParseGeneralizedTime(t *testing.T) {
	t.Parallel()
	gt := func(s string) asn1.RawValue {
		return asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagGeneralizedTime, Bytes: []byte(s)}
	}
	got, err := parseGeneralizedTime(gt("20260115120000.25Z"))
	if want := time.Date(2026, 1, 15, 12, 0, 0, 250_000_000, time.UTC); err != nil || !got.Equal(want) {
		t.Fatalf("fractional: (%v, %v), want %v", got, err, want)
	}
	if _, err := parseGeneralizedTime(asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagUTCTime, Bytes: []byte("260115120000Z")}); err == nil {
		t.Error("UTCTime accepted")
	}
	if _, err := parseGeneralizedTime(gt("20260115120000+0100")); err == nil {
		t.Error("non-UTC time accepted")
	}
}

// Spec: §4.7.9 — the SignerInfo algorithms map to x509 algorithms, a bare
// key algorithm taking its hash from the digest algorithm.
func TestSignatureAlgorithm(t *testing.T) {
	t.Parallel()
	cases := []struct {
		oid  asn1.ObjectIdentifier
		hash crypto.Hash
		want x509.SignatureAlgorithm
	}{
		{oidECDSAWithSHA384, crypto.SHA256, x509.ECDSAWithSHA384},
		{oidECDSAWithSHA512, crypto.SHA256, x509.ECDSAWithSHA512},
		{oidSHA384WithRSA, crypto.SHA256, x509.SHA384WithRSA},
		{oidSHA512WithRSA, crypto.SHA256, x509.SHA512WithRSA},
		{oidECPublicKey, crypto.SHA512, x509.ECDSAWithSHA512},
		{oidRSAEncryption, crypto.SHA384, x509.SHA384WithRSA},
	}
	for _, c := range cases {
		if got, ok := signatureAlgorithm(c.oid, c.hash); !ok || got != c.want {
			t.Errorf("signatureAlgorithm(%v, %v) = (%v, %v), want %v", c.oid, c.hash, got, ok, c.want)
		}
	}
	for _, oid := range []asn1.ObjectIdentifier{oidSHA384, oidSHA512} {
		if _, ok := digestHash(oid); !ok {
			t.Errorf("digestHash(%v) unsupported", oid)
		}
	}
	if _, ok := digestHash(asn1.ObjectIdentifier{1, 2, 3}); ok {
		t.Error("unknown digest algorithm accepted")
	}
}

// Spec: §4.7.9 — a SignerIdentifier that is neither an
// IssuerAndSerialNumber nor a [0] subject key identifier names no signer.
func TestFindSigner(t *testing.T) {
	t.Parallel()
	h := sigstoreharness.New(t)
	certs := []*x509.Certificate{h.TSALeaf()}
	if c := findSigner(asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 1, Bytes: h.TSALeaf().SubjectKeyId}, certs); c != nil {
		t.Error("[1] sid matched")
	}
	if c := findSigner(asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSequence, FullBytes: []byte{0x30, 0x00}}, certs); c != nil {
		t.Error("malformed IssuerAndSerialNumber matched")
	}
}

// Spec: §4.7.9 — requestTimestamp refuses a response that is not a
// TimeStampResp.
func TestRequestTimestamp_UndecodableResponse(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("acme, not DER"))
	}))
	t.Cleanup(srv.Close)
	s := SigstoreKeyless{TSAURL: srv.URL, Client: srv.Client()}
	if _, err := s.requestTimestamp(context.Background(), []byte("sig")); err == nil || !strings.Contains(err.Error(), "decode response") {
		t.Fatalf("err = %v, want decode response", err)
	}
}

// Spec: §4.7.9 — signed attributes that do not decode are refused.
func TestCheckSignedAttrs_Malformed(t *testing.T) {
	t.Parallel()
	attr := func(oid asn1.ObjectIdentifier, value []byte) []byte {
		b, err := asn1.Marshal(cmsAttribute{Type: oid, Values: asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSet, IsCompound: true, Bytes: value}})
		if err != nil {
			t.Fatalf("attribute: %v", err)
		}
		return append([]byte{0x31, byte(len(b))}, b...)
	}
	notAnOID := []byte{0x04, 0x01, 0x00}
	notOctets := []byte{0x06, 0x01, 0x2a}
	cases := map[string][]byte{
		"not a set":               {0x04, 0x00},
		"content type not an OID": attr(oidAttrContentType, notAnOID),
		"digest not octets":       attr(oidAttrMessageDigest, notOctets),
	}
	for name, set := range cases {
		if err := checkSignedAttrs(set, crypto.SHA256, nil); err == nil || !strings.Contains(err.Error(), "timestamp does not parse") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// Spec: §4.7.9 — an unreachable timestamp authority fails the request.
func TestRequestTimestamp_Unreachable(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	s := SigstoreKeyless{TSAURL: srv.URL, Client: srv.Client()}
	if _, err := s.requestTimestamp(context.Background(), []byte("sig")); err == nil {
		t.Fatal("request to a closed server succeeded")
	}
}
