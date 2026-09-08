package serverboot

import "testing"

// Spec: §6.3.1, §6.3.2, §6.3.3 — the SCIM `groups:` expansion applies under
// the providers that read a credential the registry verifies, and never under
// trusted-headers, where the gateway is the source of group membership.
func TestSCIMResolvesGroups_ProviderAllowlist(t *testing.T) {
	cases := map[string]bool{
		"oidc-jwt":               true,
		"injected-session-token": true,
		"trusted-headers":        false,
		"":                       false,
		"oidc":                   false,
		"oauth-device-code":      false,
		"not-a-provider":         false,
	}
	for provider, want := range cases {
		if got := scimResolvesGroups(provider); got != want {
			t.Errorf("scimResolvesGroups(%q) = %v, want %v", provider, got, want)
		}
	}
}
