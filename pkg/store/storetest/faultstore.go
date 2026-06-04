package storetest

import (
	"context"
	"errors"
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

// GetTenant returns the injected error while the store is severed and
// otherwise forwards to the wrapped store. Every call is counted.
func (f *FaultStore) GetTenant(ctx context.Context, id string) (store.Tenant, error) {
	f.calls.Add(1)
	if f.severed.Load() {
		if f.Err != nil {
			return store.Tenant{}, f.Err
		}
		return store.Tenant{}, ErrSevered
	}
	return f.Store.GetTenant(ctx, id)
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
