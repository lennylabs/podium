package sign

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness/sigstoreharness"
)

// trustedRootTemplate is a trusted_root.json in Sigstore's layout, with
// the member names and protojson formatting Sigstore's own file uses. The
// %s verbs take the certificate-authority chain, the log key, the log
// key's keyDetails, the timestamp-authority chain, and the
// certificate-authority validFor, in that order.
const trustedRootTemplate = `{
  "mediaType": "application/vnd.dev.sigstore.trustedroot+json;version=0.1",
  "tlogs": [
    {
      "baseUrl": "https://log.acme.test",
      "hashAlgorithm": "SHA2_256",
      "publicKey": {
        "rawBytes": "%s",
        "keyDetails": "%s",
        "validFor": {"start": "2026-01-01T00:00:00.000Z"}
      },
      "logId": {"keyId": "YWNtZQ=="}
    }
  ],
  "certificateAuthorities": [
    {
      "subject": {"organization": "acme", "commonName": "acme-fulcio"},
      "uri": "https://fulcio.acme.test",
      "certChain": {"certificates": [%s]},
      "validFor": %s
    }
  ],
  "ctlogs": [
    {
      "baseUrl": "https://ctlog.acme.test",
      "hashAlgorithm": "SHA2_256",
      "publicKey": {"rawBytes": "YWNtZQ==", "keyDetails": "PKIX_ECDSA_P256_SHA_256", "validFor": {"start": "2026-01-01T00:00:00Z"}},
      "logId": {"keyId": "YWNtZQ=="}
    }
  ],
  "timestampAuthorities": [
    {
      "subject": {"organization": "acme", "commonName": "acme-tsa"},
      "uri": "https://tsa.acme.test",
      "certChain": {"certificates": [%s]},
      "validFor": {"start": "2026-01-01T00:00:00Z"}
    }
  ]
}`

// rootDoc fills trustedRootTemplate.
type rootDoc struct {
	logKey, keyDetails, caChain, caValidFor, tsaChain string
}

func (d rootDoc) json() []byte {
	return fmt.Appendf(nil, trustedRootTemplate, d.logKey, d.keyDetails, d.caChain, d.caValidFor, d.tsaChain)
}

// chainJSON writes certificates as certChain entries.
func chainJSON(certs ...*x509.Certificate) string {
	parts := make([]string, len(certs))
	for i, c := range certs {
		parts[i] = fmt.Sprintf(`{"rawBytes": %q}`, base64.StdEncoding.EncodeToString(c.Raw))
	}
	return strings.Join(parts, ", ")
}

// pkixB64 returns the base64 DER SubjectPublicKeyInfo of pub.
func pkixB64(t *testing.T, pub any) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return base64.StdEncoding.EncodeToString(der)
}

// Spec: §4.7.9, §6.2 — parseTrustedRoot reads the certificate
// authorities, the usable log keys, and the timestamp authorities with
// their windows, skips an unsupported log key, ignores ctlogs, and
// refuses a document it cannot use.
func TestParseTrustedRoot(t *testing.T) {
	t.Parallel()
	h := sigstoreharness.New(t)
	good := rootDoc{
		logKey:     pkixB64(t, h.LogPublicKey()),
		keyDetails: keyDetailsP256,
		caChain:    chainJSON(h.FulcioIntermediate(), h.FulcioRoot()),
		caValidFor: `{"start": "2026-01-01T00:00:00.000Z", "end": "2027-01-01T00:00:00.123Z"}`,
		tsaChain:   chainJSON(h.TSALeaf(), h.TSARoot()),
	}
	root, err := parseTrustedRoot(good.json())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ca := root.certificateAuthorities[0]
	if !ca.anchor.Equal(h.FulcioRoot()) || len(ca.intermediates) != 1 || !ca.intermediates[0].Equal(h.FulcioIntermediate()) {
		t.Errorf("certificate authority = %+v", ca)
	}
	if ca.window.End == nil || !ca.window.End.Equal(time.Date(2027, 1, 1, 0, 0, 0, 123_000_000, time.UTC)) {
		t.Errorf("certificate-authority window end = %v", ca.window.End)
	}
	if tsa := root.timestampAuthorities[0]; !tsa.anchor.Equal(h.TSARoot()) || tsa.window.End != nil {
		t.Errorf("timestamp authority = %+v", tsa)
	}
	if len(root.logKeys) != 1 || !root.logKeys[0].key.(*ecdsa.PublicKey).Equal(h.LogPublicKey()) {
		t.Errorf("log keys = %+v", root.logKeys)
	}

	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	refusals := []struct {
		name string
		edit func(*rootDoc)
		raw  []byte
		want string
	}{
		{name: "empty", raw: []byte{}, want: "no trust root configured"},
		{name: "malformed JSON", raw: []byte(`{"tlogs": [`), want: "trust root does not parse"},
		{name: "rawBytes not base64", edit: func(d *rootDoc) { d.logKey = "%%%" }, want: "trust root does not parse"},
		{name: "bad timestamp", edit: func(d *rootDoc) { d.caValidFor = `{"start": "yesterday"}` }, want: "trust root does not parse"},
		{name: "authority with no certificate", edit: func(d *rootDoc) { d.caChain = "" }, want: "trust root authority 0 carries no certificate"},
		{name: "timestamp authority with no certificate", edit: func(d *rootDoc) { d.tsaChain = "" }, want: "trust root authority 0 carries no certificate"},
		{name: "non-DER certificate", edit: func(d *rootDoc) { d.caChain = `{"rawBytes": "YWNtZQ=="}` }, want: "trust root certificate"},
		{name: "no certificate authority", raw: []byte(`{"tlogs": [], "timestampAuthorities": []}`), want: "no certificate authority"},
		{name: "RSA keyDetails", edit: func(d *rootDoc) { d.keyDetails = "PKIX_RSA_PKCS1V15_2048_SHA256" }, want: "no usable transparency-log key"},
		{name: "P-384 key labelled P-256", edit: func(d *rootDoc) { d.logKey = pkixB64(t, p384.Public()) }, want: "no usable transparency-log key"},
		{name: "P-256 key labelled Ed25519", edit: func(d *rootDoc) { d.keyDetails = keyDetailsEd25519 }, want: "no usable transparency-log key"},
		{name: "Ed25519 key labelled P-256", edit: func(d *rootDoc) { d.logKey = pkixB64(t, h.Ed25519LogPublicKey()) }, want: "no usable transparency-log key"},
		{name: "key not DER", edit: func(d *rootDoc) { d.logKey = "YWNtZQ==" }, want: "no usable transparency-log key"},
		{name: "no timestamp authority", raw: h.TrustedRootJSON(sigstoreharness.WithoutTSAs()), want: "no timestamp authority"},
	}
	for _, r := range refusals {
		raw := r.raw
		if raw == nil {
			doc := good
			r.edit(&doc)
			raw = doc.json()
		}
		if _, err := parseTrustedRoot(raw); err == nil || !strings.Contains(err.Error(), r.want) {
			t.Errorf("%s: err = %v, want %q", r.name, err, r.want)
		}
	}

	ed := good
	ed.logKey, ed.keyDetails = pkixB64(t, h.Ed25519LogPublicKey()), keyDetailsEd25519
	if root, err := parseTrustedRoot(ed.json()); err != nil || len(root.logKeys) != 1 {
		t.Errorf("Ed25519 log key: (%+v, %v)", root.logKeys, err)
	}
}

// Spec: §6.2 — a validFor window includes both ends, and an absent bound
// leaves that side unbounded.
func TestValidityWindow_Contains(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 1, 15, 11, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 15, 13, 0, 0, 0, time.UTC)
	bounded := validFor{Start: &start, End: &end}
	cases := []struct {
		name string
		w    validFor
		t    time.Time
		want bool
	}{
		{"at start", bounded, start, true},
		{"at end", bounded, end, true},
		{"before start", bounded, start.Add(-time.Second), false},
		{"after end", bounded, end.Add(time.Second), false},
		{"no start", validFor{End: &end}, start.AddDate(-10, 0, 0), true},
		{"no end", validFor{Start: &start}, end.AddDate(10, 0, 0), true},
		{"unbounded", validFor{}, end, true},
	}
	for _, c := range cases {
		if got := c.w.contains(c.t); got != c.want {
			t.Errorf("%s: contains = %v, want %v", c.name, got, c.want)
		}
	}
}
