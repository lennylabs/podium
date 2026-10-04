package vectors_test

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/version"
)

// base64Vector is one input to the §4.7.10 canonical base64 rule. Decoded is
// the standard base64 of the decoded bytes of an ok case.
type base64Vector struct {
	Name    string `json:"name"`
	Input   string `json:"input"`
	Outcome string `json:"outcome"`
	Decoded string `json:"decoded,omitempty"`
}

// envelopeVector is one §4.7.9 registry-managed envelope, verified over
// SignedHash under the key set Keys. KeyID names the key that verified an ok
// case.
type envelopeVector struct {
	Name       string   `json:"name"`
	Envelope   string   `json:"envelope"`
	SignedHash string   `json:"signed_hash"`
	Keys       []string `json:"keys"`
	Outcome    string   `json:"outcome"`
	KeyID      string   `json:"key_id,omitempty"`
}

// base64Vectors builds the base64 section. Only "", QQ==, and QUJD are
// canonical.
func base64Vectors(t *testing.T) []base64Vector {
	cases := []struct {
		name, input string
		ok          bool
		want        string
	}{
		{"empty", "", true, ""},
		{"one byte padded", "QQ==", true, "A"},
		{"three bytes", "QUJD", true, "ABC"},
		{"non-zero pad bit", "QR==", false, ""},
		{"missing padding", "QQ", false, ""},
		{"trailing line feed", "QQ==\n", false, ""},
		{"embedded CRLF", "Q\r\nQ==", false, ""},
		{"embedded space", "Q Q==", false, ""},
		{"URL-safe alphabet", "-_8=", false, ""},
		{"padding before more data", "QQ==QQ==", false, ""},
	}
	out := make([]base64Vector, 0, len(cases))
	for _, c := range cases {
		v := base64Vector{Name: c.name, Input: c.input, Outcome: codeMismatch}
		got, err := version.DecodeBase64(c.input)
		switch {
		case c.ok && err != nil:
			t.Errorf("base64/%s: Go refused a canonical value: %v", c.name, err)
		case !c.ok && err == nil:
			t.Errorf("base64/%s: Go accepted a non-canonical value", c.name)
		case c.ok && string(got) != c.want:
			t.Errorf("base64/%s: decoded %q, want %q", c.name, got, c.want)
		case c.ok:
			v.Outcome, v.Decoded = outcomeOK, b64(got)
		}
		out = append(out, v)
	}
	return sortByName(out, func(v base64Vector) string { return v.Name })
}

// envelopeCase is one envelope case: the envelope text, the keys it is
// verified under, and whether it verifies.
type envelopeCase struct {
	name     string
	envelope string
	keys     []ed25519.PublicKey
	ok       bool
}

// envelopeHash returns the delivery hash every envelope case signs: the
// first candidate digest whose seedA signature's standard encoding holds a
// '+' or a '/', so the URL-safe case differs from the canonical one.
func envelopeHash(t testing.TB) (string, []byte) {
	t.Helper()
	priv := privateKey(seedAHex)
	for i := 0; i < 64; i++ {
		sum := sha256.Sum256([]byte("envelope vector " + strconv.Itoa(i)))
		sig := ed25519.Sign(priv, sum[:])
		if strings.ContainsAny(b64(sig), "+/") {
			return "sha256:" + hex.EncodeToString(sum[:]), sig
		}
	}
	t.Fatal("no candidate digest yields a signature whose encoding holds + or /")
	return "", nil
}

// envelopeVectors builds the envelope section and checks each case.
func envelopeVectors(t *testing.T) []envelopeVector {
	hash, sig := envelopeHash(t)
	cases := append(verifyingEnvelopes(t, hash, sig), refusedEnvelopes(hash, sig)...)
	out := make([]envelopeVector, 0, len(cases))
	names := make([]string, 0, len(cases))
	for _, c := range cases {
		out = append(out, c.vector(t, hash))
		names = append(names, c.name)
	}
	checkUniqueNames(t, "envelope", names)
	return sortByName(out, func(v envelopeVector) string { return v.Name })
}

// vector verifies the envelope with sign.RegistryManagedKey and asserts the
// declared outcome.
func (c envelopeCase) vector(t *testing.T, hash string) envelopeVector {
	v := envelopeVector{Name: c.name, Envelope: c.envelope, SignedHash: hash, Keys: keysB64(c.keys), Outcome: codeSigInvalid}
	id, err := sign.RegistryManagedKey{Trusted: c.keys}.VerifiedKeyID(context.Background(), hash, c.envelope)
	switch {
	case c.ok && err != nil:
		t.Errorf("envelope/%s: Go refused an envelope the case declares verifying: %v", c.name, err)
	case !c.ok && err == nil:
		t.Errorf("envelope/%s: Go verified an envelope the case declares refused", c.name)
	case c.ok:
		v.Outcome, v.KeyID = outcomeOK, id
	}
	return v
}

// env encodes an envelope from raw member text.
func env(members ...member) string { return object(members).String() }

// m is a member whose name is encoded and whose value is raw JSON text.
func m(name, value string) member { return member{str(name), value} }

// verifyingEnvelopes are the envelope cases that verify under §4.7.9.
func verifyingEnvelopes(t *testing.T, hash string, sig []byte) []envelopeCase {
	pubA, pubB := publicKey(seedAHex), publicKey(seedBHex)
	idA := str(sign.KeyIDFor(pubA))
	sigA := str(b64(sig))
	valid, err := sign.RegistryManagedKey{PrivateKey: privateKey(seedAHex)}.Sign(context.Background(), hash)
	if err != nil {
		t.Fatalf("sign envelope hash: %v", err)
	}
	hashBytes, _ := hex.DecodeString(strings.TrimPrefix(hash, "sha256:"))
	sigB := str(b64(ed25519.Sign(privateKey(seedBHex), hashBytes)))
	a := []ed25519.PublicKey{pubA}
	return []envelopeCase{
		{"valid", valid, a, true},
		{"signature by a second key under a key_id naming the first",
			env(m("key_id", idA), m("signature", sigB)), []ed25519.PublicKey{pubA, pubB}, true},
		{"absent key_id", env(m("signature", sigA)), a, true},
		{"null key_id", env(m("key_id", "null"), m("signature", sigA)), a, true},
		{"unknown key_id", env(m("key_id", str("0000000000000000")), m("signature", sigA)), a, true},
		{"mistyped Key_ID is ignored", env(m("key_id", idA), m("signature", sigA), m("Key_ID", "5")), a, true},
		{"repeated signature whose later value is valid",
			env(m("key_id", idA), m("signature", str("AAAA")), m("signature", sigA)), a, true},
		{"envelope nested exactly 64 levels",
			env(m("key_id", idA), m("signature", sigA), m("x", nest(63))), a, true},
		{"unknown member holding a 5000-digit integer",
			env(m("key_id", idA), m("signature", sigA), m("x", bigInt)), a, true},
	}
}

// refusedEnvelopes are the envelope cases refused with
// materialize.signature_invalid.
func refusedEnvelopes(hash string, sig []byte) []envelopeCase {
	pubA, pubB := publicKey(seedAHex), publicKey(seedBHex)
	idA := str(sign.KeyIDFor(pubA))
	enc := b64(sig)
	sigA := str(enc)
	a := []ed25519.PublicKey{pubA}
	withSig := func(value string) string { return env(m("key_id", idA), m("signature", value)) }
	tampered := append([]byte(nil), sig...)
	tampered[0] ^= 0x01
	cases := []envelopeCase{
		{"tampered signature", withSig(str(b64(tampered))), a, false},
		{"wrong key set", withSig(sigA), []ed25519.PublicKey{pubB}, false},
		{"malformed JSON", `{"signature":`, a, false},
		{"JSON null", "null", a, false},
		{"JSON array", "[]", a, false},
		{"JSON string", str(enc), a, false},
		{"no signature", env(m("key_id", idA)), a, false},
		{"numeric signature", withSig("5"), a, false},
		{"null signature", withSig("null"), a, false},
		{"numeric key_id", env(m("key_id", "5"), m("signature", sigA)), a, false},
		{"SIGNATURE alone", env(m("key_id", idA), m("SIGNATURE", sigA)), a, false},
		{"signature with an escaped line break", withSig(`"` + enc[:4] + `\r\n` + enc[4:] + `"`), a, false},
		{"unpadded signature", withSig(str(strings.TrimRight(enc, "="))), a, false},
		{"signature in the URL-safe alphabet", withSig(str(strings.NewReplacer("+", "-", "/", "_").Replace(enc))), a, false},
		{"signature with a non-zero pad bit", withSig(str(flipPadBit(enc))), a, false},
		{"canonical 63-byte signature", withSig(str(b64(sig[:63]))), a, false},
		{"repeated signature whose later value is invalid",
			env(m("key_id", idA), m("signature", sigA), m("signature", str("AAAA"))), a, false},
		{"leading byte order mark", "\ufeff" + withSig(sigA), a, false},
		{"NaN in an unknown member", env(m("key_id", idA), m("signature", sigA), m("x", "NaN")), a, false},
		{"envelope nested 65 levels", env(m("key_id", idA), m("signature", sigA), m("x", nest(64))), a, false},
		{"key_id holding an unpaired surrogate", env(m("key_id", `"\ud800"`), m("signature", sigA)), a, false},
		{"repeated unknown member whose earlier value holds a surrogate",
			env(m("key_id", idA), m("signature", sigA), m("x", `"\ud800"`), m("x", `"ok"`)), a, false},
		{"repeated unknown member whose earlier value nests 65 levels",
			env(m("key_id", idA), m("signature", sigA), m("x", nest(64)), m("x", "1")), a, false},
	}
	return cases
}

// flipPadBit sets the lowest pad bit of a 64-byte signature's encoding: the
// character at index 85 carries the last 2 data bits and 4 zero pad bits, and
// its alphabet neighbour differs from it only in the lowest bit.
func flipPadBit(enc string) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	i := strings.IndexByte(alphabet, enc[85])
	return enc[:85] + string(alphabet[i^1]) + enc[86:]
}
