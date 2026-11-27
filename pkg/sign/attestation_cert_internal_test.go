package sign

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"testing"
)

// utf8DER encodes s as a DER UTF8String.
func utf8DER(s string) []byte { return append([]byte{0x0c, byte(len(s))}, s...) }

// sanDER encodes a GeneralNames SEQUENCE of the encoded names.
func sanDER(names ...[]byte) []byte {
	var body []byte
	for _, n := range names {
		body = append(body, n...)
	}
	return append([]byte{0x30, byte(len(body))}, body...)
}

// generalName encodes one context-specific primitive GeneralName.
func generalName(tag byte, v string) []byte { return append([]byte{0x80 | tag, byte(len(v))}, v...) }

// leafWith builds a certificate value with the issuer and identity of a
// valid leaf, followed by extra extensions. readCertificateFields reads
// Extensions only, so the value needs no signature or encoding; that lets
// the test hold a repeated extension, which crypto/x509 refuses to parse.
func leafWith(extra ...pkix.Extension) *x509.Certificate {
	exts := []pkix.Extension{
		{Id: oidFulcioIssuerV2, Value: utf8DER("https://accounts.acme.com")},
		{Id: oidSubjectAltName, Value: sanDER(generalName(1, "alice@acme.com"))},
	}
	return &x509.Certificate{Extensions: append(exts, extra...)}
}

// TestReadCertificateFields_DER covers the DER reading of the subject
// alternative name and of each Fulcio extension of §4.7.11, including the
// forms crypto/x509 refuses to parse.
//
// Spec: §4.7.11
func TestReadCertificateFields_DER(t *testing.T) {
	t.Parallel()
	ok := leafWith(
		pkix.Extension{Id: oidSourceRepository, Value: utf8DER("https://github.com/acme/skills")},
		pkix.Extension{Id: oidSourceRepositoryDigest, Value: utf8DER("0123")},
		pkix.Extension{Id: oidSourceRepositoryRef, Value: utf8DER("refs/heads/main")},
		pkix.Extension{Id: oidSourceRepositoryID, Value: utf8DER("101")},
		pkix.Extension{Id: oidSourceRepositoryOwner, Value: utf8DER("https://github.com/acme")},
		pkix.Extension{Id: oidSourceRepositoryOwnerID, Value: utf8DER("201")},
	)
	f, err := readCertificateFields(ok)
	if err != nil {
		t.Fatalf("readCertificateFields: %v", err)
	}
	if f.Issuer.Value != "https://accounts.acme.com" || f.Identity.Value != "alice@acme.com" || f.IdentityIsURI ||
		f.SourceRepository.Value != "https://github.com/acme/skills" || f.SourceRepositoryDigest.Value != "0123" ||
		f.SourceRepositoryRef.Value != "refs/heads/main" || f.SourceRepositoryID.Value != "101" ||
		f.SourceRepositoryOwner.Value != "https://github.com/acme" || f.SourceRepositoryOwnerID.Value != "201" {
		t.Errorf("fields = %+v", f)
	}

	uriLeaf := &x509.Certificate{Extensions: []pkix.Extension{
		{Id: oidFulcioIssuerV2, Value: utf8DER("i")},
		{Id: oidSubjectAltName, Value: sanDER(generalName(6, "https://acme.com/w"))},
	}}
	if f, err := readCertificateFields(uriLeaf); err != nil || !f.IdentityIsURI || f.Identity.Value != "https://acme.com/w" {
		t.Errorf("URI leaf: %+v, %v", f, err)
	}

	issuerTwice := &x509.Certificate{Extensions: []pkix.Extension{
		{Id: oidFulcioIssuerV2, Value: utf8DER("a")},
		{Id: oidFulcioIssuerV2, Value: utf8DER("a")},
		{Id: oidSubjectAltName, Value: sanDER(generalName(1, "alice@acme.com"))},
	}}
	for _, tc := range []struct {
		name string
		leaf *x509.Certificate
		want AttestationReason
	}{
		{"issuer twice", issuerTwice, ReasonIssuer},
		{"issuer with trailing bytes", &x509.Certificate{Extensions: []pkix.Extension{
			{Id: oidFulcioIssuerV2, Value: append(utf8DER("a"), 0)},
		}}, ReasonIssuer},
		{"issuer that is not UTF-8", &x509.Certificate{Extensions: []pkix.Extension{
			{Id: oidFulcioIssuerV2, Value: []byte{0x0c, 0x01, 0xff}},
		}}, ReasonIssuer},
		{"SAN twice", leafWith(pkix.Extension{Id: oidSubjectAltName, Value: sanDER(generalName(1, "alice@acme.com"))}), ReasonIdentity},
		{"SAN that is not a sequence", &x509.Certificate{Extensions: []pkix.Extension{
			{Id: oidFulcioIssuerV2, Value: utf8DER("a")}, {Id: oidSubjectAltName, Value: utf8DER("alice@acme.com")},
		}}, ReasonIdentity},
		{"SAN with no name", &x509.Certificate{Extensions: []pkix.Extension{
			{Id: oidFulcioIssuerV2, Value: utf8DER("a")}, {Id: oidSubjectAltName, Value: sanDER()},
		}}, ReasonIdentity},
		{"DNS name", &x509.Certificate{Extensions: []pkix.Extension{
			{Id: oidFulcioIssuerV2, Value: utf8DER("a")}, {Id: oidSubjectAltName, Value: sanDER(generalName(2, "acme.com"))},
		}}, ReasonIdentity},
		{"repository twice", leafWith(
			pkix.Extension{Id: oidSourceRepository, Value: utf8DER("x")},
			pkix.Extension{Id: oidSourceRepository, Value: utf8DER("x")}), ReasonSourceBinding},
		{"ref twice", leafWith(
			pkix.Extension{Id: oidSourceRepositoryRef, Value: utf8DER("x")},
			pkix.Extension{Id: oidSourceRepositoryRef, Value: utf8DER("x")}), ReasonRefBinding},
		{"owner identifier as an integer", leafWith(
			pkix.Extension{Id: oidSourceRepositoryOwnerID, Value: mustMarshal(t, 201)}), ReasonSourceBinding},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := readCertificateFields(tc.leaf)
			if r, _ := AttestationReasonOf(err); r != tc.want {
				t.Fatalf("reason = %q, want %q (err %v)", r, tc.want, err)
			}
		})
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := asn1.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
