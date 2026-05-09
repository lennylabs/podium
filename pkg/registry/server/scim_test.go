package server_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/scim"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §6.3.1 — when WithSCIM is configured, /scim/v2/Users
// requests are routed to the SCIM handler and bearer-token auth
// applies.
// Phase: 7
func TestServer_MountsSCIMHandler(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	scimStore := scim.NewMemory()
	srv := server.New(core.New(st, "default", nil),
		server.WithSCIM(&scim.Handler{Store: scimStore, Tokens: map[string]bool{"tok": true}}),
	)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	body := []byte(`{"schemas":["urn:ietf:params:scim:schemas:core:2.0:User"],"userName":"alice@x","active":true}`)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/scim/v2/Users", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("Content-Type", "application/scim+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d: %s", resp.StatusCode, buf)
	}

	users, _ := scimStore.ListUsers(context.Background(), scim.Filter{})
	if len(users) != 1 || users[0].UserName != "alice@x" {
		t.Errorf("store users = %+v, want one alice@x", users)
	}
}

// Spec: §6.3.1 — without a configured SCIM handler the route is
// not registered and /scim/v2/* returns 404.
// Phase: 7
func TestServer_SCIMUnmountedReturns404(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "default"})
	srv := server.New(core.New(st, "default", nil))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/scim/v2/Users")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (SCIM not mounted)", resp.StatusCode)
	}
}
