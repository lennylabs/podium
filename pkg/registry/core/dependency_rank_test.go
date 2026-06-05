package core_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// §4.7.3 "frequently-depended-on artifacts surface higher": the reverse-
// dependency in-degree breaks a BM25 tie so the more-depended-on artifact
// ranks first.
func TestSearchArtifacts_DependencyRerank(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"tools/aaa/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextManifest("shared helper"))},
		"tools/bbb/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextManifest("shared helper"))},
	}
	ctx := context.Background()

	// Control: equal BM25, no dependents, so the tie breaks alphabetically.
	ctrl := core.New(seedDepStore(t, fsys), tenant, depPublicLayers())
	res, err := ctrl.SearchArtifacts(ctx, publicID, core.SearchArtifactsOptions{Query: "helper"})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	if len(res.Results) != 2 || res.Results[0].ID != "tools/aaa" {
		t.Fatalf("control order = %v, want tools/aaa first", ids(res.Results))
	}

	// tools/bbb gains two distinct dependents; its in-degree lifts it over the
	// alphabetical winner. A second edge kind from the same source must not
	// inflate the count.
	st := seedDepStore(t, fsys)
	putDep(t, st, store.DependencyEdge{From: "consumer/one", To: "tools/bbb", Kind: "extends"})
	putDep(t, st, store.DependencyEdge{From: "consumer/two", To: "tools/bbb", Kind: "delegates_to"})
	putDep(t, st, store.DependencyEdge{From: "consumer/two", To: "tools/bbb", Kind: "mcpServers"})
	reg := core.New(st, tenant, depPublicLayers())
	res, err = reg.SearchArtifacts(ctx, publicID, core.SearchArtifactsOptions{Query: "helper"})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	if len(res.Results) != 2 || res.Results[0].ID != "tools/bbb" {
		t.Errorf("dependency-reranked order = %v, want tools/bbb first", ids(res.Results))
	}
}

func seedDepStore(t *testing.T, fsys fstest.MapFS) *store.Memory {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: tenant}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{TenantID: tenant, LayerID: "L", Files: fsys}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	return st
}

func depPublicLayers() []layer.Layer {
	return []layer.Layer{{ID: "L", Visibility: layer.Visibility{Public: true}, Precedence: 1}}
}

func putDep(t *testing.T, st *store.Memory, e store.DependencyEdge) {
	t.Helper()
	if err := st.PutDependency(context.Background(), tenant, e); err != nil {
		t.Fatalf("PutDependency: %v", err)
	}
}
