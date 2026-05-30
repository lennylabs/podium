package server_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/pkg/identity"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// verifyFixture boots a server whose meta-tool routes run the §6.3.2
// injected-session-token verifier. The "finance/secret" artifact lives in a
// layer visible only to the "finance" group so a verified identity's claims
// can be observed driving visibility.
func verifyFixture(t *testing.T, verify func(*http.Request) (layer.Identity, error)) *httptest.Server {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if err := st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: "default", ArtifactID: "finance/secret", Version: "1.0.0",
		ContentHash: "sha256:a", Type: "context", Description: "finance secret", Layer: "fin",
	}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	reg := core.New(st, "default", []layer.Layer{
		{ID: "fin", Precedence: 1, Visibility: layer.Visibility{Groups: []string{"finance"}}},
	})
	srv := server.New(reg, server.WithIdentityVerifier(verify))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func decodeEnvelope(t *testing.T, resp *http.Response) server.ErrorResponse {
	t.Helper()
	var e server.ErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return e
}

// Spec: §6.3.2 / §6.9 / §6.10 — an unregistered/unsigned runtime token is
// rejected on a real meta-tool call with auth.untrusted_runtime, carrying
// details.runtime_iss and the canonical remediation. F-6.3.1, F-6.3.2.
func TestVerify_UntrustedRuntimeRejected(t *testing.T) {
	t.Parallel()
	ts := verifyFixture(t, func(*http.Request) (layer.Identity, error) {
		return layer.Identity{}, &identity.UntrustedRuntimeError{
			Issuer: "managed-runtime-x", Reason: "issuer is not a registered runtime",
		}
	})
	resp, err := http.Get(ts.URL + "/v1/search_artifacts?query=secret")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	e := decodeEnvelope(t, resp)
	if e.Code != "auth.untrusted_runtime" {
		t.Errorf("code = %q, want auth.untrusted_runtime", e.Code)
	}
	if e.Details["runtime_iss"] != "managed-runtime-x" {
		t.Errorf("details.runtime_iss = %v, want managed-runtime-x", e.Details["runtime_iss"])
	}
	if e.SuggestedAction == "" {
		t.Errorf("suggested_action empty; expected the §6.10 remediation")
	}
}

// Spec: §6.9 / §6.10 — an expired injected token surfaces auth.token_expired.
func TestVerify_TokenExpiredRejected(t *testing.T) {
	t.Parallel()
	ts := verifyFixture(t, func(*http.Request) (layer.Identity, error) {
		return layer.Identity{}, fmt.Errorf("%w: token is expired", identity.ErrTokenExpired)
	})
	resp, err := http.Get(ts.URL + "/v1/search_artifacts?query=secret")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if e := decodeEnvelope(t, resp); e.Code != "auth.token_expired" {
		t.Errorf("code = %q, want auth.token_expired", e.Code)
	}
}

// Spec: §6.3.2 — a verified identity's claims drive visibility: the
// "finance"-group caller sees the finance-only artifact. F-6.3.1.
func TestVerify_VerifiedIdentityDrivesVisibility(t *testing.T) {
	t.Parallel()
	ts := verifyFixture(t, func(*http.Request) (layer.Identity, error) {
		return layer.Identity{Sub: "alice", IsAuthenticated: true, Groups: []string{"finance"}}, nil
	})
	resp, err := http.Get(ts.URL + "/v1/load_artifact?id=finance/secret")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (finance group should see finance/secret)", resp.StatusCode)
	}
}

// Spec: §6.3.2 — a verified identity outside the layer's group cannot see
// the artifact (verification succeeds, visibility still applies). F-6.3.1.
func TestVerify_VerifiedIdentityVisibilityFiltered(t *testing.T) {
	t.Parallel()
	ts := verifyFixture(t, func(*http.Request) (layer.Identity, error) {
		return layer.Identity{Sub: "bob", IsAuthenticated: true, Groups: []string{"hr"}}, nil
	})
	resp, err := http.Get(ts.URL + "/v1/load_artifact?id=finance/secret")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (hr caller must not see finance/secret)", resp.StatusCode)
	}
}

// Spec: §6.3.2 — operational probes do not run on a caller session token,
// so /healthz answers even when verification would reject every request.
func TestVerify_HealthExemptFromVerification(t *testing.T) {
	t.Parallel()
	ts := verifyFixture(t, func(*http.Request) (layer.Identity, error) {
		return layer.Identity{}, &identity.UntrustedRuntimeError{Reason: "no token"}
	})
	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/healthz status = %d, want 200 (exempt from verification)", resp.StatusCode)
	}
}

// Spec: §6.3.2 — with no verifier installed the server stays on the
// anonymous resolver, so standalone / public deployments are unaffected.
func TestVerify_NoVerifierServesAnonymously(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	reg := core.New(st, "default", nil) // no layers => everything visible
	srv := server.New(reg)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	resp, err := http.Get(ts.URL + "/v1/search_artifacts?query=anything")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (anonymous passthrough)", resp.StatusCode)
	}
}
