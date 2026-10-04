package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// spec §8.1: the trace id is taken from a well-formed W3C traceparent and
// rejected when malformed or all-zero.
func TestParseTraceparent(t *testing.T) {
	valid := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	if got := parseTraceparent(valid); got != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("valid traceparent: got %q", got)
	}
	for _, bad := range []string{
		"",
		"garbage",
		"00-tooshort-00f067aa0ba902b7-01",
		"00-00000000000000000000000000000000-00f067aa0ba902b7-01", // invalid all-zero trace id
		"00-4bf92f3577b34da6a3ce929d0e0e4736",                     // missing fields
	} {
		if got := parseTraceparent(bad); got != "" {
			t.Errorf("malformed traceparent %q: expected empty, got %q", bad, got)
		}
	}
}

// spec §8.1: authenticated callers carry email and groups (no network);
// public-mode callers carry source IP, forwarded user, and the public flag
// (no email or groups). The trace id is generated when no header is present.
func TestAuditMetaFrom_AuthenticatedVsPublic(t *testing.T) {
	authedReq := httptest.NewRequest("GET", "/v1/load_domain", nil)
	authedReq.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	authed := auditMetaFrom(authedReq, layer.Identity{
		Sub: "alice", Email: "alice@acme.com", Groups: []string{"eng"}, IsAuthenticated: true,
	})
	if authed.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("trace id from header: got %q", authed.TraceID)
	}
	if authed.PublicMode {
		t.Error("authenticated caller marked public")
	}
	if authed.Email != "alice@acme.com" || len(authed.Groups) != 1 {
		t.Errorf("authenticated caller missing email/groups: %+v", authed)
	}
	if authed.SourceIP != "" || authed.ForwardedUser != "" {
		t.Errorf("authenticated caller should not capture network: %+v", authed)
	}

	pubReq := httptest.NewRequest("GET", "/v1/load_domain", nil)
	pubReq.RemoteAddr = "203.0.113.7:54321"
	pubReq.Header.Set("X-Forwarded-User", "bob")
	pub := auditMetaFrom(pubReq, layer.Identity{IsPublic: true})
	if !pub.PublicMode {
		t.Error("public caller not marked public")
	}
	if pub.SourceIP != "203.0.113.7" || pub.ForwardedUser != "bob" {
		t.Errorf("public caller missing network: %+v", pub)
	}
	if pub.Email != "" || len(pub.Groups) != 0 {
		t.Errorf("public caller should not carry email/groups: %+v", pub)
	}
	if pub.TraceID == "" {
		t.Error("trace id should be generated when no traceparent header is present")
	}
}

// spec §8.1: emitAuditEvent records the structured caller identity, and a nil
// sink is a no-op.
func TestEmitAuditEvent_PublicCallerNetwork(t *testing.T) {
	sink, err := audit.NewFileSink(filepath.Join(t.TempDir(), "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/v1/admin/grants", nil)
	req.RemoteAddr = "203.0.113.7:9999"
	// nil sink must not panic.
	emitAuditEvent(nil, req, layer.Identity{IsPublic: true}, audit.EventAdminGranted, "carol", nil)

	emitAuditEvent(sink, req, layer.Identity{IsPublic: true}, audit.EventAdminGranted, "carol",
		map[string]string{"action": "grant"})

	data, err := os.ReadFile(sink.Path())
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		`"type":"admin.granted"`,
		`"caller":{"identity":"system:public"`,
		`"target":"carol"`,
		`"public_mode":true`,
		`"source_ip":"203.0.113.7"`,
		`"action":"grant"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("emitted event missing %s\nlog:\n%s", want, got)
		}
	}
	if !strings.Contains(got, `"trace_id"`) {
		t.Errorf("emitted event missing generated trace_id\nlog:\n%s", got)
	}
}

// spec §8.1: an authenticated caller's event records email and groups and no
// public-mode network block.
func TestEmitAuditEvent_AuthenticatedCaller(t *testing.T) {
	sink, err := audit.NewFileSink(filepath.Join(t.TempDir(), "audit.log"))
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("DELETE", "/v1/admin/grants?user_id=carol", nil)
	id := layer.Identity{Sub: "alice", Email: "alice@acme.com", Groups: []string{"admins"}, IsAuthenticated: true}
	emitAuditEvent(sink, req, id, audit.EventAdminGranted, "carol", map[string]string{"action": "revoke"})

	got, err := os.ReadFile(sink.Path())
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	for _, want := range []string{`"caller":{"identity":"alice"`, `"email":"alice@acme.com"`, `"groups":["admins"]`, `"action":"revoke"`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s\nlog:\n%s", want, s)
		}
	}
	if strings.Contains(s, `"network"`) || strings.Contains(s, "public_mode") {
		t.Errorf("authenticated caller leaked public-mode fields:\n%s", s)
	}
}

// auditTenant keys the §8.1 label on multi-tenant mode: the routed tenant or
// nothing on a multi-tenant registry, whatever bound holds, and the bound
// tenant on a single-tenant registry.
//
// Spec: §8.1, §6.3.1
func TestAuditTenant_KeysOnMultiTenantMode(t *testing.T) {
	t.Parallel()
	routed := core.ContextWithTenant(context.Background(), "acme")
	for _, tc := range []struct {
		name        string
		ctx         context.Context
		multiTenant bool
		bound       string
		want        string
	}{
		{"single-tenant uses the bound tenant", context.Background(), false, "t", "t"},
		{"multi-tenant routed uses the routed tenant", routed, true, "acme", "acme"},
		{"multi-tenant unrouted records no tenant", context.Background(), true, unroutedTenant, ""},
	} {
		if got := auditTenant(tc.ctx, tc.multiTenant, tc.bound); got != tc.want {
			t.Errorf("%s: auditTenant = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// unroutedTenant mirrors the no-data binding serverboot gives a multi-tenant
// registry's core, which a request with no routed tenant falls back to.
const unroutedTenant = "podium:unrouted"

// grantTenantLabel issues one POST /v1/admin/grants as alice, who holds admin
// in adminIn, against a server over a core bound to bound, and returns the
// tenant label on the recorded admin.granted event.
func grantTenantLabel(t *testing.T, org, bound, adminIn string, opts ...Option) string {
	t.Helper()
	st := store.NewMemory()
	if err := st.GrantAdmin(context.Background(), store.AdminGrant{UserID: "alice", OrgID: adminIn}); err != nil {
		t.Fatal(err)
	}
	sink := audit.NewMemory()
	alice := layer.Identity{Sub: "alice", OrgID: org, IsAuthenticated: true}
	opts = append([]Option{
		WithAudit(sink),
		WithIdentityResolver(func(*http.Request) layer.Identity { return alice }),
	}, opts...)
	ts := httptest.NewServer(New(core.New(st, bound, nil), opts...).Handler())
	defer ts.Close()
	resp, err := http.Post(ts.URL+"/v1/admin/grants", "application/json", strings.NewReader(`{"user_id":"bob"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /v1/admin/grants status = %d, want 201", resp.StatusCode)
	}
	for _, ev := range sink.Events() {
		if ev.Type == audit.EventAdminGranted {
			return ev.Tenant
		}
	}
	t.Fatal("no admin.granted event recorded")
	return ""
}

// admin.granted records the routed tenant on a routed request, no tenant on
// an unrouted multi-tenant request with or without a tenant router, and the
// bound tenant on a single-tenant registry. The podium:unrouted binding never
// reaches the record.
//
// Spec: §8.1, §6.3.1
func TestAdminGranted_TenantLabel(t *testing.T) {
	t.Parallel()
	router := func(_ context.Context, org string) (store.Tenant, bool) {
		if org == "acme.com" {
			return store.Tenant{ID: "acme"}, true
		}
		return store.Tenant{}, false
	}
	t.Run("routed request records the routed tenant", func(t *testing.T) {
		t.Parallel()
		got := grantTenantLabel(t, "acme.com", unroutedTenant, "acme", WithTenantRouter(router, false))
		if got != "acme" {
			t.Errorf("tenant = %q, want acme", got)
		}
	})
	t.Run("unrouted request under a router records no tenant", func(t *testing.T) {
		t.Parallel()
		got := grantTenantLabel(t, "globex.com", unroutedTenant, unroutedTenant, WithTenantRouter(router, false))
		if got != "" {
			t.Errorf("tenant = %q, want none", got)
		}
	})
	t.Run("multi-tenant with no router records no tenant", func(t *testing.T) {
		t.Parallel()
		got := grantTenantLabel(t, "acme.com", unroutedTenant, unroutedTenant, WithMultiTenant())
		if got != "" {
			t.Errorf("tenant = %q, want none", got)
		}
	})
	t.Run("single-tenant request records the bound tenant", func(t *testing.T) {
		t.Parallel()
		if got := grantTenantLabel(t, "acme.com", "t", "t"); got != "t" {
			t.Errorf("tenant = %q, want t", got)
		}
	})
}

// A public-mode meta-tool read on a multi-tenant registry with no router
// carries no tenant in its audit metadata, although the core resolves the
// request against the podium:unrouted binding.
//
// Spec: §8.1, §6.3.1
func TestAuditMeta_PublicModeMultiTenantNoRouter(t *testing.T) {
	t.Parallel()
	var (
		got    AuditMeta
		gotOK  bool
		bound  string
		called bool
	)
	reg := core.New(store.NewMemory(), unroutedTenant, nil)
	reg.WithAudit(func(ctx context.Context, _ core.AuditEvent) {
		got, gotOK = AuditMetaFromContext(ctx)
		bound = reg.TenantFor(ctx)
		called = true
	})
	ts := httptest.NewServer(New(reg, WithMultiTenant(), WithPublicMode()).Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/v1/search_artifacts?query=x")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if !called {
		t.Fatalf("search_artifacts (status %d) emitted no audit event", resp.StatusCode)
	}
	if !gotOK || got.Tenant != "" {
		t.Errorf("audit meta = %+v (attached %v), want attached with no tenant", got, gotOK)
	}
	if bound != unroutedTenant {
		t.Errorf("core tenant = %q, want %q", bound, unroutedTenant)
	}
}
