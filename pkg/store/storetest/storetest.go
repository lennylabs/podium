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
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
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
	t.Run("ConcurrentImmutabilityViolation", func(t *testing.T) { concurrentImmutabilityViolation(t, factory(t)) })
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
	t.Run("SearchVisibilityRoundTrip", func(t *testing.T) { searchVisibilityRoundTrip(t, factory(t)) })
	t.Run("DomainRecordRoundTrip", func(t *testing.T) { domainRecordRoundTrip(t, factory(t)) })
	t.Run("DomainPutReplacesPerLayer", func(t *testing.T) { domainPutReplacesPerLayer(t, factory(t)) })
}

// Spec: §4.5.1 / §4.5.4 (F-4.5.1) — DOMAIN.md is persisted per (tenant,
// layer, path) so LoadDomain can read it and merge candidates for the
// same path across layers. ListDomains is tenant-scoped and stable.
func domainRecordRoundTrip(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "a")
	mustCreateTenant(t, s, "b")
	mustPutDomain(t, s, store.DomainRecord{TenantID: "a", Layer: "team", Path: "finance/ap", Raw: []byte("---\ndescription: AP\n---\n\n# AP\n")})
	mustPutDomain(t, s, store.DomainRecord{TenantID: "a", Layer: "org", Path: "finance/ap", Raw: []byte("---\nunlisted: true\n---\n")})
	mustPutDomain(t, s, store.DomainRecord{TenantID: "b", Layer: "team", Path: "ops", Raw: []byte("---\n---\n")})

	list, err := s.ListDomains(ctx, "a")
	if err != nil {
		t.Fatalf("ListDomains: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ListDomains(a) = %d records, want 2 (tenant isolation): %+v", len(list), list)
	}
	// Both records share the path but come from distinct layers.
	for _, rec := range list {
		if rec.Path != "finance/ap" {
			t.Errorf("unexpected path %q", rec.Path)
		}
		if len(rec.Raw) == 0 {
			t.Errorf("raw bytes lost for layer %q", rec.Layer)
		}
	}
	// Stable order: path then layer (org < team).
	if list[0].Layer != "org" || list[1].Layer != "team" {
		t.Errorf("ListDomains order = [%s,%s], want [org,team]", list[0].Layer, list[1].Layer)
	}
}

// Spec: §4.5.1 (F-4.5.1) — re-ingesting a layer replaces its DOMAIN.md
// record for a path rather than accumulating duplicates.
func domainPutReplacesPerLayer(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "a")
	mustPutDomain(t, s, store.DomainRecord{TenantID: "a", Layer: "team", Path: "finance", Raw: []byte("v1")})
	mustPutDomain(t, s, store.DomainRecord{TenantID: "a", Layer: "team", Path: "finance", Raw: []byte("v2")})

	list, err := s.ListDomains(ctx, "a")
	if err != nil {
		t.Fatalf("ListDomains: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 record after replace, got %d", len(list))
	}
	if string(list[0].Raw) != "v2" {
		t.Errorf("raw = %q, want v2 (upsert)", list[0].Raw)
	}
}

func mustPutDomain(t *testing.T, s store.Store, rec store.DomainRecord) {
	t.Helper()
	if err := s.PutDomain(context.Background(), rec); err != nil {
		t.Fatalf("PutDomain %s/%s: %v", rec.Layer, rec.Path, err)
	}
}

// Spec: §4.3 universal fields (F-4.3.3) — search_visibility persists on
// the manifest record and survives both GetManifest and ListManifests so
// SearchArtifacts can exclude direct-only artifacts. Without a backing
// column the SQLite and Postgres backends silently dropped the value.
func searchVisibilityRoundTrip(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "a")
	rec := manifestRec("a", "secret-tool", "1.0.0", "sha:1")
	rec.SearchVisibility = "direct-only"
	mustPut(t, s, rec)

	got, err := s.GetManifest(ctx, "a", "secret-tool", "1.0.0")
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	if got.SearchVisibility != "direct-only" {
		t.Errorf("GetManifest SearchVisibility = %q, want direct-only", got.SearchVisibility)
	}

	list, err := s.ListManifests(ctx, "a")
	if err != nil {
		t.Fatalf("ListManifests: %v", err)
	}
	if len(list) != 1 || list[0].SearchVisibility != "direct-only" {
		t.Errorf("ListManifests SearchVisibility not preserved: %+v", list)
	}
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

// Spec: §4.7 Version immutability invariant (F-1.3.1) — under concurrent
// ingest of different content for the same (tenant, id, version), the
// store accepts exactly one writer and reports every other writer as
// ErrImmutableViolation. No writer may leak a raw driver error: the
// ingest orchestrator maps only ErrImmutableViolation to a per-artifact
// conflict and aborts the whole batch on any other error
// (pkg/registry/ingest/ingest.go). A SELECT-then-INSERT pair lets two
// writers both pass the existence check under READ COMMITTED and then
// collide on the primary key, leaking the raw unique-violation error; an
// atomic INSERT ... ON CONFLICT DO NOTHING plus read-back keeps the
// conflict path deterministic. Memory serializes on a mutex and SQLite on
// a single connection, so they accept exactly one without ever leaking;
// Postgres exercises the true MVCC race.
func concurrentImmutabilityViolation(t *testing.T, s store.Store) {
	t.Helper()
	mustCreateTenant(t, s, "a")

	const writers = 64
	var accepted, violations atomic.Int64
	var mu sync.Mutex
	var leaks []error // any non-immutability error a writer saw

	ready := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		// Each writer stores a distinct content hash for the same key, so
		// at most one can win; the rest are immutability violations.
		hash := fmt.Sprintf("sha:%d", i)
		go func() {
			defer wg.Done()
			<-ready
			switch err := s.PutManifest(context.Background(), manifestRec("a", "x", "1.0.0", hash)); {
			case err == nil:
				accepted.Add(1)
			case errors.Is(err, store.ErrImmutableViolation):
				violations.Add(1)
			default:
				mu.Lock()
				leaks = append(leaks, err)
				mu.Unlock()
			}
		}()
	}
	close(ready)
	wg.Wait()

	if len(leaks) > 0 {
		t.Fatalf("%d/%d writers leaked a non-immutability error under concurrency (want every "+
			"loser mapped to ErrImmutableViolation); first leak: %v", len(leaks), writers, leaks[0])
	}
	if accepted.Load() != 1 {
		t.Errorf("accepted = %d, want exactly 1 under concurrent conflicting writes", accepted.Load())
	}
	if got := accepted.Load() + violations.Load(); got != int64(writers) {
		t.Errorf("accepted (%d) + violations (%d) = %d, want %d (no writer lost or double-counted)",
			accepted.Load(), violations.Load(), got, writers)
	}

	// Exactly one set of bytes is durably stored and is one of the racers'.
	got, err := s.GetManifest(context.Background(), "a", "x", "1.0.0")
	if err != nil {
		t.Fatalf("GetManifest after race: %v", err)
	}
	if !strings.HasPrefix(got.ContentHash, "sha:") {
		t.Errorf("stored ContentHash = %q, want one of the racing hashes", got.ContentHash)
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
