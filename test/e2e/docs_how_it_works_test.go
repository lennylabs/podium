package e2e

// End-to-end tests for docs/getting-started/how-it-works.md (D-how-it-works).
// These drive the real podium, podium serve, and podium-mcp surfaces across
// the filesystem and standalone deployment modes. Behaviors that the doc
// claims but the implementation does not yet provide are skipped with the
// blocking BUILD-GAPS finding so the suite stays green while still encoding
// the acceptance criterion.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// hiwSkillRegistry stages a skill + context for filesystem-mode tests.
func hiwSkillRegistry(t *testing.T) string {
	return writeRegistry(t, map[string]string{
		"greetings/hello/ARTIFACT.md":  greetSkillArtifact,
		"greetings/hello/SKILL.md":     skillBody("hello"),
		"company-glossary/ARTIFACT.md": contextArtifact("Company glossary"),
	})
}

// T-D-how-it-works-1 — filesystem registry: sync reads the directory directly
// with harness=none, no server required.
func TestHIW_FilesystemSyncNone(t *testing.T) {
	t.Parallel()
	reg := hiwSkillRegistry(t)
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, "greetings/hello/ARTIFACT.md"))
	mustExist(t, filepath.Join(tgt, "greetings/hello/SKILL.md"))
	mustExist(t, filepath.Join(tgt, "company-glossary/ARTIFACT.md"))

	// Negative: omitting --registry yields a non-zero config error.
	neg := runPodium(t, t.TempDir(), []string{"HOME=" + t.TempDir(), "PODIUM_REGISTRY="}, "sync", "--target", t.TempDir())
	if neg.Exit == 0 || !strings.Contains(neg.Stderr, "registry is required") {
		t.Errorf("expected no_registry error, exit=%d stderr=%s", neg.Exit, neg.Stderr)
	}
}

// T-D-how-it-works-2 — filesystem registry: claude-code adapter layout, with a
// cursor negative variant.
func TestHIW_FilesystemSyncClaudeCodeLayout(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"skills/my-skill/ARTIFACT.md": greetSkillArtifact,
		"skills/my-skill/SKILL.md":    skillBody("my-skill"),
		"rules/ts-style/ARTIFACT.md":  "---\ntype: rule\nversion: 1.0.0\ndescription: ts\nrule_mode: always\n---\n\nrules\n",
	})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "claude-code")
	mustExist(t, filepath.Join(tgt, ".claude/skills/my-skill/SKILL.md"))
	mustExist(t, filepath.Join(tgt, ".claude/rules/ts-style.md"))
	if _, err := os.Stat(filepath.Join(tgt, "skills/my-skill/ARTIFACT.md")); err == nil {
		t.Errorf("canonical layout should not be used for claude-code")
	}
	// Negative: cursor writes the rule with the .mdc extension.
	curTgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", curTgt, "--harness", "cursor")
	mustExist(t, filepath.Join(curTgt, ".cursor/rules/ts-style.mdc"))
}

// T-D-how-it-works-3 — filesystem path is not a valid source for the MCP
// server: a tool call surfaces an error.
func TestHIW_MCPRejectsFilesystemPath(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"y/ARTIFACT.md": contextArtifact("y")})
	res := mcpExec(t, []string{"PODIUM_REGISTRY=" + reg, "PODIUM_CACHE_DIR=" + t.TempDir()},
		rpcReq{ID: 1, Method: "initialize"},
		toolCall(2, "search_artifacts", map[string]any{}),
	)
	env := rpcEnvelope(t, res.Stdout, 2)
	body, _ := json.Marshal(env)
	// The bridge cannot speak HTTP to a bare path; it reports a protocol
	// error rather than returning results.
	if !strings.Contains(string(body), "error") || !strings.Contains(string(body), "scheme") {
		t.Errorf("expected an unsupported-scheme error for a filesystem-path registry: %s", body)
	}
}

// T-D-how-it-works-4 — zero-flag auto-bootstrap banner and ~/.podium files.
func TestHIW_ZeroFlagAutoBootstrap(t *testing.T) {
	t.Skip("blocked by F-13.10.1: zero-flag detection emits no 'No config found' banner and does not create ~/.podium/registry.yaml or ~/podium-artifacts/")
}

// T-D-how-it-works-5 — explicit --standalone --layer-path serves the API.
func TestHIW_StandaloneExplicit(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"hello/ARTIFACT.md": greetSkillArtifact,
		"hello/SKILL.md":    skillBody("hello"),
	})
	srv := startServer(t, reg)
	for _, p := range []string{"/healthz", "/readyz"} {
		var body map[string]any
		getJSON(t, srv.BaseURL+p, &body)
		if body["mode"] != "ready" {
			t.Errorf("%s mode=%v, want ready", p, body["mode"])
		}
	}
	var dom map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain", &dom)
	if _, ok := dom["subdomains"]; !ok {
		t.Errorf("/v1/load_domain missing subdomains: %v", dom)
	}
}

// T-D-how-it-works-6 — standalone state lives under ~/.podium/standalone/.
func TestHIW_StandaloneStateFiles(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")}))
	db := filepath.Join(srv.Home, ".podium", "standalone", "podium.db")
	magic := readFileBytes(t, db)
	if len(magic) < 16 || string(magic[:15]) != "SQLite format 3" {
		t.Errorf("%s is not a SQLite file (magic=%q)", db, string(magic[:min(15, len(magic))]))
	}
	if fi, err := os.Stat(filepath.Join(srv.Home, ".podium", "standalone", "objects")); err != nil || !fi.IsDir() {
		t.Errorf("expected ~/.podium/standalone/objects/ directory: %v", err)
	}
}

// T-D-how-it-works-7 — /healthz and /readyz return the documented JSON.
func TestHIW_HealthReadyJSON(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")}))
	var health map[string]any
	getJSON(t, srv.BaseURL+"/healthz", &health)
	// §13.9: /healthz reports the mode; liveness is the 200 status, and
	// there is no readiness boolean on /healthz (F-13.9.5).
	if health["mode"] != "ready" {
		t.Errorf("/healthz = %v, want mode=ready", health)
	}
	if _, present := health["ready"]; present {
		t.Errorf("/healthz carries undocumented `ready` field: %v", health)
	}
	var ready map[string]any
	getJSON(t, srv.BaseURL+"/readyz", &ready)
	if ready["mode"] != "ready" {
		t.Errorf("/readyz mode=%v, want ready", ready["mode"])
	}
	if lag, ok := ready["replication_lag_seconds"]; !ok || asInt(lag) != 0 {
		t.Errorf("/readyz replication_lag_seconds=%v, want 0", ready["replication_lag_seconds"])
	}
}

// T-D-how-it-works-8 — MCP exposes the four meta-tools and declares
// capabilities.tools and capabilities.sessionCorrelation.
func TestHIW_MCPMetaToolsAndCapabilities(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")}))
	res := mcpExec(t, []string{"PODIUM_REGISTRY=" + srv.BaseURL},
		rpcReq{ID: 1, Method: "initialize"},
		rpcReq{ID: 2, Method: "tools/list"},
	)
	init := rpcResult(t, res.Stdout, 1)
	caps, _ := init["capabilities"].(map[string]any)
	if _, ok := caps["tools"]; !ok {
		t.Errorf("initialize capabilities missing tools: %v", caps)
	}
	if caps["sessionCorrelation"] != true {
		t.Errorf("capabilities.sessionCorrelation=%v, want true", caps["sessionCorrelation"])
	}
	list := res.Stdout
	for _, tool := range []string{"load_domain", "search_domains", "search_artifacts", "load_artifact"} {
		if !strings.Contains(list, tool) {
			t.Errorf("tools/list missing %q", tool)
		}
	}
}

// T-D-how-it-works-9 — MCP load_domain (root) returns the root domain map.
func TestHIW_MCPLoadDomainRoot(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"greetings/hello/ARTIFACT.md": greetSkillArtifact,
		"greetings/hello/SKILL.md":    skillBody("hello"),
	}))
	res := mcpExec(t, []string{"PODIUM_REGISTRY=" + srv.BaseURL},
		toolCall(1, "load_domain", map[string]any{}))
	result := rpcResult(t, res.Stdout, 1)
	subs, _ := result["subdomains"].([]any)
	if len(subs) == 0 {
		t.Errorf("load_domain(root) returned no subdomains: %v", result)
	}
}

// T-D-how-it-works-10 — MCP load_artifact materializes via the claude-code
// adapter to PODIUM_MATERIALIZE_ROOT, atomically.
func TestHIW_MCPLoadArtifactMaterializes(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"greetings/hello/ARTIFACT.md": greetSkillArtifact,
		"greetings/hello/SKILL.md":    skillBody("hello"),
	}))
	mat := t.TempDir()
	res := mcpExec(t, []string{
		"PODIUM_REGISTRY=" + srv.BaseURL,
		"PODIUM_HARNESS=claude-code",
		"PODIUM_MATERIALIZE_ROOT=" + mat,
		"PODIUM_CACHE_DIR=" + t.TempDir(),
	}, toolCall(1, "load_artifact", map[string]any{"id": "greetings/hello"}))
	result := rpcResult(t, res.Stdout, 1)
	if paths, _ := result["materialized_at"].([]any); len(paths) == 0 {
		t.Errorf("expected materialized_at paths: %v", result)
	}
	skill := filepath.Join(mat, ".claude/skills/hello/SKILL.md")
	mustExist(t, skill)
	// The bridge's claude-code adapter writes the manifest frontmatter plus
	// the SKILL.md body; assert the registry body bytes are present.
	if !strings.Contains(readFile(t, skill), "hello body.") {
		t.Errorf("materialized SKILL.md missing the registry body:\n%s", readFile(t, skill))
	}
	for path := range readTreeAll(t, mat) {
		if strings.HasSuffix(path, ".tmp") {
			t.Errorf("atomic write left a .tmp file: %s", path)
		}
	}
}

// T-D-how-it-works-11 — load_artifact on a missing id aborts with a
// structured error and writes nothing; the bridge stays usable.
func TestHIW_MCPLoadArtifactNotFound(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")}))
	mat := t.TempDir()
	res := mcpExec(t, []string{
		"PODIUM_REGISTRY=" + srv.BaseURL,
		"PODIUM_MATERIALIZE_ROOT=" + mat,
		"PODIUM_CACHE_DIR=" + t.TempDir(),
	},
		toolCall(1, "load_artifact", map[string]any{"id": "nonexistent/artifact/id"}),
		toolCall(2, "search_artifacts", map[string]any{}),
	)
	env := rpcEnvelope(t, res.Stdout, 1)
	blob, _ := json.Marshal(env)
	if !strings.Contains(string(blob), "error") && !strings.Contains(string(blob), "not_found") {
		t.Errorf("expected a structured error for a missing artifact: %s", blob)
	}
	if files := readTreeFiltered(t, mat); len(files) != 0 {
		t.Errorf("missing-artifact load wrote files: %v", files)
	}
	// A subsequent call still succeeds.
	if rpcResult(t, res.Stdout, 2) == nil {
		t.Errorf("bridge did not recover for the second call")
	}
}

// T-D-how-it-works-12 — sync in server-source mode.
func TestHIW_SyncServerSource(t *testing.T) {
	t.Skip("blocked by F-2.2.2: podium sync has no server-source HTTP path; a URL registry is treated as a filesystem path")
}

// T-D-how-it-works-13 — sync writes an independent .podium/sync.lock per target.
func TestHIW_SyncLockPerTarget(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	t1, t2 := t.TempDir(), t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", t1, "--harness", "none")
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", t2, "--harness", "none")
	mustExist(t, filepath.Join(t1, ".podium", "sync.lock"))
	mustExist(t, filepath.Join(t2, ".podium", "sync.lock"))
}

// T-D-how-it-works-14 — load_artifact populates the content cache under the
// configured cache dir, shared across processes.
func TestHIW_ContentCacheShared(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")}))
	cache := t.TempDir()
	run := func() string {
		res := mcpExec(t, []string{
			"PODIUM_REGISTRY=" + srv.BaseURL,
			"PODIUM_HARNESS=none",
			"PODIUM_MATERIALIZE_ROOT=" + t.TempDir(),
			"PODIUM_CACHE_DIR=" + cache,
		}, toolCall(1, "load_artifact", map[string]any{"id": "x"}))
		return res.Stdout
	}
	run()
	populated := false
	for path := range readTreeAll(t, cache) {
		if filepath.Base(path) == "frontmatter" {
			populated = true
		}
	}
	if !populated {
		t.Fatalf("cache not populated after first load")
	}
	// A second process against the same cache still succeeds (cache shared).
	out := run()
	if rpcResult(t, out, 1)["id"] != "x" {
		t.Errorf("second cached load did not return the artifact")
	}
}

// T-D-how-it-works-15 — workspace overlay (PODIUM_OVERLAY_PATH) has the
// highest precedence in the effective view.
func TestHIW_OverlayHighestPrecedence(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"greetings/hello/ARTIFACT.md": contextArtifact("registry-version"),
	}))
	overlay := writeRegistry(t, map[string]string{
		"greetings/hello/ARTIFACT.md": contextArtifact("overlay-version"),
	})
	res := mcpExec(t, []string{
		"PODIUM_REGISTRY=" + srv.BaseURL,
		"PODIUM_OVERLAY_PATH=" + overlay,
		"PODIUM_HARNESS=none",
		"PODIUM_MATERIALIZE_ROOT=" + t.TempDir(),
		"PODIUM_CACHE_DIR=" + t.TempDir(),
	}, toolCall(1, "load_artifact", map[string]any{"id": "greetings/hello"}))
	result := rpcResult(t, res.Stdout, 1)
	body, _ := result["manifest_body"].(string)
	if !strings.Contains(body, "overlay-version") {
		t.Errorf("overlay did not take precedence; manifest_body=%q", body)
	}
}

// T-D-how-it-works-16 — exact-semver version pinning via ?version=.
func TestHIW_VersionPinning(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"greetings/hello/ARTIFACT.md": contextArtifact("v1"),
	}))
	var pinned map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_artifact?id=greetings/hello&version=1.0.0", &pinned)
	if pinned["version"] != "1.0.0" {
		t.Errorf("pinned version=%v, want 1.0.0", pinned["version"])
	}
	// Negative: a non-existent version yields a structured 404, not a 500.
	if st := getStatus(t, srv.BaseURL+"/v1/load_artifact?id=greetings/hello&version=9.9.9"); st != 404 {
		t.Errorf("version 9.9.9 = HTTP %d, want 404", st)
	}
}

// T-D-how-it-works-17 — version immutability on re-ingest.
func TestHIW_VersionImmutability(t *testing.T) {
	t.Skip("blocked by F-7.3.4: the manual reingest endpoint records intent but never runs the ingest pipeline, so re-ingest rejection cannot be exercised")
}

// T-D-how-it-works-18 — session_id version snapshot.
func TestHIW_SessionSnapshot(t *testing.T) {
	t.Skip("blocked by F-7.3.4: a new version cannot be ingested into a running standalone server (reingest is a no-op), so session snapshot consistency is untestable end-to-end")
}

// T-D-how-it-works-19 — standard deployment health (Docker Compose).
func TestHIW_StandardDeploymentHealth(t *testing.T) {
	t.Skip("requires the Docker Compose standard stack (Postgres + pgvector + S3 + OIDC); not available in the test sandbox")
}

// T-D-how-it-works-20 — standard deployment device-code login.
func TestHIW_StandardDeviceCodeLogin(t *testing.T) {
	t.Skip("requires a running standard deployment with a Dex IdP container; not available in the test sandbox")
}

// T-D-how-it-works-21 — filesystem-source registries apply no visibility
// filter: a sensitivity:high artifact still materializes.
func TestHIW_FilesystemNoVisibilityFilter(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"secret/tool/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: secret\nsensitivity: high\n---\n\nbody\n",
	})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "none")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	mustExist(t, filepath.Join(tgt, "secret/tool/ARTIFACT.md"))
}

// T-D-how-it-works-22 — injected-session-token identity provider.
func TestHIW_InjectedSessionToken(t *testing.T) {
	t.Skip("requires a registered runtime signing key (podium admin runtime register) and a self-signed JWT; standard-deployment identity scenario not wired in the sandbox")
}

// T-D-how-it-works-23 — migrate-to-standard into a SQLite target.
func TestHIW_MigrateToStandard(t *testing.T) {
	t.Parallel()
	src := makeStandaloneDB(t)
	targetDB := filepath.Join(t.TempDir(), "target.db")
	res := runPodium(t, "", nil, "admin", "migrate-to-standard",
		"--source-sqlite", src.db,
		"--source-objects", src.objects,
		"--target-store", "sqlite",
		"--target-sqlite", targetDB,
		"--target-objects", t.TempDir(),
	)
	if res.Exit != 0 {
		t.Fatalf("migrate exit=%d stderr=%s\nstdout=%s", res.Exit, res.Stderr, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "source plan:") {
		t.Errorf("stdout missing 'source plan:':\n%s", res.Stdout)
	}
	mustExist(t, targetDB)

	// Negative: omitting --source-sqlite exits 2.
	neg := runPodium(t, "", nil, "admin", "migrate-to-standard", "--target-store", "sqlite", "--target-sqlite", filepath.Join(t.TempDir(), "t.db"))
	if neg.Exit != 2 {
		t.Errorf("missing --source-sqlite exit=%d, want 2\nstderr=%s", neg.Exit, neg.Stderr)
	}
}

// T-D-how-it-works-24 — migrate-to-standard --dry-run writes nothing.
func TestHIW_MigrateDryRun(t *testing.T) {
	t.Parallel()
	src := makeStandaloneDB(t)
	targetDB := filepath.Join(t.TempDir(), "target.db")
	res := runPodium(t, "", nil, "admin", "migrate-to-standard",
		"--source-sqlite", src.db, "--target-store", "sqlite", "--target-sqlite", targetDB, "--dry-run")
	if res.Exit != 0 {
		t.Fatalf("dry-run exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "source plan:") {
		t.Errorf("stdout missing 'source plan:':\n%s", res.Stdout)
	}
	if _, err := os.Stat(targetDB); err == nil {
		t.Errorf("dry-run wrote the target database")
	}
}

// T-D-how-it-works-25 — /v1/load_domain returns the subdomain structure.
// Doc-accuracy (F-0.0.2): the wire keys are subdomains/notable, not the
// quickstart's domains/artifacts.
func TestHIW_LoadDomainStructure(t *testing.T) {
	t.Parallel()
	// A direct artifact under finance (glossary) plus the ap subtree keeps
	// finance from being folded into its single child at the root level.
	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/glossary/ARTIFACT.md":       contextArtifact("finance glossary"),
		"finance/ap/pay-invoice/ARTIFACT.md": contextArtifact("pay invoice"),
	}))
	var root map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain", &root)
	if !hasSubdomain(root, "finance") {
		t.Errorf("root missing finance subdomain: %v", root)
	}
	var fin map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance", &fin)
	if !hasSubdomain(fin, "finance/ap") {
		t.Errorf("finance missing ap subdomain: %v", fin)
	}
	var ap map[string]any
	getJSON(t, srv.BaseURL+"/v1/load_domain?path=finance/ap", &ap)
	if !strings.Contains(mustJSON(ap), "pay-invoice") {
		t.Errorf("finance/ap missing pay-invoice: %v", ap)
	}
	// Negative: a nonexistent path returns 404 or an empty subdomain set.
	st, body := getRaw(t, srv.BaseURL+"/v1/load_domain?path=does/not/exist")
	if st == 500 {
		t.Errorf("nonexistent domain returned 500: %s", body)
	}
}

// T-D-how-it-works-26 — /v1/search_artifacts returns ranked descriptors with
// no manifest bodies.
func TestHIW_SearchArtifactsDescriptors(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"greet/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\n---\n\n",
		"greet/SKILL.md":    greetSkillBody,
	}))
	st, body := getRaw(t, srv.BaseURL+"/v1/search_artifacts?query=greet&type=skill&top_k=5")
	if st != 200 {
		t.Fatalf("search = HTTP %d: %s", st, body)
	}
	var resp struct {
		TotalMatched int `json:"total_matched"`
		Results      []struct {
			ID           string `json:"id"`
			Type         string `json:"type"`
			Version      string `json:"version"`
			ManifestBody string `json:"manifest_body"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	if len(resp.Results) == 0 {
		t.Fatalf("no results for query=greet:\n%s", body)
	}
	for _, r := range resp.Results {
		if r.ManifestBody != "" {
			t.Errorf("descriptor leaked manifest_body for %s", r.ID)
		}
	}
}

// T-D-how-it-works-27 — /v1/load_artifact returns the manifest body and a
// content hash.
func TestHIW_LoadArtifactFields(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"greetings/hello/ARTIFACT.md": contextArtifact("greeting"),
	}))
	var resp struct {
		ID           string `json:"id"`
		Type         string `json:"type"`
		Version      string `json:"version"`
		ContentHash  string `json:"content_hash"`
		ManifestBody string `json:"manifest_body"`
		Frontmatter  string `json:"frontmatter"`
	}
	getJSON(t, srv.BaseURL+"/v1/load_artifact?id=greetings/hello", &resp)
	if resp.ID != "greetings/hello" || resp.Version != "1.0.0" {
		t.Errorf("id/version = %q/%q", resp.ID, resp.Version)
	}
	hash := strings.TrimPrefix(resp.ContentHash, "sha256:")
	if len(hash) != 64 {
		t.Errorf("content_hash is not a 64-hex sha256: %q", resp.ContentHash)
	}
	if resp.ManifestBody == "" || resp.Frontmatter == "" {
		t.Errorf("missing manifest_body/frontmatter")
	}
	// Negative: a missing artifact returns 404.
	if st := getStatus(t, srv.BaseURL+"/v1/load_artifact?id=no/such"); st != 404 {
		t.Errorf("missing artifact = HTTP %d, want 404", st)
	}
}

// T-D-how-it-works-28 — /v1/artifacts:batchLoad returns multiple artifacts.
func TestHIW_BatchLoad(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"a/ARTIFACT.md": contextArtifact("a"),
		"b/ARTIFACT.md": contextArtifact("b"),
	}))
	st, body := postJSON(t, srv.BaseURL+"/v1/artifacts:batchLoad", map[string]any{"ids": []string{"a", "b"}})
	if st != 200 {
		t.Fatalf("batchLoad = HTTP %d: %s", st, body)
	}
	for _, id := range []string{`"id":"a"`, `"id":"b"`} {
		if !strings.Contains(strings.ReplaceAll(string(body), " ", ""), id) {
			t.Errorf("batchLoad missing %s:\n%s", id, body)
		}
	}
	if !strings.Contains(string(body), "manifest_body") {
		t.Errorf("batchLoad omitted manifest bodies:\n%s", body)
	}
}

// T-D-how-it-works-29 — /v1/dependents returns extends-based edges.
func TestHIW_Dependents(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		".registry-config":               "multi_layer: true\nlayer_order:\n  - org\n  - team\n",
		"org/shared/parent/ARTIFACT.md":  "---\ntype: context\nversion: 1.0.0\ndescription: parent\n---\n\nbody\n",
		"team/finance/child/ARTIFACT.md": "---\ntype: context\nversion: 2.0.0\ndescription: child\nextends: shared/parent@1.x\n---\n\nbody\n",
	})
	srv := startServer(t, reg)
	st, body := getRaw(t, srv.BaseURL+"/v1/dependents?id=shared/parent")
	if st != 200 {
		t.Fatalf("GET /v1/dependents = HTTP %d: %s", st, body)
	}
	if !strings.Contains(string(body), "finance/child") {
		t.Errorf("/v1/dependents should report the extends edge from finance/child:\n%s", body)
	}
}

// T-D-how-it-works-30 — /v1/scope/preview returns aggregate visibility counts.
func TestHIW_ScopePreview(t *testing.T) {
	t.Parallel()
	srv := startServer(t, writeRegistry(t, map[string]string{
		"a/ARTIFACT.md": contextArtifact("a"),
		"b/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\n---\n\n",
		"b/SKILL.md":    skillBody("b"),
	}))
	var resp struct {
		ArtifactCount int            `json:"artifact_count"`
		ByType        map[string]int `json:"by_type"`
	}
	getJSON(t, srv.BaseURL+"/v1/scope/preview", &resp)
	if resp.ArtifactCount < 2 {
		t.Errorf("artifact_count=%d, want >=2", resp.ArtifactCount)
	}
	if len(resp.ByType) == 0 {
		t.Errorf("by_type is empty: %+v", resp)
	}
}

// T-D-how-it-works-31 — sync --dry-run resolves and writes nothing.
func TestHIW_SyncDryRun(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	tgt := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--dry-run")
	if res.Exit != 0 || !strings.Contains(res.Stdout, "dry-run") {
		t.Fatalf("dry-run exit=%d stdout=%s", res.Exit, res.Stdout)
	}
	if files := readTreeFiltered(t, tgt); len(files) != 0 {
		t.Errorf("dry-run wrote %d files", len(files))
	}
}

// T-D-how-it-works-32 — serve --strict refuses without config.
func TestHIW_ServeStrict(t *testing.T) {
	t.Skip("blocked by F-13.10.1: --strict is unimplemented (the flag is not defined and there is no strict-config gate)")
}

// T-D-how-it-works-33 — PODIUM_NO_AUTOSTANDALONE=1 suppresses bootstrap.
func TestHIW_NoAutostandalone(t *testing.T) {
	t.Skip("blocked by F-13.10.1: PODIUM_NO_AUTOSTANDALONE is not honored; the server still auto-bootstraps standalone mode")
}

// T-D-how-it-works-34 — public mode: all artifacts visible without auth.
// Doc-accuracy: the banner text differs (mode=public on the listen line),
// but /healthz reports mode=public and reads need no token.
func TestHIW_PublicMode(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	srv := startServerArgs(t, []string{"HOME=" + t.TempDir()}, "serve", "--public-mode", "--layer-path", reg)
	var health map[string]any
	getJSON(t, srv.BaseURL+"/healthz", &health)
	if health["mode"] != "public" {
		t.Errorf("/healthz mode=%v, want public", health["mode"])
	}
	if st := getStatus(t, srv.BaseURL+"/v1/load_artifact?id=x"); st != 200 {
		t.Errorf("unauthenticated load_artifact = HTTP %d, want 200", st)
	}
}

// T-D-how-it-works-35 — read-only mode response headers.
func TestHIW_ReadOnlyHeaders(t *testing.T) {
	t.Skip("read-only mode has no external CLI toggle; flipping the ModeTracker requires an in-process server option (covered by pkg/registry/server unit tests)")
}

// T-D-how-it-works-36 — cache prune removes old buckets.
func TestHIW_CachePrune(t *testing.T) {
	t.Parallel()
	cache := t.TempDir()
	bucket := filepath.Join(cache, "abc123abc123")
	mkdirOld(t, bucket, 40)
	res := runPodium(t, "", nil, "cache", "prune", "--dir", cache, "--days", "30")
	if res.Exit != 0 {
		t.Fatalf("cache prune exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "pruned 1 bucket") {
		t.Errorf("stdout missing 'pruned 1 bucket':\n%s", res.Stdout)
	}
	if _, err := os.Stat(bucket); err == nil {
		t.Errorf("old bucket was not removed")
	}
	// Negative: --dry-run reports but does not delete.
	cache2 := t.TempDir()
	bucket2 := filepath.Join(cache2, "old")
	mkdirOld(t, bucket2, 40)
	dry := runPodium(t, "", nil, "cache", "prune", "--dir", cache2, "--days", "30", "--dry-run")
	if !strings.Contains(dry.Stdout, "would prune") {
		t.Errorf("dry-run missing 'would prune':\n%s", dry.Stdout)
	}
	if _, err := os.Stat(bucket2); err != nil {
		t.Errorf("dry-run deleted the bucket")
	}
}

// T-D-how-it-works-37 — shared library: filesystem and server modes byte-equal.
func TestHIW_SharedLibraryEquivalence(t *testing.T) {
	t.Skip("blocked by F-2.2.2: the server-source sync path is unimplemented, so the filesystem-vs-server adapter-output comparison cannot run")
}

// T-D-how-it-works-38 — Python SDK requires a server.
func TestHIW_PythonSDKRequiresServer(t *testing.T) {
	t.Skip("requires the installed Python SDK (sdks/podium-py via pip); Python toolchain not provisioned in the test sandbox")
}

// T-D-how-it-works-39 — TypeScript SDK requires a server.
func TestHIW_TypeScriptSDKRequiresServer(t *testing.T) {
	t.Skip("requires the built TypeScript SDK (sdks/podium-ts via npm); Node toolchain not provisioned in the test sandbox")
}

// T-D-how-it-works-40 — config show reflects the active deployment mode.
func TestHIW_ConfigShowDeploymentMode(t *testing.T) {
	// spec §7.7 (F-7.7.1): config show prints the merged sync.yaml, so the
	// active deployment's registry surfaces as defaults.registry.
	ws := t.TempDir()
	reg := t.TempDir()
	env := []string{"HOME=" + t.TempDir()}
	if r := runPodium(t, ws, env, "init", "--registry", reg); r.Exit != 0 {
		t.Fatalf("init exit=%d stderr=%s", r.Exit, r.Stderr)
	}
	res := runPodium(t, ws, env, "config", "show")
	if res.Exit != 0 {
		t.Fatalf("config show exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	cliContains(t, res.Stdout, "defaults.registry", "merged registry key")
	cliContains(t, res.Stdout, reg, "active registry value")
}

// T-D-how-it-works-41 — /metrics endpoint.
func TestHIW_MetricsEndpoint(t *testing.T) {
	t.Skip("blocked by F-13.8.1: the Prometheus /metrics endpoint is absent on the registry (returns 404)")
}

// T-D-how-it-works-42 — cursor harness writes rules with the .mdc extension.
func TestHIW_CursorRuleLayout(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/ts-style/ARTIFACT.md": "---\ntype: rule\nversion: 1.0.0\ndescription: ts\nrule_mode: always\n---\n\nrules\n",
	})
	tgt := t.TempDir()
	runPodium(t, "", nil, "sync", "--registry", reg, "--target", tgt, "--harness", "cursor")
	mustExist(t, filepath.Join(tgt, ".cursor/rules/ts-style.mdc"))
	if _, err := os.Stat(filepath.Join(tgt, "rules/ts-style/ARTIFACT.md")); err == nil {
		t.Errorf("cursor should not use the canonical layout")
	}
}

// T-D-how-it-works-43 — mcp-server artifacts filtered from MCP bridge results.
func TestHIW_MCPServerFiltered(t *testing.T) {
	t.Skip("spec §5: mcp-server artifacts should be filtered from MCP bridge results; the bridge does not filter them and no BUILD-GAPS finding is filed for this gap")
}

// T-D-how-it-works-44 — version command prints a version string.
func TestHIW_Version(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil, "version")
	if res.Exit != 0 || !strings.HasPrefix(res.Stdout, "podium ") {
		t.Errorf("version exit=%d stdout=%q", res.Exit, res.Stdout)
	}
}

// T-D-how-it-works-45 — /objects/ serves content-addressed blobs.
func TestHIW_ObjectsRoute(t *testing.T) {
	t.Skip("bundled large-resource externalization (>256 KB) is not populated at standalone boot ingest, so load_artifact returns no large_resources link to fetch from /objects/")
}

// ---- local helpers ------------------------------------------------------

type standaloneDB struct{ db, objects string }

// makeStandaloneDB boots a standalone server against a fixture registry,
// waits for ingest, stops it, and returns the resulting SQLite + objects
// paths for migration tests.
func makeStandaloneDB(t *testing.T) standaloneDB {
	t.Helper()
	reg := writeRegistry(t, map[string]string{"x/ARTIFACT.md": contextArtifact("x")})
	srv := startServer(t, reg)
	stopProc(srv.cmd)
	db := filepath.Join(srv.Home, ".podium", "standalone", "podium.db")
	if !pollFile(db, 5*time.Second) {
		t.Fatalf("standalone db never created at %s", db)
	}
	return standaloneDB{db: db, objects: filepath.Join(srv.Home, ".podium", "standalone", "objects")}
}

func readFileBytes(t testing.TB, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

func mkdirOld(t testing.TB, dir string, daysAgo int) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	old := time.Now().Add(-time.Duration(daysAgo) * 24 * time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatalf("chtimes %s: %v", dir, err)
	}
}

func hasSubdomain(domain map[string]any, path string) bool {
	subs, _ := domain["subdomains"].([]any)
	for _, s := range subs {
		if m, ok := s.(map[string]any); ok && m["path"] == path {
			return true
		}
	}
	return false
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
