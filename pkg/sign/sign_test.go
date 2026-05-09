package sign

import (
	"errors"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/manifest"
)

// Spec: §4.7.9 Signing — Sign + Verify round-trip on a known content hash.
// Phase: 1
func TestNoop_RoundTrip(t *testing.T) {
	testharness.RequirePhase(t, 1)
	t.Parallel()
	p := Noop{}
	sig, err := p.Sign("sha256:abc")
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := p.Verify("sha256:abc", sig); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// Spec: §4.7.9 — a signature that does not match the content hash fails
// with ErrSignatureInvalid (maps to materialize.signature_invalid).
// Phase: 1
func TestNoop_VerifyRejectsMismatch(t *testing.T) {
	testharness.RequirePhase(t, 1)
	t.Parallel()
	p := Noop{}
	err := p.Verify("sha256:abc", "noop:sha256:def")
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("got %v, want ErrSignatureInvalid", err)
	}
}

// Spec: §6.2 — PolicyMediumAndAbove enforces verification for medium
// and high sensitivity, skips low.
// Phase: 1
func TestEnforceVerification_PolicyMediumAndAbove(t *testing.T) {
	testharness.RequirePhase(t, 1)
	t.Parallel()
	p := Noop{}
	cases := []struct {
		s         manifest.Sensitivity
		signature string
		wantErr   error
	}{
		{manifest.SensitivityLow, "", nil},
		{manifest.SensitivityMedium, "noop:sha256:abc", nil},
		{manifest.SensitivityHigh, "noop:sha256:abc", nil},
		{manifest.SensitivityMedium, "", ErrSignatureMissing},
		{manifest.SensitivityHigh, "noop:wrong", ErrSignatureInvalid},
	}
	for _, c := range cases {
		err := EnforceVerification(PolicyMediumAndAbove, p, c.s, "sha256:abc", c.signature)
		if c.wantErr == nil {
			if err != nil {
				t.Errorf("(s=%s sig=%q) got %v, want nil", c.s, c.signature, err)
			}
		} else if !errors.Is(err, c.wantErr) {
			t.Errorf("(s=%s sig=%q) got %v, want %v", c.s, c.signature, err, c.wantErr)
		}
	}
}

// Spec: §6.2 — PolicyNever skips verification regardless of sensitivity.
// Phase: 1
func TestEnforceVerification_PolicyNever(t *testing.T) {
	testharness.RequirePhase(t, 1)
	t.Parallel()
	p := Noop{}
	for _, s := range []manifest.Sensitivity{
		manifest.SensitivityLow,
		manifest.SensitivityMedium,
		manifest.SensitivityHigh,
	} {
		if err := EnforceVerification(PolicyNever, p, s, "sha256:abc", ""); err != nil {
			t.Errorf("PolicyNever, s=%s: got %v, want nil", s, err)
		}
	}
}

// Spec: §6.2 — PolicyAlways enforces every sensitivity, including low.
// Phase: 1
func TestEnforceVerification_PolicyAlways(t *testing.T) {
	testharness.RequirePhase(t, 1)
	t.Parallel()
	p := Noop{}
	if err := EnforceVerification(PolicyAlways, p, manifest.SensitivityLow, "sha256:abc", ""); !errors.Is(err, ErrSignatureMissing) {
		t.Errorf("PolicyAlways low: got %v, want ErrSignatureMissing", err)
	}
}
