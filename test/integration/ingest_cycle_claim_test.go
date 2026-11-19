package integration

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// cycleOutcome is what one ingest cycle returned.
type cycleOutcome struct {
	res *ingest.Result
	err error
}

// seedCycleLayer creates tenant t and layer team on st and runs a first
// cycle over files at reference r0.
func seedCycleLayer(t *testing.T, st store.Store, files fstest.MapFS) store.LayerConfig {
	t.Helper()
	ctx := context.Background()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatal(err)
	}
	cfg := store.LayerConfig{TenantID: "t", ID: "team", SourceType: "fake", Order: 1}
	if err := st.PutLayerConfig(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := ingest.SourceIngestWithOptions(ctx, st, &cycleOrderProvider{files: files, ref: "r0"}, cfg, ingest.SourceIngestOptions{}); err != nil {
		t.Fatalf("first cycle: %v", err)
	}
	return cfg
}

// Spec: §7.3.1 (Removed artifacts) — a newer cycle on one replica that has
// passed its removal check but not yet recorded its reference still holds the
// latest claim, so an older cycle on another replica that reaches its checks
// meanwhile stops and removes nothing, and the newer cycle records its
// reference.
func TestIngestCycles_OlderCycleRemovesNothingWhileANewerCycleRecords(t *testing.T) {
	for _, pair := range replicaPairs(t) {
		t.Run(pair.name, func(t *testing.T) {
			ctx := context.Background()
			stA, stB := pair.open(t)
			onlyA := fstest.MapFS{"a/ARTIFACT.md": cycleOrderManifest("runbook for the ingest worker")}
			cfg := seedCycleLayer(t, stA, onlyA)

			older := &cycleOrderProvider{files: onlyA, ref: "r1", taken: make(chan struct{}), release: make(chan struct{})}
			doneA := make(chan cycleOutcome, 1)
			go func() {
				res, err := ingest.SourceIngestWithOptions(ctx, stA, older, cfg, ingest.SourceIngestOptions{CycleLocks: ingest.NewCycleLocks()})
				doneA <- cycleOutcome{res, err}
			}()
			<-older.taken

			// The newer cycle pauses while it publishes layer.ingested, after
			// its removal check and before it records r2.
			inWindow, resumeB := make(chan struct{}), make(chan struct{})
			newer := &cycleOrderProvider{files: fstest.MapFS{
				"a/ARTIFACT.md": cycleOrderManifest("runbook for the ingest worker"),
				"b/ARTIFACT.md": cycleOrderManifest("checklist for the on-call handover"),
			}, ref: "r2"}
			doneB := make(chan cycleOutcome, 1)
			go func() {
				res, err := ingest.SourceIngestWithOptions(ctx, stB, newer, cfg, ingest.SourceIngestOptions{
					CycleLocks: ingest.NewCycleLocks(),
					PublishEvent: func(_ context.Context, _ core.EventScope, ev string, _ map[string]any) {
						if ev == "layer.ingested" {
							close(inWindow)
							<-resumeB
						}
					},
				})
				doneB <- cycleOutcome{res, err}
			}()
			<-inWindow
			close(older.release)
			a := <-doneA
			close(resumeB)
			b := <-doneB
			if a.err != nil || !a.res.Superseded || len(a.res.Removed) != 0 {
				t.Errorf("older cycle = %+v, %v; want superseded with nothing removed", a.res, a.err)
			}
			if b.err != nil || b.res.Superseded {
				t.Errorf("newer cycle = %+v, %v; want completed", b.res, b.err)
			}
			if _, err := stA.GetManifest(ctx, "t", "b", "1.0.0"); err != nil {
				t.Errorf("GetManifest(b) = %v, want b served: the newer snapshot holds it", err)
			}
			if lc, err := stA.GetLayerConfig(ctx, "t", "team"); err != nil || lc.LastIngestedRef != "r2" {
				t.Errorf("last_ingested_ref = %q (%v), want r2", lc.LastIngestedRef, err)
			}
		})
	}
}

// Spec: §7.3.1 (Removed artifacts) — an older cycle that a newer cycle
// superseded before the older cycle's first write stores nothing, so it does
// not restore the version the newer cycle removed.
func TestIngestCycles_SupersededCycleRestoresNothing(t *testing.T) {
	for _, pair := range replicaPairs(t) {
		t.Run(pair.name, func(t *testing.T) {
			ctx := context.Background()
			stA, stB := pair.open(t)
			both := fstest.MapFS{
				"a/ARTIFACT.md": cycleOrderManifest("runbook for the ingest worker"),
				"b/ARTIFACT.md": cycleOrderManifest("checklist for the on-call handover"),
			}
			cfg := seedCycleLayer(t, stA, both)
			older := &cycleOrderProvider{files: both, ref: "r1", taken: make(chan struct{}), release: make(chan struct{})}
			doneA := make(chan cycleOutcome, 1)
			go func() {
				res, err := ingest.SourceIngestWithOptions(ctx, stA, older, cfg, ingest.SourceIngestOptions{CycleLocks: ingest.NewCycleLocks()})
				doneA <- cycleOutcome{res, err}
			}()
			<-older.taken
			// The newer snapshot no longer holds b.
			onlyA := fstest.MapFS{"a/ARTIFACT.md": cycleOrderManifest("runbook for the ingest worker")}
			res, err := ingest.SourceIngestWithOptions(ctx, stB, &cycleOrderProvider{files: onlyA, ref: "r2"}, cfg, ingest.SourceIngestOptions{CycleLocks: ingest.NewCycleLocks()})
			if err != nil || len(res.Removed) != 1 {
				t.Fatalf("newer cycle = %+v, %v; want b removed", res, err)
			}
			close(older.release)
			a := <-doneA
			if a.err != nil || !a.res.Superseded || a.res.Accepted != 0 || a.res.Idempotent != 0 {
				t.Errorf("older cycle = %+v, %v; want superseded before any write", a.res, a.err)
			}
			if _, err := stA.GetManifest(ctx, "t", "b", "1.0.0"); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("GetManifest(b) = %v, want b still removed", err)
			}
		})
	}
}

// Spec: §7.3.1 (Removed artifacts), §8.4 — a cycle whose layer is
// unregistered while the cycle waits for the layer's lock runs no ingest: it
// restores none of the layer's artifacts and leaves the layer unregistered,
// and the cycle that held the lock when the unregister landed stops at its
// next check.
func TestIngestCycles_WaitingCycleAfterUnregisterRunsNothing(t *testing.T) {
	for _, pair := range replicaPairs(t) {
		t.Run(pair.name, func(t *testing.T) {
			ctx := context.Background()
			st, _ := pair.open(t)
			files := fstest.MapFS{"a/ARTIFACT.md": cycleOrderManifest("runbook for the ingest worker")}
			cfg := seedCycleLayer(t, st, files)
			locks := ingest.NewCycleLocks()
			first := &cycleOrderProvider{files: files, ref: "r1", taken: make(chan struct{}), release: make(chan struct{})}
			d1 := make(chan cycleOutcome, 1)
			go func() {
				res, err := ingest.SourceIngestWithOptions(ctx, st, first, cfg, ingest.SourceIngestOptions{CycleLocks: locks})
				d1 <- cycleOutcome{res, err}
			}()
			<-first.taken
			d2 := make(chan cycleOutcome, 1)
			go func() {
				res, err := ingest.SourceIngestWithOptions(ctx, st, &cycleOrderProvider{files: files, ref: "r2"}, cfg, ingest.SourceIngestOptions{CycleLocks: locks})
				d2 <- cycleOutcome{res, err}
			}()
			// Give the second cycle time to reach the lock; the assertions hold
			// whether or not it got there first.
			time.Sleep(50 * time.Millisecond)
			if err := st.DeleteLayerConfig(ctx, "t", "team"); err != nil {
				t.Fatal(err)
			}
			close(first.release)
			for _, o := range []cycleOutcome{<-d1, <-d2} {
				if o.err != nil || !o.res.Superseded {
					t.Errorf("cycle = %+v, %v; want superseded by the unregister", o.res, o.err)
				}
			}
			if _, err := st.GetLayerConfig(ctx, "t", "team"); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("GetLayerConfig after the unregister = %v, want the layer still unregistered", err)
			}
			if _, err := st.GetManifest(ctx, "t", "a", "1.0.0"); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("GetManifest(a) = %v, want the unregistered layer's artifact still removed", err)
			}
		})
	}
}

// Spec: §7.3.1 (Removed artifacts) — a layer update made while cycles of the
// layer run or wait stands: a cycle records only its ingest reference and
// time, so it reverts neither a visibility change, nor the order, nor a
// rotated webhook secret.
func TestIngestCycles_CycleKeepsALayerUpdate(t *testing.T) {
	for _, pair := range replicaPairs(t) {
		t.Run(pair.name, func(t *testing.T) {
			ctx := context.Background()
			st, _ := pair.open(t)
			files := fstest.MapFS{"a/ARTIFACT.md": cycleOrderManifest("runbook for the ingest worker")}
			cfg := seedCycleLayer(t, st, files)
			locks := ingest.NewCycleLocks()
			first := &cycleOrderProvider{files: files, ref: "r1", taken: make(chan struct{}), release: make(chan struct{})}
			d1 := make(chan error, 1)
			go func() {
				_, err := ingest.SourceIngestWithOptions(ctx, st, first, cfg, ingest.SourceIngestOptions{CycleLocks: locks})
				d1 <- err
			}()
			<-first.taken
			d2 := make(chan error, 1)
			go func() {
				_, err := ingest.SourceIngestWithOptions(ctx, st, &cycleOrderProvider{files: files, ref: "r2"}, cfg, ingest.SourceIngestOptions{CycleLocks: locks})
				d2 <- err
			}()
			time.Sleep(50 * time.Millisecond)
			upd, err := st.GetLayerConfig(ctx, "t", "team")
			if err != nil {
				t.Fatal(err)
			}
			upd.Public, upd.Order, upd.WebhookSecret = true, 5, "rotated"
			if err := st.PutLayerConfig(ctx, upd); err != nil {
				t.Fatal(err)
			}
			close(first.release)
			if err := <-d1; err != nil {
				t.Fatal(err)
			}
			if err := <-d2; err != nil {
				t.Fatal(err)
			}
			got, err := st.GetLayerConfig(ctx, "t", "team")
			if err != nil {
				t.Fatal(err)
			}
			if !got.Public || got.Order != 5 || got.WebhookSecret != "rotated" || got.LastIngestedRef != "r2" {
				t.Errorf("layer after the cycles: public=%v order=%d secret rotated=%v ref=%q; want the update kept and r2 recorded",
					got.Public, got.Order, got.WebhookSecret == "rotated", got.LastIngestedRef)
			}
		})
	}
}
