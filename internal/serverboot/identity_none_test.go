package serverboot

import (
	"testing"
)

// Spec: §13.10 (Loopback bind without an identity provider) — an identity
// provider of `none` is treated as unset by the default layer visibility, the
// bootstrap visibility fallback, and the start banner, as the loopback-bind
// refusal already treats it.
func TestConfig_IdentityProviderNoneConfiguresNone(t *testing.T) {
	for _, provider := range []string{"", "none"} {
		t.Setenv("PODIUM_IDENTITY_PROVIDER", provider)
		t.Setenv("PODIUM_DEFAULT_LAYER_VISIBILITY", "")
		t.Setenv("PODIUM_CONFIG_FILE", "")
		t.Setenv("HOME", t.TempDir())
		c := LoadConfig()
		if c.hasIdentityProvider() {
			t.Errorf("%q: hasIdentityProvider = true, want false", provider)
		}
		if c.defaultLayerVisibility != "public" {
			t.Errorf("%q: default layer visibility = %q, want public", provider, c.defaultLayerVisibility)
		}
		if got := c.modeBanner(); got != "standalone" {
			t.Errorf("%q: modeBanner = %q, want standalone", provider, got)
		}
		direct := &Config{identityProvider: provider}
		if got := defaultBootstrapVisibility(direct); !got.Public || got.Organization {
			t.Errorf("%q: bootstrap visibility = %+v, want public", provider, got)
		}
	}
	gated := &Config{identityProvider: "oidc-jwt"}
	if !gated.hasIdentityProvider() || gated.modeBanner() != "oidc-jwt" || defaultBootstrapVisibility(gated).Public {
		t.Errorf("oidc-jwt: hasIdentityProvider=%v banner=%q bootstrap=%+v, want a configured provider with a private default",
			gated.hasIdentityProvider(), gated.modeBanner(), defaultBootstrapVisibility(gated))
	}
}
