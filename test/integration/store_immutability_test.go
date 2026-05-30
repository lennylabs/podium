package integration

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §4.7 Version immutability invariant (F-1.3.1) — concurrent ingest
// of conflicting bytes for the same (id, version) against the file-backed
// SQLite store (the standalone backend) must accept exactly one writer and
// report the rest as per-artifact Conflicts, never failing the whole batch
// with a raw store error. The orchestrator maps store.ErrImmutableViolation
// to a Conflict and returns any other error as a hard batch failure
// (pkg/registry/ingest/ingest.go), so a store that leaked the raw
// primary-key violation under the race would abort the entire ingest.
// Drives the real ingest pipeline against a pooled, multi-connection SQLite
// database rather than the mutex-serialized in-memory store.
func TestIngest_ConcurrentConflict_SQLiteReportsConflicts(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "registry.db")
	st, err := store.OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	ctx := context.Background()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	const goroutines = 16
	var acceptedSum, conflictSum atomic.Int64
	var mu sync.Mutex
	var hardErr error

	ready := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		// Same (id, version), different body, so the content hash differs
		// and only the first writer may win the immutability race.
		body := []byte("---\ntype: context\nversion: 1.0.0\ndescription: x\nsensitivity: low\n---\n\nbody variant\n")
		body[len(body)-2] = byte('A' + i)
		files := fstest.MapFS{
			"x/ARTIFACT.md": &fstest.MapFile{Data: body},
		}
		go func() {
			defer wg.Done()
			<-ready
			res, err := ingest.Ingest(ctx, st, ingest.Request{
				TenantID: "t",
				LayerID:  "L",
				Files:    files,
			})
			if err != nil {
				mu.Lock()
				hardErr = err
				mu.Unlock()
				return
			}
			acceptedSum.Add(int64(res.Accepted))
			conflictSum.Add(int64(len(res.Conflicts)))
		}()
	}
	close(ready)
	wg.Wait()

	if hardErr != nil {
		t.Fatalf("Ingest returned a hard error under the immutability race "+
			"(the store leaked a raw error instead of ErrImmutableViolation): %v", hardErr)
	}
	if acceptedSum.Load() != 1 {
		t.Errorf("Accepted across all goroutines = %d, want exactly 1 "+
			"(immutability: only the first bytes for an (id, version) win)", acceptedSum.Load())
	}
	if got := acceptedSum.Load() + conflictSum.Load(); got != goroutines {
		t.Errorf("Accepted (%d) + Conflicts (%d) = %d, want %d",
			acceptedSum.Load(), conflictSum.Load(), got, goroutines)
	}
}
