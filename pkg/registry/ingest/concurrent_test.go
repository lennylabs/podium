package ingest_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §4.7 — concurrent ingests of the same content_hash must
// produce at most one artifact.published event, regardless of how
// many goroutines race through the pipeline. The store's atomic
// per-row UPSERT is the immutability anchor; the ingest pipeline
// must not double-emit events when two goroutines both observe
// ErrNotFound from GetManifest and then both succeed at PutManifest.
func TestIngest_ConcurrentSameContentEmitsOnce(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	files := fstest.MapFS{
		"x/ARTIFACT.md": &fstest.MapFile{
			Data: []byte("---\ntype: context\nversion: 1.0.0\ndescription: x\nsensitivity: low\n---\n\nbody\n"),
		},
	}

	const goroutines = 8
	var publishedCount atomic.Int64
	var auditCount atomic.Int64
	var acceptedSum atomic.Int64
	var idempotentSum atomic.Int64

	// Start all goroutines at once via a barrier so the race window
	// between GetManifest and PutManifest is maximal.
	ready := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-ready
			res, err := ingest.Ingest(context.Background(), st, ingest.Request{
				TenantID: "t",
				LayerID:  "L",
				Files:    files,
				PublishEvent: func(typ string, _ map[string]any) {
					if typ == "artifact.published" {
						publishedCount.Add(1)
					}
				},
				AuditEmit: func(typ, _ string, _ map[string]string) {
					if typ == "artifact.published" {
						auditCount.Add(1)
					}
				},
			})
			if err != nil {
				t.Errorf("Ingest: %v", err)
				return
			}
			acceptedSum.Add(int64(res.Accepted))
			idempotentSum.Add(int64(res.Idempotent))
		}()
	}
	close(ready)
	wg.Wait()

	// The accepted + idempotent counts MUST sum to exactly the number
	// of goroutines (one per call), with exactly one Accepted across
	// the whole run.
	total := acceptedSum.Load() + idempotentSum.Load()
	if total != goroutines {
		t.Errorf("Accepted+Idempotent across all goroutines = %d, want %d", total, goroutines)
	}
	if acceptedSum.Load() != 1 {
		t.Errorf("Accepted across all goroutines = %d, want exactly 1 (the rest should be Idempotent). "+
			"This indicates the ingest pipeline emitted duplicate publishes for the same content.",
			acceptedSum.Load())
	}
	if publishedCount.Load() != 1 {
		t.Errorf("artifact.published events = %d, want 1 (concurrent ingests of identical "+
			"content must not double-emit)", publishedCount.Load())
	}
	if auditCount.Load() != 1 {
		t.Errorf("artifact.published audit emissions = %d, want 1", auditCount.Load())
	}
}

// Spec: §4.7 — concurrent ingests with DIFFERENT content_hash for the
// same (id, version) must accept exactly one and report the rest as
// Conflicts (immutability violation), regardless of which goroutine
// wins the race.
func TestIngest_ConcurrentDifferentContentOneAcceptsRestConflict(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	const goroutines = 8
	var publishedCount atomic.Int64
	var acceptedSum atomic.Int64
	var conflictSum atomic.Int64

	ready := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		// Each goroutine ingests the same (id, version) but with a
		// different body so the content_hash differs.
		body := []byte("---\ntype: context\nversion: 1.0.0\ndescription: x\nsensitivity: low\n---\n\nbody variant\n")
		body[len(body)-2] = byte('A' + i) // perturb to ensure distinct hashes
		files := fstest.MapFS{
			"x/ARTIFACT.md": &fstest.MapFile{Data: body},
		}
		go func() {
			defer wg.Done()
			<-ready
			res, err := ingest.Ingest(context.Background(), st, ingest.Request{
				TenantID: "t",
				LayerID:  "L",
				Files:    files,
				PublishEvent: func(typ string, _ map[string]any) {
					if typ == "artifact.published" {
						publishedCount.Add(1)
					}
				},
			})
			if err != nil {
				t.Errorf("Ingest: %v", err)
				return
			}
			acceptedSum.Add(int64(res.Accepted))
			conflictSum.Add(int64(len(res.Conflicts)))
		}()
	}
	close(ready)
	wg.Wait()

	if acceptedSum.Load() != 1 {
		t.Errorf("Accepted across all goroutines = %d, want exactly 1 "+
			"(immutability invariant: same (id, version) with different bytes "+
			"must accept only the first)", acceptedSum.Load())
	}
	if acceptedSum.Load()+conflictSum.Load() != goroutines {
		t.Errorf("Accepted (%d) + Conflicts (%d) = %d, want %d",
			acceptedSum.Load(), conflictSum.Load(),
			acceptedSum.Load()+conflictSum.Load(), goroutines)
	}
	if publishedCount.Load() != 1 {
		t.Errorf("artifact.published events = %d, want 1", publishedCount.Load())
	}
}
