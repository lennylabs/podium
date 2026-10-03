package sign

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness/sigstoreharness"
)

func TestSplitContentHash(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		wantAlg string
		wantHex string
		wantErr bool
	}{
		{"sha256:" + hex.EncodeToString([]byte("abc")), "sha256", "616263", false},
		{"sha512:" + hex.EncodeToString([]byte("def")), "sha512", "646566", false},
		{"no-colon", "", "", true},
		{":alone", "", "", true},
		{"sha256:", "", "", true},
		{"sha256:not-hex", "", "", true},
	}
	for _, c := range cases {
		alg, hexOut, err := splitContentHash(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("splitContentHash(%q): err = %v, wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if c.wantErr {
			continue
		}
		if alg != c.wantAlg || hexOut != c.wantHex {
			t.Errorf("splitContentHash(%q) = (%q, %q), want (%q, %q)",
				c.in, alg, hexOut, c.wantAlg, c.wantHex)
		}
	}
}

// harnessEntry returns the entry body, signature, and leaf of a harness
// envelope over hash.
func harnessEntry(t *testing.T, h *sigstoreharness.Harness, hash string, opts ...sigstoreharness.EnvOpt) (string, []byte, *x509.Certificate) {
	t.Helper()
	env, err := decodeEnvelope(h.Envelope(t, hash, opts...))
	if err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	leaf, _, err := pemDecodeChain(env.Cert)
	if err != nil {
		t.Fatalf("leaf: %v", err)
	}
	return env.TLog.Body, env.sig, leaf
}

// rewriteBody re-encodes an entry body after edit changes its JSON.
func rewriteBody(t *testing.T, body string, edit func(map[string]any)) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		t.Fatalf("body: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("body: %v", err)
	}
	edit(m)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("body: %v", err)
	}
	return base64.StdEncoding.EncodeToString(out)
}

// Spec: §4.7.9 — the proven entry body must be a hashedrekord v0.0.2
// entry recording the SHA-256 digest, the envelope's signature, and the
// envelope's leaf.
func TestBindEntry(t *testing.T) {
	t.Parallel()
	h := sigstoreharness.New(t)
	sum := sha256.Sum256([]byte("acme artifact"))
	hash := "sha256:" + hex.EncodeToString(sum[:])
	body, sig, leaf := harnessEntry(t, h, hash)
	_, _, otherLeaf := harnessEntry(t, h, hash)
	sha384 := rewriteBody(t, body, func(m map[string]any) {
		m["spec"].(map[string]any)["hashedRekordV002"].(map[string]any)["data"].(map[string]any)["algorithm"] = "SHA2_384"
	})
	v001 := rewriteBody(t, body, func(m map[string]any) { m["apiVersion"] = "0.0.1" })
	notJSON := base64.StdEncoding.EncodeToString([]byte("acme, not JSON"))
	cases := []struct {
		name   string
		body   string
		digest []byte
		sig    []byte
		leaf   *x509.Certificate
		want   string
	}{
		{"bound", body, sum[:], sig, leaf, ""},
		{"other digest", body, make([]byte, 32), sig, leaf, "does not bind the digest"},
		{"SHA2_384 algorithm", sha384, sum[:], sig, leaf, "does not bind the digest"},
		{"other signature", body, sum[:], []byte("acme other signature"), leaf, "does not bind the signature"},
		{"other leaf", body, sum[:], sig, otherLeaf, "does not bind the certificate"},
		{"v0.0.1 entry", v001, sum[:], sig, leaf, "not a hashedrekord v0.0.2 entry"},
		{"non-base64 body", "%%%", sum[:], sig, leaf, "not a hashedrekord v0.0.2 entry"},
		{"non-JSON body", notJSON, sum[:], sig, leaf, "not a hashedrekord v0.0.2 entry"},
	}
	for _, c := range cases {
		err := bindEntry(c.body, c.digest, c.sig, c.leaf)
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

// Spec: §4.7.9 — the Rekor v2 response decodes logIndex as a decimal
// string and refuses one that is not.
func TestRekorEntryResponse_LogIndex(t *testing.T) {
	t.Parallel()
	if _, err := (rekorEntryResponse{LogIndex: "five"}).tlogEntry(); err == nil || !strings.Contains(err.Error(), "log index") {
		t.Fatalf("err = %v, want a log index decode error", err)
	}
}
