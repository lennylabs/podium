package ingest_test

import (
	"context"
	"testing/fstest"
	"time"

	"testing"

	"github.com/lennylabs/podium/internal/clock"
	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §8.1 — a freeze window with BreakGlass=true that would
// otherwise block ingest emits a freeze.break_glass audit event
// so the override is recorded.
// Phase: 8
func TestIngest_FreezeBreakGlassEmitsAuditEvent(t *testing.T) {
	testharness.RequirePhase(t, 8)
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	now := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)

	type emitted struct {
		eventType string
		target    string
		ctx       map[string]string
	}
	var emits []emitted
	auditEmit := func(eventType, target string, ctxFields map[string]string) {
		emits = append(emits, emitted{eventType, target, ctxFields})
	}

	files := fstest.MapFS{}
	_, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t",
		LayerID:  "team",
		Linter:   &lint.Linter{},
		Files:    files,
		Clock:    clock.NewFrozen(now),
		FreezeWindows: []ingest.FreezeWindow{{
			Name:          "annual_freeze",
			Start:         now.Add(-1 * time.Hour),
			End:           now.Add(1 * time.Hour),
			Blocks:        []string{"ingest"},
			BreakGlass:    true,
			Approvers:     []string{"alice", "bob"},
			Justification: "approved override for INC-1",
			GrantedAt:     now,
		}},
		AuditEmit: auditEmit,
		CallerID:  "alice@example",
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	got := false
	for _, e := range emits {
		if e.eventType == "freeze.break_glass" && e.target == "team" {
			got = true
			if e.ctx["window"] != "annual_freeze" {
				t.Errorf("ctx.window = %q", e.ctx["window"])
			}
			if e.ctx["caller"] != "alice@example" {
				t.Errorf("ctx.caller = %q", e.ctx["caller"])
			}
		}
	}
	if !got {
		t.Errorf("freeze.break_glass not emitted; got %+v", emits)
	}
}

// Spec: §8.1 — a freeze window with BreakGlass=true that's NOT
// active (out of time range, or doesn't list "ingest" in
// Blocks) does not emit freeze.break_glass.
// Phase: 8
func TestIngest_FreezeBreakGlassSkipsWhenWouldNotBlock(t *testing.T) {
	testharness.RequirePhase(t, 8)
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})

	now := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	var emits []string
	auditEmit := func(eventType, _ string, _ map[string]string) {
		emits = append(emits, eventType)
	}

	files := fstest.MapFS{}
	_, _ = ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t",
		LayerID:  "team",
		Linter:   &lint.Linter{},
		Files:    files,
		Clock:    clock.NewFrozen(now),
		FreezeWindows: []ingest.FreezeWindow{{
			Name:          "future_freeze",
			Start:         now.Add(24 * time.Hour),
			End:           now.Add(48 * time.Hour),
			Blocks:        []string{"ingest"},
			BreakGlass:    true,
			Approvers:     []string{"alice", "bob"},
			Justification: "scheduled for next week's freeze rehearsal",
			GrantedAt:     now,
		}},
		AuditEmit: auditEmit,
	})
	for _, e := range emits {
		if e == "freeze.break_glass" {
			t.Errorf("emitted freeze.break_glass for inactive window")
		}
	}
}
