package integration

// End-to-end coverage for §4.7.1 multi-tenant org isolation through the
// verified injected-session-token path, backed by the per-org Postgres schema.
//
// The spec model: the tenant boundary is the org; "Each org has its own
// schema" and a connection scoped to one org cannot read another org's rows.
// The IdP JWT carries an `org_id` claim (§6.3.1) which the verifier maps onto
// layer.Identity.OrgID. This test stands up two registries over one shared
// Postgres database, one pinned to org acme and one to org globex, each behind
// the injected-session-token verifier. It mints a token whose `org_id=acme`
// claim is carried into the identity, then asserts that the acme-scoped
// endpoint serves acme data and never returns globex data, end-to-end over
// HTTP. The globex endpoint is the mirror. Cross-org reads resolve to 404
// because the per-org schema the registry reads physically never held the
// other org's manifest.
//
// Gated on PODIUM_POSTGRES_DSN: the schema-per-org isolation is a Postgres
// property (SQLite is single-schema), so without a live DSN this test skips
// cleanly. Identifiers are prefixed orgiso* to avoid collisions in package
// integration.

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/lennylabs/podium/pkg/identity"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

const (
	orgisoAudience = "https://podium.e2e"
	orgisoIssuer   = "orgiso-rt"
)

// orgisoVerifier mirrors the serverboot injected-session-token wiring: it
// verifies the bearer against the runtime key registry and carries the
// verified claims, including org_id, onto layer.Identity. spec: §6.3.1, §6.3.2.
func orgisoVerifier(reg *identity.RuntimeKeyRegistry) func(*http.Request) (layer.Identity, error) {
	verify := reg.JWTVerifier(orgisoAudience, nil)
	return func(r *http.Request) (layer.Identity, error) {
		h := r.Header.Get("Authorization")
		var raw string
		if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
			raw = strings.TrimSpace(h[7:])
		}
		id, err := verify(raw)
		if err != nil {
			return layer.Identity{}, err
		}
		return layer.Identity{
			Sub: id.Sub, Email: id.Email, OrgID: id.OrgID,
			Groups: id.Groups, Scopes: id.Scopes, IsAuthenticated: true,
		}, nil
	}
}

// orgisoClaims builds a valid claim set carrying the org_id claim.
func orgisoClaims(sub, orgID string) jwt.MapClaims {
	return jwt.MapClaims{
		"iss": orgisoIssuer, "aud": orgisoAudience, "sub": sub, "act": orgisoIssuer,
		"org_id": orgID,
		"exp":    time.Now().Add(10 * time.Minute).Unix(),
	}
}

func orgisoSign(t *testing.T, priv *rsa.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	s, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(priv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

func orgisoGet(t *testing.T, url, token string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// orgisoServer builds a registry pinned to tenantID over the shared store st,
// behind the injected-session-token verifier registered for orgisoIssuer.
func orgisoServer(t *testing.T, st store.Store, tenantID string, priv *rsa.PrivateKey) *httptest.Server {
	t.Helper()
	reg := core.New(st, tenantID, []layer.Layer{
		{ID: "shared", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	keys := identity.NewRuntimeKeyRegistry()
	if err := keys.Register(identity.RuntimeKey{Issuer: orgisoIssuer, Algorithm: "RS256", Key: &priv.PublicKey}); err != nil {
		t.Fatalf("Register runtime key: %v", err)
	}
	srv := server.New(reg, server.WithIdentityVerifier(orgisoVerifier(keys)))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// Spec: §4.7.1 — the tenant boundary is the org and each org has its own
// schema, so a read scoped to one org cannot reach another org's rows. With the
// caller's identity carried from the verified token (including org_id), the
// acme-scoped registry serves only acme data and the globex-scoped registry
// serves only globex data, end-to-end over HTTP.
func TestAuthOrgIsolation_OrgScopedReadsAreIsolated(t *testing.T) {
	dsn := os.Getenv("PODIUM_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PODIUM_POSTGRES_DSN unset; skipping Postgres-backed org-isolation test")
	}
	pg, err := store.OpenPostgres(dsn)
	if err != nil {
		t.Skipf("OpenPostgres %q: %v (database unreachable)", dsn, err)
	}
	t.Cleanup(func() { _ = pg.Close() })
	// ResetForTest wipes every org schema on the shared database; hold the
	// whole-database lock across the reset, the seed, and every later assertion
	// so no sibling reset lands mid-flight.
	lockPostgresReset(t)
	if err := pg.ResetForTest(context.Background()); err != nil {
		t.Fatalf("ResetForTest: %v", err)
	}

	ctx := context.Background()
	const tenantAcme = "orgiso-acme"
	const tenantGlobex = "orgiso-globex"
	for _, id := range []string{tenantAcme, tenantGlobex} {
		if err := pg.CreateTenant(ctx, store.Tenant{ID: id, Name: id}); err != nil {
			t.Fatalf("CreateTenant(%s): %v", id, err)
		}
	}

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Each org owns a manifest at an org-specific artifact id, plus a manifest
	// at a SHARED id whose content differs per org. The shared id proves the
	// read resolves within the caller's org schema rather than a global table:
	// acme reads acme's content hash for the shared id, never globex's.
	mustPut := func(tenant, artifactID, hash, desc string) {
		t.Helper()
		if err := pg.PutManifest(ctx, store.ManifestRecord{
			TenantID: tenant, ArtifactID: artifactID, Version: "1.0.0",
			ContentHash: hash, Type: "context", Description: desc, Layer: "shared",
			IngestedAt: base,
		}); err != nil {
			t.Fatalf("PutManifest(%s/%s): %v", tenant, artifactID, err)
		}
	}
	mustPut(tenantAcme, "acme/secret/ledger", "sha256:acme-only", "acme ledger")
	mustPut(tenantGlobex, "globex/secret/ledger", "sha256:globex-only", "globex ledger")
	mustPut(tenantAcme, "shared/policy", "sha256:acme-policy", "acme policy")
	mustPut(tenantGlobex, "shared/policy", "sha256:globex-policy", "globex policy")

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	acmeSrv := orgisoServer(t, pg, tenantAcme, priv)
	globexSrv := orgisoServer(t, pg, tenantGlobex, priv)

	// A token whose org_id claim is acme. The verifier carries org_id onto the
	// identity (§6.3.1); the acme registry is the per-org data plane for it.
	acmeToken := orgisoSign(t, priv, orgisoClaims("alice@acme.com", "acme"))
	globexToken := orgisoSign(t, priv, orgisoClaims("bob@globex.com", "globex"))

	// The acme org_id token reads acme's own artifact (200).
	if st, body := orgisoGet(t, acmeSrv.URL+"/v1/load_artifact?id=acme/secret/ledger", acmeToken); st != http.StatusOK {
		t.Fatalf("acme reading acme artifact = %d, want 200\nbody: %s", st, body)
	}

	// The acme endpoint never returns globex's artifact: it does not exist in
	// the acme schema, so the read is 404 rather than a cross-org leak.
	if st, body := orgisoGet(t, acmeSrv.URL+"/v1/load_artifact?id=globex/secret/ledger", acmeToken); st != http.StatusNotFound {
		t.Errorf("acme reading globex artifact = %d, want 404 (cross-org isolation)\nbody: %s", st, body)
	}

	// The globex endpoint is the mirror: it serves globex and denies acme.
	if st, body := orgisoGet(t, globexSrv.URL+"/v1/load_artifact?id=globex/secret/ledger", globexToken); st != http.StatusOK {
		t.Fatalf("globex reading globex artifact = %d, want 200\nbody: %s", st, body)
	}
	if st, _ := orgisoGet(t, globexSrv.URL+"/v1/load_artifact?id=acme/secret/ledger", globexToken); st != http.StatusNotFound {
		t.Errorf("globex reading acme artifact = %d, want 404 (cross-org isolation)", st)
	}

	// The shared artifact id resolves to each org's OWN content. acme's read of
	// shared/policy must carry acme's content hash, never globex's, proving the
	// per-org schema (not a single global table) backs the read.
	st, body := orgisoGet(t, acmeSrv.URL+"/v1/load_artifact?id=shared/policy", acmeToken)
	if st != http.StatusOK {
		t.Fatalf("acme reading shared/policy = %d, want 200\nbody: %s", st, body)
	}
	if got := orgisoContentHash(t, body); got != "sha256:acme-policy" {
		t.Errorf("acme shared/policy content_hash = %q, want sha256:acme-policy (org schema leak)", got)
	}
	st, body = orgisoGet(t, globexSrv.URL+"/v1/load_artifact?id=shared/policy", globexToken)
	if st != http.StatusOK {
		t.Fatalf("globex reading shared/policy = %d, want 200\nbody: %s", st, body)
	}
	if got := orgisoContentHash(t, body); got != "sha256:globex-policy" {
		t.Errorf("globex shared/policy content_hash = %q, want sha256:globex-policy (org schema leak)", got)
	}

	// Discovery is likewise org-scoped: an acme search never surfaces a globex
	// artifact id and vice versa.
	if ids := orgisoSearchIDs(t, acmeSrv.URL, acmeToken); orgisoContains(ids, "globex/secret/ledger") {
		t.Errorf("acme search leaked globex artifact: %v", ids)
	} else if !orgisoContains(ids, "acme/secret/ledger") {
		t.Errorf("acme search missing its own artifact: %v", ids)
	}
	if ids := orgisoSearchIDs(t, globexSrv.URL, globexToken); orgisoContains(ids, "acme/secret/ledger") {
		t.Errorf("globex search leaked acme artifact: %v", ids)
	} else if !orgisoContains(ids, "globex/secret/ledger") {
		t.Errorf("globex search missing its own artifact: %v", ids)
	}
}

// orgisoContentHash extracts the top-level content_hash field from a
// load_artifact response body (LoadArtifactResponse.ContentHash).
func orgisoContentHash(t *testing.T, body []byte) string {
	t.Helper()
	var resp struct {
		ContentHash string `json:"content_hash"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode load_artifact: %v (body=%s)", err, body)
	}
	if resp.ContentHash == "" {
		t.Fatalf("load_artifact response missing content_hash: %s", body)
	}
	return resp.ContentHash
}

// orgisoSearchIDs returns the artifact ids a search over the empty query
// surfaces for the given org endpoint.
func orgisoSearchIDs(t *testing.T, baseURL, token string) []string {
	t.Helper()
	st, body := orgisoGet(t, baseURL+"/v1/search_artifacts?query=", token)
	if st != http.StatusOK {
		t.Fatalf("search = %d, want 200\nbody: %s", st, body)
	}
	var resp struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode search: %v (body=%s)", err, body)
	}
	out := make([]string, 0, len(resp.Results))
	for _, r := range resp.Results {
		out = append(out, r.ID)
	}
	return out
}

func orgisoContains(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
