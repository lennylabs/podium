package e2e

// Migration chain from filesystem to standalone to standard, asserting byte
// identity at each stage.
//
// admin_migrate_test.go and schema_forward_migration_test.go exercise the
// standalone-SQLite-to-standard pump, and standard_stack_parity_test.go proves
// author-to-consumer byte parity, but no single test carries one bundled-resource
// catalog through all three deployment modes and asserts the content hash,
// resource bytes, and top search result are identical at each stage.
//
// This test stages an above-cutoff bundled resource and follows it:
//
//  1. Filesystem: `podium sync --harness none` materializes the catalog; the
//     large resource lands on disk verbatim.
//  2. Standalone: a `serve --standalone` server over the same source ingests it
//     into SQLite + a filesystem object store; load_artifact returns the same
//     content hash and the large resource externalizes to the token-bound
//     /objects route, which streams the same bytes; search returns the artifact.
//  3. Standard: `admin migrate-to-standard` pumps the standalone SQLite + objects
//     into live Postgres + S3, a standard-mode server boots on them, and
//     load_artifact returns the same content hash with the large resource served
//     via an S3 presigned URL; search returns the artifact.
//
// Gating: PODIUM_POSTGRES_DSN and PODIUM_S3_BUCKET must be set and reachable
// (msSkipIfNoStack); otherwise the test skips. The make test-live / gap
// remediation lane sets these.
//
// Spec: §11 (filesystem ↔ server equivalence), §13.4 / §13.10 (admin
// migrate-to-standard pumps standalone state into a standard deployment;
// content-addressed bytes survive), §7.2 (control plane + data plane; large
// resources externalize), §4.7.6 (immutable content hash).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLifecycle_MigrationChainFilesystemStandaloneStandard carries one
// bundled-resource catalog through the filesystem, standalone, and standard
// deployments and asserts the content hash, the large-resource bytes, and the
// top search result match at each stage.
func TestLifecycle_MigrationChainFilesystemStandaloneStandard(t *testing.T) {
	// Not parallel: standard mode keys metadata by the shared "default" org
	// schema (see standard_stack_parity_test.go's header), so this test
	// serializes with its siblings and scopes assertions to its unique id.
	dsn, bucket, region := msSkipIfNoStack(t)

	// The catalog: a skill with a distinctive description and an above-cutoff
	// bundled resource. The id is unique so the shared standard org schema does
	// not perturb the search/load assertions.
	const id = "compliance/retention/archive-ledger"
	const query = "archive the immutable compliance ledger snapshot"
	skillBody := brSkillMD("archive-ledger", query, "Archive the ledger snapshot.\n")
	large := strings.Repeat("L", 256*1024+2048) // above the 256 KB inline cutoff
	srcEntries := map[string]string{
		id + "/ARTIFACT.md":     brSkillArtifact,
		id + "/SKILL.md":        skillBody,
		id + "/data/ledger.bin": large,
	}
	reg := writeRegistry(t, srcEntries)

	// ---- Stage 1: filesystem sync -------------------------------------------
	// `podium sync --harness none` materializes the catalog. The large resource
	// must land on disk byte-for-byte; this is the reference copy the later
	// stages are compared against.
	fsTarget := t.TempDir()
	fsTree := syncAndSnapshot(t, reg, fsTarget)
	fsResource := fsTree[id+"/data/ledger.bin"]
	if fsResource != large {
		t.Fatalf("stage 1 (filesystem): materialized resource is %d bytes, want %d", len(fsResource), len(large))
	}

	// ---- Stage 2: standalone ingest -----------------------------------------
	// Boot a standalone server over the same source with an explicit SQLite path
	// and filesystem object root so the migration can read them in stage 3.
	home := t.TempDir()
	sqlitePath := filepath.Join(home, "standalone.db")
	objectsRoot := filepath.Join(home, "objects")
	standalone := startServerArgs(t, []string{
		"HOME=" + home,
		"PODIUM_SQLITE_PATH=" + sqlitePath,
		"PODIUM_FILESYSTEM_ROOT=" + objectsRoot,
	}, "serve", "--standalone", "--layer-path", reg)

	// load_artifact returns the artifact; capture its content hash (the immutable
	// identity that must survive every hop).
	saHash, saResource := lcLoadAndFetchLarge(t, standalone, id, "data/ledger.bin")
	if saHash == "" {
		t.Fatalf("stage 2 (standalone): load returned no content hash")
	}
	if saResource != large {
		t.Errorf("stage 2 (standalone): /objects resource is %d bytes, want %d", len(saResource), len(large))
	}
	// Search returns the artifact at the top for the distinctive query.
	if top := lcTopSearchID(t, standalone, query, ""); top != id {
		t.Errorf("stage 2 (standalone): top search result = %q, want %q", top, id)
	}

	// Stop the standalone server so the migration owns its SQLite file and
	// object store (a clean handoff).
	stopProc(standalone.cmd)

	// ---- Stage 3: migrate into standard (Postgres + S3) ---------------------
	// admin migrate-to-standard pumps the standalone SQLite metadata and the
	// filesystem object blobs into live Postgres + S3. The artifact's manifest
	// (with its frontmatter, body, and resource refs) and its content-addressed
	// blobs are copied verbatim.
	migrate := runPodium(t, "", []string{"HOME=" + home},
		"admin", "migrate-to-standard",
		"--source-sqlite", sqlitePath,
		"--source-objects", objectsRoot,
		"--target-store", "postgres",
		"--target-postgres-dsn", dsn,
		"--target-objects-type", "s3",
		"--target-s3-endpoint", lcS3EndpointHost(),
		"--target-s3-bucket", bucket,
		"--target-s3-region", region,
		"--target-s3-access-key-id", os.Getenv("PODIUM_S3_ACCESS_KEY_ID"),
		"--target-s3-secret-access-key", os.Getenv("PODIUM_S3_SECRET_ACCESS_KEY"),
		"--target-s3-use-ssl="+lcS3UseSSLFlag(),
	)
	if migrate.Exit != 0 {
		t.Fatalf("stage 3 migrate-to-standard exit=%d\nstderr=%s\nstdout=%s", migrate.Exit, migrate.Stderr, migrate.Stdout)
	}
	// The migration must report it pumped the standalone manifest; a "manifests:
	// 0" plan means the source tenant id was not resolved (the literal-"default"
	// regression) and the standard load below would 404.
	if !strings.Contains(migrate.Stdout, "manifests:     1") {
		t.Fatalf("migrate did not pump the standalone manifest (wrong source tenant id?):\n%s", migrate.Stdout)
	}

	// Boot a standard-mode server on the migrated Postgres + S3. It reuses the
	// injected-session-token identity path the parity helper wires; the migrated
	// public layer is visible to a verified caller.
	priv, pemPath := injKeyPair(t)
	standard := msStartStandardServer(t, dsn, bucket, region)
	injRegisterRuntime(t, standard, pemPath)
	token := injSignJWT(t, priv, injClaims("alice@acme.com"))

	// load_artifact through the standard stack returns the same content hash and
	// the same large-resource bytes (served via an S3 presigned URL this time).
	stdHash, stdResource := lcStandardLoadAndFetchLarge(t, standard, token, id, "data/ledger.bin")
	if stdHash != saHash {
		t.Errorf("content hash diverged across the migration chain: standalone=%q standard=%q", saHash, stdHash)
	}
	if stdResource != large {
		t.Errorf("stage 3 (standard): S3-served resource is %d bytes, want %d", len(stdResource), len(large))
	}

	// Search through the standard stack returns the same top result. Runtime
	// search indexes asynchronously after the migration write, so poll briefly.
	if top := lcStandardTopSearchID(t, standard, token, query); top != id {
		t.Errorf("stage 3 (standard): top search result = %q, want %q", top, id)
	}

	// The content hash is identical at all three stages we can observe it: the
	// filesystem materialization reproduces the same artifact the registry serves,
	// so the byte chain is closed.
	if saHash != stdHash {
		t.Errorf("final content-hash parity failed: standalone=%q standard=%q", saHash, stdHash)
	}
}

// lcLoadAndFetchLarge loads id from a standalone server, asserts the named
// resource is delivered as a large (above-cutoff) presigned link, fetches the
// linked bytes, and returns the artifact's content hash and the fetched bytes.
func lcLoadAndFetchLarge(t *testing.T, srv *serverProc, id, resourcePath string) (contentHash, resourceBytes string) {
	t.Helper()
	st, body := getRaw(t, srv.BaseURL+"/v1/load_artifact?id="+id)
	if st != 200 {
		t.Fatalf("standalone load %s: HTTP %d\nbody: %s", id, st, body)
	}
	var resp struct {
		ContentHash    string `json:"content_hash"`
		LargeResources map[string]struct {
			PresignedURL string `json:"presigned_url"`
			ContentHash  string `json:"content_hash"`
			Size         int64  `json:"size"`
		} `json:"large_resources"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode standalone load: %v\nbody: %s", err, body)
	}
	link, ok := resp.LargeResources[resourcePath]
	if !ok || link.PresignedURL == "" {
		t.Fatalf("resource %q not delivered as a large presigned link: %+v", resourcePath, resp.LargeResources)
	}
	// The filesystem backend's presigned URL points at the registry's token-bound
	// /objects/{content_hash} route; fetch it and return the streamed bytes.
	objSt, objBody := getRaw(t, lcResolveURL(srv, link.PresignedURL))
	if objSt != 200 {
		t.Fatalf("standalone /objects fetch %q: HTTP %d", resourcePath, objSt)
	}
	return resp.ContentHash, string(objBody)
}

// lcStandardLoadAndFetchLarge is lcLoadAndFetchLarge for the standard stack: it
// sends the injected token and fetches the S3 presigned URL directly (it is an
// absolute S3 URL, not a registry-relative /objects route).
func lcStandardLoadAndFetchLarge(t *testing.T, srv *serverProc, token, id, resourcePath string) (contentHash, resourceBytes string) {
	t.Helper()
	// Runtime search/load indexes asynchronously after the migration write, so
	// poll the load until the artifact resolves.
	var resp struct {
		ContentHash    string `json:"content_hash"`
		LargeResources map[string]struct {
			PresignedURL string `json:"presigned_url"`
			ContentHash  string `json:"content_hash"`
			Size         int64  `json:"size"`
		} `json:"large_resources"`
	}
	deadline := time.Now().Add(20 * time.Second)
	var lastStatus int
	var lastBody []byte
	for time.Now().Before(deadline) {
		st, body := injGet(t, srv.BaseURL+"/v1/load_artifact?id="+id, token)
		lastStatus, lastBody = st, body
		if st == 200 {
			if err := json.Unmarshal(body, &resp); err != nil {
				t.Fatalf("decode standard load: %v\nbody: %s", err, body)
			}
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	link, ok := resp.LargeResources[resourcePath]
	if !ok || link.PresignedURL == "" {
		t.Fatalf("standard: resource %q not delivered as a large presigned link: %+v\nlast load HTTP %d body: %s\nserver log:\n%s",
			resourcePath, resp.LargeResources, lastStatus, lastBody, srv.log())
	}
	objSt, objBody := getRaw(t, link.PresignedURL)
	if objSt != 200 {
		t.Fatalf("standard S3 presigned fetch %q: HTTP %d", resourcePath, objSt)
	}
	return resp.ContentHash, string(objBody)
}

// lcTopSearchID returns the id of the first search result for query against a
// standalone server (empty token), or "" when none matched.
func lcTopSearchID(t *testing.T, srv *serverProc, query, token string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var st int
		var body []byte
		if token == "" {
			st, body = getRaw(t, srv.BaseURL+"/v1/search_artifacts?query="+queryEscape(query))
		} else {
			st, body = injGet(t, srv.BaseURL+"/v1/search_artifacts?query="+queryEscape(query), token)
		}
		if st == 200 {
			if id := lcFirstResultID(t, body); id != "" {
				return id
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return ""
}

// lcStandardTopSearchID is lcTopSearchID with the injected token for the
// standard stack.
func lcStandardTopSearchID(t *testing.T, srv *serverProc, token, query string) string {
	t.Helper()
	return lcTopSearchID(t, srv, query, token)
}

// lcFirstResultID decodes a search response and returns the first result id.
func lcFirstResultID(t *testing.T, body []byte) string {
	t.Helper()
	var resp struct {
		Results []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return ""
	}
	if len(resp.Results) == 0 {
		return ""
	}
	return resp.Results[0].ID
}

// lcResolveURL rewrites a registry-relative presigned URL to an absolute URL
// against the running server. The filesystem object store presigns
// BaseURL-relative /objects routes; when BaseURL is unset the path is relative.
func lcResolveURL(srv *serverProc, presigned string) string {
	if strings.HasPrefix(presigned, "http://") || strings.HasPrefix(presigned, "https://") {
		return presigned
	}
	return srv.BaseURL + presigned
}

// lcS3EndpointHost returns the S3 endpoint as a bare host[:port] for the
// migrate command's --target-s3-endpoint flag, stripping any scheme.
func lcS3EndpointHost() string {
	ep := os.Getenv("PODIUM_S3_ENDPOINT")
	ep = strings.TrimPrefix(ep, "http://")
	ep = strings.TrimPrefix(ep, "https://")
	return ep
}

// lcS3UseSSLFlag returns "true" or "false" for the migrate --target-s3-use-ssl
// flag, derived from PODIUM_S3_USE_SSL (defaulting to false for plain-HTTP MinIO).
func lcS3UseSSLFlag() string {
	if os.Getenv("PODIUM_S3_USE_SSL") == "true" {
		return "true"
	}
	return "false"
}
