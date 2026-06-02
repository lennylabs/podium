package verification

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §11 / §7.1 Performance — the full target is 1K QPS sustained for
// search_artifacts with load_artifact p99 under the SLO, measured against a
// deployed cluster. This scaled-down version drives concurrent reads in process
// to confirm the registry serves them without error or data race (run under
// -race) and applies a generous p99 ceiling that catches a catastrophic
// regression rather than enforcing the SLO number.
func TestPerformance_ConcurrentReadsSucceed(t *testing.T) {
	t.Parallel()
	reg := seedRegistry(t, store.NewMemory(), 1000)
	id := layer.Identity{IsPublic: true}
	ctx := context.Background()

	const workers = 16
	perWorker := 100
	if testing.Short() {
		perWorker = 20
	}

	latencies := make([][]time.Duration, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	start := time.Now()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			lat := make([]time.Duration, 0, perWorker)
			for i := 0; i < perWorker; i++ {
				t0 := time.Now()
				if _, err := reg.SearchArtifacts(ctx, id, core.SearchArtifactsOptions{Query: "variance", TopK: 10}); err != nil {
					errs[w] = err
					return
				}
				lat = append(lat, time.Since(t0))
			}
			latencies[w] = lat
		}(w)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("concurrent search failed: %v", err)
		}
	}

	var all []time.Duration
	for _, l := range latencies {
		all = append(all, l...)
	}
	p99 := percentile(all, 0.99)
	t.Logf("performance: %d search ops across %d workers in %s, p99=%s", len(all), workers, time.Since(start), p99)
	if p99 > 2*time.Second {
		t.Errorf("search p99 %s exceeds the 2s catastrophic-regression ceiling", p99)
	}
}

// percentile returns the p-quantile (0..1) of ds by nearest-rank. Empty input
// returns zero.
func percentile(ds []time.Duration, p float64) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), ds...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(p * float64(len(sorted)))
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
