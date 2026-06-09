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

// operatorCaller is an authenticated caller the tests grant the operator role.
var operatorCaller = layer.Identity{Sub: "operator@acme.com", OrgID: "acme.com", IsAuthenticated: true}

// bootTenantServer boots a server bound to the "default" org with the given
// caller identity. multiTenant installs a tenant router so the §7.3.3
// endpoints are available; single-tenant leaves it nil. It returns the store
// so a test can grant the operator role or inspect a tenant directly.
func bootTenantServer(t *testing.T, id layer.Identity, multiTenant bool, opts ...server.Option) (*httptest.Server, store.Store) {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default", Name: "default"}); err != nil {
		t.Fatalf("seed default tenant: %v", err)
	}
	options := []server.Option{
		server.WithIdentityResolver(func(*http.Request) layer.Identity { return id }),
	}
	if multiTenant {
		options = append(options, server.WithTenantRouter(
			func(context.Context, string) (string, bool) { return "default", true }, true))
	}
	options = append(options, opts...)
	srv := server.New(core.New(st, "default", nil), options...)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, st
}

// tenantWire mirrors the §7.3.3 response object for decoding.
type tenantWire struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Quota struct {
		StorageBytes      int64 `json:"storage_bytes"`
		SearchQPS         int   `json:"search_qps"`
		MaterializeRate   int   `json:"materialize_rate"`
		AuditVolumePerDay int64 `json:"audit_volume_per_day"`
		MaxUserLayers     int   `json:"max_user_layers"`
	} `json:"quota"`
	ExposeScopePreview *bool `json:"expose_scope_preview"`
	Active             bool  `json:"active"`
}

// tenantHTTP issues a request with an optional JSON body, returning the status
// and raw body.
func tenantHTTP(t *testing.T, method, url, body string) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = bytes.NewReader([]byte(body))
	}
	httpReq, err := http.NewRequest(method, url, r)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != "" {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

// tenantErrCode decodes a §6.10 error envelope and returns its code.
func tenantErrCode(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(body, &env)
	return env.Code
}

// Spec: §7.3.3 — the create / list / update / deactivate lifecycle on a
// multi-tenant registry, with idempotent create, the PATCH partial merge, and
// reactivation through PATCH active:true.
func TestTenants_CRUDRoundTrip(t *testing.T) {
	t.Parallel()
	ts, st := bootTenantServer(t, operatorCaller, true)
	if err := st.GrantOperator(context.Background(), operatorCaller.Sub); err != nil {
		t.Fatalf("grant operator: %v", err)
	}
	tenantsURL := ts.URL + "/v1/admin/tenants"

	code, body := tenantHTTP(t, http.MethodPost, tenantsURL, `{"name":"globex.com","quota":{"storage_bytes":1000,"max_user_layers":5}}`)
	if code != http.StatusCreated {
		t.Fatalf("create status = %d (%s), want 201", code, body)
	}
	var created tenantWire
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.Name != "globex.com" || !created.Active || created.Quota.StorageBytes != 1000 || created.Quota.MaxUserLayers != 5 {
		t.Fatalf("created tenant = %+v", created)
	}
	gid := created.ID

	// Idempotent re-create returns 200 with the existing tenant.
	if code, _ := tenantHTTP(t, http.MethodPost, tenantsURL, `{"name":"globex.com"}`); code != http.StatusOK {
		t.Errorf("re-create status = %d, want 200", code)
	}

	// List includes default and globex.
	code, body = tenantHTTP(t, http.MethodGet, tenantsURL, "")
	if code != http.StatusOK {
		t.Fatalf("list status = %d", code)
	}
	var list struct {
		Tenants []tenantWire `json:"tenants"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	names := map[string]bool{}
	for _, tn := range list.Tenants {
		names[tn.Name] = true
	}
	if !names["default"] || !names["globex.com"] {
		t.Errorf("list missing tenants: %+v", list.Tenants)
	}

	// PATCH only storage_bytes preserves max_user_layers.
	code, body = tenantHTTP(t, http.MethodPatch, tenantsURL+"/"+gid, `{"quota":{"storage_bytes":2000}}`)
	if code != http.StatusOK {
		t.Fatalf("patch status = %d (%s)", code, body)
	}
	var patched tenantWire
	_ = json.Unmarshal(body, &patched)
	if patched.Quota.StorageBytes != 2000 || patched.Quota.MaxUserLayers != 5 {
		t.Errorf("patch did not merge: %+v", patched.Quota)
	}

	// Deactivate (soft): the tenant still exists, inactive.
	if code, _ := tenantHTTP(t, http.MethodDelete, tenantsURL+"/"+gid, ""); code != http.StatusNoContent {
		t.Errorf("deactivate status = %d, want 204", code)
	}
	if tn, err := st.GetTenant(context.Background(), gid); err != nil || tn.Active {
		t.Errorf("after deactivate: tenant=%+v err=%v (want exists, inactive)", tn, err)
	}

	// Reactivate via PATCH active:true.
	if code, _ := tenantHTTP(t, http.MethodPatch, tenantsURL+"/"+gid, `{"active":true}`); code != http.StatusOK {
		t.Errorf("reactivate status = %d", code)
	}
	if tn, _ := st.GetTenant(context.Background(), gid); !tn.Active {
		t.Errorf("PATCH active:true did not reactivate")
	}
}

// Spec: §6.10 / §4.7.1 — an authenticated non-operator is rejected with
// auth.forbidden.
func TestTenants_NonOperatorForbidden(t *testing.T) {
	t.Parallel()
	ts, _ := bootTenantServer(t, layer.Identity{Sub: "intruder@acme.com", OrgID: "acme.com", IsAuthenticated: true}, true)
	code, body := tenantHTTP(t, http.MethodPost, ts.URL+"/v1/admin/tenants", `{"name":"x"}`)
	if code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", code)
	}
	if c := tenantErrCode(t, body); c != "auth.forbidden" {
		t.Errorf("code = %q, want auth.forbidden", c)
	}
}

// Spec: §7.3.3 / §4.7.1 — a single-tenant registry rejects every tenant-
// management request with registry.tenant_management_unavailable, regardless
// of operator authorization.
func TestTenants_SingleTenantUnavailable(t *testing.T) {
	t.Parallel()
	ts, st := bootTenantServer(t, operatorCaller, false /* single-tenant */)
	_ = st.GrantOperator(context.Background(), operatorCaller.Sub) // still rejected
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/v1/admin/tenants", ""},
		{http.MethodPost, "/v1/admin/tenants", `{"name":"x"}`},
		{http.MethodPatch, "/v1/admin/tenants/abc", `{"active":false}`},
		{http.MethodDelete, "/v1/admin/tenants/abc", ""},
	} {
		code, body := tenantHTTP(t, tc.method, ts.URL+tc.path, tc.body)
		if code != http.StatusNotFound {
			t.Errorf("%s %s status = %d, want 404", tc.method, tc.path, code)
		}
		if c := tenantErrCode(t, body); c != "registry.tenant_management_unavailable" {
			t.Errorf("%s %s code = %q, want registry.tenant_management_unavailable", tc.method, tc.path, c)
		}
	}
}

// Spec: §13.2.1 — the mutating endpoints are rejected in read-only mode with
// registry.read_only; GET stays available.
func TestTenants_ReadOnly(t *testing.T) {
	t.Parallel()
	mode := server.NewModeTracker()
	mode.Set(server.ModeReadOnly)
	ts, st := bootTenantServer(t, operatorCaller, true, server.WithMode(mode))
	_ = st.GrantOperator(context.Background(), operatorCaller.Sub)

	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/v1/admin/tenants", `{"name":"x"}`},
		{http.MethodPatch, "/v1/admin/tenants/abc", `{"active":false}`},
		{http.MethodDelete, "/v1/admin/tenants/abc", ""},
	} {
		code, body := tenantHTTP(t, tc.method, ts.URL+tc.path, tc.body)
		if code != http.StatusServiceUnavailable {
			t.Errorf("%s status = %d, want 503", tc.method, code)
		}
		if c := tenantErrCode(t, body); c != "registry.read_only" {
			t.Errorf("%s code = %q, want registry.read_only", tc.method, c)
		}
	}
	if code, _ := tenantHTTP(t, http.MethodGet, ts.URL+"/v1/admin/tenants", ""); code != http.StatusOK {
		t.Errorf("GET in read-only = %d, want 200", code)
	}
}

// Spec: §6.10 — PATCH/DELETE on an unknown tenant ID returns
// registry.tenant_not_found.
func TestTenants_UnknownIDNotFound(t *testing.T) {
	t.Parallel()
	ts, st := bootTenantServer(t, operatorCaller, true)
	_ = st.GrantOperator(context.Background(), operatorCaller.Sub)
	for _, tc := range []struct{ method, body string }{
		{http.MethodPatch, `{"active":false}`},
		{http.MethodDelete, ""},
	} {
		code, body := tenantHTTP(t, tc.method, ts.URL+"/v1/admin/tenants/does-not-exist", tc.body)
		if code != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", tc.method, code)
		}
		if c := tenantErrCode(t, body); c != "registry.tenant_not_found" {
			t.Errorf("%s code = %q, want registry.tenant_not_found", tc.method, c)
		}
	}
}

// Spec: §7.3.3 — create accepts every quota sub-field and the scope-preview
// gate; PATCH sets the gate; a malformed body is rejected with 400.
func TestTenants_FieldsAndMalformedBody(t *testing.T) {
	t.Parallel()
	ts, st := bootTenantServer(t, operatorCaller, true)
	_ = st.GrantOperator(context.Background(), operatorCaller.Sub)
	tenantsURL := ts.URL + "/v1/admin/tenants"

	code, body := tenantHTTP(t, http.MethodPost, tenantsURL,
		`{"name":"acme.com","quota":{"storage_bytes":1,"search_qps":2,"materialize_rate":3,"audit_volume_per_day":4,"max_user_layers":5},"expose_scope_preview":false}`)
	if code != http.StatusCreated {
		t.Fatalf("create status = %d (%s)", code, body)
	}
	var created tenantWire
	_ = json.Unmarshal(body, &created)
	if created.Quota.SearchQPS != 2 || created.Quota.MaterializeRate != 3 || created.Quota.AuditVolumePerDay != 4 {
		t.Errorf("create dropped quota sub-fields: %+v", created.Quota)
	}
	if created.ExposeScopePreview == nil || *created.ExposeScopePreview {
		t.Errorf("create did not set expose_scope_preview false: %v", created.ExposeScopePreview)
	}

	code, body = tenantHTTP(t, http.MethodPatch, tenantsURL+"/"+created.ID, `{"expose_scope_preview":true}`)
	if code != http.StatusOK {
		t.Fatalf("patch expose status = %d (%s)", code, body)
	}
	var patched tenantWire
	_ = json.Unmarshal(body, &patched)
	if patched.ExposeScopePreview == nil || !*patched.ExposeScopePreview {
		t.Errorf("patch did not set expose_scope_preview true: %v", patched.ExposeScopePreview)
	}

	if code, _ := tenantHTTP(t, http.MethodPost, tenantsURL, `{not json`); code != http.StatusBadRequest {
		t.Errorf("malformed create status = %d, want 400", code)
	}
	if code, _ := tenantHTTP(t, http.MethodPatch, tenantsURL+"/"+created.ID, `{not json`); code != http.StatusBadRequest {
		t.Errorf("malformed patch status = %d, want 400", code)
	}
}

// Spec: §4.7.1 / §6.10 — an unauthenticated (public) caller is rejected with
// auth.forbidden, covering the public-caller branch of operator authorization.
func TestTenants_UnauthenticatedForbidden(t *testing.T) {
	t.Parallel()
	ts, _ := bootTenantServer(t, layer.Identity{IsPublic: true}, true)
	code, body := tenantHTTP(t, http.MethodGet, ts.URL+"/v1/admin/tenants", "")
	if code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", code)
	}
	if c := tenantErrCode(t, body); c != "auth.forbidden" {
		t.Errorf("code = %q, want auth.forbidden", c)
	}
}

// Spec: §7.3.3 — a PATCH whose field carries the wrong JSON type is rejected
// with 400, covering the per-field unmarshal-error branches.
func TestTenants_InvalidPatchFields(t *testing.T) {
	t.Parallel()
	ts, st := bootTenantServer(t, operatorCaller, true)
	_ = st.GrantOperator(context.Background(), operatorCaller.Sub)
	code, body := tenantHTTP(t, http.MethodPost, ts.URL+"/v1/admin/tenants", `{"name":"acme.com"}`)
	if code != http.StatusCreated {
		t.Fatalf("create status = %d (%s)", code, body)
	}
	var created tenantWire
	_ = json.Unmarshal(body, &created)
	target := ts.URL + "/v1/admin/tenants/" + created.ID
	for _, bad := range []string{`{"quota":123}`, `{"active":"x"}`, `{"expose_scope_preview":"x"}`} {
		if code, _ := tenantHTTP(t, http.MethodPatch, target, bad); code != http.StatusBadRequest {
			t.Errorf("PATCH %s status = %d, want 400", bad, code)
		}
	}
}
