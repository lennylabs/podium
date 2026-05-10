package ingest_test

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lennylabs/podium/internal/clock"
	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

const breakGlassArtifact = `---
type: skill
version: 1.0.0
description: x
sensitivity: low
---

`
const breakGlassSkill = `---
name: x
description: x
---
body
`

func breakGlassFiles() fstest.MapFS {
	return fstest.MapFS{
		"x/ARTIFACT.md": &fstest.MapFile{Data: []byte(breakGlassArtifact)},
		"x/SKILL.md":    &fstest.MapFile{Data: []byte(breakGlassSkill)},
	}
}

func breakGlassWindow(now time.Time, fields func(*ingest.FreezeWindow)) ingest.FreezeWindow {
	w := ingest.FreezeWindow{
		Name:   "y",
		Start:  now.Add(-1 * time.Hour),
		End:    now.Add(1 * time.Hour),
		Blocks: []string{"ingest"},
	}
	fields(&w)
	return w
}

// Spec: §4.7.2 — break-glass requires two distinct admin
// approvers and a non-empty justification; a valid grant lets
// ingest bypass the freeze.
func TestIngest_BreakGlassValidGrantBypassesFreeze(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	now := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "team",
		Linter: &lint.Linter{},
		Files:  breakGlassFiles(),
		Clock:  clock.NewFrozen(now),
		FreezeWindows: []ingest.FreezeWindow{breakGlassWindow(now, func(w *ingest.FreezeWindow) {
			w.BreakGlass = true
			w.Approvers = []string{"alice", "bob"}
			w.Justification = "Hotfix for incident INC-2026-01"
			w.GrantedAt = now.Add(-30 * time.Minute)
		})},
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if res.Accepted != 1 {
		t.Errorf("Accepted = %d, want 1 (valid grant should bypass)", res.Accepted)
	}
}

// Spec: §4.7.2 — single approver is not dual-signoff; bypass is
// refused and ingest is blocked.
func TestIngest_BreakGlassRejectsSingleApprover(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	now := time.Now().UTC()
	_, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "team",
		Linter: &lint.Linter{},
		Files:  breakGlassFiles(),
		Clock:  clock.NewFrozen(now),
		FreezeWindows: []ingest.FreezeWindow{breakGlassWindow(now, func(w *ingest.FreezeWindow) {
			w.BreakGlass = true
			w.Approvers = []string{"alice"}
			w.Justification = "x"
			w.GrantedAt = now
		})},
	})
	if err == nil || !errors.Is(err, ingest.ErrFrozen) {
		t.Errorf("err = %v, want ErrFrozen (single approver should not bypass)", err)
	}
}

// Spec: §4.7.2 — duplicate approvers do not satisfy "two
// admins": the unique-set check.
func TestIngest_BreakGlassRejectsDuplicateApprovers(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	now := time.Now().UTC()
	_, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "team",
		Linter: &lint.Linter{},
		Files:  breakGlassFiles(),
		Clock:  clock.NewFrozen(now),
		FreezeWindows: []ingest.FreezeWindow{breakGlassWindow(now, func(w *ingest.FreezeWindow) {
			w.BreakGlass = true
			w.Approvers = []string{"alice", "alice"}
			w.Justification = "x"
			w.GrantedAt = now
		})},
	})
	if err == nil || !errors.Is(err, ingest.ErrFrozen) {
		t.Errorf("err = %v, want ErrFrozen (duplicate approvers)", err)
	}
}

// Spec: §4.7.2 — justification is required; an empty string is
// rejected.
func TestIngest_BreakGlassRejectsMissingJustification(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	now := time.Now().UTC()
	_, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "team",
		Linter: &lint.Linter{},
		Files:  breakGlassFiles(),
		Clock:  clock.NewFrozen(now),
		FreezeWindows: []ingest.FreezeWindow{breakGlassWindow(now, func(w *ingest.FreezeWindow) {
			w.BreakGlass = true
			w.Approvers = []string{"alice", "bob"}
			w.Justification = ""
			w.GrantedAt = now
		})},
	})
	if err == nil || !errors.Is(err, ingest.ErrFrozen) {
		t.Errorf("err = %v, want ErrFrozen (missing justification)", err)
	}
}

// Spec: §4.7.2 — break-glass auto-expires after 24h; a grant
// older than that is refused.
func TestIngest_BreakGlassExpiresAfter24Hours(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	now := time.Now().UTC()
	_, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "team",
		Linter: &lint.Linter{},
		Files:  breakGlassFiles(),
		Clock:  clock.NewFrozen(now),
		FreezeWindows: []ingest.FreezeWindow{breakGlassWindow(now, func(w *ingest.FreezeWindow) {
			w.BreakGlass = true
			w.Approvers = []string{"alice", "bob"}
			w.Justification = "stale grant"
			w.GrantedAt = now.Add(-25 * time.Hour) // > 24h
		})},
	})
	if err == nil || !errors.Is(err, ingest.ErrFrozen) {
		t.Errorf("err = %v, want ErrFrozen (expired grant)", err)
	}
}

// Spec: §4.7.2 — the audit event for a successful bypass
// records both approvers and the justification so post-hoc
// security review has the context.
func TestIngest_BreakGlassAuditEventCarriesGrantMetadata(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})
	now := time.Now().UTC()
	type emitted struct {
		event string
		ctx   map[string]string
	}
	var emits []emitted
	_, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "team",
		Linter: &lint.Linter{},
		Files:  breakGlassFiles(),
		Clock:  clock.NewFrozen(now),
		FreezeWindows: []ingest.FreezeWindow{breakGlassWindow(now, func(w *ingest.FreezeWindow) {
			w.BreakGlass = true
			w.Approvers = []string{"alice", "bob"}
			w.Justification = "Hotfix for INC-1"
			w.GrantedAt = now
		})},
		AuditEmit: func(eventType, _ string, ctx map[string]string) {
			cp := map[string]string{}
			for k, v := range ctx {
				cp[k] = v
			}
			emits = append(emits, emitted{eventType, cp})
		},
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	for _, e := range emits {
		if e.event == "freeze.break_glass" {
			if e.ctx["approvers"] != "alice,bob" && e.ctx["approvers"] != "bob,alice" {
				t.Errorf("approvers = %q, want alice,bob", e.ctx["approvers"])
			}
			if e.ctx["justification"] != "Hotfix for INC-1" {
				t.Errorf("justification = %q", e.ctx["justification"])
			}
			return
		}
	}
	t.Errorf("freeze.break_glass not emitted: %+v", emits)
}
