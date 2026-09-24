package sign

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
)

// testHash is a well-formed sha256 content hash the registry-managed key can
// sign; decodeContentHash rejects anything shorter.
const testHash = "sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"

// newTestKey returns a registry-managed key generated for one test.
func newTestKey(t *testing.T) RegistryManagedKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return RegistryManagedKey{PrivateKey: priv, PublicKey: pub}
}

// Spec: §4.7.9 — the noop provider refuses the placeholder its own Sign
// produced, because that value is derived from a content hash served in the
// clear and anyone could mint it.
// Matrix: §6.10 (materialize.signature_invalid)
func TestNoopVerifyRefusesItsOwnSignature(t *testing.T) {
	t.Parallel()
	p := Noop{}
	sig, err := p.Sign(context.Background(), testHash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	err = p.Verify(context.Background(), testHash, sig)
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("Verify(own signature) = %v, want ErrSignatureInvalid", err)
	}
	if !strings.Contains(err.Error(), "PODIUM_VERIFY_SIGNATURES=never") {
		t.Errorf("refusal %q does not name the never policy as the way to skip verification", err)
	}
}

// Spec: §4.7.9 — Sign still returns "noop:" + hash, which the §8.3 audit
// anchor and the ingest tests depend on.
func TestNoopSignUnchanged(t *testing.T) {
	t.Parallel()
	sig, err := Noop{}.Sign(context.Background(), testHash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if want := "noop:" + testHash; sig != want {
		t.Errorf("Sign = %q, want %q", sig, want)
	}
}

// Spec: §4.7.9 — a signature the response carries is verified under any
// policy other than never: a signature that does not validate is refused and
// one that validates is admitted.
func TestEnforceVerification_AlwaysVerifiesAPresentSignature(t *testing.T) {
	t.Parallel()
	key := newTestKey(t)
	err := EnforceVerification(context.Background(), PolicyAlways, key, testHash, "noop:"+testHash)
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("always, invalid signature = %v, want ErrSignatureInvalid", err)
	}
	sig, err := key.Sign(context.Background(), testHash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := EnforceVerification(context.Background(), PolicyAlways, key, testHash, sig); err != nil {
		t.Errorf("always, valid signature = %v, want nil", err)
	}
}

// Spec: §4.7.9 — under always a missing signature aborts the load.
// Matrix: §6.10 (materialize.signature_missing)
func TestEnforceVerification_MissingSignatureUnderAlways(t *testing.T) {
	t.Parallel()
	err := EnforceVerification(context.Background(), PolicyAlways, newTestKey(t), testHash, "")
	if !errors.Is(err, ErrSignatureMissing) {
		t.Fatalf("always, no signature = %v, want ErrSignatureMissing", err)
	}
}

// Spec: §4.7.9 — never checks nothing, including a signature the response
// carries.
func TestEnforceVerification_NeverSkipsAPresentInvalidSignature(t *testing.T) {
	t.Parallel()
	key := newTestKey(t)
	for _, sig := range []string{"", "noop:" + testHash, "not an envelope"} {
		if err := EnforceVerification(context.Background(), PolicyNever, key, testHash, sig); err != nil {
			t.Errorf("never, signature %q = %v, want nil", sig, err)
		}
	}
}
