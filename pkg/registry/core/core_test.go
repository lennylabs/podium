package core_test

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

const tenant = "tenant-1"

// publicID is the standalone / filesystem-mode identity (§13.10 / §13.11):
// every layer visible.
var publicID = layer.Identity{IsPublic: true}

// setupRegistry builds a Memory store, ingests fsys into the named
// layer, and returns the configured core.Registry.
func setupRegistry(t *testing.T, fsys fstest.MapFS, layerID string) *core.Registry {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: tenant}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
		TenantID: tenant, LayerID: layerID, Files: fsys,
	}); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	return core.New(st, tenant, []layer.Layer{
		{ID: layerID, Visibility: layer.Visibility{Public: true}, Precedence: 1},
	})
}

func contextManifest(desc string) string {
	return "---\ntype: context\nversion: 1.0.0\ndescription: " + desc + "\nsensitivity: low\n---\n\nbody of " + desc + "\n"
}

func contextManifestVer(desc, ver string) string {
	return "---\ntype: context\nversion: " + ver + "\ndescription: " + desc + "\nsensitivity: low\n---\n\nbody of " + desc + "\n"
}

// Spec: §5 load_domain — root call returns top-level subdomains.
// Phase: 7
func TestLoadDomain_RootReturnsTopLevelSubdomains(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	reg := setupRegistry(t, fstest.MapFS{
		"finance/ap/pay/ARTIFACT.md":      &fstest.MapFile{Data: []byte(contextManifest("pay"))},
		"finance/close/run/ARTIFACT.md":   &fstest.MapFile{Data: []byte(contextManifest("variance"))},
		"company-glossary/ARTIFACT.md":    &fstest.MapFile{Data: []byte(contextManifest("glossary"))},
	}, "team-shared")

	got, err := reg.LoadDomain(context.Background(), publicID, "", core.LoadDomainOptions{})
	if err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	wantPaths := []string{"finance"}
	gotPaths := []string{}
	for _, s := range got.Subdomains {
		gotPaths = append(gotPaths, s.Path)
	}
	for _, want := range wantPaths {
		if !contains(gotPaths, want) {
			t.Errorf("expected subdomain %q in %v", want, gotPaths)
		}
	}
	notableIDs := []string{}
	for _, n := range got.Notable {
		notableIDs = append(notableIDs, n.ID)
	}
	if !contains(notableIDs, "company-glossary") {
		t.Errorf("expected company-glossary in notable, got %v", notableIDs)
	}
}

// Spec: §5 load_domain — drilling into a path returns subdomains and
// notable artifacts under that path.
// Phase: 7
func TestLoadDomain_DrillIn(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	reg := setupRegistry(t, fstest.MapFS{
		"finance/ap/pay/ARTIFACT.md":    &fstest.MapFile{Data: []byte(contextManifest("pay"))},
		"finance/close/run/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextManifest("variance"))},
		"finance/notes/ARTIFACT.md":     &fstest.MapFile{Data: []byte(contextManifest("notes"))},
	}, "team-shared")

	got, err := reg.LoadDomain(context.Background(), publicID, "finance", core.LoadDomainOptions{})
	if err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	gotPaths := []string{}
	for _, s := range got.Subdomains {
		gotPaths = append(gotPaths, s.Path)
	}
	for _, want := range []string{"finance/ap", "finance/close"} {
		if !contains(gotPaths, want) {
			t.Errorf("expected subdomain %q in %v", want, gotPaths)
		}
	}
	notableIDs := []string{}
	for _, n := range got.Notable {
		notableIDs = append(notableIDs, n.ID)
	}
	if !contains(notableIDs, "finance/notes") {
		t.Errorf("expected finance/notes in notable, got %v", notableIDs)
	}
}

// Spec: §6.10 — domain.not_found is returned for paths that do not
// resolve to any visible domain.
// Phase: 7
// Matrix: §6.10 (domain.not_found)
func TestLoadDomain_UnknownPathFails(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	reg := setupRegistry(t, fstest.MapFS{
		"finance/notes/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextManifest("notes"))},
	}, "team-shared")
	_, err := reg.LoadDomain(context.Background(), publicID, "marketing", core.LoadDomainOptions{})
	if !errors.Is(err, core.ErrDomainNotFound) {
		t.Fatalf("got %v, want ErrDomainNotFound", err)
	}
}

// Spec: §5 search_artifacts — query returns descriptors ranked by
// relevance.
// Phase: 7
func TestSearchArtifacts_RanksByRelevance(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	reg := setupRegistry(t, fstest.MapFS{
		"finance/run/ARTIFACT.md":    &fstest.MapFile{Data: []byte(contextManifest("Variance analysis after close"))},
		"finance/pay/ARTIFACT.md":    &fstest.MapFile{Data: []byte(contextManifest("Pay an invoice"))},
		"misc/random/ARTIFACT.md":    &fstest.MapFile{Data: []byte(contextManifest("Unrelated content"))},
	}, "team-shared")
	res, err := reg.SearchArtifacts(context.Background(), publicID, core.SearchArtifactsOptions{
		Query: "variance",
	})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	if len(res.Results) == 0 || res.Results[0].ID != "finance/run" {
		t.Fatalf("expected finance/run ranked first, got %+v", res.Results)
	}
	if res.Results[0].Score <= 0 {
		t.Errorf("Score = %v, want > 0", res.Results[0].Score)
	}
}

// Spec: §5 search_artifacts — type filter excludes mismatched types.
// Phase: 7
func TestSearchArtifacts_TypeFilter(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	reg := setupRegistry(t, fstest.MapFS{
		"finance/notes/ARTIFACT.md":   &fstest.MapFile{Data: []byte(contextManifest("notes"))},
		"finance/agent/ARTIFACT.md":   &fstest.MapFile{Data: []byte("---\ntype: agent\nversion: 1.0.0\ndescription: agent\nsensitivity: low\n---\n\nagent body\n")},
	}, "team-shared")
	res, err := reg.SearchArtifacts(context.Background(), publicID, core.SearchArtifactsOptions{
		Type: "context",
	})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	if len(res.Results) != 1 || res.Results[0].ID != "finance/notes" {
		t.Errorf("type filter wrong: %+v", res.Results)
	}
}

// Spec: §5 search_artifacts — scope (path prefix) restricts results.
// Phase: 7
func TestSearchArtifacts_ScopeFilter(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	reg := setupRegistry(t, fstest.MapFS{
		"finance/x/ARTIFACT.md": &fstest.MapFile{Data: []byte(contextManifest("x"))},
		"misc/y/ARTIFACT.md":    &fstest.MapFile{Data: []byte(contextManifest("y"))},
	}, "team-shared")
	res, err := reg.SearchArtifacts(context.Background(), publicID, core.SearchArtifactsOptions{
		Scope: "finance",
	})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	if len(res.Results) != 1 || res.Results[0].ID != "finance/x" {
		t.Errorf("scope filter wrong: %+v", res.Results)
	}
}

// Spec: §6.10 — top_k > 50 is rejected with registry.invalid_argument.
// Phase: 7
// Matrix: §6.10 (registry.invalid_argument)
func TestSearchArtifacts_TopKBound(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	reg := setupRegistry(t, fstest.MapFS{}, "team-shared")
	_, err := reg.SearchArtifacts(context.Background(), publicID, core.SearchArtifactsOptions{
		TopK: 51,
	})
	if !errors.Is(err, core.ErrInvalidArgument) {
		t.Errorf("got %v, want ErrInvalidArgument", err)
	}
}

// Spec: §5 search_artifacts — total_matched reflects the true count
// even when top_k truncates results.
// Phase: 7
func TestSearchArtifacts_TotalMatchedAccurate(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	files := fstest.MapFS{}
	for i := 0; i < 12; i++ {
		path := "x/a" + string(rune('a'+i)) + "/ARTIFACT.md"
		files[path] = &fstest.MapFile{Data: []byte(contextManifest("desc"))}
	}
	reg := setupRegistry(t, files, "team-shared")
	res, err := reg.SearchArtifacts(context.Background(), publicID, core.SearchArtifactsOptions{
		TopK: 5,
	})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	if res.TotalMatched != 12 {
		t.Errorf("TotalMatched = %d, want 12", res.TotalMatched)
	}
	if len(res.Results) != 5 {
		t.Errorf("len(Results) = %d, want 5", len(res.Results))
	}
}

// Spec: §5 load_artifact — version="" resolves to latest per §4.7.6.
// Phase: 7
func TestLoadArtifact_LatestResolution(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	st2 := store.NewMemory()
	_ = st2.CreateTenant(context.Background(), store.Tenant{ID: tenant})
	_ = st2.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: tenant, ArtifactID: "x", Version: "1.0.0",
		ContentHash: "sha256:a", Type: "context", Body: []byte("v1"),
		Layer: "team-shared",
	})
	_ = st2.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: tenant, ArtifactID: "x", Version: "2.1.0",
		ContentHash: "sha256:b", Type: "context", Body: []byte("v2"),
		Layer: "team-shared",
	})
	_ = st2.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: tenant, ArtifactID: "x", Version: "1.5.0",
		ContentHash: "sha256:c", Type: "context", Body: []byte("v15"),
		Layer: "team-shared",
	})
	reg2 := core.New(st2, tenant, []layer.Layer{
		{ID: "team-shared", Visibility: layer.Visibility{Public: true}, Precedence: 1},
	})
	got, err := reg2.LoadArtifact(context.Background(), publicID, "x", core.LoadArtifactOptions{})
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	if got.Version != "2.1.0" {
		t.Errorf("Version = %q, want 2.1.0 (latest)", got.Version)
	}
}

// Spec: §4.7.6 — id@<major>.x resolves to the highest matching version.
// Phase: 7
func TestLoadArtifact_MajorPin(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: tenant})
	for _, v := range []string{"1.0.0", "1.5.2", "1.5.10", "2.0.0"} {
		_ = st.PutManifest(context.Background(), store.ManifestRecord{
			TenantID: tenant, ArtifactID: "x", Version: v,
			ContentHash: "sha256:" + v, Type: "context", Layer: "L",
		})
	}
	reg := core.New(st, tenant, []layer.Layer{
		{ID: "L", Visibility: layer.Visibility{Public: true}, Precedence: 1},
	})
	got, err := reg.LoadArtifact(context.Background(), publicID, "x", core.LoadArtifactOptions{Version: "1.x"})
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	if got.Version != "1.5.10" {
		t.Errorf("Version = %q, want 1.5.10", got.Version)
	}
}

// Spec: §6.10 — load_artifact for an unknown id returns
// registry.not_found.
// Phase: 7
// Matrix: §6.10 (registry.not_found)
func TestLoadArtifact_NotFound(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	reg := setupRegistry(t, fstest.MapFS{}, "team-shared")
	_, err := reg.LoadArtifact(context.Background(), publicID, "missing", core.LoadArtifactOptions{})
	if !errors.Is(err, core.ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

// Spec: §4.6 Visibility — manifests under a layer the caller cannot
// see are filtered out before search / load.
// Phase: 7
// Matrix: §4.6 (users)
func TestVisibility_FiltersByLayer(t *testing.T) {
	testharness.RequirePhase(t, 7)
	t.Parallel()
	st := store.NewMemory()
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: tenant})
	_ = st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: tenant, ArtifactID: "public-x", Version: "1.0.0",
		ContentHash: "sha256:a", Type: "context", Layer: "public",
	})
	_ = st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: tenant, ArtifactID: "private-y", Version: "1.0.0",
		ContentHash: "sha256:b", Type: "context", Layer: "private",
	})
	reg := core.New(st, tenant, []layer.Layer{
		{ID: "public", Visibility: layer.Visibility{Public: true}, Precedence: 1},
		{ID: "private", Visibility: layer.Visibility{Users: []string{"specific-user"}}, Precedence: 2},
	})
	id := layer.Identity{Sub: "joan", IsAuthenticated: true}
	res, err := reg.SearchArtifacts(context.Background(), id, core.SearchArtifactsOptions{})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	ids := []string{}
	for _, r := range res.Results {
		ids = append(ids, r.ID)
	}
	if !contains(ids, "public-x") || contains(ids, "private-y") {
		t.Errorf("expected only public-x in results, got %v", ids)
	}

	// load_artifact for the invisible artifact returns ErrNotFound, not
	// a leak about its layer.
	_, err = reg.LoadArtifact(context.Background(), id, "private-y", core.LoadArtifactOptions{})
	if !errors.Is(err, core.ErrNotFound) {
		t.Errorf("LoadArtifact for invisible artifact: got %v, want ErrNotFound", err)
	}
}

// helpers ------------------------------------------------------------------

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

