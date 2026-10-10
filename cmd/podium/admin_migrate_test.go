package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/store"
)

// captureStderr redirects os.Stderr for the duration of fn and returns
// the captured bytes.
//
// os.Stderr is a process-wide variable, so this swap is visible to every
// goroutine. The commands under test write their diagnostics straight to it,
// which is why capturing requires the swap rather than an injected writer.
// Nothing in this package may therefore run in parallel: a test running
// alongside a capture either loses its output into the capturing test's pipe
// or reads os.Stderr while the swap writes it, which the race detector
// reports as a data race and which surfaces in CI as an intermittent failure
// in whichever test loses the interleaving. TestNoParallelTests guards the
// rule.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	done := make(chan []byte, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.Bytes()
	}()
	defer func() {
		os.Stderr = orig
		_ = r.Close()
	}()
	fn()
	_ = w.Close()
	return string(<-done)
}

// Spec: §13.4 — parseObjectStoreURL maps the short-form URL onto a
// target object-store kind and config.
func TestParseObjectStoreURL(t *testing.T) {
	t.Run("filesystem file scheme", func(t *testing.T) {
		kind, root, _, err := parseObjectStoreURL("file:///srv/objects", "us-east-1", "", "", true)
		if err != nil || kind != "filesystem" || root != "/srv/objects" {
			t.Fatalf("got kind=%q root=%q err=%v", kind, root, err)
		}
	})
	t.Run("bare path", func(t *testing.T) {
		kind, root, _, err := parseObjectStoreURL("/var/objects", "us-east-1", "", "", true)
		if err != nil || kind != "filesystem" || root != "/var/objects" {
			t.Fatalf("got kind=%q root=%q err=%v", kind, root, err)
		}
	})
	t.Run("s3 with creds region ssl", func(t *testing.T) {
		kind, _, s3, err := parseObjectStoreURL("s3://key:secret@minio:9000/podium?region=eu-west-1&ssl=false", "us-east-1", "", "", true)
		if err != nil || kind != "s3" {
			t.Fatalf("kind=%q err=%v", kind, err)
		}
		if s3.Endpoint != "minio:9000" || s3.Bucket != "podium" {
			t.Errorf("endpoint=%q bucket=%q", s3.Endpoint, s3.Bucket)
		}
		if s3.Region != "eu-west-1" || s3.AccessKeyID != "key" || s3.SecretAccessKey != "secret" || s3.UseSSL {
			t.Errorf("region=%q ak=%q ssl=%v", s3.Region, s3.AccessKeyID, s3.UseSSL)
		}
	})
	t.Run("s3 falls back to defaults", func(t *testing.T) {
		_, _, s3, err := parseObjectStoreURL("s3://minio:9000/podium", "us-east-1", "akid", "skey", true)
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		if s3.Region != "us-east-1" || s3.AccessKeyID != "akid" || s3.SecretAccessKey != "skey" || !s3.UseSSL {
			t.Errorf("fallbacks not applied: %+v", s3)
		}
	})
	t.Run("s3 missing bucket errors", func(t *testing.T) {
		if _, _, _, err := parseObjectStoreURL("s3://minio:9000", "", "", "", true); err == nil {
			t.Error("want error for missing bucket")
		}
	})
	t.Run("unsupported scheme errors", func(t *testing.T) {
		if _, _, _, err := parseObjectStoreURL("gcs://bucket", "", "", "", true); err == nil {
			t.Error("want error for unsupported scheme")
		}
	})
}

// Spec: §13.10 — admin migrate-to-standard pumps standalone
// metadata + objects into a target deployment. The Tier 1 test
// uses SQLite-to-SQLite and filesystem-to-filesystem so live
// Postgres / S3 are not required for CI; the pump path is
// identical regardless of backend.
func TestAdminMigrateToStandard_PumpsMetadataAndObjects(t *testing.T) {
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
	// Spec: §7.3.1 (Repository credentials) — a second layer whose repo carries
	// a URL credential, which the source store splits on read.
	const credRepo = "https://alice-user:s3cr3tpw@git.acme.com/acme/private.git"
	if err := src.PutLayerConfig(context.Background(), store.LayerConfig{
		TenantID: "default", ID: "team-private", SourceType: "git", Repo: credRepo, Ref: "main",
	}); err != nil {
		t.Fatalf("PutLayerConfig team-private: %v", err)
	}
	srcPrivate, err := src.GetLayerConfig(context.Background(), "default", "team-private")
	if err != nil {
		t.Fatalf("GetLayerConfig team-private: %v", err)
	}
	if !srcPrivate.RegisteredRepo.Present() || srcPrivate.CloneRepo() != credRepo {
		t.Fatalf("source team-private = Repo %q, CloneRepo %q; want a split of the registered URL",
			srcPrivate.Repo, srcPrivate.CloneRepo())
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

	var (
		rc     int
		stderr string
	)
	stdout := captureStdout(t, func() {
		stderr = captureStderr(t, func() {
			rc = adminMigrateToStandard([]string{
				"--source-sqlite", srcDB,
				"--source-objects", srcObjs,
				"--source-audit-log", srcAudit,
				"--target-store", "sqlite",
				"--target-sqlite", dstDB,
				"--target-objects-type", "filesystem",
				"--target-objects", dstObjs,
				"--target-audit-log", dstAudit,
			})
		})
	})
	if rc != 0 {
		t.Fatalf("rc = %d, want 0\nstdout: %s\nstderr: %s", rc, stdout, stderr)
	}
	// The plan line is the proof that the capture covers the command's output,
	// so the absence checks below do not pass on an empty capture.
	if !strings.Contains(stdout, "layer configs: 2") {
		t.Errorf("stdout does not carry the source plan with two layer configs: %s", stdout)
	}
	for _, part := range []string{"alice-user", "s3cr3tpw"} {
		if strings.Contains(stdout+stderr, part) {
			t.Errorf("command output carries the credential part %q:\nstdout: %s\nstderr: %s", part, stdout, stderr)
		}
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
	if len(layers) != 2 {
		t.Fatalf("layers = %+v, want team-shared and team-private", layers)
	}
	byID := map[string]store.LayerConfig{}
	for _, l := range layers {
		byID[l.ID] = l
	}
	if got := byID["team-shared"].CloneRepo(); got != "git@example/team.git" {
		t.Errorf("target team-shared CloneRepo = %q, want git@example/team.git", got)
	}
	// The target clones with the same registered URL as the source and reports
	// the same Repo.
	dstPrivate := byID["team-private"]
	if dstPrivate.CloneRepo() != srcPrivate.CloneRepo() || dstPrivate.Repo != srcPrivate.Repo {
		t.Errorf("target team-private = Repo %q, CloneRepo %q; want the source's %q and %q",
			dstPrivate.Repo, dstPrivate.CloneRepo(), srcPrivate.CloneRepo(), srcPrivate.Repo)
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

// Spec: §13.10 / §4.7.1 — a real standalone source keys every row by the
// deterministic UUIDv5 of the "default" org, not the literal string "default".
// The migration must resolve that tenant id and pump its manifests; probing only
// the literal id silently migrates zero metadata (blobs copy, manifests do not).
func TestAdminMigrateToStandard_MigratesStandaloneUUIDTenant(t *testing.T) {
	dir := t.TempDir()
	srcDB := filepath.Join(dir, "source.db")
	dstDB := filepath.Join(dir, "dest.db")

	// Seed the source the way a running standalone server does: under the UUID
	// tenant id, not the literal "default".
	uuidTenant := standaloneDefaultOrgID()
	if uuidTenant == "default" {
		t.Fatal("standaloneDefaultOrgID returned the literal default; the UUID derivation is wrong")
	}
	src, err := store.OpenSQLite(srcDB)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	if err := src.CreateTenant(context.Background(), store.Tenant{ID: uuidTenant, Name: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if err := src.PutManifest(context.Background(), store.ManifestRecord{
		TenantID: uuidTenant, ArtifactID: "team/standalone-skill", Version: "1.0.0",
		ContentHash: "sha256:uuidtenant", Type: "skill", Layer: "team-shared",
	}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}
	_ = src.Close()

	rc := adminMigrateToStandard([]string{
		"--source-sqlite", srcDB,
		"--target-store", "sqlite",
		"--target-sqlite", dstDB,
	})
	if rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}

	// The target carries the manifest keyed by the same UUID tenant; resolving
	// only the literal "default" would have left it empty.
	dst, err := store.OpenSQLite(dstDB)
	if err != nil {
		t.Fatalf("open dest: %v", err)
	}
	t.Cleanup(func() { _ = dst.Close() })
	manifests, err := dst.ListManifests(context.Background(), uuidTenant)
	if err != nil {
		t.Fatalf("ListManifests on target: %v", err)
	}
	if len(manifests) != 1 || manifests[0].ArtifactID != "team/standalone-skill" {
		t.Errorf("target manifests = %+v, want one team/standalone-skill under the UUID tenant", manifests)
	}
}

// Spec: §13.10 — --dry-run reports the plan and writes nothing.
func TestAdminMigrateToStandard_DryRunWritesNothing(t *testing.T) {
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
	// No target DSN: the postgres-target default leaves --postgres unset.
	if rc := adminMigrateToStandard([]string{}); rc != 2 {
		t.Errorf("rc = %d, want 2 (missing --postgres)", rc)
	}
	if rc := adminMigrateToStandard([]string{
		"--source-sqlite", "/nonexistent.db",
		"--target-store", "postgres",
	}); rc != 2 {
		t.Errorf("rc = %d, want 2 (missing --target-postgres-dsn)", rc)
	}
	// sqlite target without a target path is an argument error.
	if rc := adminMigrateToStandard([]string{
		"--source-sqlite", "/nonexistent.db",
		"--target-store", "sqlite",
	}); rc != 2 {
		t.Errorf("rc = %d, want 2 (missing --target-sqlite)", rc)
	}
}

// Spec: §13.4 / §13.10 — the documented short form uses --postgres and
// --object-store; --object-store=file://... selects the filesystem
// backend, and --postgres folds into the postgres target DSN.
func TestAdminMigrateToStandard_SpecShortFormFlags(t *testing.T) {
	dir := t.TempDir()
	srcDB := filepath.Join(dir, "source.db")
	dstDB := filepath.Join(dir, "dest.db")
	srcObjs := filepath.Join(dir, "src-objs")
	dstObjs := filepath.Join(dir, "dst-objs")

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
	if err := os.MkdirAll(srcObjs, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	srcStore, err := objectstore.Open(srcObjs)
	if err != nil {
		t.Fatalf("objectstore.Open: %v", err)
	}
	if err := srcStore.Put(context.Background(), "blob-1", []byte("hi"), "text/plain"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Spec short form: --object-store selects the filesystem backend via
	// a file:// URL; --target-store=sqlite stands in for --postgres so
	// the test needs no live Postgres.
	rc := adminMigrateToStandard([]string{
		"--source-sqlite", srcDB,
		"--source-objects", srcObjs,
		"--target-store", "sqlite",
		"--target-sqlite", dstDB,
		"--object-store", "file://" + dstObjs,
	})
	if rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}

	dstStore, err := objectstore.Open(dstObjs)
	if err != nil {
		t.Fatalf("dst objectstore: %v", err)
	}
	body, err := dstStore.Get(context.Background(), "blob-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(body) != "hi" {
		t.Errorf("blob = %q, want hi", body)
	}
}

// Spec: §13.10 — "admin grants ... are preserved". The pump path must
// carry every source grant to the target deployment.
func TestAdminMigrateToStandard_PreservesAdminGrants(t *testing.T) {
	dir := t.TempDir()
	srcDB := filepath.Join(dir, "source.db")
	dstDB := filepath.Join(dir, "dest.db")

	src, err := store.OpenSQLite(srcDB)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	if err := src.CreateTenant(context.Background(), store.Tenant{ID: "default", Name: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	for _, u := range []string{"alice@acme.com", "bob@acme.com"} {
		if err := src.GrantAdmin(context.Background(), store.AdminGrant{UserID: u, OrgID: "default"}); err != nil {
			t.Fatalf("GrantAdmin %s: %v", u, err)
		}
	}

	rc := adminMigrateToStandard([]string{
		"--source-sqlite", srcDB,
		"--target-store", "sqlite",
		"--target-sqlite", dstDB,
	})
	if rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}

	dst, err := store.OpenSQLite(dstDB)
	if err != nil {
		t.Fatalf("open dest: %v", err)
	}
	for _, u := range []string{"alice@acme.com", "bob@acme.com"} {
		ok, err := dst.IsAdmin(context.Background(), u, "default")
		if err != nil {
			t.Fatalf("IsAdmin %s: %v", u, err)
		}
		if !ok {
			t.Errorf("admin grant for %s was not preserved on the target", u)
		}
	}
	grants, err := dst.ListAdminGrants(context.Background(), "default")
	if err != nil {
		t.Fatalf("ListAdminGrants: %v", err)
	}
	if len(grants) != 2 {
		t.Errorf("target grants = %d, want 2", len(grants))
	}
}

// Spec: §13.10 — audit history is preserved. When a source audit log is
// named but no target sink is given, the command must warn rather than
// silently completing with the history left behind.
func TestAdminMigrateToStandard_AuditWarnsWithoutTarget(t *testing.T) {
	dir := t.TempDir()
	srcDB := filepath.Join(dir, "source.db")
	dstDB := filepath.Join(dir, "dest.db")
	srcAudit := filepath.Join(dir, "src-audit.log")

	src, _ := store.OpenSQLite(srcDB)
	_ = src.CreateTenant(context.Background(), store.Tenant{ID: "default"})
	if err := os.WriteFile(srcAudit, []byte("audit line\n"), 0o644); err != nil {
		t.Fatalf("WriteFile audit: %v", err)
	}

	stderr := captureStderr(t, func() {
		rc := adminMigrateToStandard([]string{
			"--source-sqlite", srcDB,
			"--source-audit-log", srcAudit,
			"--target-store", "sqlite",
			"--target-sqlite", dstDB,
		})
		if rc != 0 {
			t.Fatalf("rc = %d, want 0", rc)
		}
	})
	if !strings.Contains(stderr, "audit history") || !strings.Contains(stderr, "NOT copied") {
		t.Errorf("stderr = %q, want an audit-not-copied warning", stderr)
	}
}

// Spec: §13.4 — the command clears the target store's record of the
// content-hash rewrite before it copies the first manifest row, so the rewrite
// runs again over the copied rows, at the target's next start or through
// sign-stored-rows. A run that copies no manifest row,
// and a --dry-run, leave the record as it was.
func TestAdminMigrateToStandard_ClearsTheTargetMarker(t *testing.T) {
	ctx := context.Background()
	newTarget := func(t *testing.T, path string) {
		t.Helper()
		target, err := store.OpenSQLite(path)
		if err != nil {
			t.Fatalf("open target: %v", err)
		}
		defer func() { _ = target.Close() }()
		if err := target.SetDataMigrationApplied(ctx, store.DataMigrationContentHashFraming, true); err != nil {
			t.Fatalf("set marker: %v", err)
		}
	}
	targetMarker := func(t *testing.T, path string) bool {
		t.Helper()
		target, err := store.OpenSQLite(path)
		if err != nil {
			t.Fatalf("open target: %v", err)
		}
		defer func() { _ = target.Close() }()
		applied, err := target.DataMigrationApplied(ctx, store.DataMigrationContentHashFraming)
		if err != nil {
			t.Fatalf("read marker: %v", err)
		}
		return applied
	}
	newSource := func(t *testing.T, path string, withManifest bool) {
		t.Helper()
		src, err := store.OpenSQLite(path)
		if err != nil {
			t.Fatalf("open source: %v", err)
		}
		defer func() { _ = src.Close() }()
		if err := src.CreateTenant(ctx, store.Tenant{ID: "default", Name: "default"}); err != nil {
			t.Fatalf("CreateTenant: %v", err)
		}
		if !withManifest {
			return
		}
		if err := src.PutManifest(ctx, store.ManifestRecord{
			TenantID: "default", ArtifactID: "alpha", Version: "1.0.0",
			ContentHash: "sha256:abc", Type: "skill", Layer: "team",
		}); err != nil {
			t.Fatalf("PutManifest: %v", err)
		}
	}
	migrate := func(srcDB, dstDB string, extra ...string) int {
		args := []string{"--source-sqlite", srcDB, "--target-store", "sqlite", "--target-sqlite", dstDB}
		return adminMigrateToStandard(append(args, extra...))
	}

	t.Run("a copied manifest row clears the marker", func(t *testing.T) {
		dir := t.TempDir()
		srcDB, dstDB := filepath.Join(dir, "src.db"), filepath.Join(dir, "dst.db")
		newSource(t, srcDB, true)
		newTarget(t, dstDB)
		if rc := migrate(srcDB, dstDB); rc != 0 {
			t.Fatalf("rc = %d, want 0", rc)
		}
		if targetMarker(t, dstDB) {
			t.Error("the target's marker is still set after a run that copied a manifest row")
		}
	})

	t.Run("dry run leaves the marker set", func(t *testing.T) {
		dir := t.TempDir()
		srcDB, dstDB := filepath.Join(dir, "src.db"), filepath.Join(dir, "dst.db")
		newSource(t, srcDB, true)
		newTarget(t, dstDB)
		if rc := migrate(srcDB, dstDB, "--dry-run"); rc != 0 {
			t.Fatalf("rc = %d, want 0", rc)
		}
		if !targetMarker(t, dstDB) {
			t.Error("--dry-run cleared the target's marker")
		}
	})

	t.Run("no manifest row leaves the marker set", func(t *testing.T) {
		dir := t.TempDir()
		srcDB, dstDB := filepath.Join(dir, "src.db"), filepath.Join(dir, "dst.db")
		newSource(t, srcDB, false)
		newTarget(t, dstDB)
		if rc := migrate(srcDB, dstDB); rc != 0 {
			t.Fatalf("rc = %d, want 0", rc)
		}
		if !targetMarker(t, dstDB) {
			t.Error("a run that copied no manifest row cleared the target's marker")
		}
	})
}

// markerRecorder records the order of the target writes pumpStore makes and can
// fail either of them, which is what pins the clear-before-copy order.
type markerRecorder struct {
	store.Store
	calls      []string
	markerErr  error
	putErrOn   int
	putErr     error
	putCounter int
}

func (m *markerRecorder) SetDataMigrationApplied(ctx context.Context, name string, applied bool) error {
	m.calls = append(m.calls, "marker")
	if m.markerErr != nil {
		return m.markerErr
	}
	return m.Store.SetDataMigrationApplied(ctx, name, applied)
}

func (m *markerRecorder) PutManifest(ctx context.Context, rec store.ManifestRecord) error {
	m.calls = append(m.calls, "put")
	m.putCounter++
	if m.putErr != nil && m.putCounter == m.putErrOn {
		return m.putErr
	}
	return m.Store.PutManifest(ctx, rec)
}

// Spec: §13.4 — pumpStore clears the target's record of the content-hash
// rewrite before its first PutManifest, so a pump that fails partway leaves the
// target unmarked and the rewrite runs again over whatever was copied, at the
// target's next start or through sign-stored-rows.
func TestPumpStore_ClearsTheMarkerBeforeTheFirstWrite(t *testing.T) {
	ctx := context.Background()
	newPlan := func() *migrationPlan {
		return &migrationPlan{
			tenants: []store.Tenant{{ID: "default", Name: "default"}},
			manifests: map[string][]store.ManifestRecord{"default": {
				{TenantID: "default", ArtifactID: "alpha", Version: "1.0.0", ContentHash: "sha256:a", Type: "skill", Layer: "team"},
				{TenantID: "default", ArtifactID: "beta", Version: "1.0.0", ContentHash: "sha256:b", Type: "skill", Layer: "team"},
			}},
			manifestCount: 2,
			layerConfigs:  map[string][]store.LayerConfig{},
			adminGrants:   map[string][]store.AdminGrant{},
		}
	}
	newTarget := func(t *testing.T) *store.Memory {
		t.Helper()
		mem := store.NewMemory()
		if err := mem.SetDataMigrationApplied(ctx, store.DataMigrationContentHashFraming, true); err != nil {
			t.Fatalf("set marker: %v", err)
		}
		return mem
	}

	t.Run("a failed clear stops the copy", func(t *testing.T) {
		target := &markerRecorder{Store: newTarget(t), markerErr: errors.New("connection refused")}
		err := pumpStore(newPlan(), target)
		if err == nil || !strings.Contains(err.Error(), "clear data-migration marker:") {
			t.Fatalf("pumpStore = %v, want an error naming the marker clear", err)
		}
		for _, call := range target.calls {
			if call == "put" {
				t.Error("a manifest row was written after the clear failed")
			}
		}
	})

	t.Run("the clear precedes the first copy", func(t *testing.T) {
		mem := newTarget(t)
		target := &markerRecorder{Store: mem, putErrOn: 2, putErr: errors.New("disk full")}
		err := pumpStore(newPlan(), target)
		if err == nil || !strings.Contains(err.Error(), "put manifest") {
			t.Fatalf("pumpStore = %v, want the write error", err)
		}
		if len(target.calls) < 2 || target.calls[0] != "marker" || target.calls[1] != "put" {
			t.Errorf("call order = %v, want the marker clear before the first put", target.calls)
		}
		applied, err := mem.DataMigrationApplied(ctx, store.DataMigrationContentHashFraming)
		if err != nil || applied {
			t.Errorf("marker applied = %v (err %v), want false after a partial pump", applied, err)
		}
	})
}
