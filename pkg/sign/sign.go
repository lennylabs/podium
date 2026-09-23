// Package sign exposes the SignatureProvider SPI (spec §9.1) plus the
// medium-and-above verification policy enforced at materialization time
// (§4.7.9). The policy has two axes: a signature the response carries is
// verified under any policy other than never, and the policy governs only
// whether a missing signature aborts the load. Built-ins ship a noop
// provider whose Verify refuses every signature, Sigstore-keyless, and
// registry-managed-key implementations.
package sign

import (
	"context"
	"fmt"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/spi"
)

// Errors returned by Verify and Sign. Each is a structured *spi.Error carrying
// its §6.10 code so the SignatureProvider SPI conforms to the §9.3 "Structured
// errors" constraint. Tests assert against them via errors.Is, which matches
// the package-level sentinel pointer through fmt.Errorf wrapping.
var (
	// ErrSignatureInvalid signals that the signature does not validate
	// against the artifact's content hash.
	ErrSignatureInvalid = &spi.Error{Code: "materialize.signature_invalid", Message: "signature_invalid"}
	// ErrSignatureMissing signals that an artifact requires a signature
	// (sensitivity ≥ medium under the default policy) but none was
	// provided.
	ErrSignatureMissing = &spi.Error{Code: "materialize.signature_missing", Message: "signature_missing"}
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

// ValidPolicy reports whether p is one of the three recognized
// PODIUM_VERIFY_SIGNATURES values (spec §6.2 / §4.7.9). Callers that
// read the policy from configuration use this to refuse an unknown
// value at startup rather than silently falling through to a skip.
//
// spec: §6.2 — PODIUM_VERIFY_SIGNATURES is never | medium-and-above | always.
func ValidPolicy(p VerificationPolicy) bool {
	switch p {
	case PolicyNever, PolicyMediumAndAbove, PolicyAlways:
		return true
	default:
		return false
	}
}

// Provider is the SPI implementations satisfy.
type Provider interface {
	// ID returns the provider identifier (e.g., "sigstore-keyless").
	ID() string
	// Sign produces a signature over the canonical content hash.
	//
	// spec: §9.3 — context-first so deadlines and cancellation propagate to
	// the network calls a real provider makes (Fulcio, Rekor).
	Sign(ctx context.Context, contentHash string) (string, error)
	// Verify checks that signature is valid for contentHash.
	//
	// spec: §9.3 — context-first for the same reason as Sign.
	Verify(ctx context.Context, contentHash, signature string) error
}

// Noop is a Provider that signs by returning a deterministic placeholder
// and refuses every signature on Verify. Sign stays usable because the
// audit anchor and the ingest tests depend on it.
type Noop struct{}

// ID returns "noop".
func (Noop) ID() string { return "noop" }

// Sign returns a placeholder signature derived from the content hash.
func (Noop) Sign(_ context.Context, contentHash string) (string, error) {
	return "noop:" + contentHash, nil
}

// Verify always refuses. The value Sign produces is "noop:" + contentHash, and
// contentHash is served in the clear on every load_artifact response, so
// accepting it would let any party mint a passing signature for any artifact.
// A deployment that does not verify says so with PODIUM_VERIFY_SIGNATURES=never
// (§6.2); it does not say so by configuring a provider that accepts a public
// value.
//
// Spec: §4.7.9
func (Noop) Verify(_ context.Context, _, _ string) error {
	return fmt.Errorf("%w: the noop provider does not verify; set PODIUM_SIGNATURE_PROVIDER to registry-managed or sigstore-keyless, or set PODIUM_VERIFY_SIGNATURES=never", ErrSignatureInvalid)
}

// EnforceVerification applies policy to one served artifact.
//
// The two axes are separate. A signature that is present is always verified
// under any policy other than never, because the sensitivity the policy would
// otherwise gate on is reported by the same party that supplies the signature,
// and for a child declaring extends: (§4.6) no attested source for it exists:
// the merge takes the most-restrictive value (pkg/manifest/merge.go) while the
// content hash covers only the child's pre-merge bytes. Whether a missing
// signature aborts the load is the axis the policy governs, and only always
// resists a registry that serves an unsigned forgery.
//
// Spec: §4.7.9, §6.6 step 2.
func EnforceVerification(ctx context.Context, policy VerificationPolicy, provider Provider, sensitivity manifest.Sensitivity, contentHash, signature string) error {
	if policy == PolicyNever {
		return nil
	}
	if signature == "" {
		if !requiresSignature(policy, sensitivity) {
			return nil
		}
		return fmt.Errorf("%w: sensitivity %q requires a signature", ErrSignatureMissing, sensitivity)
	}
	return provider.Verify(ctx, contentHash, signature)
}

// requiresSignature reports whether a missing signature is a refusal. An
// unrecognized policy fails closed; loadConfig refuses such a value at startup
// (§6.2), so this is defense in depth for a direct constructor.
//
// Spec: §4.7.9
func requiresSignature(policy VerificationPolicy, s manifest.Sensitivity) bool {
	switch policy {
	case PolicyAlways:
		return true
	case PolicyMediumAndAbove:
		return s == manifest.SensitivityMedium || s == manifest.SensitivityHigh
	case PolicyNever:
		// EnforceVerification returns before this call under never; the arm
		// keeps the predicate correct for the full value set.
		return false
	default:
		return true
	}
}
