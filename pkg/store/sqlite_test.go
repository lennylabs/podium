package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// Spec: §13.10 Standalone Deployment — the SQLite backend persists
// across opens of the same file path.
func TestSQLite_PersistsAcrossOpens(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "podium.db")

	s1, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	ctx := context.Background()
	if err := s1.CreateTenant(ctx, Tenant{ID: "a", Name: "acme"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	rec := ManifestRecord{
		TenantID: "a", ArtifactID: "x", Version: "1.0.0",
		ContentHash: "sha:1", Type: "skill",
		Tags: []string{"finance", "ap"},
	}
	if err := s1.PutManifest(ctx, rec); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite again: %v", err)
	}
	defer s2.Close()

	got, err := s2.GetManifest(ctx, "a", "x", "1.0.0")
	if err != nil {
		t.Fatalf("GetManifest after reopen: %v", err)
	}
	if got.ContentHash != "sha:1" {
		t.Errorf("ContentHash = %q, want sha:1", got.ContentHash)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "finance" || got.Tags[1] != "ap" {
		t.Errorf("Tags = %v, want [finance ap]", got.Tags)
	}

	tenant, err := s2.GetTenant(ctx, "a")
	if err != nil {
		t.Fatalf("GetTenant: %v", err)
	}
	if tenant.Name != "acme" {
		t.Errorf("Name = %q, want acme", tenant.Name)
	}
}

// Spec: §13.10 — schema apply is idempotent (re-opening an existing
// database does not error and preserves data).
func TestSQLite_SchemaIsIdempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "podium.db")

	for i := 0; i < 3; i++ {
		s, err := OpenSQLite(path)
		if err != nil {
			t.Fatalf("OpenSQLite #%d: %v", i, err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close #%d: %v", i, err)
		}
	}
}

// Spec: §4.7 Version immutability invariant (F-1.3.1) — the standalone
// backend is a file-backed SQLite database (WAL plus a busy timeout)
// served through a pooled *sql.DB, so writers run on separate
// connections rather than the serialized single connection the in-memory
// store uses. Concurrent ingest of different content for the same
// (tenant, id, version) must accept exactly one writer and reject every
// other with ErrImmutableViolation, never leaking a raw SQLite error
// (a primary-key unique violation or a "database is locked" contention
// error). This exercises true multi-connection concurrency that the
// in-memory conformance run cannot.
func TestSQLite_ConcurrentConflictMapsToImmutableViolation(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "concurrent.db")
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	ctx := context.Background()
	if err := s.CreateTenant(ctx, Tenant{ID: "a", Name: "a"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}

	const writers = 32
	var accepted, violations atomic.Int64
	var mu sync.Mutex
	var leaks []error

	ready := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		rec := ManifestRecord{
			TenantID:    "a",
			ArtifactID:  "x",
			Version:     "1.0.0",
			ContentHash: fmt.Sprintf("sha:%d", i),
			Type:        "skill",
			Sensitivity: "low",
			Layer:       "L",
		}
		go func() {
			defer wg.Done()
			<-ready
			switch err := s.PutManifest(ctx, rec); {
			case err == nil:
				accepted.Add(1)
			case errors.Is(err, ErrImmutableViolation):
				violations.Add(1)
			default:
				mu.Lock()
				leaks = append(leaks, err)
				mu.Unlock()
			}
		}()
	}
	close(ready)
	wg.Wait()

	if len(leaks) > 0 {
		t.Fatalf("%d/%d writers leaked a raw SQLite error instead of ErrImmutableViolation; first: %v",
			len(leaks), writers, leaks[0])
	}
	if accepted.Load() != 1 {
		t.Errorf("accepted = %d, want exactly 1", accepted.Load())
	}
	if got := accepted.Load() + violations.Load(); got != int64(writers) {
		t.Errorf("accepted (%d) + violations (%d) = %d, want %d", accepted.Load(), violations.Load(), got, writers)
	}
}
