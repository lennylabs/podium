package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/webhook"
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

// postReembed posts a bare tenant-wide re-embed and returns the status
// and the §6.10 envelope code the registry answered with.
func postReembed(t *testing.T, base string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/admin/reembed", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	var env struct {
		Code string `json:"code"`
	}
	buf, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(buf, &env)
	return resp.StatusCode, env.Code
}

// Spec: §4.7.2 — POST /v1/admin/reembed is authorized by the per-tenant
// admin role, so a caller without it is rejected with auth.forbidden.
// The helper passes no WithUnauthenticatedReembed, so the gate is live
// and the resolver supplies the anonymous-public identity requireAdmin
// reads.
func TestAdminReembed_AnonymousCallerForbidden(t *testing.T) {
	t.Parallel()
	ts := bootRegistryWithAdmin(t, "", nil)
	status, code := postReembed(t, ts.URL)
	if status != http.StatusForbidden {
		t.Errorf("status = %d, want 403", status)
	}
	if code != "auth.forbidden" {
		t.Errorf("code = %q, want auth.forbidden", code)
	}
}

// Spec: §4.7.2 — a caller holding the per-tenant admin role passes the
// gate and reaches the handler. The test registry wires no vector search
// backend, so the pass itself fails with registry.unavailable; what this
// pins is that the request is not rejected as forbidden.
func TestAdminReembed_AdminCallerReachesHandler(t *testing.T) {
	t.Parallel()
	ts := bootRegistryWithAdmin(t, "alice", nil)
	status, code := postReembed(t, ts.URL)
	if status == http.StatusForbidden {
		t.Fatalf("status = 403 (code %q), want the handler to run for an admin caller", code)
	}
	if status != http.StatusOK && status != http.StatusInternalServerError {
		t.Errorf("status = %d, want 200 or 500", status)
	}
}

// Spec: §4.7, §13.1.1 — a deployment that authenticates no caller has no
// per-tenant admin role to check, so the boot path records that with
// WithUnauthenticatedReembed and the re-embed gate is skipped. Without the
// carve-out the endpoint would be unreachable on such a registry.
func TestAdminReembed_UnauthenticatedDeploymentAdmitted(t *testing.T) {
	t.Parallel()
	ts := bootRegistryWithAdmin(t, "", nil, server.WithUnauthenticatedReembed())
	status, code := postReembed(t, ts.URL)
	if status == http.StatusForbidden {
		t.Fatalf("status = 403 (code %q), want the handler to run on a registry that authenticates no caller", code)
	}
	if status != http.StatusOK && status != http.StatusInternalServerError {
		t.Errorf("status = %d, want 200 or 500", status)
	}
}

// Spec: §4.7.2, §7.3.2 — the no-caller carve-out is read by handleReembed
// alone. requireAdmin keeps refusing an anonymous caller on the other admin
// routes, so a registry that authenticates nobody does not thereby open
// /v1/admin/grants or the webhook receiver CRUD.
func TestAdminReembed_CarveOutDoesNotReopenOtherAdminRoutes(t *testing.T) {
	t.Parallel()
	worker := &webhook.Worker{Store: webhook.NewMemoryStore(), HTTPClient: http.DefaultClient}
	ts := bootRegistryWithAdmin(t, "", nil,
		server.WithUnauthenticatedReembed(), server.WithWebhooks(worker))

	cases := []struct {
		name string
		path string
		body string
	}{
		{"grants", "/v1/admin/grants", `{"user_id":"bob"}`},
		{"webhooks", "/v1/webhooks", `{"url":"https://example.test/hook"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.Post(ts.URL+tc.path, "application/json", strings.NewReader(tc.body))
			if err != nil {
				t.Fatalf("POST %s: %v", tc.path, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", resp.StatusCode)
			}
			buf, _ := io.ReadAll(resp.Body)
			if !bytes.Contains(buf, []byte("auth.forbidden")) {
				t.Errorf("response missing auth.forbidden: %s", buf)
			}
		})
	}
}
