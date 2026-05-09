// Package sign exposes the SignatureProvider SPI (spec §9.1) plus the
// medium-and-above verification policy enforced at materialization time
// (§4.7.9). Phase 1 ships the SPI plus a noop provider; Sigstore-keyless
// and registry-managed key implementations land in Phase 5+.
package sign

import (
	"errors"
	"fmt"

	"github.com/lennylabs/podium/pkg/manifest"
)

// Errors returned by Verify and Sign. Tests assert against them via errors.Is.
var (
	// ErrSignatureInvalid signals that the signature does not validate
	// against the artifact's content hash. Maps to
	// materialize.signature_invalid in §6.10.
	ErrSignatureInvalid = errors.New("signature_invalid")
	// ErrSignatureMissing signals that an artifact requires a signature
	// (sensitivity ≥ medium under the default policy) but none was
	// provided.
	ErrSignatureMissing = errors.New("signature_missing")
)

// VerificationPolicy controls when Verify enforces the presence of a
// valid signature. Maps to PODIUM_VERIFY_SIGNATURES (spec §6.2).
type VerificationPolicy string

// VerificationPolicy values.
const (
	// PolicyNever skips verification entirely.
	PolicyNever VerificationPolicy = "never"
	// PolicyMediumAndAbove enforces signatures for sensitivity ≥ medium.
	// Default in standard deployments per §6.2.
	PolicyMediumAndAbove VerificationPolicy = "medium-and-above"
	// PolicyAlways enforces signatures for every artifact.
	PolicyAlways VerificationPolicy = "always"
)

// Provider is the SPI implementations satisfy.
type Provider interface {
	// ID returns the provider identifier (e.g., "sigstore-keyless").
	ID() string
	// Sign produces a signature over the canonical content hash.
	Sign(contentHash string) (string, error)
	// Verify checks that signature is valid for contentHash.
	Verify(contentHash, signature string) error
}

// Noop is a Provider that signs by returning a deterministic placeholder
// and verifies by accepting any matching placeholder. Used as a safe
// default in standalone deployments where signing is opt-in (§13.10).
type Noop struct{}

// ID returns "noop".
func (Noop) ID() string { return "noop" }

// Sign returns a placeholder signature derived from the content hash.
func (Noop) Sign(contentHash string) (string, error) {
	return "noop:" + contentHash, nil
}

// Verify accepts the placeholder produced by Sign for the same content
// hash and rejects anything else.
func (Noop) Verify(contentHash, signature string) error {
	want := "noop:" + contentHash
	if signature != want {
		return fmt.Errorf("%w: %q != %q", ErrSignatureInvalid, signature, want)
	}
	return nil
}

// EnforceVerification applies policy to the artifact's sensitivity and
// returns nil when the artifact does not require verification, or the
// result of provider.Verify when it does.
func EnforceVerification(policy VerificationPolicy, provider Provider, sensitivity manifest.Sensitivity, contentHash, signature string) error {
	if !needsVerification(policy, sensitivity) {
		return nil
	}
	if signature == "" {
		return fmt.Errorf("%w: sensitivity %q requires a signature", ErrSignatureMissing, sensitivity)
	}
	return provider.Verify(contentHash, signature)
}

func needsVerification(policy VerificationPolicy, s manifest.Sensitivity) bool {
	switch policy {
	case PolicyAlways:
		return true
	case PolicyMediumAndAbove:
		return s == manifest.SensitivityMedium || s == manifest.SensitivityHigh
	case PolicyNever:
		return false
	default:
		return false
	}
}
