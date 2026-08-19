package e2e

// The §6.3.2 trusted runtime signing-key set comes from operator-supplied
// configuration read at startup, and the registry exposes no request-time
// registration API. These tests pin the absence of the route on the running
// binary.

import (
	"net/http"
	"strings"
	"testing"
)

// injPost issues a POST with an optional Bearer token and returns the status
// code. It is the write-path counterpart of injGet.
func injPost(t *testing.T, url, token, body string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

// A standalone registry runs no identity verifier, so an unauthenticated
// request reaches the mux and both verbs of the removed route answer 404.
//
// Spec: §6.3.2
func TestRuntimeKeyRoute_RemovedOnStandaloneBoot(t *testing.T) {
	t.Parallel()
	srv := startServer(t, apiReg(t))
	status, body := injGet(t, srv.BaseURL+"/v1/admin/runtime", "")
	if status != http.StatusNotFound {
		t.Errorf("GET status = %d, want 404\nbody: %s", status, body)
	}
	post := injPost(t, srv.BaseURL+"/v1/admin/runtime", "",
		`{"issuer":"e2e-runtime","algorithm":"RS256","public_key_pem":"x"}`)
	if post != http.StatusNotFound {
		t.Errorf("POST status = %d, want 404", post)
	}
}

// Under injected-session-token the route is covered by pathRequiresIdentity,
// so only a verified caller gets past the identity gate. That caller reaches
// the mux and the removed route answers 404. An unauthenticated caller is
// rejected with 401 auth.untrusted_runtime before routing, which the
// authentication tests already pin.
//
// Spec: §6.3.2
func TestRuntimeKeyRoute_RemovedForVerifiedCaller(t *testing.T) {
	t.Parallel()
	priv, pem := injKeyPair(t)
	srv := injServer(t, apiReg(t), priv, pem)
	token := injSignJWT(t, priv, injClaims("alice"))
	status, body := injGet(t, srv.BaseURL+"/v1/admin/runtime", token)
	if status != http.StatusNotFound {
		t.Errorf("GET status = %d, want 404\nbody: %s", status, body)
	}
}
