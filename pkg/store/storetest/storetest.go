// Package storetest is the conformance suite for RegistryStore
// implementations (spec §9.1, §9.3). Every backend (Memory, SQLite,
// Postgres, custom) imports this and runs Suite against itself.
//
// The suite exercises the documented contract: tenant isolation
// (§4.7.1), version immutability (§4.7), the dependency graph (§4.7.3),
// and admin grants (§4.7.2). Backends that pass here are interchangeable
// behind the Store SPI.
package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/podium/pkg/store"
)

// Factory returns a fresh Store backed by the implementation under test.
// It is invoked once per sub-test so each case starts clean.
type Factory func(t *testing.T) store.Store

// Suite runs the full conformance set against the implementation that
// factory produces.
func Suite(t *testing.T, factory Factory) {
	t.Helper()
	t.Run("TenantIsolation", func(t *testing.T) { tenantIsolation(t, factory(t)) })
	t.Run("ImmutabilityInvariant", func(t *testing.T) { immutabilityInvariant(t, factory(t)) })
	t.Run("ImmutabilityIdempotent", func(t *testing.T) { immutabilityIdempotent(t, factory(t)) })
	t.Run("ListManifestsScopedToTenant", func(t *testing.T) { listManifestsScopedToTenant(t, factory(t)) })
	t.Run("ListManifestsStableOrder", func(t *testing.T) { listManifestsStableOrder(t, factory(t)) })
	t.Run("DependencyEdges", func(t *testing.T) { dependencyEdges(t, factory(t)) })
	t.Run("DependentsScopedToTenant", func(t *testing.T) { dependentsScopedToTenant(t, factory(t)) })
	t.Run("AdminGrants", func(t *testing.T) { adminGrants(t, factory(t)) })
	t.Run("AdminGrantsAreOrgScoped", func(t *testing.T) { adminGrantsAreOrgScoped(t, factory(t)) })
	t.Run("RevokeAdmin", func(t *testing.T) { revokeAdmin(t, factory(t)) })
	t.Run("GetTenantNotFound", func(t *testing.T) { getTenantNotFound(t, factory(t)) })
	t.Run("GetManifestNotFound", func(t *testing.T) { getManifestNotFound(t, factory(t)) })
	t.Run("LayerConfigCRUD", func(t *testing.T) { layerConfigCRUD(t, factory(t)) })
	t.Run("LayerConfigDelete", func(t *testing.T) { layerConfigDelete(t, factory(t)) })
}

// Spec: §4.7.1 Tenancy — tenant boundaries isolate manifests.
func tenantIsolation(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "a")
	mustCreateTenant(t, s, "b")
	mustPut(t, s, manifestRec("a", "x", "1.0.0", "sha:1"))

	if _, err := s.GetManifest(ctx, "b", "x", "1.0.0"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound for cross-tenant read, got %v", err)
	}
	got, err := s.GetManifest(ctx, "a", "x", "1.0.0")
	if err != nil {
		t.Fatalf("GetManifest(a): %v", err)
	}
	if got.ContentHash != "sha:1" {
		t.Errorf("ContentHash = %q, want sha:1", got.ContentHash)
	}
}

// Spec: §4.7 Version immutability invariant — re-ingesting a
// (tenant, id, version) with a different content hash fails with
// ingest.immutable_violation.
func immutabilityInvariant(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "a")
	mustPut(t, s, manifestRec("a", "x", "1.0.0", "sha:1"))
	err := s.PutManifest(ctx, manifestRec("a", "x", "1.0.0", "sha:2"))
	if !errors.Is(err, store.ErrImmutableViolation) {
		t.Fatalf("got %v, want ErrImmutableViolation", err)
	}
}

// Spec: §4.7 — the same (id, version) with the same content hash is
// idempotent (handles webhook retries).
func immutabilityIdempotent(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "a")
	rec := manifestRec("a", "x", "1.0.0", "sha:1")
	for i := 0; i < 3; i++ {
		if err := s.PutManifest(ctx, rec); err != nil {
			t.Fatalf("Put #%d: %v", i, err)
		}
	}
}

// Spec: §4.7 — ListManifests returns only the requested tenant's
// records.
func listManifestsScopedToTenant(t *testing.T, s store.Store) {
	t.Helper()
	mustCreateTenant(t, s, "a")
	mustCreateTenant(t, s, "b")
	mustPut(t, s, manifestRec("a", "x", "1.0.0", "sha:1"))
	mustPut(t, s, manifestRec("a", "y", "1.0.0", "sha:2"))
	mustPut(t, s, manifestRec("b", "z", "1.0.0", "sha:3"))

	got, err := s.ListManifests(context.Background(), "a")
	if err != nil {
		t.Fatalf("ListManifests: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d manifests, want 2", len(got))
	}
	for _, m := range got {
		if m.TenantID != "a" {
			t.Errorf("tenant leak: %+v", m)
		}
	}
}

// Spec: §4.7 — ListManifests returns records in stable order (artifact
// ID then version) so callers can rely on it for golden output.
func listManifestsStableOrder(t *testing.T, s store.Store) {
	t.Helper()
	mustCreateTenant(t, s, "a")
	mustPut(t, s, manifestRec("a", "z", "1.0.0", "sha:1"))
	mustPut(t, s, manifestRec("a", "a", "2.0.0", "sha:2"))
	mustPut(t, s, manifestRec("a", "a", "1.0.0", "sha:3"))

	got, err := s.ListManifests(context.Background(), "a")
	if err != nil {
		t.Fatalf("ListManifests: %v", err)
	}
	wantIDs := []string{"a", "a", "z"}
	wantVers := []string{"1.0.0", "2.0.0", "1.0.0"}
	if len(got) != len(wantIDs) {
		t.Fatalf("got %d, want %d", len(got), len(wantIDs))
	}
	for i := range wantIDs {
		if got[i].ArtifactID != wantIDs[i] || got[i].Version != wantVers[i] {
			t.Errorf("[%d] = (%s,%s), want (%s,%s)", i,
				got[i].ArtifactID, got[i].Version, wantIDs[i], wantVers[i])
		}
	}
}

// Spec: §4.7.3 Reverse Dependency Index — DependentsOf returns every
// edge ending at the artifact, regardless of edge kind.
func dependencyEdges(t *testing.T, s store.Store) {
	t.Helper()
	mustCreateTenant(t, s, "a")
	ctx := context.Background()
	must(t, s.PutDependency(ctx, "a", store.DependencyEdge{From: "child", To: "parent", Kind: "extends"}))
	must(t, s.PutDependency(ctx, "a", store.DependencyEdge{From: "agent1", To: "parent", Kind: "delegates_to"}))
	must(t, s.PutDependency(ctx, "a", store.DependencyEdge{From: "skill", To: "parent", Kind: "mcpServers"}))
	must(t, s.PutDependency(ctx, "a", store.DependencyEdge{From: "other", To: "elsewhere", Kind: "extends"}))

	got, err := s.DependentsOf(ctx, "a", "parent")
	if err != nil {
		t.Fatalf("DependentsOf: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("got %d edges, want 3", len(got))
	}
}

// Spec: §4.7.3 — DependentsOf is tenant-scoped.
func dependentsScopedToTenant(t *testing.T, s store.Store) {
	t.Helper()
	mustCreateTenant(t, s, "a")
	mustCreateTenant(t, s, "b")
	ctx := context.Background()
	must(t, s.PutDependency(ctx, "a", store.DependencyEdge{From: "x", To: "p", Kind: "extends"}))
	must(t, s.PutDependency(ctx, "b", store.DependencyEdge{From: "y", To: "p", Kind: "extends"}))

	got, err := s.DependentsOf(ctx, "a", "p")
	if err != nil {
		t.Fatalf("DependentsOf: %v", err)
	}
	if len(got) != 1 || got[0].From != "x" {
		t.Errorf("got %+v, want one edge from=x", got)
	}
}

// Spec: §4.7.2 — admin grants are persisted and queryable.
func adminGrants(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	must(t, s.GrantAdmin(ctx, store.AdminGrant{UserID: "joan", OrgID: "acme"}))
	ok, err := s.IsAdmin(ctx, "joan", "acme")
	if err != nil {
		t.Fatalf("IsAdmin: %v", err)
	}
	if !ok {
		t.Errorf("expected admin grant to take effect")
	}
}

// Spec: §4.7.2 — admin grants do not leak across orgs.
func adminGrantsAreOrgScoped(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	must(t, s.GrantAdmin(ctx, store.AdminGrant{UserID: "joan", OrgID: "acme"}))
	ok, err := s.IsAdmin(ctx, "joan", "other")
	if err != nil {
		t.Fatalf("IsAdmin: %v", err)
	}
	if ok {
		t.Errorf("admin grant leaked across orgs")
	}
}

// Spec: §4.7.2 — RevokeAdmin removes a previously-granted admin role.
func revokeAdmin(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	must(t, s.GrantAdmin(ctx, store.AdminGrant{UserID: "alice", OrgID: "acme"}))
	must(t, s.RevokeAdmin(ctx, "alice", "acme"))
	ok, err := s.IsAdmin(ctx, "alice", "acme")
	if err != nil {
		t.Fatalf("IsAdmin: %v", err)
	}
	if ok {
		t.Errorf("RevokeAdmin did not remove the grant")
	}
}

// Spec: §7.3.1 — PutLayerConfig persists a layer config that
// GetLayerConfig retrieves and ListLayerConfigs enumerates.
func layerConfigCRUD(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "t")
	cfg := store.LayerConfig{
		TenantID:     "t",
		ID:           "team-shared",
		SourceType:   "git",
		Repo:         "git@example/team.git",
		Ref:          "main",
		Order:        1,
		Organization: true,
		Groups:       []string{"engineering"},
	}
	if err := s.PutLayerConfig(ctx, cfg); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}
	got, err := s.GetLayerConfig(ctx, "t", "team-shared")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if got.ID != cfg.ID || got.Repo != cfg.Repo {
		t.Errorf("got %+v", got)
	}
	list, err := s.ListLayerConfigs(ctx, "t")
	if err != nil {
		t.Fatalf("ListLayerConfigs: %v", err)
	}
	if len(list) != 1 || list[0].ID != "team-shared" {
		t.Errorf("ListLayerConfigs = %+v", list)
	}
}

// Spec: §7.3.1 — DeleteLayerConfig removes a layer registration.
func layerConfigDelete(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "t")
	cfg := store.LayerConfig{
		TenantID: "t", ID: "victim", SourceType: "local", LocalPath: "/tmp/x",
	}
	if err := s.PutLayerConfig(ctx, cfg); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}
	if err := s.DeleteLayerConfig(ctx, "t", "victim"); err != nil {
		t.Fatalf("DeleteLayerConfig: %v", err)
	}
	if _, err := s.GetLayerConfig(ctx, "t", "victim"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

// Spec: §4.7.1 — GetTenant on a missing tenant returns ErrTenantNotFound.
func getTenantNotFound(t *testing.T, s store.Store) {
	t.Helper()
	if _, err := s.GetTenant(context.Background(), "missing"); !errors.Is(err, store.ErrTenantNotFound) {
		t.Errorf("got %v, want ErrTenantNotFound", err)
	}
}

// Spec: §4.7 — GetManifest on a missing manifest returns ErrNotFound.
func getManifestNotFound(t *testing.T, s store.Store) {
	t.Helper()
	mustCreateTenant(t, s, "a")
	if _, err := s.GetManifest(context.Background(), "a", "missing", "1.0.0"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

// ----- helpers --------------------------------------------------------------

func manifestRec(tenant, id, version, hash string) store.ManifestRecord {
	return store.ManifestRecord{
		TenantID:    tenant,
		ArtifactID:  id,
		Version:     version,
		ContentHash: hash,
		Type:        "skill",
		Description: "desc " + id,
		Tags:        []string{"t1", "t2"},
		Sensitivity: "low",
		Layer:       "test-layer",
		Frontmatter: []byte("---\ntype: skill\n---\n"),
		Body:        []byte("body of " + id),
	}
}

func mustCreateTenant(t *testing.T, s store.Store, id string) {
	t.Helper()
	if err := s.CreateTenant(context.Background(), store.Tenant{ID: id, Name: id}); err != nil {
		t.Fatalf("CreateTenant(%s): %v", id, err)
	}
}

func mustPut(t *testing.T, s store.Store, rec store.ManifestRecord) {
	t.Helper()
	if err := s.PutManifest(context.Background(), rec); err != nil {
		t.Fatalf("PutManifest(%s/%s@%s): %v", rec.TenantID, rec.ArtifactID, rec.Version, err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
