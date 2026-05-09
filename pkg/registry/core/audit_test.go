package core_test

import (
	"context"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

type recorder struct {
	mu     sync.Mutex
	events []core.AuditEvent
}

func (r *recorder) emit(_ context.Context, e core.AuditEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recorder) snapshot() []core.AuditEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]core.AuditEvent, len(r.events))
	copy(out, r.events)
	return out
}

func setupRegistryWithAudit(t *testing.T) (*core.Registry, *recorder) {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L", Files: fstest.MapFS{
			"finance/x/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextManifest("variance"))},
		},
	}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	rec := &recorder{}
	reg := core.New(st, "t", []layer.Layer{
		{ID: "L", Visibility: layer.Visibility{Public: true}, Precedence: 1},
	}).WithAudit(rec.emit)
	return reg, rec
}

// Spec: §8.1 — load_domain emits a domain.loaded audit event per call.
// Phase: 16
func TestAudit_LoadDomain(t *testing.T) {
	testharness.RequirePhase(t, 16)
	t.Parallel()
	reg, rec := setupRegistryWithAudit(t)
	if _, err := reg.LoadDomain(context.Background(), publicID, "", core.LoadDomainOptions{}); err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	events := rec.snapshot()
	if len(events) != 1 || events[0].Type != "domain.loaded" {
		t.Errorf("got %+v, want one domain.loaded event", events)
	}
	if events[0].Caller != "system:public" {
		t.Errorf("Caller = %q, want system:public for anonymous", events[0].Caller)
	}
}

// Spec: §8.1 — search_artifacts emits an artifacts.searched event;
// query / scope / type appear in context.
// Phase: 16
func TestAudit_SearchArtifacts(t *testing.T) {
	testharness.RequirePhase(t, 16)
	t.Parallel()
	reg, rec := setupRegistryWithAudit(t)
	if _, err := reg.SearchArtifacts(context.Background(), publicID, core.SearchArtifactsOptions{
		Query: "variance",
		Type:  "context",
		Scope: "finance",
	}); err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	events := rec.snapshot()
	if len(events) != 1 || events[0].Type != "artifacts.searched" {
		t.Fatalf("got %+v, want artifacts.searched", events)
	}
	if events[0].Context["query"] != "variance" ||
		events[0].Context["scope"] != "finance" ||
		events[0].Context["type"] != "context" {
		t.Errorf("Context = %v", events[0].Context)
	}
}

// Spec: §8.1 — load_artifact emits artifact.loaded with the artifact
// ID as the target.
// Phase: 16
func TestAudit_LoadArtifact(t *testing.T) {
	testharness.RequirePhase(t, 16)
	t.Parallel()
	reg, rec := setupRegistryWithAudit(t)
	if _, err := reg.LoadArtifact(context.Background(), publicID, "finance/x", core.LoadArtifactOptions{}); err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	events := rec.snapshot()
	if len(events) != 1 || events[0].Type != "artifact.loaded" {
		t.Fatalf("got %+v, want artifact.loaded", events)
	}
	if events[0].Target != "finance/x" {
		t.Errorf("Target = %q, want finance/x", events[0].Target)
	}
}

// Spec: §8.1 — authenticated callers record their sub claim, not
// system:public.
// Phase: 16
func TestAudit_AuthenticatedCallerRecordsSub(t *testing.T) {
	testharness.RequirePhase(t, 16)
	t.Parallel()
	reg, rec := setupRegistryWithAudit(t)
	id := layer.Identity{Sub: "joan", IsAuthenticated: true}
	if _, err := reg.LoadDomain(context.Background(), id, "", core.LoadDomainOptions{}); err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	events := rec.snapshot()
	if len(events) != 1 || events[0].Caller != "joan" {
		t.Errorf("Caller = %q, want joan", events[0].Caller)
	}
}
