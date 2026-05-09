package server_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/identity"
	"github.com/lennylabs/podium/pkg/registry/server"
)

// Spec: §6.3.2 — POST /v1/admin/runtime takes a PEM public key +
// algorithm and adds the runtime to the trust list.
// Phase: 11
func TestRuntimeRegister_Ed25519(t *testing.T) {
	testharness.RequirePhase(t, 11)
	t.Parallel()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	der, _ := x509.MarshalPKIXPublicKey(pub)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})

	reg := identity.NewRuntimeKeyRegistry()
	endpoint := server.NewRuntimeKeyEndpoint(reg, server.NewModeTracker())
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)

	body, _ := json.Marshal(map[string]any{
		"issuer":         "claude-runtime",
		"algorithm":      "EdDSA",
		"public_key_pem": string(pemBytes),
	})
	resp, err := http.Post(ts.URL+"/v1/admin/runtime", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	if _, ok := reg.Lookup("claude-runtime"); !ok {
		t.Errorf("registry missing claude-runtime")
	}
}

// Spec: §6.3.2 — GET /v1/admin/runtime lists registered runtimes
// with their algorithms (no key bytes).
// Phase: 11
func TestRuntimeRegister_List(t *testing.T) {
	testharness.RequirePhase(t, 11)
	t.Parallel()
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	reg := identity.NewRuntimeKeyRegistry()
	_ = reg.Register(identity.RuntimeKey{Issuer: "alpha", Algorithm: "EdDSA", Key: pub})
	_ = reg.Register(identity.RuntimeKey{Issuer: "beta", Algorithm: "EdDSA", Key: pub})

	endpoint := server.NewRuntimeKeyEndpoint(reg, server.NewModeTracker())
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/v1/admin/runtime")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got server.RuntimeListResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Runtimes) != 2 {
		t.Fatalf("len = %d, want 2", len(got.Runtimes))
	}
	if got.Runtimes[0].Issuer != "alpha" || got.Runtimes[1].Issuer != "beta" {
		t.Errorf("got = %v, want sorted [alpha, beta]", got.Runtimes)
	}
}

// Spec: §6.3.2 — algorithm mismatch surfaces as
// registry.invalid_argument.
// Phase: 11
func TestRuntimeRegister_AlgorithmMismatch(t *testing.T) {
	testharness.RequirePhase(t, 11)
	t.Parallel()
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(pub)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})

	reg := identity.NewRuntimeKeyRegistry()
	endpoint := server.NewRuntimeKeyEndpoint(reg, server.NewModeTracker())
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)

	body, _ := json.Marshal(map[string]any{
		"issuer":         "x",
		"algorithm":      "RS256",
		"public_key_pem": string(pemBytes),
	})
	resp, _ := http.Post(ts.URL+"/v1/admin/runtime", "application/json", bytes.NewReader(body))
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}
