package verification

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §11 Soak — the full target is 24h of continuous load with no memory
// growth and no descriptor leaks. This scaled-down version runs continuous
// reads for a short window and asserts the goroutine count is stable across the
// run, the CI-runnable proxy for "no leak under sustained load". The 24h
// variant runs against a deployed instance.
func TestSoak_NoGoroutineLeakUnderContinuousLoad(t *testing.T) {
	reg := seedRegistry(t, store.NewMemory(), 200)
	id := layer.Identity{IsPublic: true}
	ctx := context.Background()

	dur := time.Second
	if testing.Short() {
		dur = 200 * time.Millisecond
	}

	runtime.GC()
	before := runtime.NumGoroutine()

	ops := 0
	deadline := time.Now().Add(dur)
	for time.Now().Before(deadline) {
		if _, err := reg.SearchArtifacts(ctx, id, core.SearchArtifactsOptions{Query: "ledgers", TopK: 5}); err != nil {
			t.Fatalf("soak search failed after %d ops: %v", ops, err)
		}
		ops++
	}

	// Let any transient per-request goroutines wind down before sampling.
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	after := runtime.NumGoroutine()
	t.Logf("soak: %d ops in %s, goroutines %d -> %d", ops, dur, before, after)
	if after > before+5 {
		t.Errorf("goroutine leak under continuous load: %d -> %d", before, after)
	}
}
