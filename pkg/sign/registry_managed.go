package sign

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lennylabs/podium/pkg/spi"
)

// RegistryManagedKey implements §4.7.9's registry-managed key model. A
// registry deployment holds one Ed25519 signing keypair, loaded from the key
// file at PODIUM_SIGN_KEY_PATH, and every process serving one store signs
// under it across every tenant. Sign produces a detached signature under
// PrivateKey alone. Verify accepts a signature any key of the verification key
// set verifies: PublicKey (or the key derived from PrivateKey) followed by
// Trusted. A rotation moves the retired public key into Trusted, so envelopes
// it made keep verifying while new ones are made under the new key.
//
// Signature envelope: JSON { "key_id", "signature" }. The key_id names the
// signing key so a verifier tries that key first; it is not covered by the
// signature and never on its own refuses an envelope.
//
// Spec: §4.7.9.
type RegistryManagedKey struct {
	// PrivateKey is the Ed25519 signing key (64 bytes). Required for Sign.
	PrivateKey ed25519.PrivateKey
	// PublicKey is the signing key's Ed25519 public half (32 bytes). Verify
	// derives it from PrivateKey when this is unset.
	PublicKey ed25519.PublicKey
	// Trusted holds the verification-only keys: the key file's verify:
	// lines on the registry, and the whole resolved set on a consumer, which
	// holds no signing key.
	Trusted []ed25519.PublicKey
}

// ID returns "registry-managed".
func (RegistryManagedKey) ID() string { return "registry-managed" }

// PublicKeyFromBase64 decodes a standard-base64-encoded Ed25519 public
// key (the 32-byte form the key file and PODIUM_SIGNATURE_VERIFY_KEY carry)
// into an ed25519.PublicKey. PublicKeysFromList and ParseKeyFile decode each
// key of a §4.7.9 verification key set through it.
//
// Spec: §4.7.9.
func PublicKeyFromBase64(s string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("decode public key: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("public key is %d bytes, want %d", len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

// ErrRegistryManagedUnavailable signals the provider holds no key for the
// operation. Structured per §9.3. Sign returns it when PrivateKey is unset;
// Verify and VerifiedKeyID return it when the verification key set is empty.
var ErrRegistryManagedUnavailable = &spi.Error{Code: "config.signature_provider_unavailable", Message: "sign: registry-managed key not configured"}

// registryManagedEnvelope is the JSON encoding of a registry-managed
// signature. Compact, no version field — the format is internal to
// one deployment and changes via the §4.7.9 rotation flow.
type registryManagedEnvelope struct {
	KeyID     string `json:"key_id,omitempty"`
	Signature string `json:"signature"`
}

// KeyIDFor returns the §4.7.9 key_id of pub: the lowercase hex encoding of the
// first 8 bytes of the SHA-256 digest of the 32-byte public key.
//
// Spec: §4.7.9.
func KeyIDFor(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return hex.EncodeToString(sum[:8])
}

// Sign signs contentHash with the configured Ed25519 private key. The
// returned envelope carries the base64-encoded signature and the key_id of
// the signing key's public half, derived from PrivateKey whatever PublicKey
// holds, so the id always names the key that signed.
//
// Spec: §4.7.9.
func (k RegistryManagedKey) Sign(_ context.Context, contentHash string) (string, error) {
	if len(k.PrivateKey) == 0 {
		return "", ErrRegistryManagedUnavailable
	}
	hashBytes, err := decodeContentHash(contentHash)
	if err != nil {
		return "", err
	}
	sig := ed25519.Sign(k.PrivateKey, hashBytes)
	body, err := json.Marshal(registryManagedEnvelope{
		KeyID:     KeyIDFor(k.PrivateKey.Public().(ed25519.PublicKey)),
		Signature: base64.StdEncoding.EncodeToString(sig),
	})
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// Verify checks that signature is a valid Ed25519 signature for contentHash
// under a key of the verification key set. It is VerifiedKeyID without the
// key_id of the verifying key.
//
// Spec: §4.7.9.
func (k RegistryManagedKey) Verify(ctx context.Context, contentHash, signature string) error {
	_, err := k.VerifiedKeyID(ctx, contentHash, signature)
	return err
}

// VerifiedKeyID verifies signature over contentHash under the verification key
// set and returns the key_id of the key that verified it. It tries the key the
// envelope's key_id names first, when the set holds one, and then every other
// key, because the key_id is unauthenticated: a forged or absent id changes
// only the order of the attempts. A signature no key verifies is refused with
// ErrSignatureInvalid, and an empty set returns ErrRegistryManagedUnavailable.
//
// Spec: §4.7.9.
func (k RegistryManagedKey) VerifiedKeyID(_ context.Context, contentHash, signature string) (string, error) {
	candidates := k.candidates()
	if len(candidates) == 0 {
		return "", ErrRegistryManagedUnavailable
	}
	var env registryManagedEnvelope
	if err := json.Unmarshal([]byte(signature), &env); err != nil {
		return "", fmt.Errorf("%w: parse envelope: %v", ErrSignatureInvalid, err)
	}
	sig, err := base64.StdEncoding.DecodeString(env.Signature)
	if err != nil {
		return "", fmt.Errorf("%w: signature decode: %v", ErrSignatureInvalid, err)
	}
	hashBytes, err := decodeContentHash(contentHash)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrSignatureInvalid, err)
	}
	for _, pub := range namedKeyFirst(candidates, env.KeyID) {
		if ed25519.Verify(pub, hashBytes, sig) {
			return KeyIDFor(pub), nil
		}
	}
	return "", fmt.Errorf("%w: signature does not verify under any trusted key", ErrSignatureInvalid)
}

// CurrentKeyID returns the key_id Sign embeds, that of PrivateKey's public
// half, or "" when the provider holds no private key.
//
// Spec: §4.7.9.
func (k RegistryManagedKey) CurrentKeyID() string {
	if len(k.PrivateKey) == 0 {
		return ""
	}
	return KeyIDFor(k.PrivateKey.Public().(ed25519.PublicKey))
}

// VerifyKeyIDs returns the key_id of every verification-only key: each key of
// the set whose id differs from CurrentKeyID, in set order. With no private key
// every key of the set is verification-only. It reads only the provider's
// fields, so a key that verifies no stored row is still listed.
//
// Spec: §4.7.9.
func (k RegistryManagedKey) VerifyKeyIDs() []string {
	current := k.CurrentKeyID()
	var ids []string
	for _, pub := range k.candidates() {
		if id := KeyIDFor(pub); id != current {
			ids = append(ids, id)
		}
	}
	return ids
}

// signingPublicKey returns PublicKey, or the public half of PrivateKey when
// PublicKey is unset, or nil when the provider holds neither.
func (k RegistryManagedKey) signingPublicKey() ed25519.PublicKey {
	if len(k.PublicKey) > 0 {
		return k.PublicKey
	}
	if len(k.PrivateKey) > 0 {
		return k.PrivateKey.Public().(ed25519.PublicKey)
	}
	return nil
}

// candidates returns the verification key set: the signing public key when
// the provider holds one, then Trusted, with repeated keys dropped.
func (k RegistryManagedKey) candidates() []ed25519.PublicKey {
	all := k.Trusted
	if pub := k.signingPublicKey(); pub != nil {
		all = append([]ed25519.PublicKey{pub}, k.Trusted...)
	}
	out := make([]ed25519.PublicKey, 0, len(all))
	for _, pub := range all {
		if !containsKey(out, pub) {
			out = append(out, pub)
		}
	}
	return out
}

// namedKeyFirst returns keys with the key whose key_id equals id moved to the
// front, or keys unchanged when none matches.
func namedKeyFirst(keys []ed25519.PublicKey, id string) []ed25519.PublicKey {
	for i, pub := range keys {
		if id != "" && KeyIDFor(pub) == id {
			ordered := append([]ed25519.PublicKey{pub}, keys[:i]...)
			return append(ordered, keys[i+1:]...)
		}
	}
	return keys
}

// containsKey reports whether keys holds pub.
func containsKey(keys []ed25519.PublicKey, pub ed25519.PublicKey) bool {
	for _, k := range keys {
		if k.Equal(pub) {
			return true
		}
	}
	return false
}
