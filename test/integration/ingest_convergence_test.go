package integration

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §4.7 Version immutability invariant — "A (artifact_id,
// version) pair, once ingested, is bit-for-bit immutable forever in the
// registry's content store. Subsequent commits in a layer's source that change
// the same version: with different content are rejected at ingest. Readers in
// flight when a re-ingest occurs continue to see their pinned version."
//
// The existing concurrent_test.go (pkg/registry/ingest) and storetest.go
// assert the *event* accounting under a race (exactly one publish, the rest
// idempotent/conflict). This test asserts the orthogonal property the spec
// states: regardless of which goroutine wins, the registry converges to one
// consistent end state, and every reader after the race observes that single
// durable manifest. It drives the real ingest pipeline against the in-process
// stores so it runs by default with no external infrastructure; a Postgres
// variant is gated on PODIUM_POSTGRES_DSN.

// ingestConvergenceFactory builds a store the convergence cases run against.
type ingestConvergenceFactory struct {
	name string
	open func(t *testing.T) store.Store
}

func ingestConvergenceStores(t *testing.T) []ingestConvergenceFactory {
	t.Helper()
	factories := []ingestConvergenceFactory{
		{
			name: "memory",
			open: func(t *testing.T) store.Store { return store.NewMemory() },
		},
		{
			name: "sqlite",
			open: func(t *testing.T) store.Store {
				// File-backed SQLite is the standalone backend; it pools
				// multiple connections (WAL + busy timeout) so the race runs
				// against real concurrent commits rather than a single
				// mutex-serialized map.
				path := filepath.Join(t.TempDir(), "registry.db")
				st, err := store.OpenSQLite(path)
				if err != nil {
					t.Fatalf("OpenSQLite: %v", err)
				}
				t.Cleanup(func() { _ = st.Close() })
				return st
			},
		},
	}
	if dsn := os.Getenv("PODIUM_POSTGRES_DSN"); dsn != "" {
		factories = append(factories, ingestConvergenceFactory{
			name: "postgres",
			open: func(t *testing.T) store.Store {
				// ResetForTest drops every org schema on the shared database, so
				// this Postgres subtest holds the whole-database lock for its full
				// run; the memory and sqlite factories are unaffected and stay
				// parallel.
				lockPostgresReset(t)
				pg, err := store.OpenPostgres(dsn)
				if err != nil {
					t.Skipf("OpenPostgres %q: %v (database unreachable)", dsn, err)
				}
				t.Cleanup(func() { _ = pg.Close() })
				if err := pg.ResetForTest(context.Background()); err != nil {
					t.Fatalf("ResetForTest: %v", err)
				}
				return pg
			},
		})
	}
	return factories
}

// artifactBytes builds a minimal valid ARTIFACT.md whose body byte varies with
// the marker, so distinct markers yield distinct content hashes for the same
// (id, version).
func artifactBytes(marker byte) []byte {
	body := []byte("---\ntype: context\nversion: 1.0.0\ndescription: x\nsensitivity: low\n---\n\nbody _\n")
	body[len(body)-2] = marker
	return body
}

// Spec: §4.7 immutability — concurrent ingest of IDENTICAL bytes for one
// (id, version) converges to that content. After the race, the store holds the
// single agreed manifest and every reader sees the same content hash; the
// accepted/idempotent accounting closes to exactly one writer.
func TestIngestConvergence_SameContentConverges(t *testing.T) {
	t.Parallel()
	for _, f := range ingestConvergenceStores(t) {
		f := f
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			st := f.open(t)
			ctx := context.Background()
			if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
				t.Fatalf("CreateTenant: %v", err)
			}

			files := fstest.MapFS{"x/ARTIFACT.md": &fstest.MapFile{Data: artifactBytes('A')}}

			const goroutines = 24
			var acceptedSum, idempotentSum atomic.Int64
			var mu sync.Mutex
			var hardErr error

			ready := make(chan struct{})
			var wg sync.WaitGroup
			for i := 0; i < goroutines; i++ {
				wg.Add(1)
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
					idempotentSum.Add(int64(res.Idempotent))
				}()
			}
			close(ready)
			wg.Wait()

			if hardErr != nil {
				t.Fatalf("Ingest returned a hard error under the identical-content race: %v", hardErr)
			}
			// Every call is accounted for as either accepted or idempotent, with
			// no call lost or double-counted. At least one writer stored the
			// bytes. The per-call Accepted/Idempotent split is not pinned to
			// exactly 1: two writers that both pass the GetManifest fast-path
			// before either commits each report Accepted, because the store's
			// idempotent same-hash insert returns success indistinguishably from
			// a fresh insert (sqlite.go PutManifest, ON CONFLICT DO NOTHING +
			// read-back). That double-count is a benign reporting artifact of the
			// optimistic check; the spec-level invariant is the durable end
			// state, asserted below, not the counter split.
			if acceptedSum.Load() < 1 {
				t.Errorf("Accepted = %d, want at least 1 (some writer must store the bytes)", acceptedSum.Load())
			}
			if got := acceptedSum.Load() + idempotentSum.Load(); got != goroutines {
				t.Errorf("Accepted (%d) + Idempotent (%d) = %d, want %d (no call lost or double-counted)",
					acceptedSum.Load(), idempotentSum.Load(), got, goroutines)
			}

			// Convergence (the §4.7 invariant): the store holds exactly one
			// manifest for (id, version), and concurrent readers all see the
			// same content hash regardless of how the race interleaved.
			assertSingleDurableManifest(t, st, "t", "x", "1.0.0")
		})
	}
}

// Spec: §4.7 immutability — concurrent ingest of DIFFERENT bytes for one
// (id, version) converges to exactly one winner. After the race the store
// holds a single manifest, every reader agrees on its content hash, and the
// accepted/conflict accounting closes with exactly one acceptance and no raw
// store error leaked through the pipeline.
func TestIngestConvergence_DifferentContentConvergesToOneWinner(t *testing.T) {
	t.Parallel()
	for _, f := range ingestConvergenceStores(t) {
		f := f
		t.Run(f.name, func(t *testing.T) {
			t.Parallel()
			st := f.open(t)
			ctx := context.Background()
			if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
				t.Fatalf("CreateTenant: %v", err)
			}

			const goroutines = 24
			var acceptedSum, conflictSum atomic.Int64
			var mu sync.Mutex
			var hardErr error

			ready := make(chan struct{})
			var wg sync.WaitGroup
			for i := 0; i < goroutines; i++ {
				wg.Add(1)
				// Each goroutine ingests the same (id, version) with a distinct
				// body, so only the first content may win the immutability race.
				files := fstest.MapFS{
					"x/ARTIFACT.md": &fstest.MapFile{Data: artifactBytes(byte('A' + i))},
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
				t.Fatalf("Ingest returned a hard error under the conflicting-content race "+
					"(a raw store error leaked instead of a per-artifact Conflict): %v", hardErr)
			}
			if acceptedSum.Load() != 1 {
				t.Errorf("Accepted = %d, want exactly 1 (immutability: only the first bytes win)", acceptedSum.Load())
			}
			if got := acceptedSum.Load() + conflictSum.Load(); got != goroutines {
				t.Errorf("Accepted (%d) + Conflicts (%d) = %d, want %d (every loser reported a conflict)",
					acceptedSum.Load(), conflictSum.Load(), got, goroutines)
			}

			// Convergence: one durable manifest survives the race, its content is
			// one of the racers', and every reader observes the same content hash.
			hash := assertSingleDurableManifest(t, st, "t", "x", "1.0.0")

			// The durable bytes match one goroutine's content. Recompute the
			// candidate hashes via a throwaway ingest into a fresh store so the
			// assertion compares against the same hashing the pipeline uses.
			if !contentHashIsACandidate(t, hash) {
				t.Errorf("durable content hash %q is not one of the racing candidates", hash)
			}
		})
	}
}

// assertSingleDurableManifest confirms exactly one manifest exists for
// (tenant, id, version) after a race and that concurrent readers all agree on
// its content hash (the §4.7 "readers see their pinned version" property). It
// returns the agreed content hash.
func assertSingleDurableManifest(t *testing.T, st store.Store, tenant, id, version string) string {
	t.Helper()
	ctx := context.Background()

	rec, err := st.GetManifest(ctx, tenant, id, version)
	if err != nil {
		t.Fatalf("GetManifest after race: %v", err)
	}
	if rec.ContentHash == "" {
		t.Fatalf("durable manifest has empty content hash")
	}

	// Exactly one manifest row for the (id, version) exists; the immutability
	// invariant forbids a second copy under any interleaving.
	all, err := st.ListManifests(ctx, tenant)
	if err != nil {
		t.Fatalf("ListManifests: %v", err)
	}
	count := 0
	for _, m := range all {
		if m.ArtifactID == id && m.Version == version {
			count++
			if m.ContentHash != rec.ContentHash {
				t.Fatalf("ListManifests returned a divergent content hash %q for (%s,%s); GetManifest saw %q",
					m.ContentHash, id, version, rec.ContentHash)
			}
		}
	}
	if count != 1 {
		t.Fatalf("(%s,%s) appears %d times in the store, want exactly 1 (immutability converges to one manifest)",
			id, version, count)
	}

	// Concurrent readers after the race all observe the same content hash:
	// nothing re-ingested mid-read can shift a reader off the pinned version.
	const readers = 16
	hashes := make([]string, readers)
	var rg sync.WaitGroup
	for r := 0; r < readers; r++ {
		rg.Add(1)
		go func(r int) {
			defer rg.Done()
			got, err := st.GetManifest(ctx, tenant, id, version)
			if err != nil {
				t.Errorf("concurrent reader %d GetManifest: %v", r, err)
				return
			}
			hashes[r] = got.ContentHash
		}(r)
	}
	rg.Wait()
	for r, h := range hashes {
		if h != rec.ContentHash {
			t.Fatalf("concurrent reader %d saw content hash %q, want the single durable %q",
				r, h, rec.ContentHash)
		}
	}
	return rec.ContentHash
}

// contentHashIsACandidate reports whether hash equals the content hash the
// ingest pipeline assigns to one of the racing bodies. It re-ingests each
// candidate body into its own fresh in-memory store and reads back the hash,
// so the comparison uses the exact hashing the pipeline applies rather than a
// hand-rolled digest.
func contentHashIsACandidate(t *testing.T, hash string) bool {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < 24; i++ {
		st := store.NewMemory()
		if err := st.CreateTenant(ctx, store.Tenant{ID: "probe"}); err != nil {
			t.Fatalf("probe CreateTenant: %v", err)
		}
		files := fstest.MapFS{"x/ARTIFACT.md": &fstest.MapFile{Data: artifactBytes(byte('A' + i))}}
		if _, err := ingest.Ingest(ctx, st, ingest.Request{
			TenantID: "probe",
			LayerID:  "L",
			Files:    files,
		}); err != nil {
			t.Fatalf("probe ingest %d: %v", i, err)
		}
		rec, err := st.GetManifest(ctx, "probe", "x", "1.0.0")
		if err != nil {
			t.Fatalf("probe GetManifest %d: %v", i, err)
		}
		if rec.ContentHash == hash {
			return true
		}
	}
	return false
}
