package serverboot

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/identity"
)

// spec: §2.2, §6.3.1 — a registry configured with an identity
// provider must resolve callers to an Identity so it can compose the
// per-OAuth-identity effective view and apply per-layer visibility. The
// boot guard refuses to start when a provider is configured but no
// request-time verifier is wired, because the fallback (anonymous-public)
// silently drops every authenticated, organization, and private layer from
// every caller's view. The providers in verifiedProviders are verified
// server-side in this build; public mode and the empty/standalone default are
// exempt.
func TestIdentityVisibilityGuard(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name              string
		identityProvider  string
		providerSelected  bool
		publicMode        bool
		verifierInstalled bool
		wantErr           bool
	}{
		{"standalone empty provider", "", false, false, false, false},
		{"explicit none", "none", false, false, false, false},
		{"public mode exempt", "", false, true, false, false},
		{"public mode with provider exempt", "oauth-device-code", true, true, false, false},
		{"injected-session-token verified", "injected-session-token", true, false, true, false},
		{"oauth-device-code unverified fails", "oauth-device-code", true, false, false, true},
		// "oidc" is a free-form label, not a registered provider, so
		// selectIdentityProvider returns nil (providerSelected = false); the
		// deployment fronts the registry with external auth and is exempt.
		{"oidc label exempt", "oidc", false, false, false, false},
		{"custom registered provider unverified fails", "acme-corp", true, false, false, true},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			err := identityVisibilityGuard(c.identityProvider, c.providerSelected, c.publicMode, c.verifierInstalled)
			if c.wantErr != (err != nil) {
				t.Fatalf("identityVisibilityGuard(%q, selected=%v, public=%v, verifier=%v) err=%v, wantErr=%v",
					c.identityProvider, c.providerSelected, c.publicMode, c.verifierInstalled, err, c.wantErr)
			}
			if c.wantErr {
				if !strings.Contains(err.Error(), "config.identity_provider_unverified") {
					t.Fatalf("error missing canonical code: %v", err)
				}
				if !strings.Contains(err.Error(), c.identityProvider) {
					t.Fatalf("error should name the configured provider %q: %v", c.identityProvider, err)
				}
			}
		})
	}
}

// The guard's message tells the operator which providers to choose instead, so
// a stale list sends them at the wrong one. It named injected-session-token as
// the only verified provider long after oidc-jwt and trusted-headers gained
// verifiers, and the existing coverage did not catch it because it asserted
// only the error code and the offending provider's name.
//
// Spec: §4.6 / §13.12 — the refusal names the preconditions for public mode.
func TestIdentityVisibilityGuard_MessageNamesEveryVerifiedProvider(t *testing.T) {
	t.Parallel()
	err := identityVisibilityGuard("oauth-device-code", true, false, false)
	if err == nil {
		t.Fatal("expected an error for a selected but unverified provider")
	}
	msg := err.Error()
	for _, p := range verifiedProviders {
		if !strings.Contains(msg, p) {
			t.Errorf("message does not offer %q, which this build verifies:\n%s", p, msg)
		}
	}
	// The previous message singled one provider out. Naming a subset is the
	// defect; assert the phrasing that did so cannot come back.
	if strings.Contains(msg, "only injected-session-token") {
		t.Errorf("message still claims a single verified provider:\n%s", msg)
	}
	// Public mode alone is not a remedy: it is refused beside an identity
	// provider (config.public_mode_with_idp) and on a multi-tenant registry
	// (config.multitenant_no_verifier), so the message names both conditions.
	for _, want := range []string{"unset PODIUM_IDENTITY_PROVIDER", "PODIUM_PUBLIC_MODE=true", "PODIUM_MULTI_TENANT"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not contain %q:\n%s", want, msg)
		}
	}
}

// Spec: §6.3.1 / §13.10 / §13.12 — a multi-tenant configuration whose provider
// resolves to no registered provider is refused.
//
// The guard keys on identity.Default.Has, so a registered provider without a
// verifier (oauth-device-code or a custom registration) passes here and is left
// to identityVisibilityGuard, which keeps config.identity_provider_unverified.
func TestMultiTenantVerifierGuard(t *testing.T) {
	t.Parallel()
	// A unique id keeps this registration from colliding with the custom
	// provider other tests register. The Has guard keeps -count=N reruns from
	// failing on a duplicate registration; the registry is mutex-guarded.
	const customID = "acme-mt-guard"
	if !identity.Default.Has(customID) {
		if err := identity.Default.Register(customID, func(identity.Config) (identity.Provider, error) {
			return acmeIdentity{}, nil
		}); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}
	type guardCase struct {
		name             string
		multiTenant      bool
		publicMode       bool
		identityProvider string
		wantErr          bool
	}
	cases := []guardCase{
		{"multi-tenant empty provider", true, false, "", true},
		{"multi-tenant none", true, false, "none", true},
		{"multi-tenant oidc label", true, false, "oidc", true},
		{"multi-tenant oidc_jwt typo", true, false, "oidc_jwt", true},
		{"multi-tenant public mode empty provider", true, true, "", true},
		{"multi-tenant public mode none", true, true, "none", true},
		{"multi-tenant oauth-device-code", true, false, "oauth-device-code", false},
		{"multi-tenant custom registered provider", true, false, customID, false},
		{"single-tenant empty provider", false, false, "", false},
		{"single-tenant oidc label", false, false, "oidc", false},
		{"single-tenant public mode", false, true, "", false},
	}
	for _, p := range verifiedProviders {
		cases = append(cases, guardCase{"multi-tenant " + p, true, false, p, false})
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			err := multiTenantVerifierGuard(c.multiTenant, c.publicMode, c.identityProvider)
			if c.wantErr != (err != nil) {
				t.Fatalf("multiTenantVerifierGuard(multiTenant=%v, public=%v, %q) err=%v, wantErr=%v",
					c.multiTenant, c.publicMode, c.identityProvider, err, c.wantErr)
			}
			if !c.wantErr {
				return
			}
			assertMultiTenantNoVerifierMessage(t, err.Error(), c.publicMode, c.identityProvider)
		})
	}
}

// assertMultiTenantNoVerifierMessage checks that a config.multitenant_no_verifier
// refusal names its code, every verified provider, and the remedies that apply.
func assertMultiTenantNoVerifierMessage(t *testing.T, msg string, publicMode bool, identityProvider string) {
	t.Helper()
	want := []string{"config.multitenant_no_verifier", "unset PODIUM_MULTI_TENANT"}
	want = append(want, verifiedProviders...)
	if publicMode {
		want = append(want, "unset PODIUM_PUBLIC_MODE")
	}
	if identityProvider == "oidc" {
		// The quoted label shows the operator exactly what the registry read.
		want = append(want, `"oidc"`)
	}
	for _, w := range want {
		if !strings.Contains(msg, w) {
			t.Errorf("message does not contain %q:\n%s", w, msg)
		}
	}
}
