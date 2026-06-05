package server_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/pkg/identity"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/objectstore"
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
// details.runtime_iss and the canonical remediation.
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
// "finance"-group caller sees the finance-only artifact.
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
// the artifact (verification succeeds, visibility still applies).
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

// objectsVerifyFixture boots a server behind the verifier whose store holds a
// group-restricted ("finance") artifact that bundles one above-cutoff resource
// in an object store, so the /objects/{content_hash} route can be driven for
// distinct verified identities. It returns the running server and the resource
// key (the content hash without the sha256: prefix).
func objectsVerifyFixture(t *testing.T, verify func(*http.Request) (layer.Identity, error)) (*httptest.Server, string) {
	t.Helper()
	large := make([]byte, objectstore.InlineCutoff+2048)
	for i := range large {
		large[i] = byte('A' + i%26)
	}
	h := sha256.Sum256(large)
	key := hex.EncodeToString(h[:])

	objStore, err := objectstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("objectstore.Open: %v", err)
	}
	if err := objStore.Put(context.Background(), key, large, "application/octet-stream"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if err := st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: "default", ArtifactID: "finance/secret", Version: "1.0.0",
		ContentHash: "sha256:a", Type: "context", Description: "finance secret", Layer: "fin",
		Resources: []store.ResourceRef{
			{Path: "data/big.bin", ContentHash: "sha256:" + key, Size: int64(len(large)), ContentType: "application/octet-stream"},
		},
	}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	reg := core.New(st, "default", []layer.Layer{
		{ID: "fin", Precedence: 1, Visibility: layer.Visibility{Groups: []string{"finance"}}},
	})
	srv := server.New(reg,
		server.WithIdentityVerifier(verify),
		server.WithObjectStore(objStore, "placeholder", 0),
	)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	objStore.BaseURL = ts.URL
	return ts, key
}

// Spec: §13.11 — the filesystem /objects/{content_hash} route is token-bound:
// "the registry validates the token, confirms visibility for the artifact
// owning the content hash, and serves the bytes," so a caller who shares the
// URL "cannot grant access to bytes the other caller is not entitled to read."
// The route is therefore NOT exempt from verification: an unverifiable caller is
// rejected (401), a verified non-member is filtered (404), and only a verified
// member receives the bytes.
func TestVerify_ObjectsRouteEnforcesVisibility(t *testing.T) {
	t.Parallel()

	// Verified member of the finance group: the bytes are served.
	tsMember, keyMember := objectsVerifyFixture(t, func(*http.Request) (layer.Identity, error) {
		return layer.Identity{Sub: "alice", IsAuthenticated: true, Groups: []string{"finance"}}, nil
	})
	resp, err := http.Get(tsMember.URL + "/objects/" + keyMember)
	if err != nil {
		t.Fatalf("GET (member): %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("member /objects status = %d, want 200", resp.StatusCode)
	}

	// Verified caller outside the group: visibility re-check filters the read.
	tsOther, keyOther := objectsVerifyFixture(t, func(*http.Request) (layer.Identity, error) {
		return layer.Identity{Sub: "bob", IsAuthenticated: true, Groups: []string{"hr"}}, nil
	})
	resp2, err := http.Get(tsOther.URL + "/objects/" + keyOther)
	if err != nil {
		t.Fatalf("GET (non-member): %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("non-member /objects status = %d, want 404 (visibility re-check)", resp2.StatusCode)
	}

	// Unverifiable caller: rejected at the verifier before the handler runs, so
	// the route does not fall back to the anonymous-public resolver (the bug).
	tsBad, keyBad := objectsVerifyFixture(t, func(*http.Request) (layer.Identity, error) {
		return layer.Identity{}, &identity.UntrustedRuntimeError{Issuer: "rogue", Reason: "unregistered"}
	})
	resp3, err := http.Get(tsBad.URL + "/objects/" + keyBad)
	if err != nil {
		t.Fatalf("GET (unverifiable): %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusUnauthorized {
		t.Errorf("unverifiable /objects status = %d, want 401 (route must be verified)", resp3.StatusCode)
	}
	if e := decodeEnvelope(t, resp3); e.Code != "auth.untrusted_runtime" {
		t.Errorf("unverifiable /objects code = %q, want auth.untrusted_runtime", e.Code)
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
