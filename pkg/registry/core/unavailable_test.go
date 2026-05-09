package core_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// failingStore wraps a Memory store and forces ListManifests to
// return an error so we can exercise the registry.unavailable path.
type failingStore struct {
	store.Store
}

func (failingStore) ListManifests(_ context.Context, _ string) ([]store.ManifestRecord, error) {
	return nil, errors.New("simulated db outage")
}

// Spec: §6.10 — when the underlying store is unreachable, callers
// see registry.unavailable so they can route to the cache or surface
// staleness to the user (§7.4).
// Phase: 7
// Matrix: §6.10 (registry.unavailable)
func TestLoadDomain_StoreFailureMapsToUnavailable(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	reg := core.New(failingStore{Store: store.NewMemory()}, "t",
		[]layer.Layer{{ID: "L", Visibility: layer.Visibility{Public: true}}})
	_, err := reg.LoadDomain(context.Background(), publicID, "", core.LoadDomainOptions{})
	if !errors.Is(err, core.ErrUnavailable) {
		t.Errorf("got %v, want ErrUnavailable", err)
	}
}
