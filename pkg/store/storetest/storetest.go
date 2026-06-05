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
	"time"

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
	t.Run("DependencyInDegree", func(t *testing.T) { dependencyInDegree(t, factory(t)) })
	t.Run("AdminGrants", func(t *testing.T) { adminGrants(t, factory(t)) })
	t.Run("AdminGrantsAreOrgScoped", func(t *testing.T) { adminGrantsAreOrgScoped(t, factory(t)) })
	t.Run("RevokeAdmin", func(t *testing.T) { revokeAdmin(t, factory(t)) })
	t.Run("GetTenantNotFound", func(t *testing.T) { getTenantNotFound(t, factory(t)) })
	t.Run("QuotaRoundTrip", func(t *testing.T) { quotaRoundTrips(t, factory(t)) })
	t.Run("ScopePreviewFlagRoundTrip", func(t *testing.T) { scopePreviewFlagRoundTrips(t, factory(t)) })
	t.Run("GetManifestNotFound", func(t *testing.T) { getManifestNotFound(t, factory(t)) })
	t.Run("LayerConfigCRUD", func(t *testing.T) { layerConfigCRUD(t, factory(t)) })
	t.Run("LayerConfigDelete", func(t *testing.T) { layerConfigDelete(t, factory(t)) })
	t.Run("DeprecatedAtStampedAndPurged", func(t *testing.T) { deprecatedAtStampedAndPurged(t, factory(t)) })
	t.Run("LayerSoftDeleteRecovery", func(t *testing.T) { layerSoftDeleteRecovery(t, factory(t)) })
	t.Run("LayerDeletionPurgedAfterWindow", func(t *testing.T) { layerDeletionPurgedAfterWindow(t, factory(t)) })
	t.Run("SearchVisibilityRoundTrip", func(t *testing.T) { searchVisibilityRoundTrip(t, factory(t)) })
	t.Run("SkillRawRoundTrip", func(t *testing.T) { skillRawRoundTrip(t, factory(t)) })
	t.Run("ResourcesRoundTrip", func(t *testing.T) { resourcesRoundTrip(t, factory(t)) })
	t.Run("DomainRecordRoundTrip", func(t *testing.T) { domainRecordRoundTrip(t, factory(t)) })
	t.Run("DomainPutReplacesPerLayer", func(t *testing.T) { domainPutReplacesPerLayer(t, factory(t)) })
}

// Spec: §4.5.1 / §4.5.4 — DOMAIN.md is persisted per (tenant,
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

// Spec: §4.5.1 — re-ingesting a layer replaces its DOMAIN.md
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

// Spec: §4.3 universal fields — search_visibility persists on
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

// Spec: §4.3.4 / §6.6 step 2 / §11 — the verbatim SKILL.md bytes (SkillRaw)
// are persisted on the manifest record and survive both GetManifest and
// ListManifests, so server-source delivery reproduces the authored skill file
// and the §6.6 content_hash (which covers SkillRaw) can be re-verified by the
// consumer. Without a backing column the SQLite and Postgres backends silently
// dropped the bytes, returning skill_raw="".
func skillRawRoundTrip(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "a")
	rec := manifestRec("a", "skill/demo", "1.0.0", "sha:1")
	skillRaw := []byte("---\nname: demo\ndescription: a demo skill\n---\n\ndemo body.\n")
	rec.SkillRaw = skillRaw
	mustPut(t, s, rec)

	got, err := s.GetManifest(ctx, "a", "skill/demo", "1.0.0")
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	if string(got.SkillRaw) != string(skillRaw) {
		t.Errorf("GetManifest SkillRaw = %q, want %q", got.SkillRaw, skillRaw)
	}

	list, err := s.ListManifests(ctx, "a")
	if err != nil {
		t.Fatalf("ListManifests: %v", err)
	}
	if len(list) != 1 || string(list[0].SkillRaw) != string(skillRaw) {
		t.Errorf("ListManifests SkillRaw not preserved: %+v", list)
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

// Spec: §4.7 Version immutability invariant — under concurrent
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

// Spec: §4.7.3 "frequently-depended-on artifacts surface higher" —
// DependencyInDegree counts distinct dependents per target, tenant-scoped,
// collapsing multiple edge kinds from the same source to one.
func dependencyInDegree(t *testing.T, s store.Store) {
	t.Helper()
	mustCreateTenant(t, s, "a")
	mustCreateTenant(t, s, "b")
	ctx := context.Background()
	// parent has two distinct dependents (child, agent1); the third edge is a
	// second kind from agent1 and must not double-count.
	must(t, s.PutDependency(ctx, "a", store.DependencyEdge{From: "child", To: "parent", Kind: "extends"}))
	must(t, s.PutDependency(ctx, "a", store.DependencyEdge{From: "agent1", To: "parent", Kind: "delegates_to"}))
	must(t, s.PutDependency(ctx, "a", store.DependencyEdge{From: "agent1", To: "parent", Kind: "mcpServers"}))
	// lonely has a single dependent; another tenant's edge must not leak in.
	must(t, s.PutDependency(ctx, "a", store.DependencyEdge{From: "x", To: "lonely", Kind: "extends"}))
	must(t, s.PutDependency(ctx, "b", store.DependencyEdge{From: "y", To: "parent", Kind: "extends"}))

	got, err := s.DependencyInDegree(ctx, "a")
	if err != nil {
		t.Fatalf("DependencyInDegree: %v", err)
	}
	if got["parent"] != 2 {
		t.Errorf("parent in-degree = %d, want 2", got["parent"])
	}
	if got["lonely"] != 1 {
		t.Errorf("lonely in-degree = %d, want 1", got["lonely"])
	}
	if _, ok := got["child"]; ok {
		t.Errorf("child has no dependents; should be absent, got %d", got["child"])
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
	// spec: §7.3.1 — force_push_policy and last_ingested_at persist on the
	// layer config.
	ingestedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cfg := store.LayerConfig{
		TenantID:        "t",
		ID:              "team-shared",
		SourceType:      "git",
		Repo:            "git@example/team.git",
		Ref:             "main",
		Order:           1,
		Organization:    true,
		Groups:          []string{"engineering"},
		ForcePushPolicy: "strict",
		LastIngestedAt:  &ingestedAt,
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
	if got.ForcePushPolicy != "strict" {
		t.Errorf("ForcePushPolicy = %q, want strict", got.ForcePushPolicy)
	}
	if got.LastIngestedAt == nil || !got.LastIngestedAt.Equal(ingestedAt) {
		t.Errorf("LastIngestedAt = %v, want %v", got.LastIngestedAt, ingestedAt)
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

// Spec: §8.4 — a deprecated version is stamped with DeprecatedAt at put
// time, and PurgeDeprecatedManifests removes only those whose stamp
// predates the cutoff ("90 days after the deprecation flag is set"). A
// non-deprecated version and a freshly deprecated one are left intact.
func deprecatedAtStampedAndPurged(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "t")

	old := time.Now().UTC().Add(-100 * 24 * time.Hour)
	recent := time.Now().UTC().Add(-1 * 24 * time.Hour)

	oldDep := manifestRec("t", "skill/x", "1.0.0", "h1")
	oldDep.Deprecated = true
	oldDep.IngestedAt = old
	mustPut(t, s, oldDep)

	freshDep := manifestRec("t", "skill/x", "2.0.0", "h2")
	freshDep.Deprecated = true
	freshDep.IngestedAt = recent
	mustPut(t, s, freshDep)

	live := manifestRec("t", "skill/x", "3.0.0", "h3")
	live.IngestedAt = old
	mustPut(t, s, live)

	// DeprecatedAt is stamped from IngestedAt for deprecated versions only.
	got, err := s.GetManifest(ctx, "t", "skill/x", "1.0.0")
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	if got.DeprecatedAt == nil {
		t.Fatalf("DeprecatedAt not stamped on deprecated version")
	}
	if liveRec, _ := s.GetManifest(ctx, "t", "skill/x", "3.0.0"); liveRec.DeprecatedAt != nil {
		t.Errorf("DeprecatedAt stamped on non-deprecated version: %v", liveRec.DeprecatedAt)
	}

	// Purge versions deprecated more than 90 days ago.
	cutoff := time.Now().UTC().Add(-90 * 24 * time.Hour)
	n, err := s.PurgeDeprecatedManifests(ctx, cutoff)
	if err != nil {
		t.Fatalf("PurgeDeprecatedManifests: %v", err)
	}
	if n != 1 {
		t.Errorf("purged = %d, want 1", n)
	}
	if _, err := s.GetManifest(ctx, "t", "skill/x", "1.0.0"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("old deprecated version survived purge: %v", err)
	}
	if _, err := s.GetManifest(ctx, "t", "skill/x", "2.0.0"); err != nil {
		t.Errorf("fresh deprecated version wrongly purged: %v", err)
	}
	if _, err := s.GetManifest(ctx, "t", "skill/x", "3.0.0"); err != nil {
		t.Errorf("live version wrongly purged: %v", err)
	}
}

// Spec: §8.4 — DeleteLayerConfig soft-deletes a layer and the artifacts
// ingested from it (hidden from Get/List but listed by
// ListDeletedLayerConfigs), and RestoreLayerConfig recovers both within
// the window.
func layerSoftDeleteRecovery(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "t")
	cfg := store.LayerConfig{TenantID: "t", ID: "alice-personal", SourceType: "local", LocalPath: "/tmp/x", UserDefined: true, Owner: "alice"}
	if err := s.PutLayerConfig(ctx, cfg); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}
	art := manifestRec("t", "skill/a", "1.0.0", "ha")
	art.Layer = "alice-personal"
	mustPut(t, s, art)

	if err := s.DeleteLayerConfig(ctx, "t", "alice-personal"); err != nil {
		t.Fatalf("DeleteLayerConfig: %v", err)
	}
	// Layer and its artifact are hidden from normal reads.
	if _, err := s.GetLayerConfig(ctx, "t", "alice-personal"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("soft-deleted layer still visible: %v", err)
	}
	if _, err := s.GetManifest(ctx, "t", "skill/a", "1.0.0"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("soft-deleted artifact still visible: %v", err)
	}
	if list, _ := s.ListManifests(ctx, "t"); len(list) != 0 {
		t.Errorf("ListManifests returned soft-deleted artifact: %+v", list)
	}
	// But recoverable: it shows up in the deleted list.
	deleted, err := s.ListDeletedLayerConfigs(ctx, "t")
	if err != nil {
		t.Fatalf("ListDeletedLayerConfigs: %v", err)
	}
	if len(deleted) != 1 || deleted[0].ID != "alice-personal" || deleted[0].DeletedAt == nil {
		t.Fatalf("ListDeletedLayerConfigs = %+v", deleted)
	}

	// Restore clears the tombstone on both layer and artifact.
	if err := s.RestoreLayerConfig(ctx, "t", "alice-personal"); err != nil {
		t.Fatalf("RestoreLayerConfig: %v", err)
	}
	if _, err := s.GetLayerConfig(ctx, "t", "alice-personal"); err != nil {
		t.Errorf("layer not recovered: %v", err)
	}
	if _, err := s.GetManifest(ctx, "t", "skill/a", "1.0.0"); err != nil {
		t.Errorf("artifact not recovered: %v", err)
	}
	// Restoring a layer with no tombstone is ErrNotFound.
	if err := s.RestoreLayerConfig(ctx, "t", "alice-personal"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("restore of non-deleted layer = %v, want ErrNotFound", err)
	}
}

// Spec: §8.4 — PurgeExpiredLayerDeletions hard-deletes soft-deleted
// layers and their artifacts past the 30-day recovery window, and leaves
// a recently soft-deleted layer recoverable.
func layerDeletionPurgedAfterWindow(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "t")
	// A layer soft-deleted 40 days ago (past the window): inject the
	// tombstone directly via PutLayerConfig so the test controls the age.
	oldDeleted := time.Now().UTC().Add(-40 * 24 * time.Hour)
	expired := store.LayerConfig{TenantID: "t", ID: "expired", SourceType: "local", LocalPath: "/x", DeletedAt: &oldDeleted}
	if err := s.PutLayerConfig(ctx, expired); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}
	expiredArt := manifestRec("t", "skill/e", "1.0.0", "he")
	expiredArt.Layer = "expired"
	expiredArt.DeletedAt = &oldDeleted
	mustPut(t, s, expiredArt)

	// A layer soft-deleted 5 days ago (still recoverable).
	recentDeleted := time.Now().UTC().Add(-5 * 24 * time.Hour)
	recent := store.LayerConfig{TenantID: "t", ID: "recent", SourceType: "local", LocalPath: "/y", DeletedAt: &recentDeleted}
	if err := s.PutLayerConfig(ctx, recent); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}

	cutoff := time.Now().UTC().Add(-30 * 24 * time.Hour)
	n, err := s.PurgeExpiredLayerDeletions(ctx, cutoff)
	if err != nil {
		t.Fatalf("PurgeExpiredLayerDeletions: %v", err)
	}
	if n != 1 {
		t.Errorf("purged = %d, want 1", n)
	}
	// The expired layer's artifact is gone; the recent layer is still
	// recoverable.
	deleted, _ := s.ListDeletedLayerConfigs(ctx, "t")
	if len(deleted) != 1 || deleted[0].ID != "recent" {
		t.Errorf("after purge ListDeletedLayerConfigs = %+v", deleted)
	}
	if err := s.RestoreLayerConfig(ctx, "t", "recent"); err != nil {
		t.Errorf("recent layer should still be recoverable: %v", err)
	}
}

// Spec: §4.7.1 — GetTenant on a missing tenant returns ErrTenantNotFound.
func getTenantNotFound(t *testing.T, s store.Store) {
	t.Helper()
	if _, err := s.GetTenant(context.Background(), "missing"); !errors.Is(err, store.ErrTenantNotFound) {
		t.Errorf("got %v, want ErrTenantNotFound", err)
	}
}

// Spec: §4.7.8 / §7.3.1 — the per-tenant Quota round-trips through
// CreateTenant/GetTenant on every backend, including the §7.3.1
// MaxUserLayers cap that the user-defined-layer enforcement reads.
// A backend that drops a quota field would let the cap fall
// back to its default and silently ignore the tenant override.
func quotaRoundTrips(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	want := store.Quota{
		StorageBytes:      1 << 30,
		SearchQPS:         50,
		MaterializeRate:   25,
		AuditVolumePerDay: 100000,
		MaxUserLayers:     7,
	}
	if err := s.CreateTenant(ctx, store.Tenant{ID: "q", Name: "q", Quota: want}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	got, err := s.GetTenant(ctx, "q")
	if err != nil {
		t.Fatalf("GetTenant: %v", err)
	}
	if got.Quota != want {
		t.Errorf("Quota round-trip = %+v, want %+v", got.Quota, want)
	}
}

// Spec: §3.5 — the tenant gate expose_scope_preview round-trips through
// CreateTenant/GetTenant as a tri-state on every backend: an unset flag
// reads back nil (ScopePreviewEnabled defaults true), an explicit false
// reads back false, and an explicit true reads back true. A backend that
// dropped the column would silently re-enable the endpoint a tenant
// disabled.
func scopePreviewFlagRoundTrips(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	ptr := func(b bool) *bool { return &b }
	cases := []struct {
		id       string
		flag     *bool
		wantEnab bool
	}{
		{"unset", nil, true},
		{"off", ptr(false), false},
		{"on", ptr(true), true},
	}
	for _, c := range cases {
		if err := s.CreateTenant(ctx, store.Tenant{ID: c.id, Name: c.id, ExposeScopePreview: c.flag}); err != nil {
			t.Fatalf("CreateTenant(%s): %v", c.id, err)
		}
		got, err := s.GetTenant(ctx, c.id)
		if err != nil {
			t.Fatalf("GetTenant(%s): %v", c.id, err)
		}
		if c.flag == nil {
			if got.ExposeScopePreview != nil {
				t.Errorf("%s: ExposeScopePreview = %v, want nil (unset round-trips as NULL)", c.id, *got.ExposeScopePreview)
			}
		} else if got.ExposeScopePreview == nil || *got.ExposeScopePreview != *c.flag {
			t.Errorf("%s: ExposeScopePreview = %v, want %v", c.id, got.ExposeScopePreview, *c.flag)
		}
		if got.ScopePreviewEnabled() != c.wantEnab {
			t.Errorf("%s: ScopePreviewEnabled() = %v, want %v", c.id, got.ScopePreviewEnabled(), c.wantEnab)
		}
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

// Spec: §7.2 / §4.4 — bundled resource refs persist on the manifest
// record and survive GetManifest and ListManifests so the data plane can
// serve them. Inline bytes (small resources), the content hash, size,
// and content type all round-trip; without a backing column the SQL
// backends would silently drop them.
func resourcesRoundTrip(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	mustCreateTenant(t, s, "a")
	rec := manifestRec("a", "finance/run", "1.0.0", "sha:1")
	rec.Resources = []store.ResourceRef{
		{
			Path:        "scripts/run.py",
			ContentHash: "sha256:aaaa",
			Size:        12,
			ContentType: "text/x-python",
			Inline:      []byte("print('hi')\n"),
		},
		{
			// A large resource carries no inline bytes (served from object storage).
			Path:        "data/big.bin",
			ContentHash: "sha256:bbbb",
			Size:        9_000_000,
			ContentType: "application/octet-stream",
		},
	}
	mustPut(t, s, rec)

	check := func(label string, got store.ManifestRecord) {
		if len(got.Resources) != 2 {
			t.Fatalf("%s: Resources len = %d, want 2 (%+v)", label, len(got.Resources), got.Resources)
		}
		small := got.Resources[0]
		if small.Path != "scripts/run.py" || small.ContentHash != "sha256:aaaa" ||
			small.Size != 12 || small.ContentType != "text/x-python" ||
			string(small.Inline) != "print('hi')\n" {
			t.Errorf("%s: small ref not preserved: %+v", label, small)
		}
		large := got.Resources[1]
		if large.Path != "data/big.bin" || large.ContentHash != "sha256:bbbb" ||
			large.Size != 9_000_000 || large.Inline != nil {
			t.Errorf("%s: large ref not preserved (inline must be nil): %+v", label, large)
		}
	}

	got, err := s.GetManifest(ctx, "a", "finance/run", "1.0.0")
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	check("GetManifest", got)

	list, err := s.ListManifests(ctx, "a")
	if err != nil {
		t.Fatalf("ListManifests: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListManifests len = %d, want 1", len(list))
	}
	check("ListManifests", list[0])

	// A record with no bundled resources round-trips to an empty/nil slice.
	bare := manifestRec("a", "finance/bare", "1.0.0", "sha:2")
	mustPut(t, s, bare)
	gotBare, err := s.GetManifest(ctx, "a", "finance/bare", "1.0.0")
	if err != nil {
		t.Fatalf("GetManifest(bare): %v", err)
	}
	if len(gotBare.Resources) != 0 {
		t.Errorf("bare record Resources = %+v, want none", gotBare.Resources)
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
