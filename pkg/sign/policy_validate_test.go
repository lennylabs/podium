package sign

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/podium/pkg/manifest"
)

// spec: §6.2 / §4.7.9 — PODIUM_VERIFY_SIGNATURES is never |
// medium-and-above | always; any other value is invalid.
func TestValidPolicy(t *testing.T) {
	t.Parallel()
	valid := []VerificationPolicy{PolicyNever, PolicyMediumAndAbove, PolicyAlways}
	for _, p := range valid {
		if !ValidPolicy(p) {
			t.Errorf("ValidPolicy(%q) = false, want true", p)
		}
	}
	invalid := []VerificationPolicy{"", "medium-and-aboe", "mediumandabove", "MEDIUM-AND-ABOVE", "off"}
	for _, p := range invalid {
		if ValidPolicy(p) {
			t.Errorf("ValidPolicy(%q) = true, want false", p)
		}
	}
}

// Spec: §4.7.9 — an unrecognized verification policy fails closed on both
// axes: a missing signature is refused, and a present signature that does not
// validate is refused. A typo therefore never disables enforcement.
func TestEnforceVerification_UnknownPolicyFailsClosed(t *testing.T) {
	t.Parallel()
	key := newTestKey(t)
	err := EnforceVerification(context.Background(), "bogus", key, manifest.SensitivityLow, testHash, "")
	if !errors.Is(err, ErrSignatureMissing) {
		t.Fatalf("unknown policy with no signature = %v, want ErrSignatureMissing", err)
	}
	err = EnforceVerification(context.Background(), "bogus", key, manifest.SensitivityLow, testHash, "noop:"+testHash)
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("unknown policy with an invalid signature = %v, want ErrSignatureInvalid", err)
	}
}
