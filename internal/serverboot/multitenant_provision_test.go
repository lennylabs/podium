package serverboot

import (
	"testing"

	"github.com/lennylabs/podium/pkg/store"
)

func TestProvisionTenants(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	// The default org and an empty name are skipped; the rest are provisioned,
	// with surrounding whitespace trimmed.
	provisionTenants(t.Context(), st, []string{"acme", "globex", "", defaultOrgName, "  spaced  "}, nil)

	for _, name := range []string{"acme", "globex", "spaced"} {
		if _, err := st.GetTenant(t.Context(), orgIDForName(name)); err != nil {
			t.Errorf("org %q not provisioned: %v", name, err)
		}
	}
	// provisionTenants does not create the default org; bootstrapDefaultTenant owns it.
	if _, err := st.GetTenant(t.Context(), orgIDForName(defaultOrgName)); err == nil {
		t.Error("provisionTenants must skip the default org")
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
