package sign

import "fmt"

// ResolveVerifier resolves the delivery-signature verifier a Go consumer
// applies under policy. Under never it reads no material and returns a nil
// verifier. Under every other policy it returns the registry-managed verifier
// over the §4.7.9 verification key set that VerificationKeys resolves from
// verifyKey (the value of PODIUM_SIGNATURE_VERIFY_KEY) and keyPath (the value
// of PODIUM_SIGN_KEY_PATH), and refuses with
// config.signature_provider_unavailable when that set does not resolve.
//
// It takes no provider name because §4.7.10 fixes the verifier of a delivery
// signature as the registry-managed one. podium-mcp, the one consumer that
// reads a provider name, validates that name before it calls here.
//
// Spec: §4.7.9, §4.7.10, §6.9
func ResolveVerifier(policy VerificationPolicy, verifyKey, keyPath string) (Provider, error) {
	if policy == PolicyNever {
		return nil, nil
	}
	keys, err := VerificationKeys(verifyKey, keyPath)
	if err != nil {
		return nil, fmt.Errorf("config.signature_provider_unavailable: %w; supply the verification material or set PODIUM_VERIFY_SIGNATURES=never", err)
	}
	return RegistryManagedKey{Trusted: keys}, nil
}
