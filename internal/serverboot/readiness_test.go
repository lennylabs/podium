package serverboot

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §13.9 — the metadata-store readiness check passes when
// GetTenant answers and fails when it errors (the store is unreachable or
// the tenant is missing), which is what makes /readyz report not_ready.
func TestStoreReadinessCheck(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default", Name: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	check := storeReadinessCheck(st, "default")
	if err := check(context.Background()); err != nil {
		t.Errorf("reachable store: check err = %v, want nil", err)
	}
	missing := storeReadinessCheck(st, "no-such-tenant")
	if err := missing(context.Background()); err == nil {
		t.Errorf("unreachable/missing tenant: check err = nil, want non-nil")
	}
}

// brokenObjectStore is an objectstore.Provider whose Get reports a
// transport-style outage (anything other than ErrNotFound).
type brokenObjectStore struct{ err error }

func (b brokenObjectStore) ID() string                                        { return "broken" }
func (b brokenObjectStore) Put(context.Context, string, []byte, string) error { return b.err }
func (b brokenObjectStore) Get(context.Context, string) ([]byte, error)       { return nil, b.err }
func (b brokenObjectStore) GetStream(context.Context, string) (io.ReadCloser, objectstore.ObjectInfo, error) {
	return nil, objectstore.ObjectInfo{}, b.err
}
func (b brokenObjectStore) Stat(context.Context, string) (objectstore.ObjectInfo, error) {
	return objectstore.ObjectInfo{}, b.err
}
func (b brokenObjectStore) Presign(context.Context, string, time.Duration) (string, error) {
	return "", b.err
}
func (b brokenObjectStore) Delete(context.Context, string) error { return b.err }

// Spec: §13.9 — the object-store readiness check treats
// ErrNotFound as reachable (the backend answered) and any other error as
// an outage.
func TestObjectStoreReadinessCheck(t *testing.T) {
	t.Parallel()

	// A reachable filesystem store: the sentinel key is absent, so Get
	// returns ErrNotFound, which the check counts as reachable.
	fs, err := objectstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("objectstore.Open: %v", err)
	}
	if err := objectStoreReadinessCheck(fs)(context.Background()); err != nil {
		t.Errorf("reachable object store: check err = %v, want nil", err)
	}

	// A backend that returns a transport error is an outage.
	outage := errors.New("s3: dial tcp: connection refused")
	if err := objectStoreReadinessCheck(brokenObjectStore{err: outage})(context.Background()); err == nil {
		t.Errorf("broken object store: check err = nil, want non-nil")
	}

	// ErrNotFound from any backend counts as reachable.
	notFound := objectStoreReadinessCheck(brokenObjectStore{err: objectstore.ErrNotFound})
	if err := notFound(context.Background()); err != nil {
		t.Errorf("ErrNotFound backend: check err = %v, want nil (reachable)", err)
	}
}

// Spec: §13.2.1 — only the Postgres backend reports
// replication lag; other backends report no reporter (nil), so /readyz
// reports lag 0.
func TestStoreLagReporter_NilForNonPostgres(t *testing.T) {
	t.Parallel()
	if got := storeLagReporter(store.NewMemory()); got != nil {
		t.Errorf("memory store lag reporter = non-nil, want nil")
	}
}
