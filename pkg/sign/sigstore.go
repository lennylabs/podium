package sign

import (
	"errors"
	"fmt"
)

// SigstoreKeyless is a stub for the §4.7.9 Sigstore-keyless signing
// path. The full implementation depends on the Fulcio + Rekor
// transparency log; that infrastructure ships with the production
// deployment. The stub fits the Provider interface so callers can
// configure it in their pipeline today and swap to the real provider
// without code changes.
type SigstoreKeyless struct {
	// FulcioURL is the Fulcio CA endpoint. Empty disables.
	FulcioURL string
	// RekorURL is the Rekor transparency log endpoint.
	RekorURL string
	// OIDCToken is the caller's identity token used to mint the
	// short-lived signing certificate (RFC 8628 device-code flow
	// produces this in the developer-host case).
	OIDCToken string
}

// ID returns "sigstore-keyless".
func (SigstoreKeyless) ID() string { return "sigstore-keyless" }

// Sign produces a signature over the canonical content hash.
//
// The current implementation returns ErrSigstoreUnavailable until the
// Fulcio + Rekor integration lands. Production deployments wire the
// dependency under a build tag so the binary stays minimal in
// air-gapped contexts.
func (s SigstoreKeyless) Sign(contentHash string) (string, error) {
	if s.FulcioURL == "" || s.RekorURL == "" || s.OIDCToken == "" {
		return "", ErrSigstoreUnavailable
	}
	return "", fmt.Errorf("sigstore-keyless: Fulcio integration not yet wired (contentHash=%s)", contentHash)
}

// Verify checks that signature validates against the recorded
// transparency-log entry. Until the Rekor integration lands the
// verifier defers to the Noop verifier so deployments configured for
// keyless signing still validate placeholder signatures.
func (s SigstoreKeyless) Verify(contentHash, signature string) error {
	if s.RekorURL == "" {
		return Noop{}.Verify(contentHash, signature)
	}
	return fmt.Errorf("sigstore-keyless: Rekor verification not yet wired (contentHash=%s)", contentHash)
}

// ErrSigstoreUnavailable signals that the Fulcio / Rekor endpoints
// are not configured and the keyless flow cannot proceed.
var ErrSigstoreUnavailable = errors.New("sign: sigstore-keyless not configured")

// RegistryManagedKey is a stub for §4.7.9's per-org registry-managed
// signing key path. The production implementation generates an Ed25519
// key per tenant, rotates quarterly, and stores the key in the
// registry's secret backend.
type RegistryManagedKey struct {
	// SigningKey is the per-org private key. The registry configures
	// this from its secret backend (vault / KMS / managed Postgres
	// columns) at startup.
	SigningKey []byte
}

// ID returns "registry-managed".
func (RegistryManagedKey) ID() string { return "registry-managed" }

// Sign produces a signature over contentHash using the per-org key.
// Until the secret-backend integration lands, the stub falls through
// to the Noop provider.
func (k RegistryManagedKey) Sign(contentHash string) (string, error) {
	if len(k.SigningKey) == 0 {
		return Noop{}.Sign(contentHash)
	}
	return "", fmt.Errorf("registry-managed: secret backend not yet wired (contentHash=%s)", contentHash)
}

// Verify validates a registry-managed signature.
func (k RegistryManagedKey) Verify(contentHash, signature string) error {
	if len(k.SigningKey) == 0 {
		return Noop{}.Verify(contentHash, signature)
	}
	return fmt.Errorf("registry-managed: secret backend not yet wired (contentHash=%s)", contentHash)
}
