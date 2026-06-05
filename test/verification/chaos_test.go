package verification

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// chaosStore wraps a store and injects a backend outage on demand. It embeds
// store.Store so it satisfies the interface; only the read path the meta-tools
// exercise (ListManifests) is faulted.
type chaosStore struct {
	store.Store
	down atomic.Bool
}

func (c *chaosStore) ListManifests(ctx context.Context, tenantID string) ([]store.ManifestRecord, error) {
	if c.down.Load() {
		return nil, errors.New("chaos: metadata store unavailable")
	}
	return c.Store.ListManifests(ctx, tenantID)
}

// Spec: §11 Chaos — the full target injects Postgres failover, object-storage
// stalls, network partitions, IdP outage, and full-disk against a deployed
// cluster. This scaled-down version faults the metadata store and asserts the
// read path degrades gracefully: a meta-tool call returns a structured error
// rather than panicking, and the registry recovers when the backend returns.
func TestChaos_StoreOutageDegradesGracefully(t *testing.T) {
	cs := &chaosStore{Store: store.NewMemory()}
	reg := seedRegistry(t, cs, 50)
	id := layer.Identity{IsPublic: true}
	ctx := context.Background()
	search := func() error {
		_, err := reg.SearchArtifacts(ctx, id, core.SearchArtifactsOptions{Query: "variance", TopK: 5})
		return err
	}

	if err := search(); err != nil {
		t.Fatalf("healthy search failed: %v", err)
	}

	// Induce the outage: the call must return an error, never panic.
	cs.down.Store(true)
	if err := search(); err == nil {
		t.Error("search returned no error during the store outage")
	}

	// Recover: the registry serves again once the backend returns.
	cs.down.Store(false)
	if err := search(); err != nil {
		t.Errorf("search did not recover after the outage cleared: %v", err)
	}
}
