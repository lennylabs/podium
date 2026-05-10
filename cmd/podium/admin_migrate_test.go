package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §13.10 — admin migrate-to-standard pumps standalone
// metadata + objects into a target deployment. The Tier 1 test
// uses SQLite-to-SQLite and filesystem-to-filesystem so live
// Postgres / S3 are not required for CI; the pump path is
// identical regardless of backend.
func TestAdminMigrateToStandard_PumpsMetadataAndObjects(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	srcDB := filepath.Join(dir, "source.db")
	dstDB := filepath.Join(dir, "dest.db")
	srcObjs := filepath.Join(dir, "src-objs")
	dstObjs := filepath.Join(dir, "dst-objs")
	srcAudit := filepath.Join(dir, "src-audit.log")
	dstAudit := filepath.Join(dir, "dst-audit.log")

	// Seed source: tenant + a manifest + a layer config + an
	// object blob + an audit log file.
	src, err := store.OpenSQLite(srcDB)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	if err := src.CreateTenant(context.Background(), store.Tenant{ID: "default", Name: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if err := src.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: "default", ArtifactID: "alpha/skill-1", Version: "1.0.0",
		ContentHash: "sha256:abc", Type: "skill", Layer: "team-shared",
	}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	if err := src.PutLayerConfig(context.Background(), store.LayerConfig{
		TenantID: "default", ID: "team-shared", SourceType: "git",
		Repo: "git@example/team.git", Ref: "main",
	}); err != nil {
		t.Fatalf("PutLayerConfig: %v", err)
	}
	if err := os.MkdirAll(srcObjs, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	srcStore, err := objectstore.Open(srcObjs)
	if err != nil {
		t.Fatalf("objectstore.Open: %v", err)
	}
	if err := srcStore.Put(context.Background(), "blob-1", []byte("hello"), "text/plain"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := os.WriteFile(srcAudit, []byte("audit line\n"), 0o644); err != nil {
		t.Fatalf("WriteFile audit: %v", err)
	}

	rc := adminMigrateToStandard([]string{
		"--source-sqlite", srcDB,
		"--source-objects", srcObjs,
		"--source-audit-log", srcAudit,
		"--target-store", "sqlite",
		"--target-sqlite", dstDB,
		"--target-objects-type", "filesystem",
		"--target-objects", dstObjs,
		"--target-audit-log", dstAudit,
	})
	if rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}

	// Assert: target SQLite carries the manifest + layer.
	dst, err := store.OpenSQLite(dstDB)
	if err != nil {
		t.Fatalf("open dest: %v", err)
	}
	manifests, err := dst.ListManifests(context.Background(), "default")
	if err != nil {
		t.Fatalf("ListManifests: %v", err)
	}
	if len(manifests) != 1 || manifests[0].ArtifactID != "alpha/skill-1" {
		t.Errorf("manifests = %+v, want one alpha/skill-1", manifests)
	}
	layers, err := dst.ListLayerConfigs(context.Background(), "default")
	if err != nil {
		t.Fatalf("ListLayerConfigs: %v", err)
	}
	if len(layers) != 1 || layers[0].ID != "team-shared" {
		t.Errorf("layers = %+v, want one team-shared", layers)
	}

	// Assert: target object store carries the blob.
	dstStore, err := objectstore.Open(dstObjs)
	if err != nil {
		t.Fatalf("dst objectstore: %v", err)
	}
	body, err := dstStore.Get(context.Background(), "blob-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(body) != "hello" {
		t.Errorf("blob bytes = %q, want hello", body)
	}

	// Assert: audit log copied byte-for-byte.
	got, err := os.ReadFile(dstAudit)
	if err != nil {
		t.Fatalf("read dst audit: %v", err)
	}
	if string(got) != "audit line\n" {
		t.Errorf("audit log = %q, want passthrough", got)
	}
}

// Spec: §13.10 — --dry-run reports the plan and writes nothing.
func TestAdminMigrateToStandard_DryRunWritesNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	srcDB := filepath.Join(dir, "source.db")
	dstDB := filepath.Join(dir, "dest.db")
	src, _ := store.OpenSQLite(srcDB)
	_ = src.CreateTenant(context.Background(), store.Tenant{ID: "default"})

	rc := adminMigrateToStandard([]string{
		"--source-sqlite", srcDB,
		"--target-store", "sqlite",
		"--target-sqlite", dstDB,
		"--dry-run",
	})
	if rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
	if _, err := os.Stat(dstDB); err == nil {
		t.Errorf("dry-run created target SQLite at %s", dstDB)
	}
}

// Spec: §6.10 — missing required args surface as argument errors.
func TestAdminMigrateToStandard_MissingArgs(t *testing.T) {
	t.Parallel()
	if rc := adminMigrateToStandard([]string{}); rc != 2 {
		t.Errorf("rc = %d, want 2 (missing --source-sqlite)", rc)
	}
	if rc := adminMigrateToStandard([]string{
		"--source-sqlite", "/nonexistent.db",
		"--target-store", "postgres",
	}); rc != 2 {
		t.Errorf("rc = %d, want 2 (missing --target-postgres-dsn)", rc)
	}
}
