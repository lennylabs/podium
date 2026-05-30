package integration

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

const istAudience = "https://podium.acme.com"

// istVerifier mirrors the serverboot injected-session-token wiring: it
// extracts the bearer token, verifies it against the runtime key registry,
// and maps the verified claims (groups and OAuth scopes) onto a
// layer.Identity. spec: §6.3.1, §6.3.2.
func istVerifier(reg *identity.RuntimeKeyRegistry) func(*http.Request) (layer.Identity, error) {
	verify := reg.JWTVerifier(istAudience, nil)
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

func istSign(t *testing.T, priv *rsa.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	s, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(priv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

func istClaims(scopes string) jwt.MapClaims {
	c := jwt.MapClaims{
		"iss": "rt", "aud": istAudience, "sub": "alice", "act": "rt",
		"exp": time.Now().Add(10 * time.Minute).Unix(),
	}
	if scopes != "" {
		c["scope"] = scopes
	}
	return c
}

// istGet issues an authenticated GET and returns the status and body.
func istGet(t *testing.T, url, token string) (int, []byte) {
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

// istServer boots a SQLite-backed registry behind the §6.3.2 verifier with a
// public layer holding finance (two versions) and hr artifacts.
func istServer(t *testing.T) (*httptest.Server, *rsa.PrivateKey) {
	t.Helper()
	ctx := context.Background()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.CreateTenant(ctx, store.Tenant{ID: "default", Name: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, r := range []store.ManifestRecord{
		{TenantID: "default", ArtifactID: "finance/ap/pay-invoice", Version: "1.0.0", ContentHash: "sha256:f1", Type: "skill", Description: "pay invoice", Layer: "shared", IngestedAt: base},
		{TenantID: "default", ArtifactID: "finance/ap/pay-invoice", Version: "2.0.0", ContentHash: "sha256:f2", Type: "skill", Description: "pay invoice", Layer: "shared", IngestedAt: base.Add(time.Hour)},
		{TenantID: "default", ArtifactID: "hr/policies", Version: "1.0.0", ContentHash: "sha256:h1", Type: "context", Description: "hr policies", Layer: "shared", IngestedAt: base},
	} {
		if err := st.PutManifest(ctx, r); err != nil {
			t.Fatalf("PutManifest: %v", err)
		}
	}
	reg := core.New(st, "default", []layer.Layer{
		{ID: "shared", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	keys := identity.NewRuntimeKeyRegistry()
	if err := keys.Register(identity.RuntimeKey{Issuer: "rt", Algorithm: "RS256", Key: &priv.PublicKey}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	srv := server.New(reg, server.WithIdentityVerifier(istVerifier(keys)))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, priv
}

// Spec: §6.3.1 / §6.3.2 — over SQLite + HTTP, a "podium:read:finance/*" scope
// narrows the discovery surface to the finance subtree while the runtime
// token is verified on every call. F-6.3.1, F-6.3.5.
func TestInjectedSessionToken_ReadScopeNarrowsOverHTTP(t *testing.T) {
	t.Parallel()
	ts, priv := istServer(t)
	token := istSign(t, priv, istClaims("openid podium:read:finance/*"))
	status, body := istGet(t, ts.URL+"/v1/search_artifacts?query=", token)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody: %s", status, body)
	}
	s := string(body)
	if !strings.Contains(s, "finance/ap/pay-invoice") {
		t.Errorf("finance artifact missing under read scope: %s", s)
	}
	if strings.Contains(s, "hr/policies") {
		t.Errorf("hr artifact leaked past read:finance/* scope: %s", s)
	}
}

// Spec: §6.3.1 — a load scope authorizes loading the granted subtree only;
// an out-of-scope load is denied (404) end-to-end. F-6.3.5.
func TestInjectedSessionToken_LoadScopeOverHTTP(t *testing.T) {
	t.Parallel()
	ts, priv := istServer(t)
	token := istSign(t, priv, istClaims("podium:load:finance/*"))
	if status, body := istGet(t, ts.URL+"/v1/load_artifact?id=finance/ap/pay-invoice", token); status != http.StatusOK {
		t.Fatalf("finance load status = %d, want 200\nbody: %s", status, body)
	}
	if status, _ := istGet(t, ts.URL+"/v1/load_artifact?id=hr/policies", token); status != http.StatusNotFound {
		t.Errorf("hr load status = %d, want 404 (out of scope)", status)
	}
}

// Spec: §6.3.2 / §6.10 — a token from an unregistered runtime is rejected
// with auth.untrusted_runtime and details.runtime_iss. F-6.3.2.
func TestInjectedSessionToken_UntrustedRejectedOverHTTP(t *testing.T) {
	t.Parallel()
	ts, priv := istServer(t)
	claims := istClaims("")
	claims["iss"] = "ghost"
	claims["act"] = "ghost"
	status, body := istGet(t, ts.URL+"/v1/search_artifacts?query=", istSign(t, priv, claims))
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401\nbody: %s", status, body)
	}
	var env struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Code != "auth.untrusted_runtime" {
		t.Errorf("code = %q, want auth.untrusted_runtime", env.Code)
	}
	if env.Details["runtime_iss"] != "ghost" {
		t.Errorf("details.runtime_iss = %v, want ghost", env.Details["runtime_iss"])
	}
}
