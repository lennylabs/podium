package serverboot

import (
	"context"
	"errors"

	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/store"
)

// storeReadinessCheck returns the §13.9 metadata-store probe consulted
// by /readyz. It pings the read path with GetTenant; any error (the
// store is unreachable) makes /readyz report not_ready. This mirrors the
// background §13.2.1 read-only probe, which also treats any GetTenant
// error as a store failure.
func storeReadinessCheck(st store.Store, tenantID string) server.ReadinessCheck {
	return func(ctx context.Context) error {
		_, err := st.GetTenant(ctx, tenantID)
		return err
	}
}

// objectStoreReadinessSentinel is the key the §13.9 object-store probe
// reads to confirm the backend answers. The object never has to exist:
// ErrNotFound means the store responded (reachable); any other error is
// an outage that downgrades /readyz to not_ready.
const objectStoreReadinessSentinel = "_podium_readiness_probe"

// objectStoreReadinessCheck returns the §13.9 object-store probe
// consulted by /readyz. A read that returns ErrNotFound counts as
// reachable; a transport / credentials error counts as an outage.
func objectStoreReadinessCheck(p objectstore.Provider) server.ReadinessCheck {
	return func(ctx context.Context) error {
		_, err := p.Get(ctx, objectStoreReadinessSentinel)
		if err != nil && !errors.Is(err, objectstore.ErrNotFound) {
			return err
		}
		return nil
	}
}

// storeLagReporter returns the §13.2.1 replication-lag source for the
// store, or nil when the backend cannot report lag. Only the Postgres
// backend measures lag (against the replica's replay timestamp); every
// other backend, and a primary with no replica, reports 0.
func storeLagReporter(st store.Store) server.LagReporter {
	pg, ok := st.(*store.Postgres)
	if !ok {
		return nil
	}
	return func(ctx context.Context) int {
		n, err := pg.ReplicationLagSeconds(ctx)
		if err != nil {
			return 0
		}
		return n
	}
}
