package sign

import (
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

// spec: §6.2 — an unrecognized verification policy must fail closed: it
// enforces verification rather than silently skipping it. Before the fix
// the default branch returned false (fail open), so a typo disabled
// signature enforcement on every artifact.
func TestEnforceVerification_UnknownPolicyFailsClosed(t *testing.T) {
	t.Parallel()
	// Low sensitivity + unknown policy: fail-open would return nil; fail
	// closed requires a signature and reports it missing.
	err := EnforceVerification("bogus", Noop{}, manifest.Sensitivity("low"), "sha256:abc", "")
	if !errors.Is(err, ErrSignatureMissing) {
		t.Fatalf("unknown policy with no signature = %v, want ErrSignatureMissing", err)
	}
	// With a valid signature the unknown policy still enforces and the
	// noop provider verifies the placeholder.
	if err := EnforceVerification("bogus", Noop{}, manifest.Sensitivity("low"), "sha256:abc", "noop:sha256:abc"); err != nil {
		t.Errorf("unknown policy with valid signature = %v, want nil", err)
	}
}

// spec: §4.7.9 — a known policy keeps its documented semantics. never
// skips verification entirely even for high sensitivity.
func TestEnforceVerification_NeverSkips(t *testing.T) {
	t.Parallel()
	if err := EnforceVerification(PolicyNever, Noop{}, manifest.SensitivityHigh, "sha256:abc", ""); err != nil {
		t.Errorf("PolicyNever = %v, want nil", err)
	}
}
