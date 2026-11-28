package core_test

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// failingLayerReads fails ListLayerConfigs while fail is set, as a store
// whose layer table cannot be read does.
type failingLayerReads struct {
	store.Store
	fail atomic.Bool
}

func (s *failingLayerReads) ListLayerConfigs(ctx context.Context, tenantID string) ([]store.LayerConfig, error) {
	if s.fail.Load() {
		return nil, errors.New("layer table unavailable")
	}
	return s.Store.ListLayerConfigs(ctx, tenantID)
}

// fallbackFixture is a store with two tenants: acme, which owns the
// bootstrap layer team with one artifact, and globex, with the layer ops and
// one artifact.
func fallbackFixture(t *testing.T) *failingLayerReads {
	t.Helper()
	ctx := context.Background()
	st := &failingLayerReads{Store: store.NewMemory()}
	for _, tn := range []string{"acme", "globex"} {
		if err := st.CreateTenant(ctx, store.Tenant{ID: tn}); err != nil {
			t.Fatal(err)
		}
	}
	mustPutLayer(t, st, store.LayerConfig{TenantID: "acme", ID: "team", Order: 1, Organization: true})
	mustPutLayer(t, st, store.LayerConfig{TenantID: "globex", ID: "ops", Order: 1, Organization: true})
	mustPutManifest(t, st, store.ManifestRecord{TenantID: "acme", ArtifactID: "team-x", Version: "1.0.0", ContentHash: "sha256:a", Type: "agent", Layer: "team"})
	mustPutManifest(t, st, store.ManifestRecord{TenantID: "globex", ArtifactID: "ops-x", Version: "1.0.0", ContentHash: "sha256:b", Type: "agent", Layer: "ops"})
	return st
}

// searchIn searches as id in tenant.
func searchIn(t *testing.T, reg *core.Registry, tenant string, id layer.Identity) []string {
	t.Helper()
	res, err := reg.SearchArtifacts(core.ContextWithTenant(context.Background(), tenant), id, core.SearchArtifactsOptions{})
	if err != nil {
		t.Fatalf("SearchArtifacts in %s: %v", tenant, err)
	}
	ids := []string{}
	for _, r := range res.Results {
		ids = append(ids, r.ID)
	}
	return ids
}

// TestLayerFallback_FollowsTheLastSuccessfulRead covers the fallback list of
// a store that keeps layer rows. A read that cannot read the rows uses the
// list of the tenant's last successful read, so a row a cycle narrowed after
// the start stays narrowed. A tenant whose first read fails sees no layer,
// and a tenant's list holds none of another tenant's layers. A successful
// read that returns no rows is authoritative, so a tenant whose last layer
// was unregistered shows no layer, whether the next read succeeds or fails.
//
// Spec: §4.6 (Fallback list)
func TestLayerFallback_FollowsTheLastSuccessfulRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := fallbackFixture(t)
	reg, err := core.New(st, "acme", []layer.Layer{{ID: "team", Precedence: 1, Visibility: layer.Visibility{Public: true}}}).WithLayerRows(ctx, "acme")
	if err != nil {
		t.Fatal(err)
	}
	bob := layer.Identity{IsAuthenticated: true, Sub: "bob"}

	// Before any read of its own, the owning tenant falls back to its rows as
	// the start read them, which admit bob through organization; the
	// boot-time list, which says public, is not read.
	st.fail.Store(true)
	if got := searchIn(t, reg, "acme", bob); !slices.Equal(got, []string{"team-x"}) {
		t.Fatalf("acme fallback before a read = %v", got)
	}
	if got := searchIn(t, reg, "acme", layer.Identity{}); len(got) != 0 {
		t.Fatalf("anonymous acme fallback = %v; the boot-time public list leaked", got)
	}
	// Another tenant whose first read fails sees no layer.
	if got := searchIn(t, reg, "globex", bob); len(got) != 0 {
		t.Fatalf("globex first read failed and showed %v", got)
	}

	// A cycle narrows team to a group; the next successful read takes it,
	// and a failed read after it keeps the narrowed row.
	st.fail.Store(false)
	mustPutLayer(t, st, store.LayerConfig{TenantID: "acme", ID: "team", Order: 1, Groups: []string{"acme-finance"}})
	if got := searchIn(t, reg, "acme", bob); len(got) != 0 {
		t.Fatalf("acme after the narrowing = %v", got)
	}
	if got := searchIn(t, reg, "globex", bob); !slices.Equal(got, []string{"ops-x"}) {
		t.Fatalf("globex read = %v", got)
	}
	st.fail.Store(true)
	if got := searchIn(t, reg, "acme", bob); len(got) != 0 {
		t.Fatalf("acme fallback after the narrowing = %v", got)
	}
	carol := layer.Identity{IsAuthenticated: true, Sub: "carol", Groups: []string{"acme-finance"}}
	if got := searchIn(t, reg, "acme", carol); !slices.Equal(got, []string{"team-x"}) {
		t.Fatalf("acme fallback for a group member = %v", got)
	}
	// globex's list holds its own layer alone.
	if got := searchIn(t, reg, "globex", bob); !slices.Equal(got, []string{"ops-x"}) {
		t.Fatalf("globex fallback = %v", got)
	}

	// The last layer of globex is unregistered: a successful read returns no
	// rows, which shows no layer, and a failed read after it shows none.
	st.fail.Store(false)
	if err := st.DeleteLayerConfig(ctx, "globex", "ops"); err != nil {
		t.Fatal(err)
	}
	mustPutManifest(t, st, store.ManifestRecord{TenantID: "globex", ArtifactID: "stray", Version: "1.0.0", ContentHash: "sha256:c", Type: "agent", Layer: "ops"})
	if got := searchIn(t, reg, "globex", bob); len(got) != 0 {
		t.Fatalf("globex with no layer = %v", got)
	}
	st.fail.Store(true)
	if got := searchIn(t, reg, "globex", bob); len(got) != 0 {
		t.Fatalf("globex fallback with no layer = %v", got)
	}
}

// TestLayerFallback_StoreWithoutLayerRows covers a registry that never
// declared that its store keeps layer rows, such as a filesystem source: an
// empty or failed read falls back to the boot-time list, and an empty list
// leaves the read unfiltered.
//
// Spec: §4.6 (Fallback list)
func TestLayerFallback_StoreWithoutLayerRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := &failingLayerReads{Store: store.NewMemory()}
	if err := st.CreateTenant(ctx, store.Tenant{ID: "acme"}); err != nil {
		t.Fatal(err)
	}
	mustPutManifest(t, st, store.ManifestRecord{TenantID: "acme", ArtifactID: "fs-x", Version: "1.0.0", ContentHash: "sha256:a", Type: "agent", Layer: "fs"})
	bob := layer.Identity{IsAuthenticated: true, Sub: "bob"}
	boot := core.New(st, "acme", []layer.Layer{{ID: "fs", Precedence: 1, Visibility: layer.Visibility{Groups: []string{"g"}}}})
	if got := searchIn(t, boot, "acme", bob); len(got) != 0 {
		t.Fatalf("boot-time list = %v", got)
	}
	open := core.New(st, "acme", nil)
	st.fail.Store(true)
	if got := searchIn(t, open, "acme", bob); !slices.Equal(got, []string{"fs-x"}) {
		t.Fatalf("no layer list = %v", got)
	}
}

// TestWithLayerRows_ReadFailure covers a start that cannot read the rows of
// the tenant that owns its layers, which has no list to fall back to.
//
// Spec: §4.6 (Fallback list)
func TestWithLayerRows_ReadFailure(t *testing.T) {
	t.Parallel()
	st := fallbackFixture(t)
	st.fail.Store(true)
	if _, err := core.New(st, "acme", nil).WithLayerRows(context.Background(), "acme"); err == nil {
		t.Fatal("WithLayerRows succeeded over a failed read")
	}
}
