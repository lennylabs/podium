package store

import (
	"context"
	"errors"
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
		ContentHash: hash, Type: "context", Description: "d",
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
			ob.MarkVectorPendingDone(ctx, "default", "a/x", "1.0.0")
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
