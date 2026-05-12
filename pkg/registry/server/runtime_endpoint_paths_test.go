package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/identity"
	"github.com/lennylabs/podium/pkg/registry/server"
)

func bootRuntimeEndpoint(t *testing.T, authErr error) *httptest.Server {
	t.Helper()
	reg := identity.NewRuntimeKeyRegistry()
	endpoint := server.NewRuntimeKeyEndpoint(reg, server.NewModeTracker())
	if authErr != nil {
		endpoint = endpoint.WithAdminAuth(func(*http.Request) error { return authErr })
	}
	ts := httptest.NewServer(endpoint.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestRuntimeRegister_Forbidden(t *testing.T) {
	t.Parallel()
	ts := bootRuntimeEndpoint(t, errForbidden{})
	resp, err := http.Post(ts.URL+"/v1/admin/runtime", "application/json", strReader(`{}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestRuntimeRegister_MalformedBody(t *testing.T) {
	t.Parallel()
	ts := bootRuntimeEndpoint(t, nil)
	resp, err := http.Post(ts.URL+"/v1/admin/runtime", "application/json", strReader("not json"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestRuntimeRegister_MissingFields(t *testing.T) {
	t.Parallel()
	ts := bootRuntimeEndpoint(t, nil)
	resp, err := http.Post(ts.URL+"/v1/admin/runtime", "application/json", strReader(`{}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestRuntimeRegister_BadPEMReturns400(t *testing.T) {
	t.Parallel()
	ts := bootRuntimeEndpoint(t, nil)
	body := `{"issuer":"x","algorithm":"RS256","public_key_pem":"not a pem"}`
	resp, err := http.Post(ts.URL+"/v1/admin/runtime", "application/json", strReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestRuntimeList_Forbidden(t *testing.T) {
	t.Parallel()
	ts := bootRuntimeEndpoint(t, errForbidden{})
	resp, err := http.Get(ts.URL + "/v1/admin/runtime")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

// strReader and identity.NewRuntimeKeyRegistry are already used elsewhere.
type errForbidden struct{}

func (errForbidden) Error() string { return "forbidden" }

// Just an unused import marker.
var _ = strings.Contains
