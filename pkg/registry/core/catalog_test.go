package core_test

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

func catalogIDs(entries []core.CatalogEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.ID)
	}
	return out
}

// Spec: §4.5.2 — Catalog returns the visible artifacts under
// a scope prefix as lean descriptors (id, type, summary), and the whole visible
// catalog when scope is empty.
func TestCatalog_ScopeAndDescriptors(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: tenant})
	put := func(id, typ, desc string) {
		_ = st.PutManifest(context.Background(), store.ManifestRecord{
			TenantID: tenant, ArtifactID: id, Version: "1.0.0", ContentHash: "sha256:" + id,
			Type: typ, Description: desc, Layer: "L",
		})
	}
	put("finance/ap/pay", "skill", "pay vendors")
	put("finance/close/run", "context", "close the books")
	put("other/thing", "skill", "unrelated")
	reg := core.New(st, tenant, []layer.Layer{{ID: "L", Visibility: layer.Visibility{Public: true}, Precedence: 1}})

	scoped, err := reg.Catalog(context.Background(), publicID, "finance")
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	got := catalogIDs(scoped)
	if len(got) != 2 || !contains(got, "finance/ap/pay") || !contains(got, "finance/close/run") {
		t.Fatalf("scoped catalog = %v, want the two finance artifacts", got)
	}
	for _, e := range scoped {
		if e.ID == "finance/ap/pay" && (e.Type != "skill" || e.Summary != "pay vendors") {
			t.Errorf("descriptor = %+v, want type/summary populated", e)
		}
	}

	all, err := reg.Catalog(context.Background(), publicID, "")
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("empty-scope catalog = %v, want all 3", catalogIDs(all))
	}
}

// Spec: §4.7.6 — Catalog returns one latest-version entry per artifact.
func TestCatalog_LatestVersionOnly(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: tenant})
	for _, v := range []string{"1.0.0", "2.0.0"} {
		_ = st.PutManifest(context.Background(), store.ManifestRecord{
			TenantID: tenant, ArtifactID: "finance/x", Version: v, ContentHash: "sha256:" + v,
			Type: "context", Description: "v" + v, Layer: "L",
		})
	}
	reg := core.New(st, tenant, []layer.Layer{{ID: "L", Visibility: layer.Visibility{Public: true}, Precedence: 1}})
	entries, err := reg.Catalog(context.Background(), publicID, "finance")
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if len(entries) != 1 || entries[0].Summary != "v2.0.0" {
		t.Fatalf("catalog = %+v, want one latest (v2.0.0) entry", entries)
	}
}

// Spec: §4.6 — Catalog is visibility-filtered: an artifact under a layer the
// caller cannot see never appears, so the catalog cannot widen a read surface.
func TestCatalog_VisibilityFiltered(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: tenant})
	_ = st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: tenant, ArtifactID: "finance/public-x", Version: "1.0.0",
		ContentHash: "sha256:a", Type: "context", Layer: "public",
	})
	_ = st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: tenant, ArtifactID: "finance/private-y", Version: "1.0.0",
		ContentHash: "sha256:b", Type: "context", Layer: "private",
	})
	reg := core.New(st, tenant, []layer.Layer{
		{ID: "public", Visibility: layer.Visibility{Public: true}, Precedence: 1},
		{ID: "private", Visibility: layer.Visibility{Users: []string{"specific-user"}}, Precedence: 2},
	})
	got := catalogIDs(mustCatalog(t, reg, layer.Identity{Sub: "joan", IsAuthenticated: true}, "finance"))
	if !contains(got, "finance/public-x") || contains(got, "finance/private-y") {
		t.Errorf("catalog = %v, want only finance/public-x", got)
	}
}

func mustCatalog(t *testing.T, reg *core.Registry, id layer.Identity, scope string) []core.CatalogEntry {
	t.Helper()
	entries, err := reg.Catalog(context.Background(), id, scope)
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	return entries
}
