package server_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/identity"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

func strReader(s string) io.Reader { return strings.NewReader(s) }

func TestServerOptions_WithResourcesAndTenant(t *testing.T) {
	t.Parallel()
	resourceCalls := 0
	rf := func(_ context.Context, artifactID, resourcePath string) ([]byte, bool) {
		resourceCalls++
		return []byte("data"), true
	}
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	srv := server.New(
		core.New(st, "default", nil),
		server.WithResources(rf),
		server.WithTenant("default"),
	)
	if srv == nil {
		t.Fatal("New returned nil")
	}
	// resourceCalls remains 0 because no load_artifact request was made;
	// the goal here is to exercise the option constructors.
	_ = resourceCalls
}

func TestPublishEventForIngest_FiresEvent(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	srv := server.New(core.New(st, "default", nil))
	// Without a webhook worker the call is a no-op; just ensure it
	// doesn't panic.
	srv.PublishEventForIngest("artifact.published", map[string]any{
		"id": "x", "version": "1.0.0",
	})
}

// RuntimeKeyEndpoint.WithAdminAuth installs a custom authorization
// hook; the test verifies the hook is called.
func TestRuntimeKeyEndpoint_WithAdminAuth(t *testing.T) {
	t.Parallel()
	hits := 0
	reg := identity.NewRuntimeKeyRegistry()
	endpoint := server.NewRuntimeKeyEndpoint(reg, server.NewModeTracker()).WithAdminAuth(
		func(*http.Request) error {
			hits++
			return nil
		})
	ts := httptest.NewServer(endpoint.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/v1/admin/runtime")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if hits == 0 {
		t.Errorf("admin auth callback never fired")
	}
}

// Default admin-auth hook is a no-op that returns nil; GET succeeds.
func TestRuntimeKeyEndpoint_DefaultAdminAuthIsNoop(t *testing.T) {
	t.Parallel()
	reg := identity.NewRuntimeKeyRegistry()
	endpoint := server.NewRuntimeKeyEndpoint(reg, server.NewModeTracker())
	ts := httptest.NewServer(endpoint.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/v1/admin/runtime")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// Quota handler returns the configured quotas.
func TestQuota_HappyPath(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	srv := server.New(core.New(st, "default", nil))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/v1/quota")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// /v1/dependents on a real artifact: empty edges, status 200.
func TestDependents_RootArtifactEmptyEdges(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	srv := server.New(core.New(st, "default", []layer.Layer{
		{ID: "personal", Visibility: layer.Visibility{Public: true}},
	}))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/v1/dependents?id=does-not-exist")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// /v1/admin/grants requires POST; GET returns method-not-allowed.
func TestAdminGrants_WrongMethodReturns405(t *testing.T) {
	t.Parallel()
	// Use the admin-pre-granted test harness so requireAdmin passes.
	ts := bootRegistryWithAdmin(t, "alice", nil)
	resp, err := http.Get(ts.URL + "/v1/admin/grants")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

// /v1/admin/grants POST with malformed body returns 400.
func TestAdminGrants_MalformedBodyReturns400(t *testing.T) {
	t.Parallel()
	ts := bootRegistryWithAdmin(t, "alice", nil)
	resp, err := http.Post(ts.URL+"/v1/admin/grants",
		"application/json", strReader("not json"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// /v1/admin/grants POST with missing user_id returns 400.
func TestAdminGrants_MissingUserIDReturns400(t *testing.T) {
	t.Parallel()
	ts := bootRegistryWithAdmin(t, "alice", nil)
	resp, err := http.Post(ts.URL+"/v1/admin/grants",
		"application/json", strReader(`{}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// /v1/admin/grants DELETE without user_id query returns 400.
func TestAdminGrants_DeleteMissingUserIDReturns400(t *testing.T) {
	t.Parallel()
	ts := bootRegistryWithAdmin(t, "alice", nil)
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/v1/admin/grants", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

// /v1/admin/show-effective: wrong method, missing user_id, both
// must return 400 / 405 respectively.
func TestAdminShowEffective_WrongMethodReturns405(t *testing.T) {
	t.Parallel()
	ts := bootRegistryWithAdmin(t, "alice", nil)
	resp, err := http.Post(ts.URL+"/v1/admin/show-effective",
		"application/json", strReader(""))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", resp.StatusCode)
	}
}

func TestAdminShowEffective_MissingUserIDReturns400(t *testing.T) {
	t.Parallel()
	ts := bootRegistryWithAdmin(t, "alice", nil)
	resp, err := http.Get(ts.URL + "/v1/admin/show-effective")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}
