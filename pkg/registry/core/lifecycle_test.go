package core_test

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §4.7.4 — deprecated artifacts are excluded from default
// search results. Callers can opt back in via IncludeDeprecated.
// Phase: 7
func TestSearchArtifacts_DeprecatedExcludedByDefault(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	const tenant = "t"
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: tenant}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	for _, m := range []store.ManifestRecord{
		{TenantID: tenant, ArtifactID: "x/live", Version: "1.0.0", ContentHash: "sha256:1",
			Type: "skill", Layer: "L"},
		{TenantID: tenant, ArtifactID: "x/old", Version: "1.0.0", ContentHash: "sha256:2",
			Type: "skill", Layer: "L", Deprecated: true},
	} {
		if err := st.PutManifest(context.Background(), m); err != nil {
			t.Fatalf("PutManifest %s: %v", m.ArtifactID, err)
		}
	}
	reg := core.New(st, tenant, []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})

	res, err := reg.SearchArtifacts(context.Background(), layer.Identity{IsPublic: true},
		core.SearchArtifactsOptions{TopK: 10})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	for _, r := range res.Results {
		if r.ID == "x/old" {
			t.Errorf("default search returned deprecated artifact: %+v", r)
		}
	}

	// Opt in: deprecated artifacts surface.
	resAll, err := reg.SearchArtifacts(context.Background(), layer.Identity{IsPublic: true},
		core.SearchArtifactsOptions{TopK: 10, IncludeDeprecated: true})
	if err != nil {
		t.Fatalf("SearchArtifacts(IncludeDeprecated): %v", err)
	}
	found := false
	for _, r := range resAll.Results {
		if r.ID == "x/old" {
			found = true
		}
	}
	if !found {
		t.Errorf("IncludeDeprecated did not surface x/old: %+v", resAll.Results)
	}
}

// Spec: §4.7.4 — deprecated artifacts return a warning when
// loaded; replaced_by surfaces alongside the warning.
// Phase: 7
func TestLoadArtifact_DeprecatedReturnsWarning(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	const tenant = "t"
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: tenant})
	if err := st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: tenant, ArtifactID: "x/old", Version: "1.0.0",
		ContentHash: "sha256:dep", Type: "skill", Layer: "L",
		Deprecated: true, ReplacedBy: "x/new",
	}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	reg := core.New(st, tenant, []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	got, err := reg.LoadArtifact(context.Background(), layer.Identity{IsPublic: true},
		"x/old", core.LoadArtifactOptions{})
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	if !got.Deprecated {
		t.Errorf("Deprecated = false, want true")
	}
	if got.ReplacedBy != "x/new" {
		t.Errorf("ReplacedBy = %q, want x/new", got.ReplacedBy)
	}
	if got.DeprecationWarning == "" {
		t.Errorf("DeprecationWarning empty, want non-empty")
	}
}

// Spec: §4.7.4 — a live (non-deprecated) artifact carries no
// warning string.
// Phase: 7
func TestLoadArtifact_LiveArtifactHasNoWarning(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	const tenant = "t"
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: tenant})
	_ = st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: tenant, ArtifactID: "x/live", Version: "1.0.0",
		ContentHash: "sha256:a", Type: "skill", Layer: "L",
	})
	reg := core.New(st, tenant, []layer.Layer{
		{ID: "L", Precedence: 1, Visibility: layer.Visibility{Public: true}},
	})
	got, err := reg.LoadArtifact(context.Background(), layer.Identity{IsPublic: true},
		"x/live", core.LoadArtifactOptions{})
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	if got.DeprecationWarning != "" {
		t.Errorf("live artifact got warning %q", got.DeprecationWarning)
	}
}
