package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// Spec: §4.7.1 Tenancy — tenants isolate manifest visibility; a
// manifest stored under tenant A is invisible from tenant B.
func TestMemory_TenantIsolation(t *testing.T) {
	t.Parallel()
	s := NewMemory()
	ctx := context.Background()
	_ = s.CreateTenant(ctx, Tenant{ID: "a"})
	_ = s.CreateTenant(ctx, Tenant{ID: "b"})

	rec := ManifestRecord{TenantID: "a", ArtifactID: "x", Version: "1.0.0", ContentHash: "sha:abc"}
	if err := s.PutManifest(ctx, rec); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	if _, err := s.GetManifest(ctx, "b", "x", "1.0.0"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for tenant b, got %v", err)
	}
	if _, err := s.GetManifest(ctx, "a", "x", "1.0.0"); err != nil {
		t.Errorf("tenant a Get: %v", err)
	}
}

// Spec: §4.7 Version immutability invariant — re-ingesting the same
// (artifact, version) with different content fails with
// ingest.immutable_violation.
func TestMemory_ImmutabilityInvariant(t *testing.T) {
	t.Parallel()
	s := NewMemory()
	ctx := context.Background()
	rec1 := ManifestRecord{TenantID: "a", ArtifactID: "x", Version: "1.0.0", ContentHash: "sha:1"}
	rec2 := ManifestRecord{TenantID: "a", ArtifactID: "x", Version: "1.0.0", ContentHash: "sha:2"}
	if err := s.PutManifest(ctx, rec1); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := s.PutManifest(ctx, rec2); !errors.Is(err, ErrImmutableViolation) {
		t.Errorf("expected ErrImmutableViolation, got %v", err)
	}
	// Same content hash is idempotent.
	if err := s.PutManifest(ctx, rec1); err != nil {
		t.Errorf("idempotent re-ingest failed: %v", err)
	}
}

// Spec: §4.7.3 Reverse Dependency Index — DependentsOf returns every
// edge ending at the artifact.
func TestMemory_DependencyIndex(t *testing.T) {
	t.Parallel()
	s := NewMemory()
	ctx := context.Background()
	_ = s.PutDependency(ctx, "a", DependencyEdge{From: "child", To: "parent", Kind: "extends"})
	_ = s.PutDependency(ctx, "a", DependencyEdge{From: "agent1", To: "parent", Kind: "delegates_to"})

	got, err := s.DependentsOf(ctx, "a", "parent")
	if err != nil {
		t.Fatalf("DependentsOf: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d edges, want 2", len(got))
	}
}

// Spec: §4.7.2 Access control — admin grants persist and are queryable
// per (user, org) pair.
func TestMemory_AdminGrants(t *testing.T) {
	t.Parallel()
	s := NewMemory()
	ctx := context.Background()
	_ = s.GrantAdmin(ctx, AdminGrant{UserID: "joan", OrgID: "acme"})
	if ok, _ := s.IsAdmin(ctx, "joan", "acme"); !ok {
		t.Errorf("expected joan to be admin in acme")
	}
	if ok, _ := s.IsAdmin(ctx, "joan", "other"); ok {
		t.Errorf("admin grant leaked across orgs")
	}
}

// Spec: §8.4 (F-8.4.2) — the deprecated-version retention window is
// anchored to the moment "the deprecation flag is set". Deprecation is a
// per-version manifest field set at ingest, so stampDeprecation anchors
// DeprecatedAt to each deprecated version's own IngestedAt. A later
// "deprecation flip" (a new version ingested deprecated) anchors to its own
// later ingest time, not an earlier version's, so the 90-day window starts
// when that version's flag was set.
func TestStampDeprecation_AnchorsToPerVersionIngestTime(t *testing.T) {
	t.Parallel()

	// Not deprecated: never stamped.
	live := &ManifestRecord{IngestedAt: time.Now().UTC()}
	stampDeprecation(live)
	if live.DeprecatedAt != nil {
		t.Errorf("non-deprecated version was stamped: %v", live.DeprecatedAt)
	}

	// Born deprecated: DeprecatedAt == that version's IngestedAt (= flag-set
	// time).
	v1Ingest := time.Now().UTC().Add(-200 * 24 * time.Hour)
	v1 := &ManifestRecord{Deprecated: true, IngestedAt: v1Ingest}
	stampDeprecation(v1)
	if v1.DeprecatedAt == nil || !v1.DeprecatedAt.Equal(v1Ingest) {
		t.Errorf("born-deprecated DeprecatedAt = %v, want %v", v1.DeprecatedAt, v1Ingest)
	}

	// Deprecation flip: a newer version ingested deprecated anchors to its
	// own (later) ingest time, not v1's.
	v2Ingest := time.Now().UTC().Add(-1 * 24 * time.Hour)
	v2 := &ManifestRecord{Deprecated: true, IngestedAt: v2Ingest}
	stampDeprecation(v2)
	if v2.DeprecatedAt == nil || !v2.DeprecatedAt.Equal(v2Ingest) {
		t.Errorf("flip DeprecatedAt = %v, want %v", v2.DeprecatedAt, v2Ingest)
	}
	if v2.DeprecatedAt.Equal(*v1.DeprecatedAt) {
		t.Error("flipped version inherited the earlier version's deprecation anchor")
	}

	// Zero IngestedAt falls back to a non-zero stamp so the window still has
	// an anchor.
	noTime := &ManifestRecord{Deprecated: true}
	stampDeprecation(noTime)
	if noTime.DeprecatedAt == nil || noTime.DeprecatedAt.IsZero() {
		t.Errorf("zero-IngestedAt deprecated version got no anchor: %v", noTime.DeprecatedAt)
	}

	// An already-set DeprecatedAt is preserved (idempotent).
	preset := time.Now().UTC().Add(-10 * 24 * time.Hour)
	pre := &ManifestRecord{Deprecated: true, IngestedAt: v1Ingest, DeprecatedAt: &preset}
	stampDeprecation(pre)
	if !pre.DeprecatedAt.Equal(preset) {
		t.Errorf("preset DeprecatedAt overwritten: got %v, want %v", pre.DeprecatedAt, preset)
	}
}

// spec: §13.10 — ListAdminGrants enumerates a tenant's grants so
// migration (§13.4) can preserve them. Covers empty, ordering, and
// org-isolation corner cases across Memory and SQLite.
func TestListAdminGrants(t *testing.T) {
	t.Parallel()
	sqlitePath := filepath.Join(t.TempDir(), "grants.db")
	sq, err := OpenSQLite(sqlitePath)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	backends := map[string]Store{"memory": NewMemory(), "sqlite": sq}
	for name, s := range backends {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			// Empty: no grants for an org returns an empty slice.
			got, err := s.ListAdminGrants(ctx, "acme")
			if err != nil {
				t.Fatalf("ListAdminGrants empty: %v", err)
			}
			if len(got) != 0 {
				t.Fatalf("empty org grants = %d, want 0", len(got))
			}
			// Seed two grants in acme (out of order) and one in globex.
			_ = s.GrantAdmin(ctx, AdminGrant{UserID: "bob@acme.com", OrgID: "acme"})
			_ = s.GrantAdmin(ctx, AdminGrant{UserID: "alice@acme.com", OrgID: "acme"})
			_ = s.GrantAdmin(ctx, AdminGrant{UserID: "carol@globex.com", OrgID: "globex"})

			got, err = s.ListAdminGrants(ctx, "acme")
			if err != nil {
				t.Fatalf("ListAdminGrants acme: %v", err)
			}
			// Ordering: enumerated by UserID ascending.
			if len(got) != 2 || got[0].UserID != "alice@acme.com" || got[1].UserID != "bob@acme.com" {
				t.Fatalf("acme grants = %+v, want [alice, bob] ordered", got)
			}
			// Org isolation: globex grants do not leak into acme.
			for _, g := range got {
				if g.OrgID != "acme" {
					t.Errorf("grant %+v leaked from another org", g)
				}
			}
		})
	}
}
