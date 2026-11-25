package integration

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lennylabs/podium/pkg/layer/source"
	"github.com/lennylabs/podium/pkg/registry/ingest"
)

// testFetchTimeout is the PODIUM_GIT_FETCH_TIMEOUT value the deadline cases
// run under, short so the cases stay fast.
const testFetchTimeout = 300 * time.Millisecond

// hangingGitRemote starts an HTTP git remote that accepts each request and
// never answers it, and returns the repository URL.
func hangingGitRemote(t *testing.T) string {
	t.Helper()
	stop := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-stop:
		}
	}))
	// Cleanups run last-in first-out: the handlers return before Close waits
	// for them.
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(stop) })
	return srv.URL + "/acme/layer.git"
}

// hangingGitSource is the built-in Git provider under a short fetch deadline,
// pointed at a remote that never answers whatever the stored layer names, so
// a cycle of a layer seeded by another provider times out in its snapshot.
type hangingGitSource struct {
	git  source.Git
	repo string
}

func newHangingGitSource(t *testing.T) hangingGitSource {
	return hangingGitSource{git: source.Git{FetchTimeout: testFetchTimeout}, repo: hangingGitRemote(t)}
}

func (h hangingGitSource) ID() string                   { return h.git.ID() }
func (h hangingGitSource) Trigger() source.TriggerModel { return h.git.Trigger() }
func (h hangingGitSource) Snapshot(ctx context.Context, cfg source.LayerConfig) (*source.Snapshot, error) {
	cfg.Repo, cfg.Ref = h.repo, "main"
	return h.git.Snapshot(ctx, cfg)
}

// Spec: §7.3.1 (Source unreachable, Removed artifacts), §13.12 — a cycle whose
// Git snapshot exceeds PODIUM_GIT_FETCH_TIMEOUT fails as
// ingest.source_unreachable naming the setting. It removes nothing, the
// artifacts the layer served stay served, the layer keeps its last ingested
// reference, and the layer's next cycle runs normally.
func TestGitFetchDeadline_TimedOutCycleLeavesTheLayerServed(t *testing.T) {
	for _, pair := range replicaPairs(t) {
		t.Run(pair.name, func(t *testing.T) {
			ctx := context.Background()
			st, _ := pair.open(t)
			onlyA := fstest.MapFS{"a/ARTIFACT.md": cycleOrderManifest("runbook for the ingest worker")}
			cfg := seedCycleLayer(t, st, onlyA)

			start := time.Now()
			res, err := ingest.SourceIngestWithOptions(ctx, st, newHangingGitSource(t), cfg, ingest.SourceIngestOptions{CycleLocks: ingest.NewCycleLocks()})
			if !errors.Is(err, source.ErrSourceUnreachable) || !strings.Contains(err.Error(), source.FetchTimeoutSetting) {
				t.Fatalf("timed-out cycle = %+v, %v; want ingest.source_unreachable naming %s", res, err, source.FetchTimeoutSetting)
			}
			if elapsed := time.Since(start); elapsed > 10*time.Second {
				t.Errorf("timed-out cycle returned after %v, want shortly after %v", elapsed, testFetchTimeout)
			}
			if res != nil && len(res.Removed) != 0 {
				t.Errorf("timed-out cycle removed %v, want nothing", res.Removed)
			}
			if _, err := st.GetManifest(ctx, "t", "a", "1.0.0"); err != nil {
				t.Errorf("GetManifest(a) = %v, want a still served after the timed-out cycle", err)
			}
			if lc, err := st.GetLayerConfig(ctx, "t", "team"); err != nil || lc.LastIngestedRef != "r0" {
				t.Errorf("last_ingested_ref = %q (%v), want r0 kept", lc.LastIngestedRef, err)
			}

			next, err := ingest.SourceIngestWithOptions(ctx, st, &cycleOrderProvider{files: fstest.MapFS{
				"a/ARTIFACT.md": cycleOrderManifest("runbook for the ingest worker"),
				"b/ARTIFACT.md": cycleOrderManifest("checklist for the on-call handover"),
			}, ref: "r1"}, cfg, ingest.SourceIngestOptions{CycleLocks: ingest.NewCycleLocks()})
			if err != nil || next.Superseded || next.Accepted != 1 {
				t.Fatalf("next cycle = %+v, %v; want b accepted and no supersession", next, err)
			}
			if lc, err := st.GetLayerConfig(ctx, "t", "team"); err != nil || lc.LastIngestedRef != "r1" {
				t.Errorf("last_ingested_ref = %q (%v), want r1 from the next cycle", lc.LastIngestedRef, err)
			}
		})
	}
}
