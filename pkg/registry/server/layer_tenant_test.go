package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/identity"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// bootTenant is the tenant a multi-tenant test endpoint is bound to. No
// request routes to it, so a row read or written under it exposes a store
// call that used the bound tenant in place of the request's tenant.
const bootTenant = "P"

// tenantFixture is a multi-tenant LayerEndpoint over a memory store holding
// the boot tenant and the routed tenant "acme". Both tenants hold a live layer
// "shared" and a soft-deleted layer "gone", so a store call keyed by the wrong
// tenant finds a row and changes it rather than failing.
type tenantFixture struct {
	st         store.Store
	ep         *LayerEndpoint
	adminCalls int
	scopes     []core.EventScope
}

func newTenantFixture(t *testing.T) *tenantFixture {
	t.Helper()
	ctx := context.Background()
	st := store.NewMemory()
	f := &tenantFixture{st: st}
	for _, tenantID := range []string{bootTenant, "acme"} {
		if err := st.CreateTenant(ctx, store.Tenant{ID: tenantID, Active: true}); err != nil {
			t.Fatalf("CreateTenant %s: %v", tenantID, err)
		}
		for i, id := range []string{"shared", tenantID + "-only", "gone"} {
			if err := st.PutLayerConfig(ctx, store.LayerConfig{
				TenantID: tenantID, ID: id, SourceType: "git", Repo: "https://example.com/" + id + ".git",
				Order: (i + 1) * 10, CreatedAt: time.Now().UTC(),
			}); err != nil {
				t.Fatalf("PutLayerConfig %s/%s: %v", tenantID, id, err)
			}
		}
		if err := st.DeleteLayerConfig(ctx, tenantID, "gone"); err != nil {
			t.Fatalf("DeleteLayerConfig %s/gone: %v", tenantID, err)
		}
	}
	alice := layer.Identity{Sub: "alice@acme.com", IsAuthenticated: true}
	f.ep = NewLayerEndpoint(st, bootTenant, NewModeTracker()).
		WithTenantRouting().
		WithIdentityResolver(func(*http.Request) (layer.Identity, error) { return alice, nil }).
		WithAdminAuth(func(*http.Request) error {
			f.adminCalls++
			return nil
		}).
		WithEventPublisher(func(_ context.Context, scope core.EventScope, _ string, _ map[string]any) {
			f.scopes = append(f.scopes, scope)
		})
	return f
}

// serveLayer drives h with one request. A non-empty tenantID is attached the
// way the registry's §6.3.1 router attaches it; an empty one leaves the
// request unrouted.
func serveLayer(h http.Handler, method, target string, body any, tenantID string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, target, &buf)
	if tenantID != "" {
		req = req.WithContext(core.ContextWithTenant(req.Context(), tenantID))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// layerIDs lists the live layer IDs stored under tenantID.
func layerIDs(t *testing.T, st store.Store, tenantID string) map[string]store.LayerConfig {
	t.Helper()
	ls, err := st.ListLayerConfigs(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("ListLayerConfigs %s: %v", tenantID, err)
	}
	out := map[string]store.LayerConfig{}
	for _, l := range ls {
		out[l.ID] = l
	}
	return out
}

// Spec: §7.3.1 (Tenant selection) — a single-tenant endpoint serves its
// bound tenant whatever the context carries, and a multi-tenant endpoint
// serves the routed tenant and never falls back to the bound one.
func TestLayerEndpointTenant_Resolution(t *testing.T) {
	t.Parallel()
	routed := core.ContextWithTenant(context.Background(), "acme")
	single := NewLayerEndpoint(store.NewMemory(), bootTenant, NewModeTracker())
	multi := NewLayerEndpoint(store.NewMemory(), bootTenant, NewModeTracker())
	if got := multi.WithTenantRouting(); got != multi {
		t.Fatalf("WithTenantRouting should return the endpoint for chaining")
	}
	cases := []struct {
		name       string
		ep         *LayerEndpoint
		ctx        context.Context
		wantTenant string
		wantOK     bool
	}{
		{"single_unrouted", single, context.Background(), bootTenant, true},
		{"single_routed", single, routed, bootTenant, true},
		{"multi_routed", multi, routed, "acme", true},
		{"multi_unrouted", multi, context.Background(), "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tc.ep.tenant(tc.ctx)
			if got != tc.wantTenant || ok != tc.wantOK {
				t.Errorf("tenant() = (%q, %v), want (%q, %v)", got, ok, tc.wantTenant, tc.wantOK)
			}
		})
	}
}

// Spec: §7.3.1 (Tenant selection) — on a multi-tenant registry a write that
// resolves to no tenant is refused with 403 auth.forbidden before any layer
// is read and before the admin check, even where the admin callback admits
// every caller, and no row is written under any tenant.
func TestLayerEndpoint_UnroutedWritesRefused(t *testing.T) {
	t.Parallel()
	writes := []struct {
		name, method, target string
		body                 any
	}{
		{"register", http.MethodPost, "/v1/layers", map[string]any{"id": "new", "source_type": "git", "repo": "https://example.com/n.git"}},
		{"update", http.MethodPut, "/v1/layers/update?id=shared", map[string]any{"ref": "next"}},
		{"restore", http.MethodPost, "/v1/layers/restore?id=gone", nil},
		{"unregister", http.MethodDelete, "/v1/layers?id=shared", nil},
		{"reorder", http.MethodPost, "/v1/layers/reorder", map[string]any{"order": []string{"gone", "shared"}}},
		{"reingest", http.MethodPost, "/v1/layers/reingest?id=shared", nil},
	}
	for _, wr := range writes {
		t.Run(wr.name, func(t *testing.T) {
			t.Parallel()
			f := newTenantFixture(t)
			before := layerIDs(t, f.st, bootTenant)
			rec := serveLayer(f.ep.Handler(), wr.method, wr.target, wr.body, "")
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403; body %s", rec.Code, rec.Body.String())
			}
			env := decodeEnvelope(t, rec)
			if env.Code != "auth.forbidden" || env.Message != "request resolves to no tenant" {
				t.Errorf("envelope = %s %q, want auth.forbidden %q", env.Code, env.Message, "request resolves to no tenant")
			}
			if f.adminCalls != 0 {
				t.Errorf("admin callback ran %d times, want 0", f.adminCalls)
			}
			after := layerIDs(t, f.st, bootTenant)
			if len(after) != len(before) || after["shared"].Ref != before["shared"].Ref {
				t.Errorf("boot tenant layers changed: before %v, after %v", before, after)
			}
			if ls := layerIDs(t, f.st, ""); len(ls) != 0 {
				t.Errorf("rows written under the empty tenant: %v", ls)
			}
			if len(f.scopes) != 0 {
				t.Errorf("published %d events, want 0", len(f.scopes))
			}
		})
	}
}

// Spec: §7.3.1 (Tenant selection) — the read-only refusal and, on reorder,
// the credential-failure refusal take precedence over the unrouted refusal.
func TestLayerEndpoint_UnroutedRefusalPrecedence(t *testing.T) {
	t.Parallel()
	t.Run("read_only_first", func(t *testing.T) {
		t.Parallel()
		f := newTenantFixture(t)
		f.ep.mode.Set(ModeReadOnly)
		rec := serveLayer(f.ep.Handler(), http.MethodDelete, "/v1/layers?id=shared", nil, "")
		if env := decodeEnvelope(t, rec); env.Code != "registry.read_only" {
			t.Errorf("code = %s, want registry.read_only", env.Code)
		}
	})
	t.Run("credential_failure_first", func(t *testing.T) {
		t.Parallel()
		f := newTenantFixture(t)
		f.ep.WithIdentityResolver(func(*http.Request) (layer.Identity, error) {
			return layer.Identity{}, identity.ErrTokenExpired
		})
		for _, target := range []string{"/v1/layers/reorder", "/v1/layers"} {
			method := http.MethodPost
			if target == "/v1/layers" {
				method = http.MethodGet
			}
			rec := serveLayer(f.ep.Handler(), method, target, map[string]any{"order": []string{"shared"}}, "")
			if env := decodeEnvelope(t, rec); env.Code != "auth.token_expired" {
				t.Errorf("%s %s code = %s, want auth.token_expired", method, target, env.Code)
			}
		}
	})
}

// Spec: §7.3.1 (Tenant selection) — list returns no layers, on either arm, to
// a request that resolves to no tenant, even where the admin callback would
// grant the whole list.
func TestLayerEndpoint_UnroutedListEmpty(t *testing.T) {
	t.Parallel()
	f := newTenantFixture(t)
	for _, target := range []string{"/v1/layers", "/v1/layers?deleted=true"} {
		rec := serveLayer(f.ep.Handler(), http.MethodGet, target, nil, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", target, rec.Code)
		}
		var got struct {
			Layers []store.LayerConfig `json:"layers"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Layers == nil || len(got.Layers) != 0 {
			t.Errorf("GET %s layers = %v, want []", target, got.Layers)
		}
	}
}

// Spec: §7.3.1 (Tenant selection) — a routed request lists, registers,
// updates, reorders, unregisters, restores, and reingests in its own tenant,
// and the events those writes publish carry that tenant. The boot tenant's
// identically named rows stay untouched.
func TestLayerEndpoint_RoutedActsInRequestTenant(t *testing.T) {
	t.Parallel()
	f := newTenantFixture(t)
	h := f.ep.Handler()
	bootBefore := layerIDs(t, f.st, bootTenant)

	rec := serveLayer(h, http.MethodGet, "/v1/layers", nil, "acme")
	var listed struct {
		Layers []store.LayerConfig `json:"layers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	for _, l := range listed.Layers {
		if l.ID == bootTenant+"-only" {
			t.Errorf("routed list returned the boot tenant's layer %s", l.ID)
		}
	}
	if len(listed.Layers) != 2 {
		t.Errorf("routed list = %d layers, want 2", len(listed.Layers))
	}

	steps := []struct {
		method, target string
		body           any
		want           int
	}{
		{http.MethodPost, "/v1/layers", map[string]any{"id": "new", "source_type": "git", "repo": "https://example.com/n.git"}, http.StatusCreated},
		{http.MethodPut, "/v1/layers/update?id=shared", map[string]any{"ref": "next"}, http.StatusOK},
		{http.MethodPost, "/v1/layers/reorder", map[string]any{"order": []string{"acme-only", "shared"}}, http.StatusOK},
		{http.MethodPost, "/v1/layers/reingest?id=shared", nil, http.StatusOK},
		{http.MethodPost, "/v1/layers/reingest?id=" + bootTenant + "-only", nil, http.StatusNotFound},
		{http.MethodDelete, "/v1/layers?id=shared", nil, http.StatusOK},
		{http.MethodPost, "/v1/layers/restore?id=gone", nil, http.StatusOK},
	}
	for _, s := range steps {
		if rec := serveLayer(h, s.method, s.target, s.body, "acme"); rec.Code != s.want {
			t.Fatalf("%s %s status = %d, want %d; body %s", s.method, s.target, rec.Code, s.want, rec.Body.String())
		}
	}

	acme := layerIDs(t, f.st, "acme")
	if l, ok := acme["new"]; !ok || l.TenantID != "acme" {
		t.Errorf("registered layer = %+v (present %v), want it stored under acme", l, ok)
	}
	if _, ok := acme["shared"]; ok {
		t.Errorf("acme/shared still live after unregister")
	}
	if _, ok := acme["gone"]; !ok {
		t.Errorf("acme/gone not restored")
	}
	if acme["acme-only"].Order != 10 {
		t.Errorf("acme-only order = %d, want 10 after reorder", acme["acme-only"].Order)
	}
	bootAfter := layerIDs(t, f.st, bootTenant)
	if len(bootAfter) != len(bootBefore) || bootAfter["shared"].Ref != "" || bootAfter["shared"].Order != 10 {
		t.Errorf("boot tenant changed: before %v, after %v", bootBefore, bootAfter)
	}
	if _, ok := bootAfter["gone"]; ok {
		t.Errorf("boot tenant's gone was restored")
	}
	if len(f.scopes) == 0 {
		t.Fatalf("no events published")
	}
	for _, sc := range f.scopes {
		if sc.TenantID != "acme" {
			t.Errorf("event scope tenant = %q, want acme (layers %v)", sc.TenantID, sc.Layers)
		}
	}
}

// Spec: §7.3.1 (Tenant selection) — the user-defined layer cap is resolved in
// the request's tenant.
func TestLayerEndpoint_EffectiveCapReadsRequestTenant(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := store.NewMemory()
	for id, n := range map[string]int{bootTenant: 7, "acme": 1} {
		if err := st.CreateTenant(ctx, store.Tenant{ID: id, Quota: store.Quota{MaxUserLayers: n}}); err != nil {
			t.Fatalf("CreateTenant: %v", err)
		}
	}
	ep := NewLayerEndpoint(st, bootTenant, NewModeTracker()).WithTenantRouting()
	if got := ep.effectiveLayerCap(ctx, "acme"); got != 1 {
		t.Errorf("effectiveLayerCap(acme) = %d, want 1", got)
	}
}

// Spec: §8.5, §4.7.1 — erasure on a multi-tenant registry is refused with
// 403 auth.forbidden for every caller, routed or not, before the admin check
// and before any layer is read or purged.
func TestLayerEndpoint_MultiTenantEraseRefused(t *testing.T) {
	t.Parallel()
	for _, tenantID := range []string{"acme", ""} {
		f := newTenantFixture(t)
		ctx := context.Background()
		if err := f.st.PutLayerConfig(ctx, store.LayerConfig{
			TenantID: "acme", ID: "alice-personal", SourceType: "git", Repo: "https://example.com/a.git",
			UserDefined: true, Owner: "alice@acme.com", CreatedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("PutLayerConfig: %v", err)
		}
		rec := serveLayer(f.ep.EraseHandler(), http.MethodPost, "/v1/admin/erase",
			map[string]any{"user_id": "alice@acme.com", "salt": "s"}, tenantID)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("tenant %q: status = %d, want 403", tenantID, rec.Code)
		}
		env := decodeEnvelope(t, rec)
		if env.Code != "auth.forbidden" || env.Message != "erasure is not available on a multi-tenant registry" {
			t.Errorf("tenant %q: envelope = %s %q", tenantID, env.Code, env.Message)
		}
		if f.adminCalls != 0 {
			t.Errorf("tenant %q: admin callback ran %d times, want 0", tenantID, f.adminCalls)
		}
		if _, ok := layerIDs(t, f.st, "acme")["alice-personal"]; !ok {
			t.Errorf("tenant %q: alice-personal was purged", tenantID)
		}
	}
}

// Spec: §7.3.4, §7.3.1 (Tenant selection) — manage_any_layer is evaluated in
// the routed tenant and is false for an unrouted request on a multi-tenant
// endpoint, even where the admin callback admits every caller. A
// single-tenant endpoint keeps the admin callback's answer.
func TestLayerEndpoint_CapabilitiesFollowTenantSelection(t *testing.T) {
	t.Parallel()
	admit := func(*http.Request) error { return nil }
	deny := func(*http.Request) error { return ErrAdminRequired }
	cases := []struct {
		name   string
		multi  bool
		auth   func(*http.Request) error
		tenant string
		want   bool
	}{
		{"multi_unrouted_admit", true, admit, "", false},
		{"multi_routed_admit", true, admit, "acme", true},
		{"multi_routed_deny", true, deny, "acme", false},
		{"single_unrouted_admit", false, admit, "", true},
		{"single_unrouted_deny", false, deny, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ep := NewLayerEndpoint(store.NewMemory(), bootTenant, NewModeTracker()).WithAdminAuth(tc.auth)
			if tc.multi {
				ep.WithTenantRouting()
			}
			req := httptest.NewRequest(http.MethodGet, "/v1/ui/session", nil)
			if tc.tenant != "" {
				req = req.WithContext(core.ContextWithTenant(req.Context(), tc.tenant))
			}
			if got := ep.Capabilities(req).ManageAnyLayer; got != tc.want {
				t.Errorf("ManageAnyLayer = %v, want %v", got, tc.want)
			}
		})
	}
}
