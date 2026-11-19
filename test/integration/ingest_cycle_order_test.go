package integration

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// cycleOrderProvider is a source provider whose snapshot holds files at
// reference ref. When taken is non-nil, Snapshot signals it after taking the
// snapshot and then waits for release, so a test can complete another cycle
// between this cycle's snapshot and the rest of its run.
type cycleOrderProvider struct {
	files   fs.FS
	ref     string
	taken   chan struct{}
	release chan struct{}
}

func (p *cycleOrderProvider) ID() string                   { return "fake" }
func (p *cycleOrderProvider) Trigger() source.TriggerModel { return source.TriggerManual }
func (p *cycleOrderProvider) Snapshot(ctx context.Context, _ source.LayerConfig) (*source.Snapshot, error) {
	snap := &source.Snapshot{Reference: p.ref, Files: p.files, CreatedAt: time.Now().UTC()}
	if p.taken != nil {
		close(p.taken)
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return snap, nil
}

// cycleOrderManifest is an agent ARTIFACT.md the cycle-order cases ingest.
func cycleOrderManifest(desc string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte("---\ntype: agent\nversion: 1.0.0\ndescription: " + desc +
		"\nsensitivity: low\n---\n\nbody\n")}
}

// replicaPair opens two handles on one backing store, as two registry
// replicas hold. The memory store has no second handle, so both are one.
type replicaPair struct {
	name string
	open func(t *testing.T) (store.Store, store.Store)
}

func replicaPairs(t *testing.T) []replicaPair {
	t.Helper()
	pairs := []replicaPair{
		{name: "memory", open: func(t *testing.T) (store.Store, store.Store) {
			st := store.NewMemory()
			return st, st
		}},
		{name: "sqlite", open: func(t *testing.T) (store.Store, store.Store) {
			path := filepath.Join(t.TempDir(), "registry.db")
			open := func() store.Store {
				st, err := store.OpenSQLite(path)
				if err != nil {
					t.Fatalf("OpenSQLite: %v", err)
				}
				t.Cleanup(func() { _ = st.Close() })
				return st
			}
			return open(), open()
		}},
	}
	if dsn := os.Getenv("PODIUM_POSTGRES_DSN"); dsn != "" {
		pairs = append(pairs, replicaPair{name: "postgres", open: func(t *testing.T) (store.Store, store.Store) {
			lockPostgresReset(t)
			open := func() *store.Postgres {
				pg, err := store.OpenPostgres(dsn)
				if err != nil {
					t.Skipf("OpenPostgres: %v (database unreachable)", err)
				}
				t.Cleanup(func() { _ = pg.Close() })
				return pg
			}
			a := open()
			if err := a.ResetForTest(context.Background()); err != nil {
				t.Fatalf("ResetForTest: %v", err)
			}
			return a, open()
		}})
	}
	return pairs
}

// Spec: §7.3.1 (Removed artifacts) — a cycle on one replica whose snapshot
// predates a cycle another replica completes in the meantime removes nothing
// and records neither its reference nor its time, so the artifact the newer
// snapshot added stays served and the layer keeps the newer reference. Each
// replica has its own cycle locks, as separate processes do, so only the
// store's ingest-mark check keeps the cycles apart.
func TestIngestCycles_OlderSnapshotRemovesNothingAcrossReplicas(t *testing.T) {
	for _, pair := range replicaPairs(t) {
		t.Run(pair.name, func(t *testing.T) {
			ctx := context.Background()
			stA, stB := pair.open(t)
			if err := stA.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
				t.Fatal(err)
			}
			cfg := store.LayerConfig{TenantID: "t", ID: "team", SourceType: "fake"}
			if err := stA.PutLayerConfig(ctx, cfg); err != nil {
				t.Fatal(err)
			}
			base := &cycleOrderProvider{files: fstest.MapFS{"a/ARTIFACT.md": cycleOrderManifest("runbook for the ingest worker")}, ref: "r0"}
			if _, err := ingest.SourceIngestWithOptions(ctx, stA, base, cfg, ingest.SourceIngestOptions{}); err != nil {
				t.Fatalf("baseline cycle: %v", err)
			}

			// Replica A snapshots the source before b is added, then pauses.
			older := &cycleOrderProvider{
				files: fstest.MapFS{"a/ARTIFACT.md": cycleOrderManifest("runbook for the ingest worker")},
				ref:   "r1", taken: make(chan struct{}), release: make(chan struct{}),
			}
			type outcome struct {
				res *ingest.Result
				err error
			}
			done := make(chan outcome, 1)
			go func() {
				res, err := ingest.SourceIngestWithOptions(ctx, stA, older, cfg, ingest.SourceIngestOptions{CycleLocks: ingest.NewCycleLocks()})
				done <- outcome{res, err}
			}()
			<-older.taken

			// Replica B ingests the newer snapshot, which adds b, and completes.
			newer := &cycleOrderProvider{files: fstest.MapFS{
				"a/ARTIFACT.md": cycleOrderManifest("runbook for the ingest worker"),
				"b/ARTIFACT.md": cycleOrderManifest("checklist for the on-call handover"),
			}, ref: "r2"}
			if _, err := ingest.SourceIngestWithOptions(ctx, stB, newer, cfg, ingest.SourceIngestOptions{CycleLocks: ingest.NewCycleLocks()}); err != nil {
				t.Fatalf("newer cycle: %v", err)
			}

			close(older.release)
			got := <-done
			if got.err != nil {
				t.Fatalf("older cycle: %v", got.err)
			}
			if !got.res.Superseded || len(got.res.Removed) != 0 {
				t.Errorf("older cycle: Superseded=%v Removed=%v, want superseded with nothing removed", got.res.Superseded, got.res.Removed)
			}
			if _, err := stA.GetManifest(ctx, "t", "b", "1.0.0"); err != nil {
				t.Errorf("GetManifest(b) after the older cycle = %v, want the artifact the newer snapshot added still served", err)
			}
			layerCfg, err := stA.GetLayerConfig(ctx, "t", "team")
			if err != nil {
				t.Fatal(err)
			}
			if layerCfg.LastIngestedRef != "r2" {
				t.Errorf("last_ingested_ref = %q, want r2 from the newer cycle", layerCfg.LastIngestedRef)
			}

			// The next cycle reads the newer mark and removes normally.
			gone := &cycleOrderProvider{files: fstest.MapFS{"b/ARTIFACT.md": cycleOrderManifest("checklist for the on-call handover")}, ref: "r3"}
			res, err := ingest.SourceIngestWithOptions(ctx, stB, gone, cfg, ingest.SourceIngestOptions{})
			if err != nil || res.Superseded || len(res.Removed) != 1 || res.Removed[0] != "a" {
				t.Errorf("next cycle = %+v, %v; want a removed", res, err)
			}
			if _, err := stA.GetManifest(ctx, "t", "a", "1.0.0"); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("GetManifest(a) after its removal = %v, want ErrNotFound", err)
			}
		})
	}
}
