package sign

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/manifest"
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
// policy other than never, whatever sensitivity the response declares. A
// low-sensitivity artifact with a signature that does not validate is refused.
func TestEnforceVerification_PresentSignatureVerifiedRegardlessOfSensitivity(t *testing.T) {
	t.Parallel()
	key := newTestKey(t)
	err := EnforceVerification(context.Background(), PolicyMediumAndAbove, key, manifest.SensitivityLow, testHash, "noop:"+testHash)
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("medium-and-above, low, invalid signature = %v, want ErrSignatureInvalid", err)
	}
	sig, err := key.Sign(context.Background(), testHash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := EnforceVerification(context.Background(), PolicyMediumAndAbove, key, manifest.SensitivityLow, testHash, sig); err != nil {
		t.Errorf("medium-and-above, low, valid signature = %v, want nil", err)
	}
}

// Spec: §4.7.9 — under always a missing signature aborts the load at every
// sensitivity.
// Matrix: §6.10 (materialize.signature_missing)
func TestEnforceVerification_MissingSignatureUnderAlways(t *testing.T) {
	t.Parallel()
	err := EnforceVerification(context.Background(), PolicyAlways, newTestKey(t), manifest.SensitivityLow, testHash, "")
	if !errors.Is(err, ErrSignatureMissing) {
		t.Fatalf("always, low, no signature = %v, want ErrSignatureMissing", err)
	}
}

// Spec: §4.7.9 — never checks nothing, including a signature the response
// carries.
func TestEnforceVerification_NeverSkipsAPresentInvalidSignature(t *testing.T) {
	t.Parallel()
	key := newTestKey(t)
	for _, sig := range []string{"", "noop:" + testHash, "not an envelope"} {
		if err := EnforceVerification(context.Background(), PolicyNever, key, manifest.SensitivityHigh, testHash, sig); err != nil {
			t.Errorf("never, signature %q = %v, want nil", sig, err)
		}
	}
}

// Spec: §4.7.9 — the medium-and-above policy keeps its missing-signature arm:
// a missing signature is refused at medium and high and admitted at low, and
// a present signature is verified at every sensitivity.
func TestEnforceVerification_PolicyMediumAndAbove(t *testing.T) {
	t.Parallel()
	key := newTestKey(t)
	valid, err := key.Sign(context.Background(), testHash)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	cases := []struct {
		s         manifest.Sensitivity
		signature string
		wantErr   error
	}{
		{manifest.SensitivityLow, "", nil},
		{manifest.SensitivityMedium, valid, nil},
		{manifest.SensitivityHigh, valid, nil},
		{manifest.SensitivityMedium, "", ErrSignatureMissing},
		{manifest.SensitivityHigh, "", ErrSignatureMissing},
		{manifest.SensitivityHigh, "noop:wrong", ErrSignatureInvalid},
	}
	for _, c := range cases {
		err := EnforceVerification(context.Background(), PolicyMediumAndAbove, key, c.s, testHash, c.signature)
		if c.wantErr == nil {
			if err != nil {
				t.Errorf("(s=%s sig=%q) got %v, want nil", c.s, c.signature, err)
			}
		} else if !errors.Is(err, c.wantErr) {
			t.Errorf("(s=%s sig=%q) got %v, want %v", c.s, c.signature, err, c.wantErr)
		}
	}
}
