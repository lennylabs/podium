package store

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

// outboxBackends returns the §4.7.2-capable stores to run the suite against.
// Postgres needs a live database and is covered by its integration tests.
func outboxBackends(t *testing.T) map[string]VectorOutbox {
	t.Helper()
	sq, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = sq.Close() })
	return map[string]VectorOutbox{
		"memory": NewMemory(),
		"sqlite": sq,
	}
}

func mr(id, version, hash string) ManifestRecord {
	return ManifestRecord{
		TenantID: "default", ArtifactID: id, Version: version,
		ContentHash: hash, Type: "agent", Description: "d",
	}
}

func TestVectorOutbox_EnqueueListDone(t *testing.T) {
	for name, ob := range outboxBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
			p := VectorPending{
				TenantID: "default", ArtifactID: "a/x", Version: "1.0.0",
				Text: "embed me", EnqueuedAt: now, NextRetryAt: now,
			}
			if err := ob.PutManifestWithVectorPending(ctx, mr("a/x", "1.0.0", "sha256:1"), p); err != nil {
				t.Fatalf("put: %v", err)
			}

			pending, err := ob.ListVectorPending(ctx, 10, now)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(pending) != 1 || pending[0].Text != "embed me" {
				t.Fatalf("pending = %+v, want one row with text", pending)
			}

			depth, oldest, err := ob.VectorOutboxStats(ctx)
			if err != nil || depth != 1 {
				t.Fatalf("stats depth=%d err=%v, want 1", depth, err)
			}
			if !oldest.Equal(now) {
				t.Errorf("oldest = %v, want %v", oldest, now)
			}

			if err := ob.MarkVectorPendingDone(ctx, "default", "a/x", "1.0.0"); err != nil {
				t.Fatalf("done: %v", err)
			}
			depth, _, _ = ob.VectorOutboxStats(ctx)
			if depth != 0 {
				t.Errorf("depth after done = %d, want 0", depth)
			}
		})
	}
}

func TestVectorOutbox_IdempotentReingestDoesNotRequeue(t *testing.T) {
	for name, ob := range outboxBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
			p := VectorPending{TenantID: "default", ArtifactID: "a/x", Version: "1.0.0", Text: "t", EnqueuedAt: now, NextRetryAt: now}
			if err := ob.PutManifestWithVectorPending(ctx, mr("a/x", "1.0.0", "sha256:1"), p); err != nil {
				t.Fatalf("put: %v", err)
			}
			// Drain it.
			if err := ob.MarkVectorPendingDone(ctx, "default", "a/x", "1.0.0"); err != nil {
				t.Fatalf("done: %v", err)
			}
			// Re-ingest the same content hash: must not re-queue.
			if err := ob.PutManifestWithVectorPending(ctx, mr("a/x", "1.0.0", "sha256:1"), p); err != nil {
				t.Fatalf("reput: %v", err)
			}
			depth, _, _ := ob.VectorOutboxStats(ctx)
			if depth != 0 {
				t.Errorf("idempotent re-ingest re-queued; depth=%d, want 0", depth)
			}
		})
	}
}

func TestVectorOutbox_ImmutableViolationRollsBack(t *testing.T) {
	for name, ob := range outboxBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
			p := VectorPending{TenantID: "default", ArtifactID: "a/x", Version: "1.0.0", Text: "t", EnqueuedAt: now, NextRetryAt: now}
			if err := ob.PutManifestWithVectorPending(ctx, mr("a/x", "1.0.0", "sha256:1"), p); err != nil {
				t.Fatalf("put: %v", err)
			}
			_ = ob.MarkVectorPendingDone(ctx, "default", "a/x", "1.0.0")
			// Same version, different hash -> immutability violation, no new row.
			err := ob.PutManifestWithVectorPending(ctx, mr("a/x", "1.0.0", "sha256:2"), p)
			if !errors.Is(err, ErrImmutableViolation) {
				t.Fatalf("err = %v, want ErrImmutableViolation", err)
			}
			depth, _, _ := ob.VectorOutboxStats(ctx)
			if depth != 0 {
				t.Errorf("rolled-back violation left a row; depth=%d, want 0", depth)
			}
		})
	}
}

// checkPendingKeys enqueues two rows of tenant a, one of them backed off,
// and one row of tenant b, and checks that the key listing of each tenant
// returns its rows and only its rows, whatever their retry time.
func checkPendingKeys(t *testing.T, ob VectorOutbox, a, b string) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	for _, k := range []struct{ tenant, id string }{{a, "a/x"}, {a, "a/y"}, {b, "b/z"}} {
		rec := ManifestRecord{TenantID: k.tenant, ArtifactID: k.id, Version: "1.0.0", ContentHash: "sha256:" + k.id, Type: "agent", Description: "d"}
		p := VectorPending{TenantID: k.tenant, ArtifactID: k.id, Version: "1.0.0", Text: "t", EnqueuedAt: now, NextRetryAt: now}
		if err := ob.PutManifestWithVectorPending(ctx, rec, p); err != nil {
			t.Fatalf("put %s/%s: %v", k.tenant, k.id, err)
		}
		t.Cleanup(func() { _ = ob.MarkVectorPendingDone(context.Background(), k.tenant, k.id, "1.0.0") })
	}
	if err := ob.MarkVectorPendingRetry(ctx, a, "a/y", "1.0.0", now.Add(time.Hour), "backend down"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	keys, err := ob.ListVectorPendingKeys(ctx, a)
	got := map[VectorPendingKey]bool{}
	for _, k := range keys {
		got[k] = true
	}
	if err != nil || len(keys) != 2 || !got[VectorPendingKey{"a/x", "1.0.0"}] || !got[VectorPendingKey{"a/y", "1.0.0"}] {
		t.Errorf("keys of %s = %+v, %v; want a/x and a/y, the backed-off row included", a, keys, err)
	}
	if keys, err := ob.ListVectorPendingKeys(ctx, b); err != nil || len(keys) != 1 || keys[0] != (VectorPendingKey{"b/z", "1.0.0"}) {
		t.Errorf("keys of %s = %+v, %v; want b/z alone", b, keys, err)
	}
	if err := ob.MarkVectorPendingDone(ctx, a, "a/x", "1.0.0"); err != nil {
		t.Fatalf("done: %v", err)
	}
	if keys, err := ob.ListVectorPendingKeys(ctx, a); err != nil || len(keys) != 1 || keys[0] != (VectorPendingKey{"a/y", "1.0.0"}) {
		t.Errorf("keys of %s after a/x drained = %+v, %v; want a/y alone", a, keys, err)
	}
}

// Spec: §7.3.1, §4.7 — the outbox key listing the sweep reads returns every
// pending row of one tenant, whatever its retry time.
func TestVectorOutbox_ListPendingKeys(t *testing.T) {
	for name, ob := range outboxBackends(t) {
		t.Run(name, func(t *testing.T) { checkPendingKeys(t, ob, "default", "other") })
	}
}

// Spec: §7.3.1, §4.7 — the Postgres outbox key listing reads the cross-org
// outbox table by tenant. It skips without PODIUM_POSTGRES_DSN.
func TestVectorOutbox_ListPendingKeysPostgres(t *testing.T) {
	a := "pendkeys-a-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	b := strings.Replace(a, "-a-", "-b-", 1)
	p, _ := openPostgresOrg(t, a)
	ctx := context.Background()
	if err := p.CreateTenant(ctx, Tenant{ID: b, Name: b}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	t.Cleanup(func() { _ = p.DropOrg(context.Background(), b) })
	checkPendingKeys(t, p, a, b)
}

func TestVectorOutbox_RetryBackoffHidesRow(t *testing.T) {
	for name, ob := range outboxBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
			p := VectorPending{TenantID: "default", ArtifactID: "a/x", Version: "1.0.0", Text: "t", EnqueuedAt: now, NextRetryAt: now}
			if err := ob.PutManifestWithVectorPending(ctx, mr("a/x", "1.0.0", "sha256:1"), p); err != nil {
				t.Fatalf("put: %v", err)
			}
			next := now.Add(5 * time.Minute)
			if err := ob.MarkVectorPendingRetry(ctx, "default", "a/x", "1.0.0", next, "backend down"); err != nil {
				t.Fatalf("retry: %v", err)
			}
			// Not eligible before next_retry_at.
			if rows, _ := ob.ListVectorPending(ctx, 10, now.Add(time.Minute)); len(rows) != 0 {
				t.Errorf("row visible before backoff elapsed: %+v", rows)
			}
			// Eligible after.
			rows, _ := ob.ListVectorPending(ctx, 10, now.Add(10*time.Minute))
			if len(rows) != 1 || rows[0].Attempts != 1 || rows[0].LastError != "backend down" {
				t.Errorf("after backoff rows = %+v, want one attempt with error", rows)
			}
		})
	}
}

// Spec: §7.3.1 (Removed artifacts), §4.7.2 — on Postgres, a write with a
// vector outbox row over an existing version is idempotent for the same
// content, an immutability violation for other content, and an immutability
// violation when another layer writes the same content over a version a
// layer's removal tombstoned; a refused write leaves no outbox row.
func TestVectorOutbox_ExistingVersionPostgres(t *testing.T) {
	tenant := "outbox-exist-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	p, _ := openPostgresOrg(t, tenant)
	ctx := context.Background()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	rec := func(layer, hash string) ManifestRecord {
		m := mr("a/x", "1.0.0", hash)
		m.TenantID, m.Layer = tenant, layer
		return m
	}
	pending := VectorPending{TenantID: tenant, ArtifactID: "a/x", Version: "1.0.0", Text: "t", EnqueuedAt: now, NextRetryAt: now}
	if err := p.PutManifestWithVectorPending(ctx, rec("team", "sha256:1"), pending); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := p.MarkVectorPendingDone(ctx, tenant, "a/x", "1.0.0"); err != nil {
		t.Fatalf("done: %v", err)
	}
	same := p.PutManifestWithVectorPending(ctx, rec("team", "sha256:1"), pending)
	other := p.PutManifestWithVectorPending(ctx, rec("team", "sha256:2"), pending)
	if err := p.SoftDeleteArtifacts(ctx, tenant, "team", []string{"a/x"}, now, nil); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	foreign := p.PutManifestWithVectorPending(ctx, rec("org", "sha256:1"), pending)
	keys, err := p.ListVectorPendingKeys(ctx, tenant)
	if err != nil {
		t.Fatal(err)
	}
	if same != nil || !errors.Is(other, ErrImmutableViolation) || !errors.Is(foreign, ErrImmutableViolation) || len(keys) != 0 {
		t.Errorf("same content: %v; other content: %v; other layer over a tombstone: %v; outbox rows %v; want nil, two immutability violations, and none",
			same, other, foreign, keys)
	}
}
