package integration

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// failingSnapshot is a source whose snapshot fails, as an unreachable remote
// does.
type failingSnapshot struct{}

func (failingSnapshot) ID() string                   { return "fake" }
func (failingSnapshot) Trigger() source.TriggerModel { return source.TriggerManual }
func (failingSnapshot) Snapshot(context.Context, source.LayerConfig) (*source.Snapshot, error) {
	return nil, source.ErrSourceUnreachable
}

// Spec: §7.3.1 (Removed artifacts), §4.7.2 — a cycle that a freeze window
// refuses is refused before it claims the layer, and a cycle that fails after
// claiming hands the claim back, so neither supersedes a concurrent cycle on
// another replica: that cycle stores its snapshot and records its reference.
func TestIngestCycles_RefusedOrFailedCycleSupersedesNothing(t *testing.T) {
	now := time.Now()
	freeze := []ingest.FreezeWindow{{Name: "release", Start: now.Add(-time.Hour), End: now.Add(time.Hour), Blocks: []string{"ingest"}}}
	cases := []struct {
		name    string
		other   func(ctx context.Context, st store.Store, cfg store.LayerConfig) error
		wantErr error
	}{
		{"frozen", func(ctx context.Context, st store.Store, cfg store.LayerConfig) error {
			_, err := ingest.SourceIngestWithOptions(ctx, st, &cycleOrderProvider{ref: "r9"}, cfg, ingest.SourceIngestOptions{
				CycleLocks: ingest.NewCycleLocks(), FreezeWindows: freeze,
			})
			return err
		}, ingest.ErrFrozen},
		{"source unreachable", func(ctx context.Context, st store.Store, cfg store.LayerConfig) error {
			_, err := ingest.SourceIngestWithOptions(ctx, st, failingSnapshot{}, cfg, ingest.SourceIngestOptions{CycleLocks: ingest.NewCycleLocks()})
			return err
		}, source.ErrSourceUnreachable},
		// Spec: §13.12 — a Git snapshot that exceeds PODIUM_GIT_FETCH_TIMEOUT
		// is a source-unreachable failure, so it hands the claim back too.
		{"git fetch deadline", func(ctx context.Context, st store.Store, cfg store.LayerConfig) error {
			_, err := ingest.SourceIngestWithOptions(ctx, st, newHangingGitSource(t), cfg, ingest.SourceIngestOptions{CycleLocks: ingest.NewCycleLocks()})
			return err
		}, source.ErrSourceUnreachable},
	}
	for _, tc := range cases {
		for _, pair := range replicaPairs(t) {
			t.Run(tc.name+"/"+pair.name, func(t *testing.T) {
				ctx := context.Background()
				stA, stB := pair.open(t)
				onlyA := fstest.MapFS{"a/ARTIFACT.md": cycleOrderManifest("runbook for the ingest worker")}
				cfg := seedCycleLayer(t, stA, onlyA)
				both := fstest.MapFS{
					"a/ARTIFACT.md": cycleOrderManifest("runbook for the ingest worker"),
					"b/ARTIFACT.md": cycleOrderManifest("checklist for the on-call handover"),
				}
				running := &cycleOrderProvider{files: both, ref: "r1", taken: make(chan struct{}), release: make(chan struct{})}
				done := make(chan cycleOutcome, 1)
				go func() {
					res, err := ingest.SourceIngestWithOptions(ctx, stA, running, cfg, ingest.SourceIngestOptions{CycleLocks: ingest.NewCycleLocks()})
					done <- cycleOutcome{res, err}
				}()
				<-running.taken
				if err := tc.other(ctx, stB, cfg); !errors.Is(err, tc.wantErr) {
					t.Fatalf("other cycle = %v, want %v", err, tc.wantErr)
				}
				close(running.release)
				got := <-done
				if got.err != nil || got.res.Superseded || got.res.Accepted != 1 {
					t.Errorf("running cycle = %+v, %v; want b accepted and no supersession", got.res, got.err)
				}
				if lc, err := stA.GetLayerConfig(ctx, "t", "team"); err != nil || lc.LastIngestedRef != "r1" {
					t.Errorf("last_ingested_ref = %q (%v), want r1 from the running cycle", lc.LastIngestedRef, err)
				}
			})
		}
	}
}
