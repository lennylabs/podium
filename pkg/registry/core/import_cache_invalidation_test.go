package core_test

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// spec: §12 / §4.5.2 (F-12.0.5) — DOMAIN.md include: globs are expanded
// into the load_domain cross-cutting view, and the server-side expansion
// cache is invalidated when an ingest changes the artifact snapshot. After
// a new matching artifact is ingested, the next load_domain surfaces it in
// the notable list rather than serving a stale cached expansion.
func TestLoadDomain_IncludeCacheInvalidatesOnIngest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	put := func(id string) {
		if err := st.PutManifest(ctx, store.ManifestRecord{
			TenantID: "t", ArtifactID: id, Version: "1.0.0",
			ContentHash: "sha256:" + id, Type: "skill", Layer: "L1",
		}); err != nil {
			t.Fatalf("PutManifest %s: %v", id, err)
		}
	}
	put("_shared/regex/iban")
	// finance/hub imports the _shared/regex subtree via a bounded glob.
	if err := st.PutDomain(ctx, store.DomainRecord{
		TenantID: "t", Layer: "L1", Path: "finance/hub",
		Raw: []byte("---\nname: hub\ninclude:\n  - _shared/regex/**\n---\n"),
	}); err != nil {
		t.Fatalf("PutDomain: %v", err)
	}

	reg := core.New(st, "t", []layer.Layer{
		{ID: "L1", Visibility: layer.Visibility{Public: true}, Precedence: 1},
	})
	id := layer.Identity{IsPublic: true}

	res, err := reg.LoadDomain(ctx, id, "finance/hub", core.LoadDomainOptions{})
	if err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	if !dlContains(dlNotableIDs(res), "_shared/regex/iban") {
		t.Fatalf("first load: notable %v missing imported member", dlNotableIDs(res))
	}

	// A new ingest adds another member under the imported prefix. The
	// snapshot fingerprint changes, so the cached expansion is not reused.
	put("_shared/regex/swift")
	res2, err := reg.LoadDomain(ctx, id, "finance/hub", core.LoadDomainOptions{})
	if err != nil {
		t.Fatalf("LoadDomain (after ingest): %v", err)
	}
	got := dlNotableIDs(res2)
	if !dlContains(got, "_shared/regex/iban") || !dlContains(got, "_shared/regex/swift") {
		t.Errorf("after ingest: notable %v missing a newly-ingested import member", got)
	}
}
