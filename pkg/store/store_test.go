package store

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
)

// Spec: §4.7.1 Tenancy — tenants isolate manifest visibility; a
// manifest stored under tenant A is invisible from tenant B.
// Phase: 5
func TestMemory_TenantIsolation(t *testing.T) {
	testharness.RequirePhase(t, 5)
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
// Phase: 5
func TestMemory_ImmutabilityInvariant(t *testing.T) {
	testharness.RequirePhase(t, 5)
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
// Phase: 5
func TestMemory_DependencyIndex(t *testing.T) {
	testharness.RequirePhase(t, 5)
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
// Phase: 5
func TestMemory_AdminGrants(t *testing.T) {
	testharness.RequirePhase(t, 5)
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
