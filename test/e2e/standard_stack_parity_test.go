package e2e

// Managed-stack author-to-consumer parity end-to-end.
//
// standard_deployment_test.go documents docs/deployment/organization.md, but
// every server it boots is `serve --standalone` over SQLite metadata plus a
// filesystem object store, and its identity coverage is MCP-only. None boots
// the registry in standard mode (Postgres metadata + S3 object store) with the
// injected-session-token identity provider, then runs the full author journey
// (layer ingest) and the authenticated consumer journey (search, load,
// materialize) through that managed stack.
//
// This file boots one standard-mode server against live Postgres + live S3 and
// asserts that the managed stack produces the same observable result the
// standalone path does:
//
//   - The author ingests a bundled-resource layer through the standard-mode
//     server, so manifests land in Postgres and resource bytes land in S3.
//   - The authenticated consumer (a JWT signed by a registered runtime key)
//     searches the control plane and loads the artifact, with a negative
//     control proving an unverifiable caller is rejected before visibility is
//     consulted.
//   - The consumer materializes the artifact through `podium sync` against the
//     standard-mode server and the result is byte-identical to materializing
//     the same registry through the standalone/filesystem path.
//
// Gating: PODIUM_POSTGRES_DSN and PODIUM_S3_BUCKET must be set and the backends
// reachable (PODIUM_S3_REGION defaults to us-east-1); otherwise the test skips
// cleanly. The
// `make test-live` / gap-remediation lane sets these; a plain `go test ./...`
// with no live stack skips.
//
// Isolation: standard mode keys metadata by a per-org Postgres schema, and the
// org name is fixed to "default" with a deterministic UUID, so the org schema
// is shared and cannot be isolated per test. The pkg/store Postgres conformance
// suite drops every org schema between sub-tests. This test therefore does not
// run in parallel (so the e2e package serializes it) and the live lane is
// responsible for not pointing the metadata store at a DSN that a concurrent
// pkg/store conformance run is wiping. The S3 object store is content-hash
// keyed, so concurrent writers there are idempotent.
//
// Spec: §13.10 (standalone or standard deployment: Postgres metadata + S3
// object store, no zero-flag standalone bootstrap), §13.12 (PODIUM_REGISTRY_STORE
// / PODIUM_S3_* backend configuration; PODIUM_S3_REGION required), §7.2 (control
// plane HTTP/JSON metadata API and data plane object storage), §4.7 / §4.7.1
// (Registry as a Service; per-tenant isolation), §6.3.2 (injected-session-token
// runtime trust model).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// msSkipIfNoStack skips the test unless a live Postgres DSN and the S3 bucket
// are configured. It returns the resolved DSN, bucket, and region. serverboot
// requires a region for the s3 backend (§13.12), but no CI lane
// exports PODIUM_S3_REGION, so it defaults to us-east-1 to match the sibling
// pkg/objectstore/s3_live_test.go and run wherever the bucket and DSN are set.
// The remaining S3 connection vars (endpoint, credentials, path-style, ssl) are
// read where the server boot env is assembled.
func msSkipIfNoStack(t *testing.T) (dsn, bucket, region string) {
	t.Helper()
	dsn = firstEnv("PODIUM_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PODIUM_POSTGRES_DSN unset; skipping managed-stack parity e2e")
	}
	bucket = os.Getenv("PODIUM_S3_BUCKET")
	if bucket == "" {
		t.Skip("PODIUM_S3_BUCKET unset; skipping managed-stack parity e2e")
	}
	region = os.Getenv("PODIUM_S3_REGION")
	if region == "" {
		region = "us-east-1"
	}
	return dsn, bucket, region
}

// msStartStandardServer boots a registry in standard mode: Postgres metadata
// (PODIUM_REGISTRY_STORE=postgres) plus an S3 object store (PODIUM_OBJECT_STORE=s3)
// plus the injected-session-token identity provider. There is no --standard
// flag; the mode is implied by the explicit backend config, and the bare `serve`
// subcommand (no --standalone) lets serverboot resolve the configured backends.
// It registers the runtime signing key and returns the running server.
//
// The audience env (PODIUM_OAUTH_AUDIENCE) and the installed request-time
// verifier are required: serverboot's injectedTokenAudienceGuard fails closed
// without the audience, and identityVisibilityGuard fails closed if the selected
// provider has no verifier, so a successful boot here is itself evidence the
// managed identity path is wired (§6.3.2).
func msStartStandardServer(t *testing.T, dsn, bucket, region string) *serverProc {
	t.Helper()
	return msStartStandardServerEnv(t, dsn, bucket, region)
}

// msStartStandardServerEnv is msStartStandardServer with extra environment
// appended after the standard-mode backend config (last write wins).
// It is used to set PODIUM_PRESIGN_TTL_SECONDS so an S3 presigned URL expires
// inside the test and the 403-driven refresh can be exercised against the live
// data plane (§6.2 / §6.6).
func msStartStandardServerEnv(t *testing.T, dsn, bucket, region string, extraEnv ...string) *serverProc {
	t.Helper()
	emb := semanticMockEmbedder(t)
	env := []string{
		"HOME=" + t.TempDir(),
		"PODIUM_REGISTRY_STORE=postgres",
		"PODIUM_POSTGRES_DSN=" + dsn,
		"PODIUM_OBJECT_STORE=s3",
		"PODIUM_S3_BUCKET=" + bucket,
		"PODIUM_S3_REGION=" + region,
		// §13.12: the S3 endpoint is a URL whose scheme selects TLS (http
		// disables it, https or a bare host enables it). There is no separate
		// USE_SSL knob, so the scheme is baked into the endpoint here; a bare
		// host would default to TLS and fail against plain-HTTP MinIO.
		"PODIUM_S3_ENDPOINT=" + msS3Endpoint(),
		"PODIUM_S3_ACCESS_KEY_ID=" + os.Getenv("PODIUM_S3_ACCESS_KEY_ID"),
		"PODIUM_S3_SECRET_ACCESS_KEY=" + os.Getenv("PODIUM_S3_SECRET_ACCESS_KEY"),
		"PODIUM_S3_FORCE_PATH_STYLE=" + msS3PathStyle(),
		// Standard mode defaults to pgvector plus an embedding provider, which
		// requires OPENAI_API_KEY and fails closed without it. A mock OpenAI-format
		// embedder runs search through the real pgvector path without a live API
		// key, the same approach as the semantic-search e2e (§13.12).
		"PODIUM_EMBEDDING_PROVIDER=openai",
		"OPENAI_API_KEY=sk-test",
		"PODIUM_OPENAI_BASE_URL=" + emb.URL,
		"PODIUM_IDENTITY_PROVIDER=injected-session-token",
		"PODIUM_OAUTH_AUDIENCE=" + injAudience,
		// alice@acme.com is bootstrapped as the tenant admin so the author can
		// register a layer through the managed stack; the consumer journey then
		// runs as the same verified caller (§4.7.2).
		"PODIUM_BOOTSTRAP_ADMINS=alice@acme.com",
		// The layer below is ingested public so a verified caller sees it. An
		// unverifiable caller is still rejected at the verifier before
		// visibility is consulted, which the negative control asserts.
		"PODIUM_DEFAULT_LAYER_VISIBILITY=public",
	}
	env = append(env, extraEnv...)
	return startServerArgs(t, env, "serve")
}

// msS3PathStyle resolves the path-style flag for the object store. MinIO needs
// path-style addressing; the live lane sets PODIUM_S3_FORCE_PATH_STYLE, and an
// unset value defaults to "true" so a MinIO-backed run works out of the box
// (§13.12). A real AWS S3 endpoint set to "false" overrides it.
func msS3PathStyle() string {
	if v := os.Getenv("PODIUM_S3_FORCE_PATH_STYLE"); v != "" {
		return v
	}
	return "true"
}

// msS3UseSSL resolves the test's TLS intent for the object store. A MinIO
// endpoint speaks plain HTTP, so an unset value defaults to "false"; the live
// lane sets PODIUM_S3_USE_SSL explicitly, and a real HTTPS endpoint can set
// "true". The server has no USE_SSL knob (§13.12 derives TLS from the endpoint
// scheme), so this only feeds msS3Endpoint's scheme selection.
func msS3UseSSL() string {
	if v := os.Getenv("PODIUM_S3_USE_SSL"); v != "" {
		return v
	}
	return "false"
}

// msS3Endpoint returns PODIUM_S3_ENDPOINT as a scheme-qualified URL. The server
// derives the object-store TLS flag from the endpoint scheme (§13.12: the S3
// endpoint is a URL; http disables TLS, https or a bare host[:port] enables it),
// so a bare MinIO host must be qualified with http:// or every data-plane PUT
// fails with "server gave HTTP response to HTTPS client". An endpoint that
// already carries a scheme is passed through unchanged.
func msS3Endpoint() string {
	ep := os.Getenv("PODIUM_S3_ENDPOINT")
	if ep == "" || strings.Contains(ep, "://") {
		return ep
	}
	if msS3UseSSL() == "true" {
		return "https://" + ep
	}
	return "http://" + ep
}

// msGitInit turns dir into a git repo with one commit on main so a standard-mode
// server can register it as a git source and clone it on reingest. A remote
// server cannot read a client-side --local path, so git source is the author
// flow. Skips when git is not on PATH.
func msGitInit(t *testing.T, dir string) {
	t.Helper()
	if _, ok := runExternal(t, dir, 10*time.Second, "git", "init", "-b", "main"); !ok {
		t.Skip("git not on PATH; skipping managed-stack parity e2e")
	}
	runExternal(t, dir, 10*time.Second, "git", "config", "user.email", "alice@acme.com")
	runExternal(t, dir, 10*time.Second, "git", "config", "user.name", "alice")
	runExternal(t, dir, 10*time.Second, "git", "add", ".")
	runExternal(t, dir, 10*time.Second, "git", "commit", "-m", "managed-stack parity fixture")
}

// msPublishGitLayer publishes reg as a git-source layer through the standard
// server: it commits reg to a local git repo, registers the layer with the
// admin session token, and reingests so the server clones and parses the
// artifacts into the metadata store and the search index. A remote standard
// server cannot read a client-side --local path, so the git source is the
// author flow (§7.3.1, §6.3.2). It returns the reingest CLI output.
func msPublishGitLayer(t *testing.T, baseURL, token, layerID, reg string) cliResult {
	t.Helper()
	msGitInit(t, reg)
	tokenEnv := []string{
		"PODIUM_IDENTITY_PROVIDER=injected-session-token",
		"PODIUM_SESSION_TOKEN=" + token,
	}
	if rr := runPodium(t, "", tokenEnv, "layer", "register", "--registry", baseURL,
		"--id", layerID, "--repo", reg, "--ref", "main"); rr.Exit != 0 {
		t.Fatalf("layer register %s exit=%d stderr=%s stdout=%s", layerID, rr.Exit, rr.Stderr, rr.Stdout)
	}
	ri := runPodium(t, "", tokenEnv, "layer", "reingest", "--registry", baseURL, layerID)
	if ri.Exit != 0 {
		t.Fatalf("layer reingest %s exit=%d stderr=%s stdout=%s", layerID, ri.Exit, ri.Stderr, ri.Stdout)
	}
	return ri
}

// msLoadResponse mirrors the §7.6.1 load_artifact JSON envelope: the manifest
// frontmatter and body, the inline resource set, and the presigned large-resource
// links the S3 data plane returns above the 256 KB inline cutoff (§7.2).
type msLoadResponse struct {
	ID             string            `json:"id"`
	Type           string            `json:"type"`
	Version        string            `json:"version"`
	Frontmatter    string            `json:"frontmatter"`
	ManifestBody   string            `json:"manifest_body"`
	Resources      map[string]string `json:"resources"`
	LargeResources map[string]struct {
		PresignedURL string `json:"presigned_url"`
		ContentHash  string `json:"content_hash"`
		Size         int64  `json:"size"`
	} `json:"large_resources"`
}

// TestStandardStackParity_AuthorToConsumer boots the registry in standard mode
// (live Postgres + live S3 + injected-session-token), ingests a bundled-resource
// layer as the author, then as the authenticated consumer searches, loads, and
// materializes the artifact, asserting the managed stack produces the same
// observable result the standalone/filesystem path produces.
//
// Spec: §13.10 / §13.12 (standard deployment backends), §7.2 (control plane +
// data plane), §4.7 / §4.7.1 (Registry as a Service; tenancy), §6.3.2
// (injected-session-token runtime trust).
func TestStandardStackParity_AuthorToConsumer(t *testing.T) {
	// Not parallel: standard mode keys metadata by the fixed "default" org
	// schema, which is shared and cannot be isolated per test (see the file
	// header). The test runs whenever a live Postgres DSN and an S3 bucket are
	// configured (msSkipIfNoStack), matching the sibling pkg/objectstore S3 live
	// test; it skips otherwise, so the PR lane stays hermetic and the live and
	// release lanes exercise the full author-to-consumer journey.
	dsn, bucket, region := msSkipIfNoStack(t)

	priv, pemPath := injKeyPair(t)
	srv := msStartStandardServer(t, dsn, bucket, region)
	injRegisterRuntime(t, srv, pemPath)
	token := injSignJWT(t, priv, injClaims("alice@acme.com"))

	// ---- Author: ingest a bundled-resource layer through the managed stack ---
	// Layer registration with source_type:local triggers ingest, which uploads
	// bundled resources to the S3 object store and persists manifests to
	// Postgres. The parity registry stages only inline-sized resources so the
	// materialized tree is a deterministic, store-independent comparison; the S3
	// data plane is exercised separately below with a large resource on its own
	// layer.
	id := "finance/close-reporting/run-variance-analysis"
	reg := writeRegistry(t, map[string]string{
		id + "/ARTIFACT.md":         brSkillArtifact,
		id + "/SKILL.md":            brSkillMD("run-variance-analysis", brVarianceDesc, "Run the analysis.\n"),
		id + "/scripts/variance.py": "print('variance')\n",
	})
	// A remote standard-mode server cannot read a client --local path, so the
	// author publishes through a git source: commit the registry to a local git
	// repo, register it with --repo, and reingest so the server clones and parses
	// the artifacts. The CLI sends the injected session token, which the verifier
	// checks before core.AdminAuthorize gates the registration (§7.3.1, §6.3.2).
	msPublishGitLayer(t, srv.BaseURL, token, "finance", reg)

	// ---- Consumer 1: search the control plane as the authenticated caller ----
	// The injected-token verifier resolves a valid bearer to an authenticated
	// identity; the artifact must come back in the search envelope.
	// Runtime layer registration ingests asynchronously through the §4.7.2 outbox,
	// so poll the control plane until the artifact is indexed rather than racing
	// the ingest with a single immediate query.
	searchURL := srv.BaseURL + "/v1/search_artifacts?query=" + queryEscape("variance analysis")
	var st int
	var body []byte
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		st, body = injGet(t, searchURL, token)
		if st == 200 && strings.Contains(string(body), id) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if st != 200 {
		t.Fatalf("authenticated search_artifacts = HTTP %d\nbody: %s", st, body)
	}
	if !strings.Contains(string(body), id) {
		t.Fatalf("authenticated search did not return %q within the deadline:\nsearch body: %s\nserver log:\n%s", id, body, srv.log())
	}

	// Negative control: an unverifiable token is rejected at the verifier
	// (auth.untrusted_runtime) before visibility is consulted, so the artifact
	// is not returned even though the layer is public. This proves the managed
	// identity gate is live and not bypassed by the public default.
	stBad, bodyBad := injGet(t, srv.BaseURL+"/v1/search_artifacts?query="+queryEscape("variance analysis"), "not-a-valid-jwt")
	if stBad == 200 && strings.Contains(string(bodyBad), id) {
		t.Errorf("unverifiable token returned the artifact (managed identity gate bypassed):\nHTTP %d\n%s", stBad, bodyBad)
	}

	// ---- Consumer 2: load the artifact as the authenticated caller ----------
	// The §7.6.1 envelope carries the manifest body and the inline resource set,
	// served from the managed stack (manifests from Postgres, inline resource
	// bytes from S3).
	stLoad, loadBody := injGet(t, srv.BaseURL+"/v1/load_artifact?id="+id, token)
	if stLoad != 200 {
		t.Fatalf("authenticated load_artifact = HTTP %d\nbody: %s", stLoad, loadBody)
	}
	var loaded msLoadResponse
	if err := json.Unmarshal(loadBody, &loaded); err != nil {
		t.Fatalf("decode load_artifact envelope: %v\nbody: %s", err, loadBody)
	}
	if loaded.ID != id {
		t.Errorf("load_artifact id = %q, want %q", loaded.ID, id)
	}
	if !strings.Contains(loaded.ManifestBody, "Run the analysis.") {
		t.Errorf("load_artifact manifest_body missing the SKILL.md body:\n%s", loaded.ManifestBody)
	}
	if loaded.Resources["scripts/variance.py"] != "print('variance')\n" {
		t.Errorf("inline resource scripts/variance.py = %q, want the staged script (resources=%v)",
			loaded.Resources["scripts/variance.py"], loaded.Resources)
	}

	// ---- Consumer 3: materialize through the managed stack, assert parity ----
	// `podium sync` in server-source mode calls the registry over HTTP, then runs
	// the shared materialize writer locally. It forwards the injected token as
	// Authorization: Bearer (resolved from PODIUM_SESSION_TOKEN_FILE). The
	// resulting tree, filtered of .podium/ deployment state, must reproduce the
	// tree the standalone/filesystem path produces for this layer's artifact,
	// which is the single-canonical-implementation parity guarantee. The
	// comparison below scopes to this layer's artifact, so a shared org schema
	// that also holds other layers does not perturb the claim.
	tokFile := filepath.Join(t.TempDir(), "session.jwt")
	if err := os.WriteFile(tokFile, []byte(token+"\n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	managedTarget := t.TempDir()
	res := runPodium(t, "", []string{
		"PODIUM_REGISTRY=" + srv.BaseURL,
		"PODIUM_IDENTITY_PROVIDER=injected-session-token",
		"PODIUM_SESSION_TOKEN_FILE=" + tokFile,
	}, "sync", "--registry", srv.BaseURL, "--target", managedTarget, "--harness", "none")
	if res.Exit != 0 {
		t.Fatalf("managed-stack sync exit=%d stderr=%s stdout=%s", res.Exit, res.Stderr, res.Stdout)
	}
	managed := readTreeFiltered(t, managedTarget)

	// Baseline: materialize the same registry through the standalone/filesystem
	// path (sync --harness none against the filesystem registry).
	standalone := syncAndSnapshot(t, reg, t.TempDir())

	// The standard-mode "default" org schema is shared across runs and sibling
	// tests, so the managed materialization can include artifacts from other
	// layers (for example the large-resource layer this test registers below,
	// persisted by a prior run). Scope the comparison to this layer's artifact:
	// every file the standalone path produces must be reproduced byte-for-byte by
	// the managed stack. Extra managed artifacts from other layers fall outside
	// this layer's parity claim.
	managedScoped := map[string]string{}
	for k, v := range managed {
		if strings.HasPrefix(k, id+"/") {
			managedScoped[k] = v
		}
	}
	if !reflect.DeepEqual(managedScoped, standalone) {
		t.Errorf("managed-stack materialization differs from the standalone path:\nmanaged keys: %v\nstandalone keys: %v",
			msKeys(managedScoped), msKeys(standalone))
		for k, mv := range managedScoped {
			if sv, ok := standalone[k]; !ok {
				t.Errorf("  %s: present in managed, absent in standalone", k)
			} else if mv != sv {
				t.Errorf("  %s: managed and standalone contents differ", k)
			}
		}
		for k := range standalone {
			if _, ok := managedScoped[k]; !ok {
				t.Errorf("  %s: present in standalone, absent in managed", k)
			}
		}
	}

	// ---- Data plane: a large resource externalizes to S3 (§7.2) -------------
	// A bundled resource above the 256 KB inline cutoff is uploaded to S3 at
	// ingest and returned as a presigned URL under large_resources on load,
	// rather than inline. This proves the configured object store is S3 on both
	// the write and read sides. It registers its own layer after the parity
	// comparison so that comparison stays a deterministic, single-layer claim.
	largeID := "finance/close-reporting/variance-dataset"
	large := strings.Repeat("A", 256*1024+1024) // above the 256 KB inline cutoff
	largeReg := writeRegistry(t, map[string]string{
		largeID + "/ARTIFACT.md":  brSkillArtifact,
		largeID + "/SKILL.md":     brSkillMD("variance-dataset", brVarianceDesc, "Reference the dataset.\n"),
		largeID + "/data/big.bin": large,
	})
	msPublishGitLayer(t, srv.BaseURL, token, "finance-data", largeReg)

	stBig, bigBody := injGet(t, srv.BaseURL+"/v1/load_artifact?id="+largeID, token)
	if stBig != 200 {
		t.Fatalf("authenticated load_artifact (large) = HTTP %d\nbody: %s", stBig, bigBody)
	}
	var bigLoaded msLoadResponse
	if err := json.Unmarshal(bigBody, &bigLoaded); err != nil {
		t.Fatalf("decode large load_artifact envelope: %v\nbody: %s", err, bigBody)
	}
	if _, inline := bigLoaded.Resources["data/big.bin"]; inline {
		t.Error("a resource above the cutoff must not be returned inline by the S3 data plane")
	}
	link, ok := bigLoaded.LargeResources["data/big.bin"]
	if !ok {
		t.Fatalf("large resource missing from large_resources (S3 externalization not exercised): %v", bigLoaded.LargeResources)
	}
	if link.PresignedURL == "" {
		t.Error("large resource presigned_url is empty; the S3 data plane did not presign")
	}
	if link.Size != int64(len(large)) {
		t.Errorf("large resource size = %d, want %d", link.Size, len(large))
	}
}

// msKeys returns the sorted keys of a materialized-tree snapshot for a stable
// diff message.
func msKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Insertion-order-independent: sort so the failure message is deterministic.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
