package storetest

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lennylabs/podium/pkg/store"
)

// ErrSevered is the default error a severed FaultStore returns from its
// health call. It stands in for a metadata-store/primary outage: the
// §13.2.1 read-only probe and the §13.9 /readyz readiness check both
// ping GetTenant, so a non-nil return here trips both. Callers that want
// a specific message set FaultStore.Err.
var ErrSevered = errors.New("storetest: metadata store severed")

// FaultStore is a store.Store decorator that injects a fault into the
// metadata read path on demand. While severed, GetTenant returns an
// error; every other method forwards to the wrapped store unchanged.
//
// GetTenant is the health call the §13.2.1 read-only probe
// (ReadOnlyProbe.Run) and the §13.9 /readyz readiness check ping, so
// severing the store trips the real probe rather than forcing the mode
// with ModeTracker.Set. Ordinary search and load reads route through the
// registry core without calling GetTenant, so a severed store keeps
// serving reads while writes are refused with registry.read_only once
// the probe flips the tracker. This is the in-process inducement the
// read-only journeys need; a severable Postgres primary is the
// higher-fidelity variant.
//
// FaultStore embeds store.Store, so it satisfies the full interface
// through promotion and only overrides GetTenant. It does not re-expose
// optional capabilities the wrapped store reaches by type assertion (for
// example store.VectorOutbox); wrap a store whose extra capabilities the
// server path under test does not require, such as store.Memory.
type FaultStore struct {
	store.Store
	// Err is returned from GetTenant while severed. When nil, ErrSevered
	// is returned.
	Err error

	severed atomic.Bool
	// calls counts GetTenant invocations (severed or not) so a test can
	// confirm the probe is actually pinging the read path.
	calls atomic.Int64

	// failMethods names individual store methods that return the fault error
	// even when the global severed flag is clear, so a test can fail one
	// operation while the rest of the request path succeeds (for example fail
	// UpdateTenant while GetTenant and the operator check still work).
	failMu      sync.Mutex
	failMethods map[string]bool
}

// NewFaultStore wraps inner so its metadata health call can be severed on
// demand. The returned store starts healthy (not severed).
//
// When inner also implements store.VectorOutbox, callers that need the
// outbox to survive the wrap (for example an ingest path with
// UseVectorOutbox against an external backend) use NewFaultVectorStore
// instead; the bare *FaultStore deliberately does not re-expose the
// outbox so a SQLite or Memory inner store cannot masquerade as one.
func NewFaultStore(inner store.Store) *FaultStore {
	return &FaultStore{Store: inner}
}

// FaultVectorStore is a FaultStore whose wrapped store also implements
// store.VectorOutbox; it forwards the outbox methods so the capability
// survives the decorator and a type assertion to store.VectorOutbox
// still succeeds.
type FaultVectorStore struct {
	*FaultStore
	outbox store.VectorOutbox
}

// NewFaultVectorStore wraps inner and preserves its store.VectorOutbox
// capability through the decorator. inner must implement both store.Store
// and store.VectorOutbox.
func NewFaultVectorStore(inner interface {
	store.Store
	store.VectorOutbox
}) *FaultVectorStore {
	return &FaultVectorStore{FaultStore: NewFaultStore(inner), outbox: inner}
}

func (f *FaultVectorStore) PutManifestWithVectorPending(ctx context.Context, rec store.ManifestRecord, pending store.VectorPending) error {
	return f.outbox.PutManifestWithVectorPending(ctx, rec, pending)
}

func (f *FaultVectorStore) ListVectorPending(ctx context.Context, limit int, now time.Time) ([]store.VectorPending, error) {
	return f.outbox.ListVectorPending(ctx, limit, now)
}

func (f *FaultVectorStore) MarkVectorPendingDone(ctx context.Context, tenantID, artifactID, version string) error {
	return f.outbox.MarkVectorPendingDone(ctx, tenantID, artifactID, version)
}

func (f *FaultVectorStore) MarkVectorPendingRetry(ctx context.Context, tenantID, artifactID, version string, nextRetryAt time.Time, errMsg string) error {
	return f.outbox.MarkVectorPendingRetry(ctx, tenantID, artifactID, version, nextRetryAt, errMsg)
}

func (f *FaultVectorStore) VectorOutboxStats(ctx context.Context) (int, time.Time, error) {
	return f.outbox.VectorOutboxStats(ctx)
}

// faultErr returns the configured fault error, or ErrSevered when none is set.
func (f *FaultStore) faultErr() error {
	if f.Err != nil {
		return f.Err
	}
	return ErrSevered
}

// shouldFail reports whether method must return the fault error: either the
// global severed flag is set, or the method was named to FailMethod.
func (f *FaultStore) shouldFail(method string) bool {
	if f.severed.Load() {
		return true
	}
	f.failMu.Lock()
	defer f.failMu.Unlock()
	return f.failMethods[method]
}

// FailMethod makes the named store method return the fault error on every call,
// independent of the global Sever() flag, so a test can exercise one method's
// error branch while the rest of the request path succeeds.
func (f *FaultStore) FailMethod(method string) {
	f.failMu.Lock()
	defer f.failMu.Unlock()
	if f.failMethods == nil {
		f.failMethods = map[string]bool{}
	}
	f.failMethods[method] = true
}

// GetTenant returns the injected error while the store is severed (or GetTenant
// is named to FailMethod) and otherwise forwards. Every call is counted.
func (f *FaultStore) GetTenant(ctx context.Context, id string) (store.Tenant, error) {
	f.calls.Add(1)
	if f.shouldFail("GetTenant") {
		return store.Tenant{}, f.faultErr()
	}
	return f.Store.GetTenant(ctx, id)
}

// CreateTenant returns the injected error while severed or named, else forwards.
func (f *FaultStore) CreateTenant(ctx context.Context, t store.Tenant) error {
	if f.shouldFail("CreateTenant") {
		return f.faultErr()
	}
	return f.Store.CreateTenant(ctx, t)
}

// ListTenants returns the injected error while severed or named, else forwards.
func (f *FaultStore) ListTenants(ctx context.Context) ([]store.Tenant, error) {
	if f.shouldFail("ListTenants") {
		return nil, f.faultErr()
	}
	return f.Store.ListTenants(ctx)
}

// UpdateTenant returns the injected error while severed or named, else forwards.
func (f *FaultStore) UpdateTenant(ctx context.Context, t store.Tenant) error {
	if f.shouldFail("UpdateTenant") {
		return f.faultErr()
	}
	return f.Store.UpdateTenant(ctx, t)
}

// DeactivateTenant returns the injected error while severed or named, else
// forwards.
func (f *FaultStore) DeactivateTenant(ctx context.Context, id string) error {
	if f.shouldFail("DeactivateTenant") {
		return f.faultErr()
	}
	return f.Store.DeactivateTenant(ctx, id)
}

// IsOperator returns the injected error while severed or named, else forwards.
func (f *FaultStore) IsOperator(ctx context.Context, identity string) (bool, error) {
	if f.shouldFail("IsOperator") {
		return false, f.faultErr()
	}
	return f.Store.IsOperator(ctx, identity)
}

// Sever makes the health call fail, simulating a metadata-store outage.
func (f *FaultStore) Sever() { f.severed.Store(true) }

// Restore clears the fault so the health call succeeds again, simulating
// the primary becoming reachable.
func (f *FaultStore) Restore() { f.severed.Store(false) }

// Severed reports whether the store is currently injecting the fault.
func (f *FaultStore) Severed() bool { return f.severed.Load() }

// HealthCalls returns the number of GetTenant invocations observed so
// far, so a test can assert the probe is pinging the read path on its
// tick.
func (f *FaultStore) HealthCalls() int64 { return f.calls.Load() }
