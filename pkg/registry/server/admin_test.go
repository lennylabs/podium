package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// adminIdentity returns an Identity that AdminAuthorize accepts
// after the test pre-grants admin in the store.
func adminIdentity(sub string) layer.Identity {
	return layer.Identity{Sub: sub, IsAuthenticated: true}
}

// bootRegistryWithAdmin spins up a server where `sub` is pre-granted
// admin so admin-gated routes accept the request.
func bootRegistryWithAdmin(t *testing.T, sub string, layers []layer.Layer, opts ...server.Option) *httptest.Server {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if sub != "" {
		_ = st.GrantAdmin(context.Background(), store.AdminGrant{
			UserID: sub, OrgID: "default",
		})
	}
	options := append([]server.Option{
		server.WithIdentityResolver(func(*http.Request) layer.Identity {
			if sub == "" {
				return layer.Identity{IsPublic: true}
			}
			return adminIdentity(sub)
		}),
	}, opts...)
	srv := server.New(core.New(st, "default", layers), options...)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// Spec: §4.7.2 — POST /v1/admin/grants creates an admin grant for
// the named user; GET /v1/admin/show-effective then resolves the
// per-layer visibility for that user.
func TestAdminGrants_RoundTrip(t *testing.T) {
	t.Parallel()
	ts := bootRegistryWithAdmin(t, "alice", []layer.Layer{
		{ID: "team", Visibility: layer.Visibility{Public: true}},
	})
	body, _ := json.Marshal(map[string]string{"user_id": "bob"})
	resp, err := http.Post(ts.URL+"/v1/admin/grants", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d: %s", resp.StatusCode, buf)
	}

	// Revoke: bob is no longer admin (alice still is).
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/v1/admin/grants?user_id=bob", nil)
	delResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", delResp.StatusCode)
	}
}

// Spec: §6.10 / §4.7.2 — admin endpoints reject non-admin callers
// with auth.forbidden.
func TestAdminGrants_NonAdminRejected(t *testing.T) {
	t.Parallel()
	ts := bootRegistryWithAdmin(t, "" /* anonymous caller */, nil)
	body, _ := json.Marshal(map[string]string{"user_id": "bob"})
	resp, err := http.Post(ts.URL+"/v1/admin/grants", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	buf, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(buf, []byte("auth.forbidden")) {
		t.Errorf("response missing auth.forbidden: %s", buf)
	}
}

// Spec: §4.6 — show-effective returns one row per configured
// layer with a stable Reason string explaining the verdict.
func TestAdminShowEffective_PerLayerVisibility(t *testing.T) {
	t.Parallel()
	layers := []layer.Layer{
		{ID: "public", Visibility: layer.Visibility{Public: true}, Precedence: 1},
		{ID: "engineering", Visibility: layer.Visibility{Groups: []string{"engineering"}}, Precedence: 2},
		{ID: "alice-only", Visibility: layer.Visibility{Users: []string{"alice"}}, Precedence: 3},
	}
	ts := bootRegistryWithAdmin(t, "alice", layers)
	resp, err := http.Get(ts.URL + "/v1/admin/show-effective?user_id=bob&group=engineering")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d: %s", resp.StatusCode, buf)
	}
	var parsed struct {
		UserID string                `json:"user_id"`
		Layers []core.EffectiveLayer `json:"layers"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&parsed)
	if parsed.UserID != "bob" {
		t.Errorf("user_id = %q, want bob", parsed.UserID)
	}
	want := map[string]bool{
		"public":      true,
		"engineering": true,
		"alice-only":  false,
	}
	for _, l := range parsed.Layers {
		got := l.Visible
		if want[l.LayerID] != got {
			t.Errorf("layer %s visible = %v, want %v (reason: %s)",
				l.LayerID, got, want[l.LayerID], l.Reason)
		}
	}
}
