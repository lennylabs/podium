package serverboot

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/audit"
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
