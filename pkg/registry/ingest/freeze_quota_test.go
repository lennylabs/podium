package ingest_test

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lennylabs/podium/internal/clock"
	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §4.7.2 / §6.10 — an active freeze window blocks ingest with
// ingest.frozen.
// Phase: 6
// Matrix: §6.10 (ingest.frozen)
func TestIngest_FreezeWindowBlocks(t *testing.T) {
	testharness.RequirePhase(t, 6)
	t.Parallel()

	now := time.Date(2026, 12, 20, 12, 0, 0, 0, time.UTC)
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	_, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L",
		Files: fstest.MapFS{
			"x/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextArtifact("x"))},
		},
		FreezeWindows: []ingest.FreezeWindow{{
			Name:   "year-end-close",
			Start:  time.Date(2026, 12, 15, 0, 0, 0, 0, time.UTC),
			End:    time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
			Blocks: []string{"ingest"},
		}},
		Clock: clock.NewFrozen(now),
	})
	if !errors.Is(err, ingest.ErrFrozen) {
		t.Fatalf("got %v, want ErrFrozen", err)
	}
}

// Spec: §4.7.2 — a freeze window outside [Start, End) does not block.
// Phase: 6
func TestIngest_FreezeWindowOutsideRangeAllows(t *testing.T) {
	testharness.RequirePhase(t, 6)
	t.Parallel()

	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})

	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L",
		Files: fstest.MapFS{
			"x/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextArtifact("x"))},
		},
		FreezeWindows: []ingest.FreezeWindow{{
			Name:   "year-end",
			Start:  time.Date(2026, 12, 15, 0, 0, 0, 0, time.UTC),
			End:    time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
			Blocks: []string{"ingest"},
		}},
		Clock: clock.NewFrozen(now),
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if res.Accepted != 1 {
		t.Errorf("Accepted = %d, want 1", res.Accepted)
	}
}

// Spec: §4.7.2 — break-glass bypasses freeze enforcement.
// Phase: 6
func TestIngest_FreezeBreakGlassBypass(t *testing.T) {
	testharness.RequirePhase(t, 6)
	t.Parallel()

	now := time.Date(2026, 12, 20, 12, 0, 0, 0, time.UTC)
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})

	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L",
		Files: fstest.MapFS{
			"x/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextArtifact("x"))},
		},
		FreezeWindows: []ingest.FreezeWindow{{
			Name:          "year-end",
			Start:         time.Date(2026, 12, 15, 0, 0, 0, 0, time.UTC),
			End:           time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
			Blocks:        []string{"ingest"},
			BreakGlass:    true,
			Approvers:     []string{"alice", "bob"},
			Justification: "pre-close hotfix",
			GrantedAt:     now,
		}},
		Clock: clock.NewFrozen(now),
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if res.Accepted != 1 {
		t.Errorf("Accepted = %d under break-glass, want 1", res.Accepted)
	}
}

// Spec: §4.7.8 / §6.10 — accepting an artifact past the storage quota
// rejects with quota.storage_exceeded.
// Phase: 6
// Matrix: §6.10 (quota.storage_exceeded)
func TestIngest_StorageQuotaExceeded(t *testing.T) {
	testharness.RequirePhase(t, 6)
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})

	// Tiny quota: 50 bytes. The smallest manifest exceeds this.
	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L",
		Files: fstest.MapFS{
			"x/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextArtifact("x"))},
		},
		StorageQuotaBytes:   50,
		CurrentStorageBytes: 0,
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if len(res.Rejected) != 1 {
		t.Fatalf("got %d rejections, want 1", len(res.Rejected))
	}
	if res.Rejected[0].Code != "quota.storage_exceeded" {
		t.Errorf("Code = %q, want quota.storage_exceeded", res.Rejected[0].Code)
	}
}

// Spec: §4.7.8 — generous quota does not block ingest.
// Phase: 6
func TestIngest_StorageQuotaGenerousAllows(t *testing.T) {
	testharness.RequirePhase(t, 6)
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: "t"})

	res, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: "t", LayerID: "L",
		Files: fstest.MapFS{
			"x/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextArtifact("x"))},
		},
		StorageQuotaBytes: 1 << 30, // 1 GiB
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if res.Accepted != 1 {
		t.Errorf("Accepted = %d, want 1", res.Accepted)
	}
}
