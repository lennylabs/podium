package server_test

// In-process two-tenant HTTP integration coverage for §7.3.2 receiver
// tenancy. A registry in §6.3.1 multi-tenant mode routes each caller to the
// tenant its organization names, and the suite asserts that receiver CRUD
// reads and writes only the routed tenant's receivers, that an id naming
// another tenant's receiver answers as an unknown id, that delivery reaches
// only the receivers of the event's tenant, and that an unrouted request is
// refused before any store write. A single-tenant fixture pins the same
// keying when no tenant router is installed.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/webhook"
)

const (
	tenantA        = "A"
	tenantB        = "B"
	tenantUnrouted = "podium:unrouted"
	aliceAdmin     = "alice@acme.com"
	bobAdmin       = "bob@acme.com"
)

// tenantHookSink is an https receiver that records each delivered body by
// request path, so receivers registered at /a and /b on one listener are
// told apart. The SSRF policy requires https, and every httptest TLS server
// presents the same test certificate, so the sink's client trusts it.
type tenantHookSink struct {
	srv    *httptest.Server
	mu     sync.Mutex
	bodies map[string][]map[string]any
}

func newTenantHookSink(t *testing.T) *tenantHookSink {
	t.Helper()
	s := &tenantHookSink{bodies: map[string][]map[string]any{}}
	s.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.mu.Lock()
		s.bodies[r.URL.Path] = append(s.bodies[r.URL.Path], body)
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

// markers returns the data.marker value of every body delivered to path.
func (s *tenantHookSink) markers(path string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.bodies[path]))
	for _, b := range s.bodies[path] {
		data, _ := b["data"].(map[string]any)
		marker, _ := data["marker"].(string)
		out = append(out, marker)
	}
	return out
}

// tenantWebhookFixture is a registry with a receiver store, an https sink,
// and the server under test.
type tenantWebhookFixture struct {
	srv    *server.Server
	ts     *httptest.Server
	wstore *webhook.MemoryStore
	sink   *tenantHookSink
}

// newTenantWebhookFixture provisions tenants with one admin each and binds
// core to bound. A non-nil routes installs a §6.3.1 tenant router that maps
// an organization value to a tenant; a nil routes leaves the registry
// single-tenant. The identity resolver reads Sub from X-Test-User and OrgID
// from X-Test-Org. No WithTenant is passed, so receiver tenancy comes only
// from the request's routed tenant and the event's scope.
func newTenantWebhookFixture(t *testing.T, bound string, admins, routes map[string]string, rejectUnknown bool) *tenantWebhookFixture {
	t.Helper()
	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: bound}); err != nil {
		t.Fatalf("CreateTenant(%s): %v", bound, err)
	}
	for tenant, admin := range admins {
		if tenant != bound {
			if err := st.CreateTenant(ctx, store.Tenant{ID: tenant}); err != nil {
				t.Fatalf("CreateTenant(%s): %v", tenant, err)
			}
		}
		if err := st.GrantAdmin(ctx, store.AdminGrant{UserID: admin, OrgID: tenant}); err != nil {
			t.Fatalf("GrantAdmin(%s, %s): %v", admin, tenant, err)
		}
	}
	sink := newTenantHookSink(t)
	policy, err := webhook.NewURLPolicy([]string{"127.0.0.1"})
	if err != nil {
		t.Fatalf("NewURLPolicy: %v", err)
	}
	wstore := webhook.NewMemoryStore()
	worker := &webhook.Worker{
		Store: wstore, HTTPClient: sink.srv.Client(), URLPolicy: policy, Backoff: []time.Duration{},
	}
	opts := []server.Option{
		server.WithWebhooks(worker),
		server.WithIdentityResolver(func(r *http.Request) layer.Identity {
			sub := r.Header.Get("X-Test-User")
			return layer.Identity{Sub: sub, OrgID: r.Header.Get("X-Test-Org"), IsAuthenticated: sub != ""}
		}),
	}
	if routes != nil {
		opts = append(opts, server.WithTenantRouter(func(_ context.Context, org string) (store.Tenant, bool) {
			tenant, ok := routes[org]
			if !ok {
				return store.Tenant{}, false
			}
			return store.Tenant{ID: tenant}, true
		}, rejectUnknown))
	}
	srv := server.New(core.New(st, bound, nil), opts...)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &tenantWebhookFixture{srv: srv, ts: ts, wstore: wstore, sink: sink}
}

// newTwoTenantWebhookFixture binds core to a third, unrouted tenant and routes
// organization "A" to tenant A, where alice is admin, and "B" to tenant B,
// where bob is admin.
func newTwoTenantWebhookFixture(t *testing.T, rejectUnknown bool) *tenantWebhookFixture {
	t.Helper()
	return newTenantWebhookFixture(t, tenantUnrouted,
		map[string]string{tenantA: aliceAdmin, tenantB: bobAdmin},
		map[string]string{tenantA: tenantA, tenantB: tenantB}, rejectUnknown)
}

// do sends one request as user in org and returns the status and body.
func (f *tenantWebhookFixture) do(t *testing.T, method, path, user, org string, body any) (int, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rd = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, f.ts.URL+path, rd)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-User", user)
	req.Header.Set("X-Test-Org", org)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, out
}

// create registers a receiver at the sink path as user in org and returns
// its id.
func (f *tenantWebhookFixture) create(t *testing.T, user, org, sinkPath string) string {
	t.Helper()
	status, body := f.do(t, http.MethodPost, "/v1/webhooks", user, org, map[string]any{
		"url": f.sink.srv.URL + sinkPath, "secret": "shh",
	})
	if status != http.StatusCreated {
		t.Fatalf("POST as %s = %d, want 201: %s", user, status, body)
	}
	var rec struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &rec); err != nil || rec.ID == "" {
		t.Fatalf("decode created receiver %q: %v", body, err)
	}
	return rec.ID
}

// listIDs returns the receiver ids GET /v1/webhooks answers for user in org.
func (f *tenantWebhookFixture) listIDs(t *testing.T, user, org string) []string {
	t.Helper()
	status, body := f.do(t, http.MethodGet, "/v1/webhooks", user, org, nil)
	if status != http.StatusOK {
		t.Fatalf("GET /v1/webhooks as %s = %d: %s", user, status, body)
	}
	var out struct {
		Receivers []struct {
			ID string `json:"id"`
		} `json:"receivers"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode list %q: %v", body, err)
	}
	ids := make([]string, len(out.Receivers))
	for i, r := range out.Receivers {
		ids[i] = r.ID
	}
	return ids
}

// getReceiver sends GET /v1/webhooks/{id} as user in org, requires 200, and
// returns the decoded id and disabled flag.
func (f *tenantWebhookFixture) getReceiver(t *testing.T, user, org, id string) (string, bool) {
	t.Helper()
	status, body := f.do(t, http.MethodGet, "/v1/webhooks/"+id, user, org, nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s as %s = %d, want 200: %s", id, user, status, body)
	}
	var rec struct {
		ID       string `json:"id"`
		Disabled bool   `json:"disabled"`
	}
	if err := json.Unmarshal(body, &rec); err != nil {
		t.Fatalf("decode receiver %q: %v", body, err)
	}
	return rec.ID, rec.Disabled
}

// storedCount returns the number of receivers stored across tenants.
func (f *tenantWebhookFixture) storedCount(t *testing.T, tenants ...string) int {
	t.Helper()
	n := 0
	for _, tenant := range tenants {
		rs, err := f.wstore.List(context.Background(), tenant)
		if err != nil {
			t.Fatalf("List(%s): %v", tenant, err)
		}
		n += len(rs)
	}
	return n
}

// bodyErrorCode decodes the §6.10 error envelope in a read response body
// and returns its code.
func bodyErrorCode(t *testing.T, body []byte) string {
	t.Helper()
	var env server.ErrorResponse
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode envelope %q: %v", body, err)
	}
	return env.Code
}

// pollUntil polls cond until it holds or the deadline passes. PublishEvent
// fans out to receivers on a goroutine, so delivery assertions wait for it.
func pollUntil(t *testing.T, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return cond()
}

// Spec: §7.3.2 — each receiver belongs to the tenant whose admin registered
// it. POST stores the receiver under the routed tenant, and GET /v1/webhooks
// lists only the routed tenant's receivers.
// Spec: §6.3.1 — the caller's organization selects the tenant.
func TestWebhookTenant_CRUDIsolation(t *testing.T) {
	t.Parallel()
	f := newTwoTenantWebhookFixture(t, true)
	ra := f.create(t, aliceAdmin, tenantA, "/a")
	rb := f.create(t, bobAdmin, tenantB, "/b")

	if ids := f.listIDs(t, aliceAdmin, tenantA); len(ids) != 1 || ids[0] != ra {
		t.Errorf("alice list = %v, want [%s]", ids, ra)
	}
	if ids := f.listIDs(t, bobAdmin, tenantB); len(ids) != 1 || ids[0] != rb {
		t.Errorf("bob list = %v, want [%s]", ids, rb)
	}
	ctx := context.Background()
	for _, c := range []struct{ tenant, id, other string }{{tenantA, ra, tenantB}, {tenantB, rb, tenantA}} {
		rec, err := f.wstore.Get(ctx, c.tenant, c.id)
		if err != nil {
			t.Fatalf("Get(%s, %s): %v", c.tenant, c.id, err)
		}
		if rec.TenantID != c.tenant {
			t.Errorf("receiver %s TenantID = %q, want %q", c.id, rec.TenantID, c.tenant)
		}
		if _, err := f.wstore.Get(ctx, c.other, c.id); err == nil {
			t.Errorf("receiver %s also stored under %s", c.id, c.other)
		}
	}
	if n := f.storedCount(t, tenantUnrouted); n != 0 {
		t.Errorf("receivers under the bound tenant = %d, want 0", n)
	}
}

// Spec: §7.3.2 — an id that names another tenant's receiver answers as an
// unknown id: GET and PUT return 404 registry.not_found, and DELETE returns
// 204 and leaves the other tenant's receiver in place. The owning tenant's
// admin still updates and deletes it.
// Spec: §6.3.1 — the caller's organization selects the tenant.
func TestWebhookTenant_CrossTenantIDIsUnknown(t *testing.T) {
	t.Parallel()
	f := newTwoTenantWebhookFixture(t, true)
	rb := f.create(t, bobAdmin, tenantB, "/b")
	path := "/v1/webhooks/" + rb

	for _, method := range []string{http.MethodGet, http.MethodPut} {
		status, body := f.do(t, method, path, aliceAdmin, tenantA, map[string]any{"disabled": true})
		if status != http.StatusNotFound {
			t.Errorf("alice %s %s = %d, want 404: %s", method, rb, status, body)
			continue
		}
		if code := bodyErrorCode(t, body); code != "registry.not_found" {
			t.Errorf("alice %s code = %q, want registry.not_found", method, code)
		}
	}
	if status, body := f.do(t, http.MethodDelete, path, aliceAdmin, tenantA, nil); status != http.StatusNoContent {
		t.Errorf("alice DELETE %s = %d, want 204: %s", rb, status, body)
	}
	ctx := context.Background()
	rec, err := f.wstore.Get(ctx, tenantB, rb)
	if err != nil {
		t.Fatalf("receiver %s removed by another tenant's DELETE: %v", rb, err)
	}
	if rec.Disabled {
		t.Errorf("receiver %s disabled by another tenant's PUT", rb)
	}
	// The owning tenant's GET resolves through the routed tenant, so a GET
	// handler keyed on the bound tenant fails here rather than passing.
	if id, disabled := f.getReceiver(t, bobAdmin, tenantB, rb); id != rb || disabled {
		t.Errorf("bob GET after alice's DELETE = (id %q, disabled %v), want (%s, false)", id, disabled, rb)
	}

	status, body := f.do(t, http.MethodPut, path, bobAdmin, tenantB, map[string]any{"disabled": true})
	if status != http.StatusOK {
		t.Fatalf("bob PUT %s = %d, want 200: %s", rb, status, body)
	}
	if id, disabled := f.getReceiver(t, bobAdmin, tenantB, rb); id != rb || !disabled {
		t.Errorf("bob GET after PUT = (id %q, disabled %v), want (%s, true)", id, disabled, rb)
	}
	if rec, err := f.wstore.Get(ctx, tenantB, rb); err != nil || !rec.Disabled {
		t.Errorf("bob PUT disabled:true not persisted: rec=%+v err=%v", rec, err)
	}
	if status, body := f.do(t, http.MethodDelete, path, bobAdmin, tenantB, nil); status != http.StatusNoContent {
		t.Fatalf("bob DELETE %s = %d, want 204: %s", rb, status, body)
	}
	status, body = f.do(t, http.MethodGet, path, bobAdmin, tenantB, nil)
	if status != http.StatusNotFound {
		t.Errorf("bob GET after DELETE = %d, want 404: %s", status, body)
	} else if code := bodyErrorCode(t, body); code != "registry.not_found" {
		t.Errorf("bob GET after DELETE code = %q, want registry.not_found", code)
	}
	if _, err := f.wstore.Get(ctx, tenantB, rb); err == nil {
		t.Errorf("(B, %s) row survives bob's DELETE", rb)
	}
}

// Spec: §7.3.2 — delivery reaches only the receivers of the event's tenant.
// An event scoped to A reaches A's receiver and not B's, and an event scoped
// to B reaches B's receiver and not A's.
// Spec: §6.3.1 — each receiver is registered through its admin's routed tenant.
func TestWebhookTenant_DeliveryIsolation(t *testing.T) {
	t.Parallel()
	f := newTwoTenantWebhookFixture(t, true)
	f.create(t, aliceAdmin, tenantA, "/a")
	f.create(t, bobAdmin, tenantB, "/b")

	ctx := context.Background()
	f.srv.PublishEvent(ctx, core.EventScope{TenantID: tenantA, Layers: []string{"L"}},
		"layer.config_changed", map[string]any{"layer": "L", "marker": "event-A"})
	if !pollUntil(t, func() bool { return len(f.sink.markers("/a")) == 1 }) {
		t.Fatalf("A receiver deliveries = %v, want [event-A]", f.sink.markers("/a"))
	}
	f.srv.PublishEvent(ctx, core.EventScope{TenantID: tenantB, Layers: []string{"L"}},
		"layer.config_changed", map[string]any{"layer": "L", "marker": "event-B"})
	if !pollUntil(t, func() bool { return len(f.sink.markers("/b")) == 1 }) {
		t.Fatalf("B receiver deliveries = %v, want [event-B]", f.sink.markers("/b"))
	}

	// A misrouted delivery would arrive from the same fan-out goroutine as the
	// awaited one; the grace period lets a late one land before the counts
	// are read.
	time.Sleep(150 * time.Millisecond)
	if got := f.sink.markers("/a"); len(got) != 1 || got[0] != "event-A" {
		t.Errorf("A receiver markers = %v, want [event-A]", got)
	}
	if got := f.sink.markers("/b"); len(got) != 1 || got[0] != "event-B" {
		t.Errorf("B receiver markers = %v, want [event-B]", got)
	}
}

// Spec: §7.3.2 — receiver CRUD is admin-gated in the routed tenant, so a
// request whose organization resolves to no tenant is refused before any
// store access: 403 auth.forbidden under trusted-headers, where the caller
// stays in the bound no-data tenant, and 401 auth.tenant_unknown under a
// verified provider.
// Spec: §6.3.1 — an unresolved organization is left unrouted or rejected.
func TestWebhookTenant_UnroutedRefused(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		rejectUnknown bool
		status        int
		code          string
	}{
		{"trusted-headers", false, http.StatusForbidden, "auth.forbidden"},
		{"verified", true, http.StatusUnauthorized, "auth.tenant_unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := newTwoTenantWebhookFixture(t, c.rejectUnknown)
			ra := f.create(t, aliceAdmin, tenantA, "/a")
			before := f.storedCount(t, tenantA, tenantB, tenantUnrouted)

			requests := []struct {
				method, path string
				body         any
			}{
				{http.MethodGet, "/v1/webhooks", nil},
				{http.MethodPost, "/v1/webhooks", map[string]any{"url": f.sink.srv.URL + "/g"}},
				{http.MethodGet, "/v1/webhooks/" + ra, nil},
				{http.MethodPut, "/v1/webhooks/" + ra, map[string]any{"disabled": true}},
				{http.MethodDelete, "/v1/webhooks/" + ra, nil},
			}
			for _, r := range requests {
				status, body := f.do(t, r.method, r.path, "carol@globex.com", "globex", r.body)
				if status != c.status {
					t.Errorf("%s %s = %d, want %d: %s", r.method, r.path, status, c.status, body)
					continue
				}
				if code := bodyErrorCode(t, body); code != c.code {
					t.Errorf("%s %s code = %q, want %q", r.method, r.path, code, c.code)
				}
			}
			if after := f.storedCount(t, tenantA, tenantB, tenantUnrouted); after != before {
				t.Errorf("stored receivers = %d after unrouted requests, want %d", after, before)
			}
			if rec, err := f.wstore.Get(context.Background(), tenantA, ra); err != nil || rec.Disabled {
				t.Errorf("receiver %s changed by an unrouted request: rec=%+v err=%v", ra, rec, err)
			}
		})
	}
}

// Spec: §7.3.2 — with no tenant router the request resolves to the tenant
// core is bound to, so a receiver is stored under that tenant and receives
// an event scoped to it.
// Spec: §6.3.1 — a single-tenant registry serves its bound tenant.
func TestWebhookTenant_SingleTenantKeysOnBoundTenant(t *testing.T) {
	t.Parallel()
	const bound = "T"
	f := newTenantWebhookFixture(t, bound, map[string]string{bound: aliceAdmin}, nil, false)
	id := f.create(t, aliceAdmin, "", "/t")

	rec, err := f.wstore.Get(context.Background(), bound, id)
	if err != nil {
		t.Fatalf("Get(%s, %s): %v", bound, id, err)
	}
	if rec.TenantID != bound {
		t.Errorf("TenantID = %q, want %q", rec.TenantID, bound)
	}
	f.srv.PublishEvent(context.Background(), core.EventScope{TenantID: bound, Layers: []string{"L"}},
		"layer.config_changed", map[string]any{"layer": "L", "marker": "event-T"})
	if !pollUntil(t, func() bool { return len(f.sink.markers("/t")) == 1 }) {
		t.Fatalf("T receiver deliveries = %v, want [event-T]", f.sink.markers("/t"))
	}
	if got := f.sink.markers("/t")[0]; got != "event-T" {
		t.Errorf("marker = %q, want event-T", got)
	}
}
