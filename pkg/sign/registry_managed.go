package sign

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

// RegistryManagedKey implements §4.7.9's per-org registry-managed
// signing key path. The registry holds an Ed25519 keypair per tenant
// (managed via the secret backend in production); Sign produces a
// detached signature, Verify checks it against the configured public
// key. Rotation is handled by the secret backend; the provider holds
// only the currently-active keypair.
//
// Signature envelope: JSON { "key_id", "signature" } so consumers can
// pin to a specific key fingerprint.
type RegistryManagedKey struct {
	// PrivateKey is the Ed25519 private key (64 bytes). Required for Sign.
	PrivateKey ed25519.PrivateKey
	// PublicKey is the Ed25519 public key (32 bytes). Required for Verify.
	// Sign derives the public key from PrivateKey when this is unset.
	PublicKey ed25519.PublicKey
	// KeyID is an opaque identifier for the keypair (typically a
	// fingerprint of the public key). Embedded in the signature
	// envelope so verifiers can reject signatures from rotated keys.
	KeyID string
}

// ID returns "registry-managed".
func (RegistryManagedKey) ID() string { return "registry-managed" }

// ErrRegistryManagedUnavailable signals the keypair is not configured.
// Sign returns this when PrivateKey is unset; Verify returns it when
// PublicKey is unset.
var ErrRegistryManagedUnavailable = errors.New("sign: registry-managed key not configured")

// registryManagedEnvelope is the JSON encoding of a registry-managed
// signature. Compact, no version field — the format is internal to
// one deployment and changes via the §4.7.9 rotation flow.
type registryManagedEnvelope struct {
	KeyID     string `json:"key_id,omitempty"`
	Signature string `json:"signature"`
}

// Sign signs contentHash with the configured Ed25519 private key.
// The returned envelope is a JSON object carrying the base64-encoded
// signature plus the key ID.
func (k RegistryManagedKey) Sign(contentHash string) (string, error) {
	if len(k.PrivateKey) == 0 {
		return "", ErrRegistryManagedUnavailable
	}
	hashBytes, err := decodeContentHash(contentHash)
	if err != nil {
		return "", err
	}
	sig := ed25519.Sign(k.PrivateKey, hashBytes)
	body, err := json.Marshal(registryManagedEnvelope{
		KeyID:     k.KeyID,
		Signature: base64.StdEncoding.EncodeToString(sig),
	})
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// Verify checks that signature is a valid Ed25519 signature for
// contentHash under the configured public key. Mismatched KeyID
// rejects with ErrSignatureInvalid so verifiers can detect rotation.
func (k RegistryManagedKey) Verify(contentHash, signature string) error {
	pub := k.PublicKey
	if len(pub) == 0 && len(k.PrivateKey) > 0 {
		pub = k.PrivateKey.Public().(ed25519.PublicKey)
	}
	if len(pub) == 0 {
		return ErrRegistryManagedUnavailable
	}
	var env registryManagedEnvelope
	if err := json.Unmarshal([]byte(signature), &env); err != nil {
		return fmt.Errorf("%w: parse envelope: %v", ErrSignatureInvalid, err)
	}
	if k.KeyID != "" && env.KeyID != "" && env.KeyID != k.KeyID {
		return fmt.Errorf("%w: key id %q does not match configured %q",
			ErrSignatureInvalid, env.KeyID, k.KeyID)
	}
	sig, err := base64.StdEncoding.DecodeString(env.Signature)
	if err != nil {
		return fmt.Errorf("%w: signature decode: %v", ErrSignatureInvalid, err)
	}
	hashBytes, err := decodeContentHash(contentHash)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSignatureInvalid, err)
	}
	if !ed25519.Verify(pub, hashBytes, sig) {
		return fmt.Errorf("%w: signature does not verify", ErrSignatureInvalid)
	}
	return nil
}
