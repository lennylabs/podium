package server_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// flakyStore wraps a Memory store and returns errors on demand.
type flakyStore struct {
	*store.Memory
	failTenant atomic.Bool
}

func (f *flakyStore) GetTenant(ctx context.Context, id string) (store.Tenant, error) {
	if f.failTenant.Load() {
		return store.Tenant{}, errors.New("simulated outage")
	}
	return f.Memory.GetTenant(ctx, id)
}

// Spec: §13.2.1 — after Failures consecutive probe failures, the
// tracker flips to read_only; the first success restores ready.
// Phase: 2
func TestReadOnlyProbe_FlipsAfterFailures(t *testing.T) {
	testharness.RequirePhase(t, 2)
	t.Parallel()
	mem := store.NewMemory()
	if err := mem.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	st := &flakyStore{Memory: mem}
	tracker := server.NewModeTracker()
	enters := atomic.Int64{}
	exits := atomic.Int64{}
	probe := &server.ReadOnlyProbe{
		Store:    st,
		Tracker:  tracker,
		TenantID: "default",
		Interval: 25 * time.Millisecond,
		Failures: 2,
		OnEnter:  func() { enters.Add(1) },
		OnExit:   func() { exits.Add(1) },
	}
	ctx, cancel := context.WithCancel(context.Background())
	go probe.Run(ctx)

	// Healthy: tracker stays Ready.
	time.Sleep(80 * time.Millisecond)
	if tracker.Get() != server.ModeReady {
		t.Errorf("mode = %s before failures, want ready", tracker.Get())
	}

	// Induce failures: tracker should flip after 2 ticks.
	st.failTenant.Store(true)
	deadline := time.Now().Add(time.Second)
	for tracker.Get() != server.ModeReadOnly && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if tracker.Get() != server.ModeReadOnly {
		t.Fatalf("mode = %s after failures, want read_only", tracker.Get())
	}
	if enters.Load() == 0 {
		t.Errorf("OnEnter not invoked")
	}

	// Restore: first success should flip back.
	st.failTenant.Store(false)
	deadline = time.Now().Add(time.Second)
	for tracker.Get() != server.ModeReady && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if tracker.Get() != server.ModeReady {
		t.Fatalf("mode = %s after recovery, want ready", tracker.Get())
	}
	if exits.Load() == 0 {
		t.Errorf("OnExit not invoked")
	}
	cancel()
}

// Spec: §13.2.1 — probe is a no-op when not configured (no Store
// or Failures=0).
// Phase: 2
func TestReadOnlyProbe_NoOpWhenUnconfigured(t *testing.T) {
	testharness.RequirePhase(t, 2)
	t.Parallel()
	probe := &server.ReadOnlyProbe{}
	if err := probe.Run(context.Background()); err != nil {
		t.Errorf("Run: %v, want nil for unconfigured probe", err)
	}
}
