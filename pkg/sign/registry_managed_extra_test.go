package sign_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
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
