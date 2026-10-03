package sigstoreharness

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// decodeRoot parses a trusted root the harness wrote. It decodes into a
// generic map first so the test reads Sigstore's member names from the
// JSON text rather than from the harness's struct tags.
func decodeRoot(t *testing.T, raw []byte) (map[string]any, trustedRootDoc) {
	t.Helper()
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("trusted root: %v", err)
	}
	var doc trustedRootDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("trusted root: %v", err)
	}
	return generic, doc
}

// Spec: §4.7.9, §6.2. The default trusted root uses Sigstore's member
// names and lists one certificate authority ordered intermediate then
// root, one timestamp authority ordered signer then root, and the P-256
// log key, each valid from the clock minus 1 hour with no end.
func TestTrustedRootJSON_Default(t *testing.T) {
	t.Parallel()
	h := New(t)
	generic, doc := decodeRoot(t, h.TrustedRootJSON())
	for _, member := range []string{"mediaType", "tlogs", "certificateAuthorities", "timestampAuthorities", "ctlogs"} {
		if _, ok := generic[member]; !ok {
			t.Fatalf("member %s missing", member)
		}
	}
	tlog := generic["tlogs"].([]any)[0].(map[string]any)
	for _, member := range []string{"baseUrl", "hashAlgorithm", "publicKey", "logId"} {
		if _, ok := tlog[member]; !ok {
			t.Fatalf("tlogs member %s missing", member)
		}
	}
	const start = "2026-01-15T11:00:00.000Z"
	if len(doc.TLogs) != 1 || doc.TLogs[0].PublicKey.KeyDetails != keyDetailsP256 ||
		doc.TLogs[0].PublicKey.ValidFor != (validForDoc{Start: start}) {
		t.Fatalf("tlogs = %+v", doc.TLogs)
	}
	equalBytes(t, "log key", doc.TLogs[0].PublicKey.RawBytes, pkixBytes(t, h.LogPublicKey()))
	assertChain(t, "certificate authority", doc.CertificateAuthorities, start, h.FulcioIntermediate(), h.FulcioRoot())
	assertChain(t, "timestamp authority", doc.TimestampAuthorities, start, h.TSALeaf(), h.TSARoot())
}

// assertChain checks a single authority lists want in order with the
// given window start.
func assertChain(t *testing.T, what string, got []authorityDoc, start string, want ...*x509.Certificate) {
	t.Helper()
	if len(got) != 1 || len(got[0].CertChain.Certificates) != len(want) || got[0].ValidFor.Start != start || got[0].ValidFor.End != "" {
		t.Fatalf("%s = %+v", what, got)
	}
	for i, c := range want {
		equalBytes(t, what, got[0].CertChain.Certificates[i].RawBytes, c.Raw)
	}
}

// Spec: §4.7.9, §6.2. Each RootOpt changes the one part of the trusted
// root it names.
func TestTrustedRootJSON_Options(t *testing.T) {
	t.Parallel()
	h := New(t)
	start := time.Date(2026, 1, 15, 11, 59, 59, 0, time.UTC)
	end := time.Date(2026, 1, 15, 12, 0, 0, 500_000_000, time.UTC)
	cases := []struct {
		name  string
		opts  []RootOpt
		check func(t *testing.T, doc trustedRootDoc)
	}{
		{"without CAs", []RootOpt{WithoutCAs()}, func(t *testing.T, doc trustedRootDoc) {
			if len(doc.CertificateAuthorities) != 0 {
				t.Fatal("certificate authority present")
			}
		}},
		{"without TSAs", []RootOpt{WithoutTSAs()}, func(t *testing.T, doc trustedRootDoc) {
			if len(doc.TimestampAuthorities) != 0 {
				t.Fatal("timestamp authority present")
			}
		}},
		{"without tlogs", []RootOpt{WithoutTLogs()}, func(t *testing.T, doc trustedRootDoc) {
			if len(doc.TLogs) != 0 {
				t.Fatal("log key present")
			}
		}},
		{"unsupported key only", []RootOpt{WithoutTLogs(), WithUnsupportedLogKey()}, func(t *testing.T, doc trustedRootDoc) {
			if len(doc.TLogs) != 1 || doc.TLogs[0].PublicKey.KeyDetails != keyDetailsRSA {
				t.Fatalf("tlogs = %+v", doc.TLogs)
			}
			if pub, err := x509.ParsePKIXPublicKey(doc.TLogs[0].PublicKey.RawBytes); err != nil {
				t.Fatal(err)
			} else if _, ok := pub.(*rsa.PublicKey); !ok {
				t.Fatalf("key is %T", pub)
			}
		}},
		{"added keys", []RootOpt{WithEd25519LogKey(), WithExtraTLog()}, func(t *testing.T, doc trustedRootDoc) {
			if len(doc.TLogs) != 3 || doc.TLogs[1].PublicKey.KeyDetails != keyDetailsEd25519 {
				t.Fatalf("tlogs = %+v", doc.TLogs)
			}
			equalBytes(t, "Ed25519 key", doc.TLogs[1].PublicKey.RawBytes, pkixBytes(t, h.Ed25519LogPublicKey()))
			pub, err := x509.ParsePKIXPublicKey(doc.TLogs[2].PublicKey.RawBytes)
			if err != nil || pub.(*ecdsa.PublicKey).Equal(h.LogPublicKey()) {
				t.Fatalf("extra key is the harness key or does not parse: %v", err)
			}
		}},
		{"ct logs", []RootOpt{WithCTLogs()}, func(t *testing.T, doc trustedRootDoc) {
			if len(doc.CTLogs) != 1 {
				t.Fatalf("ctlogs = %d", len(doc.CTLogs))
			}
			if _, err := x509.ParsePKIXPublicKey(doc.CTLogs[0].PublicKey.RawBytes); err == nil {
				t.Fatal("ct log key parses")
			}
		}},
		{"CA window", []RootOpt{WithCAValidFor(&start, &end)}, func(t *testing.T, doc trustedRootDoc) {
			want := validForDoc{Start: "2026-01-15T11:59:59.000Z", End: "2026-01-15T12:00:00.500Z"}
			if doc.CertificateAuthorities[0].ValidFor != want {
				t.Fatalf("validFor = %+v", doc.CertificateAuthorities[0].ValidFor)
			}
		}},
		{"TSA window", []RootOpt{WithTSAValidFor(nil, &end)}, func(t *testing.T, doc trustedRootDoc) {
			if doc.TimestampAuthorities[0].ValidFor != (validForDoc{End: "2026-01-15T12:00:00.500Z"}) {
				t.Fatalf("validFor = %+v", doc.TimestampAuthorities[0].ValidFor)
			}
		}},
		{"log windows", []RootOpt{WithLogValidFor(Window{End: &start}, Window{Start: &end})}, func(t *testing.T, doc trustedRootDoc) {
			if len(doc.TLogs) != 2 || doc.TLogs[0].PublicKey.ValidFor.End == "" || doc.TLogs[1].PublicKey.ValidFor.Start == "" {
				t.Fatalf("tlogs = %+v", doc.TLogs)
			}
			equalBytes(t, "same key", doc.TLogs[0].PublicKey.RawBytes, doc.TLogs[1].PublicKey.RawBytes)
		}},
		{"garbage cert", []RootOpt{WithGarbageCert()}, func(t *testing.T, doc trustedRootDoc) {
			if _, err := x509.ParseCertificate(doc.CertificateAuthorities[0].CertChain.Certificates[0].RawBytes); err == nil {
				t.Fatal("garbage certificate parses")
			}
		}},
		{"foreign CA", []RootOpt{WithForeignCA()}, func(t *testing.T, doc trustedRootDoc) {
			ca := doc.CertificateAuthorities[0].CertChain.Certificates
			if bytes.Equal(ca[1].RawBytes, h.FulcioRoot().Raw) || bytes.Equal(ca[0].RawBytes, h.FulcioIntermediate().Raw) {
				t.Fatal("foreign CA is the harness Fulcio chain")
			}
			equalBytes(t, "TSA kept", doc.TimestampAuthorities[0].CertChain.Certificates[0].RawBytes, h.TSALeaf().Raw)
			equalBytes(t, "log key kept", doc.TLogs[0].PublicKey.RawBytes, pkixBytes(t, h.LogPublicKey()))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, doc := decodeRoot(t, h.TrustedRootJSON(tc.opts...))
			tc.check(t, doc)
		})
	}
}

// Spec: §4.7.9, §6.2. WithMalformedJSON yields text that does not parse.
func TestTrustedRootJSON_Malformed(t *testing.T) {
	t.Parallel()
	h := New(t)
	raw := h.TrustedRootJSON(WithMalformedJSON())
	if json.Valid(raw) || !strings.HasPrefix(string(raw), "{") {
		t.Fatalf("malformed root = %q", raw)
	}
}
