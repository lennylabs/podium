package integration

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §7.3.1 (Removed artifacts) — a newer cycle that removes an artifact
// and then fails lint has written, so it keeps its claim: the older cycle it
// displaced stays superseded, does not restore the removed artifact, and does
// not record its older reference.
func TestIngestCycles_LintFailedCycleKeepsItsClaim(t *testing.T) {
	for _, pair := range replicaPairs(t) {
		t.Run(pair.name, func(t *testing.T) {
			ctx := context.Background()
			stA, stB := pair.open(t)
			old := fstest.MapFS{
				"ops/gone/ARTIFACT.md": cycleOrderManifest("runbook for the ingest worker"),
				"ops/kept/ARTIFACT.md": cycleOrderManifest("checklist for the on-call handover"),
			}
			cfg := seedCycleLayer(t, stA, old)
			older := &cycleOrderProvider{files: old, ref: "r1", taken: make(chan struct{}), release: make(chan struct{})}
			done := make(chan cycleOutcome, 1)
			go func() {
				res, err := ingest.SourceIngestWithOptions(ctx, stA, older, cfg, ingest.SourceIngestOptions{CycleLocks: ingest.NewCycleLocks()})
				done <- cycleOutcome{res, err}
			}()
			<-older.taken
			// The newer snapshot deletes ops/gone, and ops/kept loses its
			// declared version, which lint refuses.
			newer := &cycleOrderProvider{files: fstest.MapFS{
				"ops/kept/ARTIFACT.md": &fstest.MapFile{Data: []byte("---\ntype: agent\ndescription: checklist for the on-call handover\nsensitivity: low\n---\n\nbody\n")},
			}, ref: "r2"}
			res, err := ingest.SourceIngestWithOptions(ctx, stB, newer, cfg, ingest.SourceIngestOptions{CycleLocks: ingest.NewCycleLocks()})
			if !errors.Is(err, ingest.ErrLintFailed) || res == nil || len(res.Removed) != 1 {
				t.Fatalf("newer cycle = %+v, %v; want a lint failure after removing ops/gone", res, err)
			}
			close(older.release)
			got := <-done
			if got.err != nil || !got.res.Superseded || got.res.Accepted != 0 || got.res.Idempotent != 0 {
				t.Errorf("older cycle = %+v, %v; want superseded before any write", got.res, got.err)
			}
			if _, err := stA.GetManifest(ctx, "t", "ops/gone", "1.0.0"); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("GetManifest(ops/gone) = %v, want it still removed", err)
			}
			if lc, err := stA.GetLayerConfig(ctx, "t", "team"); err != nil || lc.LastIngestedRef == "r1" {
				t.Errorf("last_ingested_ref = %q (%v), want the older reference unrecorded", lc.LastIngestedRef, err)
			}
		})
	}
}
