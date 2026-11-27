package serverboot

import (
	"errors"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/identity"
	"github.com/lennylabs/podium/pkg/registry/server"
)

// Spec: §6.3 / §13.10 (External authentication) / §13.12 — the registry starts
// with a recognized provider, external, none, or an unset value, and refuses
// every other PODIUM_IDENTITY_PROVIDER value with
// config.identity_provider_unverified. The refusal names the value, every
// verified provider, and the external declaration, so a mistyped provider
// name is reported rather than started as a registry that verifies no caller.
func TestIdentityProviderLabelGuard(t *testing.T) {
	t.Parallel()
	// A unique id keeps this registration from colliding with the custom
	// providers other tests register; Has keeps -count=N reruns working.
	const customID = "acme-label-guard"
	if !identity.Default.Has(customID) {
		if err := identity.Default.Register(customID, func(identity.Config) (identity.Provider, error) {
			return acmeIdentity{}, nil
		}); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}
	admitted := []string{"", "none", server.ExternalIdentityProvider, "oauth-device-code", customID}
	admitted = append(admitted, verifiedProviders...)
	for _, id := range admitted {
		if err := identityProviderLabelGuard(id); err != nil {
			t.Errorf("identityProviderLabelGuard(%q) = %v, want nil", id, err)
		}
	}
	// The refused values cover a one-character typo of a verified provider,
	// the free-form label earlier releases started with, and case variants,
	// which the registry does not fold.
	for _, id := range []string{"oidc-jwy", "oidc", "oidc_jwt", "External", "OIDC-JWT", "trusted-header"} {
		err := identityProviderLabelGuard(id)
		if err == nil {
			t.Errorf("identityProviderLabelGuard(%q) = nil, want config.identity_provider_unverified", id)
			continue
		}
		msg := err.Error()
		want := []string{"config.identity_provider_unverified", `"` + id + `"`, "PODIUM_IDENTITY_PROVIDER=external"}
		want = append(want, verifiedProviders...)
		for _, w := range want {
			if !strings.Contains(msg, w) {
				t.Errorf("%q: message does not contain %q:\n%s", id, w, msg)
			}
		}
	}
}

// Spec: §13.10 (External authentication) — the unknown-value refusal runs ahead
// of the other identity and bind checks, so a typo that would otherwise trip
// config.public_mode_with_idp, config.multitenant_no_verifier, or a bind check
// is reported as the typo it is.
func TestValidate_UnknownIdentityProviderPrecedesOtherChecks(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cfg  Config
	}{
		{"single-tenant loopback", Config{bind: "127.0.0.1:8080"}},
		{"non-loopback bind", Config{bind: "0.0.0.0:8080"}},
		{"public mode", Config{bind: "127.0.0.1:8080", publicMode: true}},
		{"multi-tenant", Config{bind: "127.0.0.1:8080", multiTenant: true}},
		{"web UI on a non-loopback bind", Config{bind: "0.0.0.0:8080", webUI: true}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			cfg := c.cfg
			cfg.identityProvider = "oidc-jwy"
			err := cfg.validate()
			if err == nil || !strings.Contains(err.Error(), "config.identity_provider_unverified") {
				t.Fatalf("validate() = %v, want config.identity_provider_unverified", err)
			}
			if !strings.Contains(err.Error(), `"oidc-jwy"`) {
				t.Errorf("validate() does not name the value:\n%v", err)
			}
		})
	}
}

// Spec: §13.10 (External authentication) — external starts a registry on a
// loopback bind, refuses a non-loopback bind with config.public_bind_refused
// unless PODIUM_ALLOW_PUBLIC_BIND is set, and does not satisfy multi-tenant
// mode, which requires a request-time verifier.
func TestValidate_ExternalIdentityProvider(t *testing.T) {
	t.Parallel()
	base := Config{identityProvider: server.ExternalIdentityProvider, storeType: "sqlite", objectStore: "filesystem"}

	loopback := base
	loopback.bind = "127.0.0.1:8080"
	if err := loopback.validate(); err != nil {
		t.Errorf("external on loopback: validate() = %v, want nil", err)
	}

	public := base
	public.bind = "0.0.0.0:8080"
	err := public.validate()
	if !errors.Is(err, server.ErrPublicBindNonLoopback) {
		t.Fatalf("external on 0.0.0.0: validate() = %v, want config.public_bind_refused", err)
	}
	for _, w := range []string{"PODIUM_IDENTITY_PROVIDER=external", "0.0.0.0:8080", "PODIUM_ALLOW_PUBLIC_BIND"} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("bind refusal does not contain %q:\n%v", w, err)
		}
	}

	allowed := public
	allowed.allowPublicBind = true
	if err := allowed.validate(); err != nil {
		t.Errorf("external on 0.0.0.0 with PODIUM_ALLOW_PUBLIC_BIND: validate() = %v, want nil", err)
	}

	mt := loopback
	mt.multiTenant = true
	if err := mt.validate(); err == nil || !strings.Contains(err.Error(), "config.multitenant_no_verifier") {
		t.Errorf("external multi-tenant: validate() = %v, want config.multitenant_no_verifier", err)
	}
}

// Spec: §13.10 (External authentication) — every rule other than the bind
// refusal treats external as a configured identity provider: the unset default
// layer visibility resolves to private, and the start banner names it.
func TestConfig_ExternalIdentityProviderIsConfigured(t *testing.T) {
	t.Setenv("PODIUM_IDENTITY_PROVIDER", server.ExternalIdentityProvider)
	t.Setenv("PODIUM_DEFAULT_LAYER_VISIBILITY", "")
	t.Setenv("PODIUM_CONFIG_FILE", "")
	t.Setenv("HOME", t.TempDir())
	c := LoadConfig()
	if !c.hasIdentityProvider() {
		t.Error("hasIdentityProvider = false, want true")
	}
	if c.defaultLayerVisibility != "private" {
		t.Errorf("default layer visibility = %q, want private", c.defaultLayerVisibility)
	}
	if got := c.modeBanner(); got != server.ExternalIdentityProvider {
		t.Errorf("modeBanner = %q, want external", got)
	}
}
