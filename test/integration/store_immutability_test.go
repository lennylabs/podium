package integration

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/spi"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §4.7 Version immutability invariant — concurrent ingest
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

// Spec: §9.3 "Structured errors" + §4.7 immutability — the RegistryStore SPI
// (the file-backed SQLite standalone backend) returns the immutability
// violation as a structured *spi.Error carrying the §6.10
// ingest.immutable_violation code, recoverable via spi.AsError from the real
// backend rather than only from the package-level sentinel. Guards the §9.3
// claim that the default RegistryStore conforms today.
func TestStore_ImmutableViolation_IsStructured(t *testing.T) {
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
	base := store.ManifestRecord{
		TenantID:    "t",
		ArtifactID:  "x",
		Version:     "1.0.0",
		ContentHash: "sha256:aaa",
		Type:        "skill",
		Description: "desc",
		Sensitivity: "low",
		Layer:       "L",
		Frontmatter: []byte("---\ntype: skill\n---\n"),
		Body:        []byte("body"),
	}
	if err := st.PutManifest(ctx, base); err != nil {
		t.Fatalf("first PutManifest: %v", err)
	}
	// Same (id, version), different content hash: the immutability invariant
	// rejects it.
	conflict := base
	conflict.ContentHash = "sha256:bbb"
	err = st.PutManifest(ctx, conflict)
	if !errors.Is(err, store.ErrImmutableViolation) {
		t.Fatalf("PutManifest conflict = %v, want ErrImmutableViolation", err)
	}
	e, ok := spi.AsError(err)
	if !ok {
		t.Fatalf("immutability error is not a structured *spi.Error: %T %v", err, err)
	}
	if e.Code != "ingest.immutable_violation" {
		t.Errorf("Code = %q, want ingest.immutable_violation", e.Code)
	}
}
