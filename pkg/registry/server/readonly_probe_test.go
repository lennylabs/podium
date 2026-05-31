package server_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

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
// tracker flips to read_only; after the recovery threshold of
// consecutive successes it flips back to ready.
func TestReadOnlyProbe_FlipsAfterFailures(t *testing.T) {
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

	// Restore: consecutive successes (default recovery threshold) flip back.
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

// statefulStore models a primary that can be healthy, fully down, or
// intermittently reachable (flapping). In the flapping state it alternates
// success and failure so that no run of probe successes reaches the recovery
// threshold.
type statefulStore struct {
	*store.Memory
	state atomic.Int32 // 0 healthy, 1 down, 2 flapping
	calls atomic.Int64
}

const (
	storeHealthy  = 0
	storeDown     = 1
	storeFlapping = 2
)

func (f *statefulStore) GetTenant(ctx context.Context, id string) (store.Tenant, error) {
	switch f.state.Load() {
	case storeDown:
		return store.Tenant{}, errors.New("primary unreachable")
	case storeFlapping:
		// Odd calls fail, even calls succeed: at most one success in a row.
		if f.calls.Add(1)%2 == 1 {
			return store.Tenant{}, errors.New("intermittent outage")
		}
	}
	return f.Memory.GetTenant(ctx, id)
}

// Spec: §13.2.1 — the registry flips back to ready only "after three
// consecutive probe successes". An intermittently reachable primary
// (alternating success/failure) never reaches the threshold, so the
// registry stays in read_only rather than flapping; once the primary is
// stably reachable it recovers. F-13.2.4.
func TestReadOnlyProbe_RecoveryRequiresConsecutiveSuccesses(t *testing.T) {
	t.Parallel()
	mem := store.NewMemory()
	if err := mem.CreateTenant(context.Background(), store.Tenant{ID: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	st := &statefulStore{Memory: mem}
	st.state.Store(storeDown) // start in a full outage to enter read_only
	tracker := server.NewModeTracker()
	exits := atomic.Int64{}
	probe := &server.ReadOnlyProbe{
		Store:      st,
		Tracker:    tracker,
		TenantID:   "default",
		Interval:   15 * time.Millisecond,
		Failures:   2,
		Recoveries: 3,
		OnExit:     func() { exits.Add(1) },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go probe.Run(ctx)

	// Two consecutive failures flip the tracker to read_only.
	deadline := time.Now().Add(time.Second)
	for tracker.Get() != server.ModeReadOnly && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if tracker.Get() != server.ModeReadOnly {
		t.Fatalf("mode = %s, want read_only after outage", tracker.Get())
	}

	// Flapping: a lone success between failures never accumulates three in a
	// row, so the registry must not recover.
	st.state.Store(storeFlapping)
	time.Sleep(300 * time.Millisecond)
	if tracker.Get() != server.ModeReadOnly {
		t.Fatalf("mode = %s, want read_only to persist while flapping", tracker.Get())
	}
	if exits.Load() != 0 {
		t.Errorf("OnExit fired %d times while flapping; want 0", exits.Load())
	}

	// Once the primary is stably reachable, three consecutive successes
	// restore ready.
	st.state.Store(storeHealthy)
	deadline = time.Now().Add(time.Second)
	for tracker.Get() != server.ModeReady && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if tracker.Get() != server.ModeReady {
		t.Fatalf("mode = %s after stable recovery, want ready", tracker.Get())
	}
}

// Spec: §13.2.1 — probe is a no-op when not configured (no Store
// or Failures=0).
func TestReadOnlyProbe_NoOpWhenUnconfigured(t *testing.T) {
	t.Parallel()
	probe := &server.ReadOnlyProbe{}
	if err := probe.Run(context.Background()); err != nil {
		t.Errorf("Run: %v, want nil for unconfigured probe", err)
	}
}
