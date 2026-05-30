package serverboot

import (
	"strings"
	"testing"
)

// spec: §2.2, §6.3.1 (F-2.2.3) — a registry configured with an identity
// provider must resolve callers to an Identity so it can compose the
// per-OAuth-identity effective view and apply per-layer visibility. The
// boot guard refuses to start when a provider is configured but no
// request-time verifier is wired, because the fallback (anonymous-public)
// silently drops every authenticated, organization, and private layer from
// every caller's view. Only injected-session-token is verified server-side
// in this build; public mode and the empty/standalone default are exempt.
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
