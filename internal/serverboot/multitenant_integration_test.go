package serverboot

// Full-stack integration coverage for §6.3.1 multi-tenant org_id routing: a
// registry bound to the no-data unrouted tenant, with the real tenantResolver
// wired through server.WithTenantRouter, provisioned with two orgs that each
// own data. Asserts that a caller's organization scopes the request to its
// tenant, that a verified token naming no tenant is rejected with
// auth.tenant_unknown, and that trusted-headers leaves an unknown org in no
// tenant rather than rejecting.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/lennylabs/podium/pkg/identity"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// multiTenantServer provisions orgs acme and globex, each owning an
// org-specific artifact plus a shared artifact id with org-specific content,
// behind the given verifier and the real per-request tenant router.
func multiTenantServer(t *testing.T, verify func(*http.Request) (layer.Identity, error), rejectUnknown bool) *httptest.Server {
	t.Helper()
	st := store.NewMemory()
	for _, name := range []string{"acme", "globex"} {
		if err := st.CreateTenant(t.Context(), store.Tenant{ID: orgIDForName(name), Name: name}); err != nil {
			t.Fatalf("CreateTenant(%s): %v", name, err)
		}
	}
	put := func(org, artifactID, hash string) {
		t.Helper()
		if err := st.PutManifest(t.Context(), store.ManifestRecord{
			TenantID: orgIDForName(org), ArtifactID: artifactID, Version: "1.0.0",
			ContentHash: hash, Type: "context", Description: artifactID, Layer: "pub",
			IngestedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		}); err != nil {
			t.Fatalf("PutManifest(%s/%s): %v", org, artifactID, err)
		}
	}
	put("acme", "acme/secret", "sha256:acme-secret")
	put("globex", "globex/secret", "sha256:globex-secret")
	put("acme", "shared/doc", "sha256:acme-shared")
	put("globex", "shared/doc", "sha256:globex-shared")

	reg := core.New(st, multiTenantUnrouted, []layer.Layer{
		{ID: "pub", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	srv := server.New(reg,
		server.WithIdentityVerifier(verify),
		server.WithTenantRouter(tenantResolver(st), rejectUnknown),
	)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func contentHash(t *testing.T, body []byte) string {
	t.Helper()
	var resp struct {
		ContentHash string `json:"content_hash"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode load_artifact: %v (body=%s)", err, body)
	}
	return resp.ContentHash
}

func orgToken(t *testing.T, idp *jwksIdP, sub, org string) string {
	t.Helper()
	return idp.sign(t, jwt.MapClaims{
		"iss": idp.issuer(), "aud": gwAudience, "sub": sub, "org_id": org,
		"exp": time.Now().Add(10 * time.Minute).Unix(),
	})
}

func TestMultiTenant_OIDCJWTRoutesByOrg(t *testing.T) {
	t.Parallel()
	idp := newJWKSIdP(t)
	verifier := identity.NewOIDCVerifier(idp.issuer(), gwAudience, 0)
	ts := multiTenantServer(t, oidcJWTVerifier(verifier, "", nil), true)

	acme := bearer(orgToken(t, idp, "alice@acme.com", "acme"))
	globex := bearer(orgToken(t, idp, "bob@globex.com", "globex"))

	// Each org reads its own artifact and never the other's.
	if st, _ := loadArtifact(t, ts.URL, "acme/secret", acme); st != 200 {
		t.Errorf("acme org load acme/secret = %d, want 200", st)
	}
	if st, _ := loadArtifact(t, ts.URL, "globex/secret", acme); st != 404 {
		t.Errorf("acme org load globex/secret = %d, want 404 (tenant isolation)", st)
	}
	if st, _ := loadArtifact(t, ts.URL, "globex/secret", globex); st != 200 {
		t.Errorf("globex org load globex/secret = %d, want 200", st)
	}
	if st, _ := loadArtifact(t, ts.URL, "acme/secret", globex); st != 404 {
		t.Errorf("globex org load acme/secret = %d, want 404 (tenant isolation)", st)
	}

	// The shared artifact id resolves to each org's own content.
	if _, body := loadArtifact(t, ts.URL, "shared/doc", acme); contentHash(t, body) != "sha256:acme-shared" {
		t.Errorf("acme shared/doc content_hash = %q, want sha256:acme-shared", contentHash(t, body))
	}
	if _, body := loadArtifact(t, ts.URL, "shared/doc", globex); contentHash(t, body) != "sha256:globex-shared" {
		t.Errorf("globex shared/doc content_hash = %q, want sha256:globex-shared", contentHash(t, body))
	}
}

func TestMultiTenant_OIDCJWTUnknownOrgRejected(t *testing.T) {
	t.Parallel()
	idp := newJWKSIdP(t)
	verifier := identity.NewOIDCVerifier(idp.issuer(), gwAudience, 0)
	ts := multiTenantServer(t, oidcJWTVerifier(verifier, "", nil), true)

	// A verified token whose org_id names no provisioned tenant is rejected with
	// auth.tenant_unknown (the token verified; the failure is tenancy).
	tok := bearer(orgToken(t, idp, "carol@initech.com", "initech"))
	st, body := loadArtifact(t, ts.URL, "acme/secret", tok)
	if st != 401 {
		t.Fatalf("unknown org = %d, want 401\nbody: %s", st, body)
	}
	var env struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	}
	_ = json.Unmarshal(body, &env)
	if env.Code != "auth.tenant_unknown" {
		t.Errorf("code = %q, want auth.tenant_unknown", env.Code)
	}
	if env.Details["token_org_id"] != "initech" {
		t.Errorf("details.token_org_id = %v, want initech", env.Details["token_org_id"])
	}
}

func TestMultiTenant_TrustedHeadersRoutesByOrg(t *testing.T) {
	t.Parallel()
	ts := multiTenantServer(t, trustedHeadersVerifier("sec"), false)

	acme := map[string]string{
		identity.HeaderUserSub:     "alice@acme.com",
		identity.HeaderUserOrg:     "acme",
		identity.HeaderProxySecret: "sec",
	}
	// The acme org header scopes the request to the acme tenant.
	if st, _ := loadArtifact(t, ts.URL, "acme/secret", acme); st != 200 {
		t.Errorf("acme org header load acme/secret = %d, want 200", st)
	}
	if st, _ := loadArtifact(t, ts.URL, "globex/secret", acme); st != 404 {
		t.Errorf("acme org header load globex/secret = %d, want 404 (tenant isolation)", st)
	}

	// An unknown org under trusted-headers is left in no tenant (an empty view),
	// not rejected: the request sees nothing, and never a 401.
	unknown := map[string]string{
		identity.HeaderUserSub:     "carol@initech.com",
		identity.HeaderUserOrg:     "initech",
		identity.HeaderProxySecret: "sec",
	}
	if st, body := loadArtifact(t, ts.URL, "acme/secret", unknown); st != 404 {
		t.Errorf("unknown org header load acme/secret = %d, want 404 (no tenant, not a rejection)\nbody: %s", st, body)
	}
}
