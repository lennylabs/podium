package sign

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Spec: §6.2 — PODIUM_SIGSTORE_CERT_IDENTITY is a comma-separated list
// trimmed of whitespace with empty entries dropped, and the issuer is
// trimmed so a whitespace-only value is empty.
func TestNewIdentityPolicy_ParsesList(t *testing.T) {
	t.Parallel()
	cases := []struct {
		identities, issuer string
		want               IdentityPolicy
	}{
		{" alice@acme.com , ,bob@acme.com ", "https://accounts.acme.com",
			IdentityPolicy{Identities: []string{"alice@acme.com", "bob@acme.com"}, Issuer: "https://accounts.acme.com"}},
		{",,", "", IdentityPolicy{}},
		{"", " https://accounts.acme.com ", IdentityPolicy{Issuer: "https://accounts.acme.com"}},
		{"alice@acme.com", " \t ", IdentityPolicy{Identities: []string{"alice@acme.com"}}},
	}
	for _, c := range cases {
		if got := NewIdentityPolicy(c.identities, c.issuer); !reflect.DeepEqual(got, c.want) {
			t.Errorf("NewIdentityPolicy(%q, %q) = %#v, want %#v", c.identities, c.issuer, got, c.want)
		}
	}
}

// Spec: §4.7.9 — a policy with no identity or no issuer accepts nothing,
// and the refusal names the variable.
func TestIdentityPolicy_Validate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		policy IdentityPolicy
		want   string
	}{
		{"no identities", IdentityPolicy{Issuer: "https://accounts.acme.com"}, "PODIUM_SIGSTORE_CERT_IDENTITY"},
		{"no issuer", IdentityPolicy{Identities: []string{"alice@acme.com"}}, "PODIUM_SIGSTORE_CERT_OIDC_ISSUER"},
		{"whitespace issuer", NewIdentityPolicy("alice@acme.com", "  "), "PODIUM_SIGSTORE_CERT_OIDC_ISSUER"},
		{"both set", NewIdentityPolicy("alice@acme.com", "https://accounts.acme.com"), ""},
	}
	for _, c := range cases {
		err := c.policy.Validate()
		if c.want == "" {
			if err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}

// testLeaf describes a leaf the match tests build.
type testLeaf struct {
	emails []string
	uris   []string
	exts   []pkix.Extension
}

// build issues a self-signed certificate carrying the leaf's SANs and
// extensions.
func (l testLeaf) build(t *testing.T) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:    big.NewInt(1),
		Subject:         pkix.Name{CommonName: "acme leaf"},
		NotBefore:       time.Date(2026, 1, 15, 11, 55, 0, 0, time.UTC),
		NotAfter:        time.Date(2026, 1, 15, 12, 15, 0, 0, time.UTC),
		EmailAddresses:  l.emails,
		ExtraExtensions: l.exts,
	}
	for _, raw := range l.uris {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("uri: %v", err)
		}
		tmpl.URIs = append(tmpl.URIs, u)
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return cert
}

// issuerExt returns a Fulcio .1.8 extension carrying issuer as a UTF8String.
func issuerExt(t *testing.T, issuer string) pkix.Extension {
	t.Helper()
	v, err := asn1.MarshalWithParams(issuer, "utf8")
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	return pkix.Extension{Id: oidFulcioIssuerV2, Value: v}
}

// otherNameSAN returns a SAN extension whose only name is a Fulcio
// username otherName, written non-critical so the leaf parses.
func otherNameSAN(t *testing.T) pkix.Extension {
	t.Helper()
	oid, err := asn1.Marshal(asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 7})
	if err != nil {
		t.Fatalf("oid: %v", err)
	}
	value, err := asn1.MarshalWithParams("alice", "utf8")
	if err != nil {
		t.Fatalf("value: %v", err)
	}
	explicit, err := asn1.Marshal(asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: value})
	if err != nil {
		t.Fatalf("explicit: %v", err)
	}
	name, err := asn1.Marshal(asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: append(oid, explicit...)})
	if err != nil {
		t.Fatalf("name: %v", err)
	}
	san, err := asn1.Marshal(asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSequence, IsCompound: true, Bytes: name})
	if err != nil {
		t.Fatalf("san: %v", err)
	}
	return pkix.Extension{Id: asn1.ObjectIdentifier{2, 5, 29, 17}, Value: san}
}

// Spec: §4.7.9 — a leaf matches when an email or URI SAN equals a list
// entry byte for byte and its Fulcio issuer equals the policy issuer;
// .1.8 takes precedence over .1.1, and a malformed .1.8 never falls back.
func TestIdentityPolicy_Match(t *testing.T) {
	t.Parallel()
	const issuer = "https://accounts.acme.com"
	const workflow = "https://github.com/acme/repo/.github/workflows/release.yml@refs/heads/main"
	policy := NewIdentityPolicy("alice@acme.com, "+workflow, issuer)
	v1 := func(s string) pkix.Extension { return pkix.Extension{Id: oidFulcioIssuerV1, Value: []byte(s)} }
	trailing := issuerExt(t, issuer)
	trailing.Value = append(trailing.Value, 0x00)
	cases := []struct {
		name string
		leaf testLeaf
		want string
	}{
		{"email SAN", testLeaf{emails: []string{"alice@acme.com"}, exts: []pkix.Extension{issuerExt(t, issuer)}}, ""},
		{"URI SAN", testLeaf{uris: []string{workflow}, exts: []pkix.Extension{issuerExt(t, issuer)}}, ""},
		{".1.1 only", testLeaf{emails: []string{"alice@acme.com"}, exts: []pkix.Extension{v1(issuer)}}, ""},
		{".1.8 over a disagreeing .1.1", testLeaf{emails: []string{"alice@acme.com"}, exts: []pkix.Extension{v1("https://evil.example"), issuerExt(t, issuer)}}, ""},
		{".1.8 disagreeing over a matching .1.1", testLeaf{emails: []string{"alice@acme.com"}, exts: []pkix.Extension{issuerExt(t, "https://evil.example"), v1(issuer)}}, "OIDC issuer mismatch: certificate carries https://evil.example"},
		{".1.8 with trailing bytes", testLeaf{emails: []string{"alice@acme.com"}, exts: []pkix.Extension{trailing, v1(issuer)}}, "malformed OIDC issuer extension"},
		{".1.8 not a UTF8String", testLeaf{emails: []string{"alice@acme.com"}, exts: []pkix.Extension{{Id: oidFulcioIssuerV2, Value: []byte{0x04, 0x01, 'a'}}}}, "malformed OIDC issuer extension"},
		{"no issuer extension", testLeaf{emails: []string{"alice@acme.com"}}, "certificate carries no OIDC issuer extension"},
		{"otherName SAN only", testLeaf{exts: []pkix.Extension{otherNameSAN(t), issuerExt(t, issuer)}}, "certificate identity mismatch"},
		{"email SAN differs in case", testLeaf{emails: []string{"Alice@acme.com"}, exts: []pkix.Extension{issuerExt(t, issuer)}}, "certificate identity mismatch: certificate carries Alice@acme.com"},
		{"email outside, URI inside", testLeaf{emails: []string{"carol@acme.com"}, uris: []string{workflow}, exts: []pkix.Extension{issuerExt(t, issuer)}}, ""},
		{"URI outside, email inside", testLeaf{emails: []string{"alice@acme.com"}, uris: []string{"https://github.com/acme/other"}, exts: []pkix.Extension{issuerExt(t, issuer)}}, ""},
		{"both outside", testLeaf{emails: []string{"carol@acme.com"}, uris: []string{"https://github.com/acme/other"}, exts: []pkix.Extension{issuerExt(t, issuer)}}, "certificate identity mismatch: certificate carries carol@acme.com, https://github.com/acme/other"},
	}
	for _, c := range cases {
		err := policy.match(c.leaf.build(t))
		if c.want == "" {
			if err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
	}
}
