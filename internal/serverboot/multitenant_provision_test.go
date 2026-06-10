package serverboot

import (
	"testing"

	"github.com/lennylabs/podium/pkg/store"
)

func TestSeedOperatorAdmins(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	// PODIUM_OPERATOR_ADMINS seeds the instance operator role at boot.
	n, err := seedOperatorAdmins(t.Context(), st, []string{"alice@acme.com", "bob@acme.com"})
	if err != nil {
		t.Fatalf("seedOperatorAdmins: %v", err)
	}
	if n != 2 {
		t.Errorf("seeded %d operators, want 2", n)
	}
	for _, id := range []string{"alice@acme.com", "bob@acme.com"} {
		if ok, _ := st.IsOperator(t.Context(), id); !ok {
			t.Errorf("operator %q not granted", id)
		}
	}
	// A non-seeded identity is not an operator.
	if ok, _ := st.IsOperator(t.Context(), "carol@acme.com"); ok {
		t.Error("carol must not be an operator")
	}
}

func TestTenantResolver(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	acme := orgIDForName("acme")
	if err := st.CreateTenant(t.Context(), store.Tenant{ID: acme, Name: "acme"}); err != nil {
		t.Fatal(err)
	}
	resolve := tenantResolver(st)

	// An org-name alias resolves to its deterministic org ID.
	if id, ok := resolve(t.Context(), "acme"); !ok || id != acme {
		t.Errorf("resolve(\"acme\") = %q,%v want %q,true", id, ok, acme)
	}
	// A direct org ID resolves to itself.
	if id, ok := resolve(t.Context(), acme); !ok || id != acme {
		t.Errorf("resolve(<id>) = %q,%v want %q,true", id, ok, acme)
	}
	// Surrounding whitespace is trimmed.
	if _, ok := resolve(t.Context(), "  acme  "); !ok {
		t.Error("resolve must trim surrounding whitespace")
	}
	// An unknown org and an empty value do not resolve.
	if _, ok := resolve(t.Context(), "initech"); ok {
		t.Error("resolve(\"initech\") = true, want false")
	}
	if _, ok := resolve(t.Context(), ""); ok {
		t.Error("resolve(\"\") = true, want false")
	}
	// A deactivated tenant is treated as unprovisioned (§4.7.1).
	if err := st.DeactivateTenant(t.Context(), acme); err != nil {
		t.Fatal(err)
	}
	if _, ok := resolve(t.Context(), "acme"); ok {
		t.Error("resolve must treat a deactivated tenant as unprovisioned")
	}
}

func TestApplyYAML_GatewayIdentityKeys(t *testing.T) {
	t.Parallel()
	c := &Config{}
	y := &yamlConfig{Identity: yamlIdentityCfg{
		Type:                "oidc-jwt",
		Issuer:              "https://acme.okta.example/oauth2/default",
		TokenHeader:         "X-Forwarded-Access-Token",
		JWKSCacheTTLSeconds: 600,
	}}
	applyYAML(c, y)

	if c.identityProvider != "oidc-jwt" {
		t.Errorf("identityProvider = %q", c.identityProvider)
	}
	if c.oauthIssuer != "https://acme.okta.example/oauth2/default" {
		t.Errorf("oauthIssuer = %q", c.oauthIssuer)
	}
	if c.oauthTokenHeader != "X-Forwarded-Access-Token" {
		t.Errorf("oauthTokenHeader = %q", c.oauthTokenHeader)
	}
	if c.oauthJWKSCacheTTLSeconds != 600 {
		t.Errorf("oauthJWKSCacheTTLSeconds = %d", c.oauthJWKSCacheTTLSeconds)
	}

	// Env wins: a value already set on the Config is not overwritten by YAML.
	c2 := &Config{oauthIssuer: "https://env.example"}
	applyYAML(c2, y)
	if c2.oauthIssuer != "https://env.example" {
		t.Errorf("env-set issuer overwritten by YAML: %q", c2.oauthIssuer)
	}
}
