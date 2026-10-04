package serverboot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §8.1 / §4.7.5 — auditEmitterFor adapts the file-backed
// sink to the core.AuditEmitter shape; the resulting emitter
// writes the event type, caller, target, and context to the
// sink, completing the §4.7.5 promise that the registry as a
// service emits audit events for read calls.
func TestAuditEmitterFor_AppendsToSink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	emit := auditEmitterFor(sink, audit.NewPIIScrubber(), nil)
	emit(context.Background(), core.AuditEvent{
		Type:    "domain.loaded",
		Caller:  "alice",
		Target:  "team/finance",
		Context: map[string]string{"depth": "1"},
	})

	body, err := readBytes(path)
	if err != nil {
		t.Fatalf("readBytes: %v", err)
	}
	got := string(body)
	for _, want := range []string{"domain.loaded", "alice", "team/finance", "\"depth\":\"1\""} {
		if !strings.Contains(got, want) {
			t.Errorf("audit log missing %q in:\n%s", want, got)
		}
	}
}

// wrapAuditVolume records each event under the tenant tenantOf resolves from
// the emitting call's context, so one tenant's events never spend another
// tenant's budget.
//
// Spec: §4.7.8
func TestWrapAuditVolume_RecordsPerTenant(t *testing.T) {
	t.Parallel()
	meter := server.NewAuditVolumeMeter()
	reg := core.New(store.NewMemory(), "bound", nil)
	emit := wrapAuditVolume(meter, reg.TenantFor, func(context.Context, core.AuditEvent) {})

	emit(core.ContextWithTenant(t.Context(), "tenant-a"), core.AuditEvent{Type: "artifacts.searched"})
	if meter.Allow("tenant-a", 1) {
		t.Error("Allow(tenant-a, 1) = true after one event under tenant-a, want false")
	}
	if !meter.Allow("tenant-b", 1) {
		t.Error("Allow(tenant-b, 1) = false, want true: tenant-a's event must not count against tenant-b")
	}
}

// With the production resolver, core.Registry.TenantFor, an event under a
// routed context counts against the routed tenant and an event under a bare
// context counts against the registry's bound tenant.
//
// Spec: §4.7.8
func TestWrapAuditVolume_ProductionResolver(t *testing.T) {
	t.Parallel()
	const bound = "bound-tenant"
	meter := server.NewAuditVolumeMeter()
	reg := core.New(store.NewMemory(), bound, nil)
	emit := wrapAuditVolume(meter, reg.TenantFor, nil)

	emit(core.ContextWithTenant(t.Context(), "tenant-a"), core.AuditEvent{Type: "artifacts.searched"})
	emit(t.Context(), core.AuditEvent{Type: "artifacts.searched"})
	for _, tenant := range []string{"tenant-a", bound} {
		if !meter.Allow(tenant, 2) || meter.Allow(tenant, 1) {
			t.Errorf("%s: want a count of exactly 1", tenant)
		}
	}
}

// The wrapper records every event with no limit configured and no base
// emitter, which is how Run() installs it unconditionally (D9).
//
// Spec: §4.7.8
func TestWrapAuditVolume_NilBaseRecordsWithoutLimit(t *testing.T) {
	t.Parallel()
	meter := server.NewAuditVolumeMeter()
	reg := core.New(store.NewMemory(), "bound", nil)
	emit := wrapAuditVolume(meter, reg.TenantFor, nil)
	ctx := core.ContextWithTenant(t.Context(), "tenant-a")
	emit(ctx, core.AuditEvent{Type: "artifacts.searched"})
	emit(ctx, core.AuditEvent{Type: "artifacts.searched"})
	if meter.Allow("tenant-a", 2) {
		t.Error("Allow(tenant-a, 2) = true after two events, want false")
	}
}

// searchLabelAndMeter boots a server over a core bound to podium:unrouted,
// wires the production audit chain (wrapAuditVolume over auditEmitterFor),
// issues one meta-tool read as id, and returns the recorded event's tenant
// label together with the meter.
func searchLabelAndMeter(t *testing.T, id layer.Identity, opts ...server.Option) (string, *server.AuditVolumeMeter) {
	t.Helper()
	const unrouted = "podium:unrouted"
	sink := audit.NewMemory()
	meter := server.NewAuditVolumeMeter()
	reg := core.New(store.NewMemory(), unrouted, nil)
	reg.WithAudit(wrapAuditVolume(meter, reg.TenantFor, auditEmitterFor(sink, nil, nil)))
	opts = append([]server.Option{
		server.WithIdentityResolver(func(*http.Request) layer.Identity { return id }),
	}, opts...)
	ts := httptest.NewServer(server.New(reg, opts...).Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/v1/search_artifacts?query=x")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	var label string
	found := false
	for _, ev := range sink.Events() {
		if ev.Type == audit.EventType("artifacts.searched") {
			label, found = ev.Tenant, true
		}
	}
	if !found {
		t.Fatalf("search (status %d) recorded no artifacts.searched event", resp.StatusCode)
	}
	return label, meter
}

// countedOnce reports whether meter holds exactly one event under tenant.
func countedOnce(meter *server.AuditVolumeMeter, tenant string) bool {
	return meter.Allow(tenant, 2) && !meter.Allow(tenant, 1)
}

// auditEmitterFor labels a routed request's event with the routed tenant,
// which is also the audit-volume meter key. An unrouted multi-tenant request,
// with or without a tenant router, records no tenant while the meter keys it
// under podium:unrouted (Decision 10 of proposal 0051).
//
// Spec: §8.1, §6.3.1, §4.7.8
func TestAuditEmitterFor_TenantLabelAndMeterKey(t *testing.T) {
	t.Parallel()
	router := func(_ context.Context, org string) (store.Tenant, bool) {
		if org == "acme.com" {
			return store.Tenant{ID: "acme"}, true
		}
		return store.Tenant{}, false
	}
	alice := func(org string) layer.Identity {
		return layer.Identity{Sub: "alice", OrgID: org, IsAuthenticated: true}
	}
	for _, tc := range []struct {
		name      string
		id        layer.Identity
		opts      []server.Option
		wantLabel string
		wantMeter string
	}{
		{"routed", alice("acme.com"), []server.Option{server.WithTenantRouter(router, false)}, "acme", "acme"},
		{"unrouted under a router", alice("globex.com"), []server.Option{server.WithTenantRouter(router, false)}, "", "podium:unrouted"},
		{"no router, public mode", layer.Identity{}, []server.Option{server.WithMultiTenant(), server.WithPublicMode()}, "", "podium:unrouted"},
		{"no router, no identity provider", layer.Identity{}, []server.Option{server.WithMultiTenant()}, "", "podium:unrouted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			label, meter := searchLabelAndMeter(t, tc.id, tc.opts...)
			if label != tc.wantLabel {
				t.Errorf("record tenant = %q, want %q", label, tc.wantLabel)
			}
			if !countedOnce(meter, tc.wantMeter) {
				t.Errorf("meter did not count the event under %q", tc.wantMeter)
			}
		})
	}
}

// With no per-request audit metadata on the context, the emitter records no
// tenant even when the context carries a routed tenant.
//
// Spec: §8.1
func TestAuditEmitterFor_NoMetaRecordsNoTenant(t *testing.T) {
	t.Parallel()
	sink := audit.NewMemory()
	emit := auditEmitterFor(sink, nil, nil)
	emit(core.ContextWithTenant(t.Context(), "acme"), core.AuditEvent{Type: "artifacts.searched"})
	events := sink.Events()
	if len(events) != 1 || events[0].Tenant != "" {
		t.Errorf("events = %+v, want one event with no tenant", events)
	}
}
