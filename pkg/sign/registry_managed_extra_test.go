package sign_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/sign"
)

// Verify falls back to deriving the public key from the private key
// when PublicKey is not explicitly set.
func TestRegistryManagedKey_DerivesPublicFromPrivate(t *testing.T) {
	t.Parallel()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	signer := sign.RegistryManagedKey{PrivateKey: priv}
	hash := "sha256:" + hashHex("body")
	envelope, err := signer.Sign(context.Background(), hash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	// Verifier has only the private key; should derive public.
	verifier := sign.RegistryManagedKey{PrivateKey: priv}
	if err := verifier.Verify(context.Background(), hash, envelope); err != nil {
		t.Errorf("Verify with derived public: %v", err)
	}
	// Force-set the public key from generated pub for explicit form.
	verifier2 := sign.RegistryManagedKey{PublicKey: pub}
	if err := verifier2.Verify(context.Background(), hash, envelope); err != nil {
		t.Errorf("Verify with explicit public: %v", err)
	}
}

// Verify rejects an envelope that doesn't parse as JSON.
func TestRegistryManagedKey_VerifyMalformedEnvelope(t *testing.T) {
	t.Parallel()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	v := sign.RegistryManagedKey{PrivateKey: priv}
	if err := v.Verify(context.Background(), "sha256:"+hashHex("x"), "not json"); !errors.Is(err, sign.ErrSignatureInvalid) {
		t.Errorf("err = %v", err)
	}
}

// Verify rejects a non-base64 signature field.
func TestRegistryManagedKey_VerifyBadSignatureBase64(t *testing.T) {
	t.Parallel()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	v := sign.RegistryManagedKey{PrivateKey: priv}
	bad, _ := json.Marshal(map[string]string{"signature": "!!!not base64"})
	if err := v.Verify(context.Background(), "sha256:"+hashHex("x"), string(bad)); !errors.Is(err, sign.ErrSignatureInvalid) {
		t.Errorf("err = %v", err)
	}
}

// Verify rejects a bogus content hash.
func TestRegistryManagedKey_VerifyBadContentHash(t *testing.T) {
	t.Parallel()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	v := sign.RegistryManagedKey{PrivateKey: priv}
	// Build a well-formed envelope.
	sig := ed25519.Sign(priv, []byte("ignored"))
	env, _ := json.Marshal(map[string]string{"signature": base64.StdEncoding.EncodeToString(sig)})
	if err := v.Verify(context.Background(), "not-a-valid-hash", string(env)); !errors.Is(err, sign.ErrSignatureInvalid) {
		t.Errorf("err = %v", err)
	}
}

// Verify rejects when the envelope's signature does not check out.
func TestRegistryManagedKey_VerifyTamperedSignature(t *testing.T) {
	t.Parallel()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer := sign.RegistryManagedKey{PrivateKey: priv}
	hash := "sha256:" + hashHex("body")
	env, _ := signer.Sign(context.Background(), hash)
	// Modify the signature byte string.
	var parsed map[string]string
	_ = json.Unmarshal([]byte(env), &parsed)
	parsed["signature"] = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	tampered, _ := json.Marshal(parsed)
	if err := signer.Verify(context.Background(), hash, string(tampered)); !errors.Is(err, sign.ErrSignatureInvalid) {
		t.Errorf("err = %v", err)
	}
}

// PublicKeyFromBase64 round-trips a generated public key and the decoded key
// verifies an envelope the matching private key produced. This is the
// consumer-side path: the registry publishes its base64 public key, the
// consumer decodes it and constructs a RegistryManagedKey for Verify.
func TestPublicKeyFromBase64_RoundTrip(t *testing.T) {
	t.Parallel()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString(pub)
	decoded, err := sign.PublicKeyFromBase64(encoded)
	if err != nil {
		t.Fatalf("PublicKeyFromBase64: %v", err)
	}
	if !decoded.Equal(pub) {
		t.Fatalf("decoded key != original")
	}
	hash := "sha256:" + hashHex("body")
	env, err := sign.RegistryManagedKey{PrivateKey: priv}.Sign(context.Background(), hash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := (sign.RegistryManagedKey{PublicKey: decoded}).Verify(context.Background(), hash, env); err != nil {
		t.Errorf("Verify with decoded public key: %v", err)
	}
}

// PublicKeyFromBase64 rejects malformed base64 and wrong-length keys so a
// misconfigured PODIUM_SIGNATURE_VERIFY_KEY fails loudly at startup rather than
// silently producing a verifier that rejects every signature.
func TestPublicKeyFromBase64_Rejects(t *testing.T) {
	t.Parallel()
	if _, err := sign.PublicKeyFromBase64("!!!not base64"); err == nil {
		t.Error("malformed base64 accepted")
	}
	// 16 bytes is valid base64 but the wrong length for an Ed25519 public key.
	short := base64.StdEncoding.EncodeToString(make([]byte, 16))
	if _, err := sign.PublicKeyFromBase64(short); err == nil {
		t.Error("wrong-length key accepted")
	}
}

func hashHex(s string) string {
	return "" + // placeholder; concrete hash bytes don't matter for envelope tests
		"deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
}

// genKeys returns n fresh Ed25519 keypairs.
func genKeys(t *testing.T, n int) ([]ed25519.PublicKey, []ed25519.PrivateKey) {
	t.Helper()
	pubs := make([]ed25519.PublicKey, n)
	privs := make([]ed25519.PrivateKey, n)
	for i := range pubs {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("GenerateKey: %v", err)
		}
		pubs[i], privs[i] = pub, priv
	}
	return pubs, privs
}

// signWithKeyID signs hash under priv and rewrites the envelope's key_id to
// id, or removes it when id is empty.
func signWithKeyID(t *testing.T, priv ed25519.PrivateKey, hash, id string) string {
	t.Helper()
	envelope, err := sign.RegistryManagedKey{PrivateKey: priv}.Sign(context.Background(), hash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	var env map[string]string
	if err := json.Unmarshal([]byte(envelope), &env); err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if id == "" {
		delete(env, "key_id")
	} else {
		env["key_id"] = id
	}
	out, _ := json.Marshal(env)
	return string(out)
}

// Spec: §4.7.9 — the key_id is the lowercase hex of the first 8 bytes of the
// SHA-256 digest of the 32-byte public key, deterministic and distinct per key.
func TestKeyIDFor_StableShortHex(t *testing.T) {
	t.Parallel()
	key := make(ed25519.PublicKey, ed25519.PublicKeySize)
	for i := range key {
		key[i] = byte(i)
	}
	// sha256(0x00..0x1f)[:8], computed independently from the §4.7.9
	// definition.
	if got, want := sign.KeyIDFor(key), "630dcd2966c43366"; got != want {
		t.Errorf("KeyIDFor(0x00..0x1f) = %q, want %q", got, want)
	}
	other := make(ed25519.PublicKey, ed25519.PublicKeySize)
	if sign.KeyIDFor(other) == sign.KeyIDFor(key) {
		t.Error("distinct keys share a key_id")
	}
}

// Spec: §4.7.9 — Verify accepts an envelope under the signing key and one
// under a Trusted key; a verifier built from Trusted alone verifies; one with
// no key returns ErrRegistryManagedUnavailable.
func TestRegistryManagedKey_VerifiesUnderAnyKeyOfTheSet(t *testing.T) {
	t.Parallel()
	pubs, privs := genKeys(t, 2)
	hash := "sha256:" + hashHex("body")
	current := sign.RegistryManagedKey{PrivateKey: privs[0], Trusted: []ed25519.PublicKey{pubs[1]}}
	underCurrent, _ := current.Sign(context.Background(), hash)
	underRetired := signWithKeyID(t, privs[1], hash, sign.KeyIDFor(pubs[1]))
	for name, env := range map[string]string{"signing key": underCurrent, "trusted key": underRetired} {
		if err := current.Verify(context.Background(), hash, env); err != nil {
			t.Errorf("registry verify under %s: %v", name, err)
		}
		consumer := sign.RegistryManagedKey{Trusted: pubs}
		if err := consumer.Verify(context.Background(), hash, env); err != nil {
			t.Errorf("Trusted-only verify under %s: %v", name, err)
		}
	}
	if err := (sign.RegistryManagedKey{}).Verify(context.Background(), hash, underCurrent); !errors.Is(err, sign.ErrRegistryManagedUnavailable) {
		t.Errorf("empty set Verify = %v, want ErrRegistryManagedUnavailable", err)
	}
	if _, err := (sign.RegistryManagedKey{}).VerifiedKeyID(context.Background(), hash, underCurrent); !errors.Is(err, sign.ErrRegistryManagedUnavailable) {
		t.Errorf("empty set VerifiedKeyID = %v, want ErrRegistryManagedUnavailable", err)
	}
}

// Spec: §4.7.9 — the key_id is unauthenticated and never on its own refuses an
// envelope a trusted key verifies: an id naming a key outside the set, an id
// naming another trusted key, and an absent id are all accepted, and
// VerifiedKeyID reports the key that verified rather than the claimed id.
func TestRegistryManagedKey_KeyIDNeverRefuses(t *testing.T) {
	t.Parallel()
	pubs, privs := genKeys(t, 3)
	hash := "sha256:" + hashHex("body")
	verifier := sign.RegistryManagedKey{Trusted: pubs[:2]}
	cases := map[string]string{
		"id outside the set":   signWithKeyID(t, privs[1], hash, sign.KeyIDFor(pubs[2])),
		"id of another member": signWithKeyID(t, privs[1], hash, sign.KeyIDFor(pubs[0])),
		"no id":                signWithKeyID(t, privs[1], hash, ""),
	}
	for name, env := range cases {
		got, err := verifier.VerifiedKeyID(context.Background(), hash, env)
		if err != nil {
			t.Errorf("%s: VerifiedKeyID: %v", name, err)
			continue
		}
		if want := sign.KeyIDFor(pubs[1]); got != want {
			t.Errorf("%s: VerifiedKeyID = %q, want the verifying key's %q", name, got, want)
		}
	}
}

// Spec: §4.7.9 — Sign embeds the key_id of PrivateKey's public half even when
// the PublicKey field holds a different key, and CurrentKeyID matches it.
func TestRegistryManagedKey_SignEmbedsSigningKeyID(t *testing.T) {
	t.Parallel()
	pubs, privs := genKeys(t, 2)
	hash := "sha256:" + hashHex("body")
	k := sign.RegistryManagedKey{PrivateKey: privs[0], PublicKey: pubs[1]}
	envelope, err := k.Sign(context.Background(), hash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	var env map[string]string
	if err := json.Unmarshal([]byte(envelope), &env); err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if want := sign.KeyIDFor(pubs[0]); env["key_id"] != want || k.CurrentKeyID() != want {
		t.Errorf("key_id = %q, CurrentKeyID = %q; want %q", env["key_id"], k.CurrentKeyID(), want)
	}
	if got := (sign.RegistryManagedKey{PrivateKey: privs[0]}).CurrentKeyID(); got != sign.KeyIDFor(pubs[0]) {
		t.Errorf("CurrentKeyID with PrivateKey alone = %q", got)
	}
	if got := (sign.RegistryManagedKey{Trusted: pubs}).CurrentKeyID(); got != "" {
		t.Errorf("CurrentKeyID with no private key = %q, want empty", got)
	}
}

// Spec: §4.7.9 — VerifyKeyIDs lists the verification-only keys in set order,
// omits the signing key when Trusted repeats it, and lists every key of a
// provider with no private key.
func TestRegistryManagedKey_VerifyKeyIDs(t *testing.T) {
	t.Parallel()
	pubs, privs := genKeys(t, 3)
	registry := sign.RegistryManagedKey{PrivateKey: privs[0], PublicKey: pubs[0], Trusted: []ed25519.PublicKey{pubs[1], pubs[0], pubs[2], pubs[1]}}
	want := []string{sign.KeyIDFor(pubs[1]), sign.KeyIDFor(pubs[2])}
	if got := registry.VerifyKeyIDs(); !reflect.DeepEqual(got, want) {
		t.Errorf("registry VerifyKeyIDs = %v, want %v", got, want)
	}
	consumer := sign.RegistryManagedKey{Trusted: pubs}
	wantAll := []string{sign.KeyIDFor(pubs[0]), sign.KeyIDFor(pubs[1]), sign.KeyIDFor(pubs[2])}
	if got := consumer.VerifyKeyIDs(); !reflect.DeepEqual(got, wantAll) {
		t.Errorf("consumer VerifyKeyIDs = %v, want %v", got, wantAll)
	}
	if got := (sign.RegistryManagedKey{PrivateKey: privs[0]}).VerifyKeyIDs(); len(got) != 0 {
		t.Errorf("signing-key-only VerifyKeyIDs = %v, want none", got)
	}
}

// Spec: §4.7.9 — a provider whose PublicKey also appears in Trusted verifies
// an envelope under that key and reports its id, and a set made only of that
// repeated key still refuses an envelope from a third key.
func TestRegistryManagedKey_DuplicatedKey(t *testing.T) {
	t.Parallel()
	pubs, privs := genKeys(t, 2)
	hash := "sha256:" + hashHex("body")
	k := sign.RegistryManagedKey{PublicKey: pubs[0], Trusted: []ed25519.PublicKey{pubs[0]}}
	good := signWithKeyID(t, privs[0], hash, sign.KeyIDFor(pubs[0]))
	if got, err := k.VerifiedKeyID(context.Background(), hash, good); err != nil || got != sign.KeyIDFor(pubs[0]) {
		t.Errorf("VerifiedKeyID = %q, %v; want %q", got, err, sign.KeyIDFor(pubs[0]))
	}
	third := signWithKeyID(t, privs[1], hash, sign.KeyIDFor(pubs[0]))
	if err := k.Verify(context.Background(), hash, third); !errors.Is(err, sign.ErrSignatureInvalid) {
		t.Errorf("third-key envelope = %v, want ErrSignatureInvalid", err)
	}
}

// signedEnvelopeParts signs a fixed hash under a fresh key and returns the
// verifier holding that key, the hash, the envelope's base64 signature, and
// its key_id.
func signedEnvelopeParts(t *testing.T) (sign.RegistryManagedKey, string, string, string) {
	t.Helper()
	_, privs := genKeys(t, 1)
	hash := "sha256:" + hashHex("body")
	envelope, err := sign.RegistryManagedKey{PrivateKey: privs[0]}.Sign(context.Background(), hash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	var env map[string]string
	if err := json.Unmarshal([]byte(envelope), &env); err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	return sign.RegistryManagedKey{PrivateKey: privs[0]}, hash, env["signature"], env["key_id"]
}

// Spec: §4.7.9 — a verifier reads signature and key_id by exact name, treats a
// null member as absent, refuses a non-string value, and ignores every other
// member, including a case variant of a known name.
func TestRegistryManagedKey_EnvelopeMemberRules(t *testing.T) {
	t.Parallel()
	v, hash, sig, keyID := signedEnvelopeParts(t)
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	cases := []struct {
		name     string
		envelope string
		ok       bool
	}{
		{"as signed", `{"key_id":` + q(keyID) + `,"signature":` + q(sig) + `}`, true},
		{"null key_id", `{"key_id":null,"signature":` + q(sig) + `}`, true},
		{"unknown case variant", `{"Key_ID":5,"signature":` + q(sig) + `}`, true},
		{"later duplicate wins", `{"signature":5,"signature":` + q(sig) + `}`, true},
		{"numeric key_id", `{"key_id":5,"signature":` + q(sig) + `}`, false},
		{"null signature", `{"key_id":` + q(keyID) + `,"signature":null}`, false},
		{"signature absent", `{"key_id":` + q(keyID) + `}`, false},
		{"case variant only", `{"SIGNATURE":` + q(sig) + `}`, false},
		{"array signature", `{"signature":[` + q(sig) + `]}`, false},
		{"top-level array", `[` + q(sig) + `]`, false},
		{"trailing data", `{"signature":` + q(sig) + `} x`, false},
		{"canonical but short", `{"signature":` + q(base64.StdEncoding.EncodeToString(make([]byte, 32))) + `}`, false},
	}
	for _, tc := range cases {
		_, err := v.VerifiedKeyID(context.Background(), hash, tc.envelope)
		if tc.ok && err != nil {
			t.Errorf("%s: VerifiedKeyID = %v, want verified", tc.name, err)
		}
		if !tc.ok && !errors.Is(err, sign.ErrSignatureInvalid) {
			t.Errorf("%s: VerifiedKeyID = %v, want ErrSignatureInvalid", tc.name, err)
		}
	}
}

// Spec: §4.7.9
func TestRegistryManagedKey_VerifyRefusesNonCanonicalSignature(t *testing.T) {
	t.Parallel()
	v, hash, sig, _ := signURLSensitive(t)
	raw, err := base64.StdEncoding.DecodeString(sig)
	if err != nil || len(raw) != ed25519.SignatureSize {
		t.Fatalf("decode signature: %v (%d bytes)", err, len(raw))
	}
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	padIdx := strings.IndexByte(alphabet, sig[85]) ^ 1
	padBit := sig[:85] + string(alphabet[padIdx]) + sig[86:]
	variants := map[string]string{
		"escaped CRLF": sig[:4] + "\r\n" + sig[4:],
		"trailing LF":  sig + "\n",
		"no padding":   strings.TrimSuffix(sig, "=="),
		"URL-safe":     base64.URLEncoding.EncodeToString(raw),
		"pad bit set":  padBit,
	}
	for _, name := range []string{"escaped CRLF", "pad bit set"} {
		got, err := base64.StdEncoding.DecodeString(variants[name])
		if err != nil || !reflect.DeepEqual(got, raw) {
			t.Fatalf("%s: StdEncoding decodes to %x, %v; want the original signature", name, got, err)
		}
	}
	for name, alt := range variants {
		env, _ := json.Marshal(map[string]string{"signature": alt})
		if _, err := v.VerifiedKeyID(context.Background(), hash, string(env)); !errors.Is(err, sign.ErrSignatureInvalid) {
			t.Errorf("%s: VerifiedKeyID = %v, want ErrSignatureInvalid", name, err)
		}
	}
}

// signURLSensitive signs digests under one key until the standard base64
// encoding of the signature holds a '+' or '/', so its URL-safe encoding
// differs from it, and returns the verifier, the digest, the signature, and
// its key_id.
func signURLSensitive(t *testing.T) (sign.RegistryManagedKey, string, string, string) {
	t.Helper()
	_, privs := genKeys(t, 1)
	key := sign.RegistryManagedKey{PrivateKey: privs[0]}
	for i := 0; i < 1000; i++ {
		hash := "sha256:" + fmt.Sprintf("%064x", i)
		envelope, err := key.Sign(context.Background(), hash)
		if err != nil {
			t.Fatalf("Sign: %v", err)
		}
		var env map[string]string
		if err := json.Unmarshal([]byte(envelope), &env); err != nil {
			t.Fatalf("parse envelope: %v", err)
		}
		if strings.ContainsAny(env["signature"], "+/") {
			return key, hash, env["signature"], env["key_id"]
		}
	}
	t.Fatal("no signature with '+' or '/' in 1000 digests")
	return sign.RegistryManagedKey{}, "", "", ""
}
