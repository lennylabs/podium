package e2e

// End-to-end tests for docs/deployment/operator-guide.md
// (D-operator-guide). The page covers day-two operations: health
// endpoints, read-only mode, admin commands, public-mode detection and
// mitigation, monitoring, alerting, backup/restore runbooks, and common
// operational pitfalls.
//
// Known gaps and skip rationale:
//   - F-13.2.3: read-only probe disabled by default; not triggerable in e2e.
//     Tests 5, 6, 7, 8, 9 assert this.
//   - F-13.2.8: X-Podium-Read-Only-Lag-Seconds is always "0".
//   - F-7.3.7: force_push_policy not settable via API/CLI/config. Tests 29, 30.
//   - F-13.8.1: /metrics endpoint absent. Test 34.
//   - podium admin verify: not implemented. Tests 11, 12, 22, 40.
//   - podium admin migrate --finalize/--revert: not implemented. Tests 13, 14.
//   - podium admin scim-sync: not implemented. Test 31.
//   - Tests 19, 41: need a signed artifact with tampered bytes; not expressible
//     from filesystem bootstrap.
//   - Test 20: default policy assertion; mcp starts fine, signing assertion
//     requires a signed medium artifact.
//   - Test 21: private layer visibility.denied audit event — default standalone
//     layers are public; private layer visibility control is uncertain.
//   - Test 23: webhook ingest invalid HMAC — the layer webhook ingest route
//     requires a git-type layer; the webhook URL is /v1/layers/<id>/webhook
//     which is not mounted in the standalone handler (only /v1/webhooks/* is).
//   - Tests 43, 44: need OIDC IdP or runtime-verified JWT.
//   - Tests 25: sandbox enforcement via MCP — REAL (feasible).
//   - Tests 33: /readyz in read_only — probe not triggerable.
//   - Tests 35, 36, 37: promtool checks — REAL when promtool installed.
//   - Tests 45, 46: two binary versions + Postgres; honest skip.
//   - Test 47: Postgres PITR + S3; honest skip.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/testharness/cmdharness"
)

// opguideSimpleArtifact returns an ARTIFACT.md body for a context artifact.
func opguideSimpleArtifact() string {
	return "---\ntype: context\nversion: 1.0.0\ndescription: test context\nsensitivity: low\n---\n\nTest context body.\n"
}

// opguideStartStandalone starts a standalone server with the given extra env
// and returns the proc. The caller must provide HOME= in env.
func opguideStartStandalone(t *testing.T, env []string, reg string) *serverProc {
	t.Helper()
	args := []string{"serve", "--standalone"}
	if reg != "" {
		args = append(args, "--layer-path", reg)
	}
	return startServerArgs(t, env, args...)
}

// opguideHealthz fetches /healthz and returns the decoded map.
func opguideHealthz(t *testing.T, baseURL string) map[string]any {
	t.Helper()
	var m map[string]any
	getJSON(t, baseURL+"/healthz", &m)
	return m
}

// ---- T-D-operator-guide-1: /healthz mode:ready + log mode=standalone --------

// T-D-operator-guide-1
func TestOpGuide_1_HealthzModeReady(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"ctx/ARTIFACT.md": opguideSimpleArtifact()})
	srv := opguideStartStandalone(t, []string{"HOME=" + t.TempDir()}, reg)

	h := opguideHealthz(t, srv.BaseURL)
	if h["mode"] != "ready" {
		t.Errorf("/healthz mode=%v, want ready", h["mode"])
	}
	// §13.9: /healthz reports the mode only; no readiness boolean
	// (F-13.9.5).
	if _, present := h["ready"]; present {
		t.Errorf("/healthz carries undocumented `ready` field: %v", h)
	}
	if !strings.Contains(srv.log(), "mode=standalone") {
		t.Errorf("startup log missing mode=standalone:\n%s", srv.log())
	}
}

// ---- T-D-operator-guide-2: /healthz mode:public + log mode=public -----------

// T-D-operator-guide-2
func TestOpGuide_2_HealthzModePublic(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"ctx/ARTIFACT.md": opguideSimpleArtifact()})
	srv := startServerArgs(t,
		[]string{"HOME=" + t.TempDir(), "PODIUM_PUBLIC_MODE=true"},
		"serve", "--standalone", "--layer-path", reg)

	h := opguideHealthz(t, srv.BaseURL)
	if h["mode"] != "public" {
		t.Errorf("/healthz mode=%v, want public", h["mode"])
	}
	if !strings.Contains(srv.log(), "mode=public") {
		t.Errorf("startup log missing mode=public:\n%s", srv.log())
	}
}

// ---- T-D-operator-guide-3: podium status surfaces registry mode:public ------

// T-D-operator-guide-3
func TestOpGuide_3_StatusSurfacesPublicMode(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"ctx/ARTIFACT.md": opguideSimpleArtifact()})
	srv := startServerArgs(t,
		[]string{"HOME=" + t.TempDir(), "PODIUM_PUBLIC_MODE=true"},
		"serve", "--standalone", "--layer-path", reg)

	res := runPodium(t, "", nil, "status", "--registry", srv.BaseURL)
	if res.Exit != 0 {
		t.Fatalf("status exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "registry mode:") {
		t.Errorf("status stdout missing 'registry mode:' line:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "public") {
		t.Errorf("status stdout missing 'public':\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "reachability:") {
		t.Errorf("status stdout missing 'reachability:' line:\n%s", res.Stdout)
	}
}

// ---- T-D-operator-guide-4: /readyz 200 in ready mode -----------------------

// T-D-operator-guide-4
func TestOpGuide_4_ReadyzReturns200(t *testing.T) {
	t.Parallel()
	srv := startServer(t, "")

	st, body := getRaw(t, srv.BaseURL+"/readyz")
	if st != 200 {
		t.Fatalf("GET /readyz = %d, want 200\nbody: %s", st, body)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode /readyz: %v\nbody: %s", err, body)
	}
	if m["mode"] != "ready" {
		t.Errorf("/readyz mode=%v, want ready", m["mode"])
	}
}

// ---- T-D-operator-guide-5: write endpoints return registry.read_only --------

// T-D-operator-guide-5
func TestOpGuide_5_ReadOnlyWriteEndpoints(t *testing.T) {
	t.Skip("blocked by F-13.2.3: read-only probe is disabled by default; cannot trigger read_only mode in e2e")
}

// ---- T-D-operator-guide-6: read endpoints serve with X-Podium-Read-Only headers

// T-D-operator-guide-6
func TestOpGuide_6_ReadOnlyHeaders(t *testing.T) {
	t.Skip("blocked by F-13.2.3: read-only probe is disabled by default; also F-13.2.8: lag header always 0")
}

// ---- T-D-operator-guide-7: auto-exit read-only mode -------------------------

// T-D-operator-guide-7
func TestOpGuide_7_ReadOnlyAutoExit(t *testing.T) {
	t.Skip("blocked by F-13.2.3: read-only probe is disabled by default; cannot induce or recover from read_only mode in e2e")
}

// ---- T-D-operator-guide-8: read_only_entered / read_only_exited audit events

// T-D-operator-guide-8
func TestOpGuide_8_ReadOnlyAuditEvents(t *testing.T) {
	t.Skip("blocked by F-13.2.3: read-only probe is disabled by default; state transitions never occur in e2e")
}

// ---- T-D-operator-guide-9: probe tuning via env vars -----------------------

// T-D-operator-guide-9
func TestOpGuide_9_ReadOnlyProbeTuning(t *testing.T) {
	t.Skip("blocked by F-13.2.3: read-only probe is disabled by default; tuning is not observable in e2e")
}

// ---- T-D-operator-guide-10: podium config show read_only probe settings -----

// T-D-operator-guide-10
func TestOpGuide_10_ConfigShowReadOnlyProbeSettings(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "",
		[]string{"PODIUM_READONLY_PROBE_FAILURES=3", "PODIUM_READONLY_PROBE_INTERVAL=10"},
		"config", "show")
	if res.Exit != 0 {
		t.Fatalf("config show exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	for _, want := range []struct{ name, val, src string }{
		{"read_only.probe_failures", "3", "PODIUM_READONLY_PROBE_FAILURES"},
		{"read_only.probe_interval_seconds", "10", "PODIUM_READONLY_PROBE_INTERVAL"},
	} {
		if !strings.Contains(res.Stdout, want.name) {
			t.Errorf("config show missing setting %q:\n%s", want.name, res.Stdout)
		}
		if !strings.Contains(res.Stdout, want.val) {
			t.Errorf("config show missing value %q for %s:\n%s", want.val, want.name, res.Stdout)
		}
		if !strings.Contains(res.Stdout, want.src) {
			t.Errorf("config show missing source %q for %s:\n%s", want.src, want.name, res.Stdout)
		}
	}
}

// ---- T-D-operator-guide-11: podium admin verify --check is not implemented --

// T-D-operator-guide-11
func TestOpGuide_11_AdminVerifyNotImplemented(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil,
		"admin", "verify", "--check", "audit-chain", "--check", "signatures",
		"--registry", "http://127.0.0.1:19999")
	if res.Exit != 2 {
		t.Errorf("exit=%d, want 2 (stderr=%s stdout=%s)", res.Exit, res.Stderr, res.Stdout)
	}
	if !strings.Contains(res.Stderr, "unknown admin subcommand: verify") {
		t.Errorf("stderr missing 'unknown admin subcommand: verify':\n%s", res.Stderr)
	}
}

// ---- T-D-operator-guide-12: podium admin verify --check schema is not implemented

// T-D-operator-guide-12
func TestOpGuide_12_AdminVerifyCheckSchemaNotImplemented(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil,
		"admin", "verify", "--check", "schema", "--check", "audit-chain",
		"--registry", "http://127.0.0.1:19999")
	if res.Exit != 2 {
		t.Errorf("exit=%d, want 2 (stderr=%s)", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "unknown admin subcommand: verify") {
		t.Errorf("stderr missing 'unknown admin subcommand: verify':\n%s", res.Stderr)
	}
}

// ---- T-D-operator-guide-13: podium admin migrate --finalize is not implemented

// T-D-operator-guide-13
func TestOpGuide_13_AdminMigrateFinalizeNotImplemented(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil,
		"admin", "migrate", "--finalize", "--registry", "http://127.0.0.1:19999")
	if res.Exit != 2 {
		t.Errorf("exit=%d, want 2 (stderr=%s)", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "unknown admin subcommand: migrate") {
		t.Errorf("stderr missing 'unknown admin subcommand: migrate':\n%s", res.Stderr)
	}
}

// ---- T-D-operator-guide-14: podium admin migrate --revert is not implemented

// T-D-operator-guide-14
func TestOpGuide_14_AdminMigrateRevertNotImplemented(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil,
		"admin", "migrate", "--revert", "--registry", "http://127.0.0.1:19999")
	if res.Exit != 2 {
		t.Errorf("exit=%d, want 2 (stderr=%s)", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "unknown admin subcommand: migrate") {
		t.Errorf("stderr missing 'unknown admin subcommand: migrate':\n%s", res.Stderr)
	}
}

// opguidePopulateSourceSQLite starts a standalone server with a given sqlite
// path and a local registry layer, waits for readiness, stops it, and returns
// the db path. The caller provides a temp dir for HOME and the sqlite path.
func opguidePopulateSourceSQLite(t *testing.T, home, sqlitePath string) {
	t.Helper()
	reg := writeRegistry(t, map[string]string{"ctx/ARTIFACT.md": opguideSimpleArtifact()})

	// Start server to populate the SQLite database.
	srv := startServerArgs(t,
		[]string{
			"HOME=" + home,
			"PODIUM_SQLITE_PATH=" + sqlitePath,
		},
		"serve", "--standalone", "--layer-path", reg)

	// Register a local layer so there is metadata in the db.
	layerReg := writeRegistry(t, map[string]string{"skill/ARTIFACT.md": greetSkillArtifact, "skill/SKILL.md": skillBody("skill")})
	res := runPodium(t, "", nil,
		"layer", "register",
		"--id", "opguide-layer",
		"--local", layerReg,
		"--registry", srv.BaseURL)
	if res.Exit != 0 {
		t.Logf("layer register exit=%d (non-fatal): %s", res.Exit, res.Stderr)
	}

	// Stop the server so the SQLite file is fully written.
	stopProc(srv.cmd)
	time.Sleep(200 * time.Millisecond)
}

// ---- T-D-operator-guide-15: migrate-to-standard dry-run ---------------------

// T-D-operator-guide-15
func TestOpGuide_15_MigrateToStandardDryRun(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	srcDB := filepath.Join(home, "src.db")
	dstDB := filepath.Join(home, "dst.db")

	opguidePopulateSourceSQLite(t, home, srcDB)

	res := runPodium(t, "", nil,
		"admin", "migrate-to-standard",
		"--source-sqlite", srcDB,
		"--target-store", "sqlite",
		"--target-sqlite", dstDB,
		"--dry-run")
	if res.Exit != 0 {
		t.Fatalf("migrate-to-standard --dry-run exit=%d stderr=%s stdout=%s", res.Exit, res.Stderr, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "source plan:") {
		t.Errorf("dry-run stdout missing 'source plan:':\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stdout, "dry-run") {
		t.Errorf("dry-run stdout missing 'dry-run':\n%s", res.Stdout)
	}
	if _, err := os.Stat(dstDB); err == nil {
		t.Errorf("dry-run created destination db at %s; it must not exist after dry-run", dstDB)
	}
}

// ---- T-D-operator-guide-16: migrate-to-standard copies metadata SQLite→SQLite

// T-D-operator-guide-16
func TestOpGuide_16_MigrateToStandardSQLiteToSQLite(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	srcDB := filepath.Join(home, "src.db")
	dstDB := filepath.Join(home, "dst.db")

	opguidePopulateSourceSQLite(t, home, srcDB)

	res := runPodium(t, "", nil,
		"admin", "migrate-to-standard",
		"--source-sqlite", srcDB,
		"--target-store", "sqlite",
		"--target-sqlite", dstDB)
	if res.Exit != 0 {
		t.Fatalf("migrate-to-standard exit=%d stderr=%s stdout=%s", res.Exit, res.Stderr, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "metadata migration complete") {
		t.Errorf("stdout missing 'metadata migration complete':\n%s", res.Stdout)
	}
	if _, err := os.Stat(dstDB); err != nil {
		t.Errorf("destination db not created at %s: %v", dstDB, err)
	}
}

// ---- T-D-operator-guide-17: migrate-to-standard requires --source-sqlite ----

// T-D-operator-guide-17
func TestOpGuide_17_MigrateToStandardRequiresSourceSQLite(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil,
		"admin", "migrate-to-standard",
		"--target-store", "sqlite",
		"--target-sqlite", filepath.Join(t.TempDir(), "dst.db"))
	if res.Exit != 2 {
		t.Errorf("exit=%d, want 2 (stderr=%s)", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "--source-sqlite is required") {
		t.Errorf("stderr missing '--source-sqlite is required':\n%s", res.Stderr)
	}
}

// ---- T-D-operator-guide-18: migrate-to-standard copies objects filesystem→filesystem

// T-D-operator-guide-18
func TestOpGuide_18_MigrateToStandardObjectsCopy(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	srcDB := filepath.Join(home, "src.db")
	srcObjects := filepath.Join(home, "src-objects")
	dstDB := filepath.Join(home, "dst.db")
	dstObjects := filepath.Join(home, "dst-objects")

	// Create a source objects directory with a test blob.
	if err := os.MkdirAll(srcObjects, 0o755); err != nil {
		t.Fatalf("mkdir srcObjects: %v", err)
	}
	blobPath := filepath.Join(srcObjects, "testblob.bin")
	if err := os.WriteFile(blobPath, []byte("test blob content"), 0o644); err != nil {
		t.Fatalf("write blob: %v", err)
	}

	// Start and stop a server to create the source SQLite db.
	opguidePopulateSourceSQLite(t, home, srcDB)

	res := runPodium(t, "", nil,
		"admin", "migrate-to-standard",
		"--source-sqlite", srcDB,
		"--source-objects", srcObjects,
		"--target-store", "sqlite",
		"--target-sqlite", dstDB,
		"--target-objects-type", "filesystem",
		"--target-objects", dstObjects)
	if res.Exit != 0 {
		t.Fatalf("migrate-to-standard exit=%d stderr=%s stdout=%s", res.Exit, res.Stderr, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "object migration complete") {
		t.Errorf("stdout missing 'object migration complete':\n%s", res.Stdout)
	}
}

// ---- T-D-operator-guide-19: signature invalid on tampered medium artifact ---

// T-D-operator-guide-19
func TestOpGuide_19_SignatureInvalidTamperedArtifact(t *testing.T) {
	t.Skip("requires a signed artifact whose stored bytes are then tampered; not expressible from filesystem bootstrap")
}

// ---- T-D-operator-guide-20: PODIUM_VERIFY_SIGNATURES default medium-and-above

// T-D-operator-guide-20
func TestOpGuide_20_VerifySignaturesDefault(t *testing.T) {
	t.Skip("verifying the default blocks a tampered medium artifact requires a signed-then-tampered artifact; not expressible from filesystem bootstrap")
}

// ---- T-D-operator-guide-21: visibility.denied audit event ------------------

// T-D-operator-guide-21
func TestOpGuide_21_VisibilityDeniedAuditEvent(t *testing.T) {
	t.Skip("default standalone layers are public; triggering visibility.denied for an anonymous caller requires a private layer with confirmed private visibility enforcement, which is uncertain in standalone")
}

// ---- T-D-operator-guide-22: admin verify --check audit-chain not implemented

// T-D-operator-guide-22
func TestOpGuide_22_AdminVerifyAuditChain(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil,
		"admin", "verify", "--check", "audit-chain",
		"--registry", "http://127.0.0.1:19999")
	if res.Exit != 2 {
		t.Errorf("exit=%d, want 2 (stderr=%s)", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "unknown admin subcommand: verify") {
		t.Errorf("stderr missing 'unknown admin subcommand: verify':\n%s", res.Stderr)
	}
}

// ---- T-D-operator-guide-23: webhook invalid HMAC => ingest.webhook_invalid --

// T-D-operator-guide-23
func TestOpGuide_23_WebhookInvalidHMAC(t *testing.T) {
	t.Skip("the layer webhook ingest route /v1/layers/<id>/webhook is not mounted in the standalone server handler (only /v1/webhooks/* for outbound delivery); cannot exercise ingest.webhook_invalid path in e2e")
}

// ---- T-D-operator-guide-24: per-layer visibility via injected-session-token -

// T-D-operator-guide-24 — per-layer visibility via injected-session-token: a
// JWT's group claim drives which layers the verified caller sees. F-6.3.1.
func TestOpGuide_24_PerLayerVisibilityInjectedToken(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	priv, pem := injKeyPair(t)

	// A finance-only layer (visibility: groups: [finance]) declared in
	// registry.yaml so its visibility is explicit rather than the default.
	layerRoot := writeRegistry(t, map[string]string{
		"finance/secret/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ndescription: Finance-only month-end variance secret.\n---\n\nbody\n",
	})
	cfgPath := filepath.Join(home, "registry.yaml")
	cfg := "" +
		"layers:\n" +
		"  - id: finance-layer\n" +
		"    source:\n" +
		"      local:\n" +
		"        path: " + layerRoot + "\n" +
		"    visibility:\n" +
		"      groups: [finance]\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}

	srv := startServerArgs(t, []string{
		"HOME=" + home,
		"PODIUM_CONFIG_FILE=" + cfgPath,
		"PODIUM_INGEST_OFFLINE=true",
		"PODIUM_IDENTITY_PROVIDER=injected-session-token",
		"PODIUM_OAUTH_AUDIENCE=" + injAudience,
	}, "serve", "--standalone")
	injRegisterRuntime(t, srv, pem)

	// A caller whose JWT carries the finance group sees the finance-only
	// artifact.
	finClaims := injClaims("alice")
	finClaims["groups"] = []string{"finance"}
	if status, body := injGet(t, srv.BaseURL+"/v1/load_artifact?id=finance/secret", injSignJWT(t, priv, finClaims)); status != 200 {
		t.Fatalf("finance caller: status=%d, want 200\nbody: %s\nlog:\n%s", status, body, srv.log())
	}

	// A caller outside the finance group cannot see it (404, no leak).
	hrClaims := injClaims("bob")
	hrClaims["groups"] = []string{"hr"}
	if status, _ := injGet(t, srv.BaseURL+"/v1/load_artifact?id=finance/secret", injSignJWT(t, priv, hrClaims)); status != 404 {
		t.Errorf("hr caller: status=%d, want 404 (filtered by group visibility)", status)
	}
}

// ---- T-D-operator-guide-25: sandbox read-only-fs via MCP PODIUM_HARNESS=none

// T-D-operator-guide-25
func TestOpGuide_25_SandboxReadOnlyFsMCP(t *testing.T) {
	t.Parallel()
	// Author an artifact with sandbox_profile: read-only-fs.
	// The none harness only advertises "unrestricted", so load_artifact
	// should fail with materialize.sandbox_unsupported.
	reg := writeRegistry(t, map[string]string{
		"sandboxed/ARTIFACT.md": "---\ntype: skill\nversion: 1.0.0\nsensitivity: low\nsandbox_profile: read-only-fs\n---\n\n<!-- Skill body lives in SKILL.md. -->\n",
		"sandboxed/SKILL.md":    skillBody("sandboxed"),
	})
	srv := startServer(t, reg)
	mat := t.TempDir()

	res := mcpExec(t,
		[]string{
			"PODIUM_REGISTRY=" + srv.BaseURL,
			"PODIUM_HARNESS=none",
			"PODIUM_MATERIALIZE_ROOT=" + mat,
			"PODIUM_CACHE_DIR=" + t.TempDir(),
		},
		rpcReq{ID: 1, Method: "initialize", Params: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "1"},
		}},
		toolCall(2, "load_artifact", map[string]any{"id": "sandboxed"}),
	)

	env2 := rpcEnvelope(t, res.Stdout, 2)
	result, _ := env2["result"].(map[string]any)
	// The result should carry an error about sandbox_profile.
	errStr, _ := result["error"].(string)
	if !strings.Contains(errStr, "sandbox") {
		t.Errorf("expected sandbox error in result; got result=%v stderr=%s", result, res.Stderr)
	}
	// No files should have been materialized.
	files := readTreeAll(t, mat)
	if len(files) > 0 {
		t.Errorf("files materialized despite sandbox error: %v", files)
	}
}

// ---- T-D-operator-guide-26: remove --public-mode and restart clears mode ----

// T-D-operator-guide-26
func TestOpGuide_26_RestartClearsPublicMode(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"ctx/ARTIFACT.md": opguideSimpleArtifact()})

	// Start server A in public mode.
	srvA := startServerArgs(t,
		[]string{"HOME=" + t.TempDir(), "PODIUM_PUBLIC_MODE=true"},
		"serve", "--standalone", "--layer-path", reg)
	hA := opguideHealthz(t, srvA.BaseURL)
	if hA["mode"] != "public" {
		t.Fatalf("server A: mode=%v, want public", hA["mode"])
	}
	stopProc(srvA.cmd)

	// Start server B without public mode.
	srvB := startServerArgs(t,
		[]string{"HOME=" + t.TempDir()},
		"serve", "--standalone", "--layer-path", reg)
	hB := opguideHealthz(t, srvB.BaseURL)
	if hB["mode"] == "public" {
		t.Errorf("server B after restart without --public-mode: mode=%v, want ready", hB["mode"])
	}
	if hB["mode"] != "ready" {
		t.Errorf("server B mode=%v, want ready", hB["mode"])
	}
}

// ---- T-D-operator-guide-27: PODIUM_PUBLIC_MODE overrides registry.yaml ------

// T-D-operator-guide-27
func TestOpGuide_27_EnvOverridesYAMLPublicMode(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cfgFile := filepath.Join(home, "registry.yaml")
	// Write a YAML config with public_mode: false.
	if err := os.WriteFile(cfgFile, []byte("public_mode: false\n"), 0o644); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}
	reg := writeRegistry(t, map[string]string{"ctx/ARTIFACT.md": opguideSimpleArtifact()})

	// Start server with env var overriding the YAML.
	srv := startServerArgs(t,
		[]string{
			"HOME=" + home,
			"PODIUM_CONFIG_FILE=" + cfgFile,
			"PODIUM_PUBLIC_MODE=true",
		},
		"serve", "--standalone", "--layer-path", reg)

	h := opguideHealthz(t, srv.BaseURL)
	if h["mode"] != "public" {
		t.Errorf("/healthz mode=%v, want public (env should override yaml public_mode:false)", h["mode"])
	}

	// Also verify config show reports the env var as the source.
	res := runPodium(t, "",
		[]string{"PODIUM_CONFIG_FILE=" + cfgFile, "PODIUM_PUBLIC_MODE=true"},
		"config", "show")
	if res.Exit != 0 {
		t.Fatalf("config show exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "PODIUM_PUBLIC_MODE") {
		t.Errorf("config show missing PODIUM_PUBLIC_MODE source:\n%s", res.Stdout)
	}
}

// ---- T-D-operator-guide-28: layer reingest triggers fresh ingest ------------

// T-D-operator-guide-28
func TestOpGuide_28_LayerReingest(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"ctx/ARTIFACT.md": opguideSimpleArtifact()})
	srv := startServer(t, reg)

	// Register the local layer.
	res := runPodium(t, "", nil,
		"layer", "register",
		"--id", "opguide-reingest",
		"--local", reg,
		"--registry", srv.BaseURL)
	if res.Exit != 0 {
		t.Fatalf("layer register exit=%d stderr=%s", res.Exit, res.Stderr)
	}

	// Reingest it. Flags precede the positional <id>.
	res2 := runPodium(t, "", nil,
		"layer", "reingest", "--registry", srv.BaseURL, "opguide-reingest")
	if res2.Exit != 0 {
		t.Fatalf("layer reingest exit=%d stderr=%s", res2.Exit, res2.Stderr)
	}
}

// ---- T-D-operator-guide-29: force_push_policy strict not settable -----------

// T-D-operator-guide-29
func TestOpGuide_29_ForcePushPolicyStrict(t *testing.T) {
	t.Skip("blocked by F-7.3.7: force_push_policy is not settable via API/CLI/config")
}

// ---- T-D-operator-guide-30: force-push default tolerant policy --------------

// T-D-operator-guide-30
func TestOpGuide_30_ForcePushDefaultTolerant(t *testing.T) {
	t.Skip("blocked by F-7.3.7: force_push_policy is not settable; cannot demonstrate tolerant vs strict contrast")
}

// ---- T-D-operator-guide-31: admin scim-sync not implemented -----------------

// T-D-operator-guide-31
func TestOpGuide_31_AdminScimSyncNotImplemented(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil,
		"admin", "scim-sync",
		"--user", "alice@acme.com",
		"--registry", "http://127.0.0.1:19999")
	if res.Exit != 2 {
		t.Errorf("exit=%d, want 2 (stderr=%s)", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "unknown admin subcommand: scim-sync") {
		t.Errorf("stderr missing 'unknown admin subcommand: scim-sync':\n%s", res.Stderr)
	}
}

// ---- T-D-operator-guide-32: /healthz always HTTP 200 -----------------------

// T-D-operator-guide-32
func TestOpGuide_32_HealthzAlways200(t *testing.T) {
	t.Parallel()
	srv := startServer(t, "")

	st, body := getRaw(t, srv.BaseURL+"/healthz")
	if st != 200 {
		t.Errorf("GET /healthz = %d, want 200\nbody: %s", st, body)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode /healthz: %v\nbody: %s", err, body)
	}
	// §13.9: liveness is the 200 status; the body carries the mode and
	// no readiness boolean (F-13.9.5).
	if m["mode"] == nil {
		t.Errorf("/healthz body missing mode: %s", body)
	}
	if _, present := m["ready"]; present {
		t.Errorf("/healthz carries undocumented `ready` field: %s", body)
	}
}

// ---- T-D-operator-guide-33: /readyz 200 in read_only mode ------------------

// T-D-operator-guide-33
func TestOpGuide_33_ReadyzInReadOnlyMode(t *testing.T) {
	t.Skip("blocked by F-13.2.3: read-only probe disabled by default; cannot trigger read_only mode in e2e to verify /readyz stays 200")
}

// ---- T-D-operator-guide-34: /metrics not exposed ---------------------------

// T-D-operator-guide-34
func TestOpGuide_34_MetricsNotExposed(t *testing.T) {
	t.Skip("blocked by F-13.8.1: /metrics route is absent from the registry handler; the test would assert 404 but the doc claims 200 with Prometheus output")
}

// opguideAlertsYAML is the alerting YAML from the operator guide, wrapped in
// a groups block for promtool validation.
const opguideAlertsYAML = `groups:
- name: podium
  rules:
  - alert: PodiumDown
    expr: up{job="podium-registry"} == 0
    for: 2m
  - alert: PodiumPostgresUnreachable
    expr: podium_postgres_up == 0
    for: 1m
  - alert: PodiumLoadArtifactSLOBreached
    expr: histogram_quantile(0.99, rate(podium_request_duration_seconds_bucket{handler="load_artifact"}[5m])) > 0.5
    for: 5m
  - alert: PodiumIngestFailingForLayer
    expr: increase(podium_ingest_total{status="failed"}[1h]) > 5
    for: 15m
  - alert: PodiumReadOnlyMode
    expr: podium_registry_mode{mode="read_only"} == 1
    for: 5m
  - alert: PodiumAuditLag
    expr: podium_audit_lag_seconds > 60
    for: 10m
  - alert: PodiumLowDescribeQuality
    expr: podium_lint_thin_descriptions_total > 50
`

// ---- T-D-operator-guide-35: alerting YAML is syntactically valid ------------

// T-D-operator-guide-35
func TestOpGuide_35_AlertingYAMLValid(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	alertFile := filepath.Join(dir, "podium-alerts.yaml")
	if err := os.WriteFile(alertFile, []byte(opguideAlertsYAML), 0o644); err != nil {
		t.Fatalf("write alerts yaml: %v", err)
	}
	res, ok := runExternal(t, dir, 30*time.Second, "promtool", "check", "rules", alertFile)
	if !ok {
		t.Skip("promtool not installed; skipping Prometheus alerting YAML syntax check")
	}
	if res.Exit != 0 {
		t.Errorf("promtool check rules exit=%d\nstdout=%s\nstderr=%s", res.Exit, res.Stdout, res.Stderr)
	}
}

// ---- T-D-operator-guide-36: PodiumDown alert fires at up==0 ----------------

// T-D-operator-guide-36
func TestOpGuide_36_PodiumDownAlertFires(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	alertFile := filepath.Join(dir, "podium-alerts.yaml")
	if err := os.WriteFile(alertFile, []byte(opguideAlertsYAML), 0o644); err != nil {
		t.Fatalf("write alerts yaml: %v", err)
	}
	testYAML := `rule_files:
  - podium-alerts.yaml
tests:
  - interval: 1m
    input_series:
      - series: 'up{job="podium-registry"}'
        values: '0+0x10'
    alert_rule_test:
      - alertname: PodiumDown
        eval_time: 3m
        exp_alerts:
          - exp_labels:
              job: podium-registry
              alertname: PodiumDown
`
	testFile := filepath.Join(dir, "podium-alerts-test.yaml")
	if err := os.WriteFile(testFile, []byte(testYAML), 0o644); err != nil {
		t.Fatalf("write test yaml: %v", err)
	}
	res, ok := runExternal(t, dir, 30*time.Second, "promtool", "test", "rules", testFile)
	if !ok {
		t.Skip("promtool not installed; skipping PodiumDown alert rule test")
	}
	if res.Exit != 0 {
		t.Errorf("promtool test rules exit=%d\nstdout=%s\nstderr=%s", res.Exit, res.Stdout, res.Stderr)
	}
}

// ---- T-D-operator-guide-37: PodiumLoadArtifactSLOBreached fires at p99 > 500ms

// T-D-operator-guide-37
func TestOpGuide_37_SLOBreachedAlertFires(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	alertFile := filepath.Join(dir, "podium-alerts.yaml")
	if err := os.WriteFile(alertFile, []byte(opguideAlertsYAML), 0o644); err != nil {
		t.Fatalf("write alerts yaml: %v", err)
	}
	// Construct a test fixture where p99 latency > 500ms.
	// All requests fall in the 1.0 second bucket.
	testYAML := `rule_files:
  - podium-alerts.yaml
tests:
  - interval: 1m
    input_series:
      - series: 'podium_request_duration_seconds_bucket{handler="load_artifact",le="0.5"}'
        values: '0+0x10'
      - series: 'podium_request_duration_seconds_bucket{handler="load_artifact",le="1.0"}'
        values: '0+10x10'
      - series: 'podium_request_duration_seconds_bucket{handler="load_artifact",le="+Inf"}'
        values: '0+10x10'
    alert_rule_test:
      - alertname: PodiumLoadArtifactSLOBreached
        eval_time: 6m
        exp_alerts:
          - exp_labels:
              handler: load_artifact
              alertname: PodiumLoadArtifactSLOBreached
`
	testFile := filepath.Join(dir, "podium-slo-test.yaml")
	if err := os.WriteFile(testFile, []byte(testYAML), 0o644); err != nil {
		t.Fatalf("write test yaml: %v", err)
	}
	res, ok := runExternal(t, dir, 30*time.Second, "promtool", "test", "rules", testFile)
	if !ok {
		t.Skip("promtool not installed; skipping PodiumLoadArtifactSLOBreached alert rule test")
	}
	if res.Exit != 0 {
		t.Errorf("promtool test rules exit=%d\nstdout=%s\nstderr=%s", res.Exit, res.Stdout, res.Stderr)
	}
}

// ---- §7.1 latency SLO surface: per-request access log (F-7.1.2) -------------

// The running standalone server times each meta-tool request and emits a
// structured access-log line keyed by operation name, so an operator has a
// timing surface to compare against the §7.1 SLO budgets. The liveness probe
// carries no SLO budget and is excluded.
//
// spec: §7.1 Latency budgets (SLO targets, server source) — F-7.1.2
func TestOpGuide_AccessLogLatencySurface(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"finance/run/ARTIFACT.md": "---\ntype: context\nversion: 1.0.0\ntags: [finance]\ndescription: Run variance analysis for vendor payments here today.\n---\n\nBody.\n",
	})
	srv := opguideStartStandalone(t, []string{"HOME=" + t.TempDir()}, reg)

	// An SLO-budgeted meta-tool request must produce one access line.
	if st, body := getRaw(t, srv.BaseURL+"/v1/search_artifacts?q=variance"); st != 200 {
		t.Fatalf("search_artifacts status=%d, want 200\nbody: %s", st, body)
	}

	// The access line is written after the response is sent, so poll the
	// captured server log with a bounded wait.
	want := "access op=search_artifacts status=200 duration_ms="
	deadline := time.Now().Add(5 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		if strings.Contains(srv.log(), want) {
			found = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !found {
		t.Fatalf("access log missing %q\nlog:\n%s", want, srv.log())
	}
	// The startup line announces the surface is enabled by default.
	if !strings.Contains(srv.log(), "access log: enabled") {
		t.Errorf("startup log missing the access-log enable line:\n%s", srv.log())
	}
	// The liveness/readiness probes are not SLO operations; they must not
	// leak into the access log as observed operations.
	if logText := srv.log(); strings.Contains(logText, "op=health") || strings.Contains(logText, "op=ready") {
		t.Errorf("access log observed an excluded probe:\n%s", logText)
	}
}

// PODIUM_ACCESS_LOG=false silences the §7.1 access log without affecting the
// rest of the server, for an operator who routes latency through a different
// sink.
//
// spec: §7.1 — F-7.1.2
func TestOpGuide_AccessLogDisabled(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{"ctx/ARTIFACT.md": opguideSimpleArtifact()})
	srv := opguideStartStandalone(t,
		[]string{"HOME=" + t.TempDir(), "PODIUM_ACCESS_LOG=false"}, reg)

	if st, _ := getRaw(t, srv.BaseURL+"/v1/search_artifacts?q=test"); st != 200 {
		t.Fatalf("search_artifacts status=%d, want 200", st)
	}
	// Give the server a moment in case a line were (wrongly) emitted.
	time.Sleep(300 * time.Millisecond)
	if logText := srv.log(); strings.Contains(logText, "access op=") {
		t.Errorf("PODIUM_ACCESS_LOG=false still emitted an access line:\n%s", logText)
	}
	if logText := srv.log(); strings.Contains(logText, "access log: enabled") {
		t.Errorf("PODIUM_ACCESS_LOG=false still logged the enable line:\n%s", logText)
	}
}

// ---- T-D-operator-guide-38: scope preview gating ---------------------------

// T-D-operator-guide-38: an operator disables the §3.5 scope-preview
// surface per tenant via registry.yaml's tenant.expose_scope_preview, and
// the endpoint then answers 403 scope_preview_disabled. F-3.5.1.
func TestOpGuide_38_ScopePreviewGating(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".podium")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "registry.yaml"),
		[]byte("tenant:\n  expose_scope_preview: false\n"), 0o644); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}

	srv := startServerArgs(t, []string{"HOME=" + home}, "serve", "--standalone")
	st, body := getRaw(t, srv.BaseURL+"/v1/scope/preview")
	if st != 403 {
		t.Fatalf("scope/preview status = %d, want 403 (gated by registry.yaml tenant.expose_scope_preview)\nbody:\n%s", st, body)
	}
	if !strings.Contains(string(body), "scope_preview_disabled") {
		t.Errorf("403 body missing scope_preview_disabled code:\n%s", body)
	}

	// The operator can confirm the resolved setting through config show.
	res := runPodium(t, "", []string{"HOME=" + home}, "config", "show")
	if res.Exit != 0 {
		t.Fatalf("config show exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "tenant.expose_scope_preview") {
		t.Errorf("config show missing tenant.expose_scope_preview setting:\n%s", res.Stdout)
	}
}

// ---- T-D-operator-guide-39: PODIUM_CACHE_DIR defaults to ~/.podium/cache/ ---

// T-D-operator-guide-39
func TestOpGuide_39_CacheDirDefault(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	srv := startServer(t, "")
	res := runPodium(t, "",
		[]string{"HOME=" + home},
		"status", "--registry", srv.BaseURL)
	if res.Exit != 0 {
		t.Fatalf("status exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "cache dir:") {
		t.Errorf("status missing 'cache dir:' line:\n%s", res.Stdout)
	}
	// The default should point to ~/.podium/cache/ (resolved under our temp home).
	if !strings.Contains(res.Stdout, ".podium/cache") {
		t.Errorf("cache dir not pointing to .podium/cache:\n%s", res.Stdout)
	}
}

// ---- T-D-operator-guide-40: admin verify audit chain not implemented --------

// T-D-operator-guide-40
func TestOpGuide_40_AdminVerifyAuditChainGap(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "", nil,
		"admin", "verify", "--check", "audit-chain",
		"--registry", "http://127.0.0.1:19999")
	if res.Exit != 2 {
		t.Errorf("exit=%d, want 2 (stderr=%s)", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "unknown admin subcommand: verify") {
		t.Errorf("stderr missing 'unknown admin subcommand: verify':\n%s", res.Stderr)
	}
}

// ---- T-D-operator-guide-41: repeated signature_invalid investigation --------

// T-D-operator-guide-41
func TestOpGuide_41_RepeatedSignatureInvalid(t *testing.T) {
	t.Skip("requires a signed artifact whose stored bytes are then tampered; not expressible from filesystem bootstrap")
}

// ---- T-D-operator-guide-42: config show embedding provider and model --------

// T-D-operator-guide-42
func TestOpGuide_42_ConfigShowEmbeddingProviderModel(t *testing.T) {
	t.Parallel()
	res := runPodium(t, "",
		[]string{"PODIUM_EMBEDDING_PROVIDER=voyage", "PODIUM_EMBEDDING_MODEL=voyage-3"},
		"config", "show")
	if res.Exit != 0 {
		t.Fatalf("config show exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	for _, want := range []struct{ name, val, src string }{
		{"embedding_provider", "voyage", "PODIUM_EMBEDDING_PROVIDER"},
		{"embedding_model", "voyage-3", "PODIUM_EMBEDDING_MODEL"},
	} {
		if !strings.Contains(res.Stdout, want.name) {
			t.Errorf("config show missing setting %q:\n%s", want.name, res.Stdout)
		}
		if !strings.Contains(res.Stdout, want.val) {
			t.Errorf("config show missing value %q for %s:\n%s", want.val, want.name, res.Stdout)
		}
		if !strings.Contains(res.Stdout, want.src) {
			t.Errorf("config show missing source %q for %s:\n%s", want.src, want.name, res.Stdout)
		}
	}
}

// ---- T-D-operator-guide-43: OIDC group claim mapping via audit log ----------

// T-D-operator-guide-43
func TestOpGuide_43_OIDCGroupClaimMapping(t *testing.T) {
	t.Skip("requires a standard deployment with OIDC configured and a JWT with specific group claims; not available in standalone e2e")
}

// ---- T-D-operator-guide-44: clock skew causes auth.token_expired -----------

// T-D-operator-guide-44
func TestOpGuide_44_ClockSkewTokenExpired(t *testing.T) {
	t.Skip("requires OIDC identity configured and a JWT with exp shifted beyond 60s tolerance; needs real IdP JWKS setup not available in standalone e2e")
}

// ---- T-D-operator-guide-45: rolling upgrade coexistence ---------------------

// T-D-operator-guide-45
func TestOpGuide_45_RollingUpgradeCoexistence(t *testing.T) {
	t.Skip("requires two binary versions sharing a Postgres database with expand-contract migration applied; not available in e2e")
}

// ---- T-D-operator-guide-46: rollback before finalize is safe ----------------

// T-D-operator-guide-46
func TestOpGuide_46_RollbackBeforeFinalize(t *testing.T) {
	t.Skip("requires two binary versions sharing a Postgres database; expand-contract migration with finalize step not available in e2e")
}

// ---- T-D-operator-guide-47: restore verification content_hash matches ------

// T-D-operator-guide-47
func TestOpGuide_47_RestoreVerificationContentHash(t *testing.T) {
	t.Skip("requires Postgres PITR + S3 bucket restore; not available in standalone e2e")
}

// ---- T-D-operator-guide-48: PODIUM_NO_AUTOSTANDALONE in test harness --------

// T-D-operator-guide-48
func TestOpGuide_48_NoAutostandaloneInHarness(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	harnessFile := filepath.Join(root, "internal", "testharness", "cmdharness", "cmdharness.go")
	content, err := os.ReadFile(harnessFile)
	if err != nil {
		t.Fatalf("read cmdharness.go: %v", err)
	}
	src := string(content)
	if !strings.Contains(src, "PODIUM_NO_AUTOSTANDALONE=1") {
		t.Errorf("cmdharness.go does not contain PODIUM_NO_AUTOSTANDALONE=1:\n%s", harnessFile)
	}

	// Also verify the harness actually prevents autostandalone by starting
	// a process via cmdharness.Bin and confirming it doesn't auto-bootstrap.
	bin := cmdharness.Bin(t, "podium")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "status")
	cmd.Env = mergeEnv("PODIUM_NO_AUTOSTANDALONE=1", "HOME="+t.TempDir(), "PODIUM_REGISTRY=")
	cmd.Stdin = bytes.NewReader(nil)
	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf
	// status exits 0 regardless; we just check it doesn't try to spin up a server
	_ = cmd.Run()
	out := outBuf.String()
	// The process should not have tried to start a server.
	if strings.Contains(out, "listening") {
		t.Errorf("autostandalone server started despite PODIUM_NO_AUTOSTANDALONE=1:\n%s", out)
	}
}
