package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
)

// Spec: §13.10 Standalone Deployment — the SQLite backend persists
// across opens of the same file path.
// Phase: 5
func TestSQLite_PersistsAcrossOpens(t *testing.T) {
	testharness.RequirePhase(t, 5)
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "podium.db")

	s1, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	ctx := context.Background()
	if err := s1.CreateTenant(ctx, Tenant{ID: "a", Name: "acme"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	rec := ManifestRecord{
		TenantID: "a", ArtifactID: "x", Version: "1.0.0",
		ContentHash: "sha:1", Type: "skill",
		Tags: []string{"finance", "ap"},
	}
	if err := s1.PutManifest(ctx, rec); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite again: %v", err)
	}
	defer s2.Close()

	got, err := s2.GetManifest(ctx, "a", "x", "1.0.0")
	if err != nil {
		t.Fatalf("GetManifest after reopen: %v", err)
	}
	if got.ContentHash != "sha:1" {
		t.Errorf("ContentHash = %q, want sha:1", got.ContentHash)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "finance" || got.Tags[1] != "ap" {
		t.Errorf("Tags = %v, want [finance ap]", got.Tags)
	}

	tenant, err := s2.GetTenant(ctx, "a")
	if err != nil {
		t.Fatalf("GetTenant: %v", err)
	}
	if tenant.Name != "acme" {
		t.Errorf("Name = %q, want acme", tenant.Name)
	}
}

// Spec: §13.10 — schema apply is idempotent (re-opening an existing
// database does not error and preserves data).
// Phase: 5
func TestSQLite_SchemaIsIdempotent(t *testing.T) {
	testharness.RequirePhase(t, 5)
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "podium.db")

	for i := 0; i < 3; i++ {
		s, err := OpenSQLite(path)
		if err != nil {
			t.Fatalf("OpenSQLite #%d: %v", i, err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close #%d: %v", i, err)
		}
	}
}
