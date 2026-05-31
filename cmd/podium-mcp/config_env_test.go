package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/sign"
)

// chdirTemp switches the working directory to a fresh temp dir for the
// test and restores it afterward, so sync.yaml discovery (§7.5.2) starts
// from a known-empty workspace.
func chdirTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	return dir
}

// hermetic isolates HOME and the working directory so neither a real
// ~/.podium/sync.yaml nor a workspace sync.yaml leaks into a config test.
func hermetic(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PODIUM_CONFIG", "")
	chdirTemp(t)
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	fn()
	_ = w.Close()
	os.Stderr = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

// spec: §6.2 — PODIUM_VERIFY_SIGNATURES must be a recognized policy.
// An unknown value is refused at startup (F-6.2.1) instead of silently
// disabling signature verification.
func TestLoadConfig_RejectsUnknownVerifyPolicy(t *testing.T) {
	hermetic(t)
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
	t.Setenv("PODIUM_VERIFY_SIGNATURES", "medium-and-aboe")
	if _, err := loadConfig(); err == nil {
		t.Fatal("unknown PODIUM_VERIFY_SIGNATURES: no error")
	}
}

func TestLoadConfig_AcceptsKnownVerifyPolicies(t *testing.T) {
	for _, p := range []string{"never", "medium-and-above", "always"} {
		t.Run(p, func(t *testing.T) {
			hermetic(t)
			t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
			t.Setenv("PODIUM_VERIFY_SIGNATURES", p)
			if _, err := loadConfig(); err != nil {
				t.Fatalf("policy %q: %v", p, err)
			}
		})
	}
}

// spec: §6.2 — PODIUM_IDENTITY_PROVIDER selects a built-in provider; an
// unrecognized value is refused at startup (F-6.2.3).
func TestLoadConfig_RejectsUnknownIdentityProvider(t *testing.T) {
	hermetic(t)
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
	t.Setenv("PODIUM_IDENTITY_PROVIDER", "mtls")
	if _, err := loadConfig(); err == nil {
		t.Fatal("unknown PODIUM_IDENTITY_PROVIDER: no error")
	}
}

func TestLoadConfig_AcceptsKnownIdentityProviders(t *testing.T) {
	for _, p := range []string{"oauth-device-code", "injected-session-token"} {
		t.Run(p, func(t *testing.T) {
			hermetic(t)
			t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
			t.Setenv("PODIUM_IDENTITY_PROVIDER", p)
			cfg, err := loadConfig()
			if err != nil {
				t.Fatalf("provider %q: %v", p, err)
			}
			if cfg.identityProvider != p {
				t.Errorf("identityProvider = %q, want %q", cfg.identityProvider, p)
			}
		})
	}
}

// spec: §6.9 "Unknown PODIUM_HARNESS value" — the bridge refuses to start
// and the error lists the available adapter values, instead of detecting the
// unknown harness lazily on the first load_artifact materialization (F-6.9.2).
func TestLoadConfig_RejectsUnknownHarness(t *testing.T) {
	hermetic(t)
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
	t.Setenv("PODIUM_HARNESS", "claude-codex-typo")
	_, err := loadConfig()
	if err == nil {
		t.Fatal("unknown PODIUM_HARNESS: no error")
	}
	if !strings.Contains(err.Error(), "config.unknown_harness") {
		t.Errorf("error %q missing config.unknown_harness code", err)
	}
	// The error enumerates the registered adapters so the operator can pick a
	// valid value; "none" is always registered.
	if !strings.Contains(err.Error(), "none") {
		t.Errorf("error %q does not list the available adapters", err)
	}
}

// spec: §6.9 — a registered PODIUM_HARNESS (including the default "none")
// starts cleanly (F-6.9.2).
func TestLoadConfig_AcceptsKnownHarness(t *testing.T) {
	for _, h := range []string{"none", "claude-code", "cursor"} {
		t.Run(h, func(t *testing.T) {
			hermetic(t)
			t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
			t.Setenv("PODIUM_HARNESS", h)
			cfg, err := loadConfig()
			if err != nil {
				t.Fatalf("harness %q: %v", h, err)
			}
			if cfg.harness != h {
				t.Errorf("harness = %q, want %q", cfg.harness, h)
			}
		})
	}
}

// spec: §6.1 / §7.5.2 — the MCP server requires a server-source registry.
// A PODIUM_REGISTRY value that is not an http:// or https:// URL is a
// filesystem source under the §7.5.2 dispatch rule and the bridge refuses
// to start rather than failing opaquely on the first tool call (F-6.1.1).
func TestLoadConfig_RejectsFilesystemRegistry(t *testing.T) {
	for _, reg := range []string{
		"/srv/registry",        // absolute filesystem path
		"./registry",           // relative filesystem path
		"file:///srv/registry", // file:// URI
		"registry.local",       // bare host-like value, no scheme
	} {
		t.Run(reg, func(t *testing.T) {
			hermetic(t)
			t.Setenv("PODIUM_REGISTRY", reg)
			_, err := loadConfig()
			if err == nil {
				t.Fatalf("filesystem registry %q: loadConfig returned no error", reg)
			}
			if !strings.Contains(err.Error(), "config.filesystem_registry_unsupported") {
				t.Errorf("error %q lacks config.filesystem_registry_unsupported code", err)
			}
			if !strings.Contains(err.Error(), reg) {
				t.Errorf("error %q does not name the offending value %q", err, reg)
			}
		})
	}
}

// spec: §6.1 / §7.5.2 — http:// and https:// registries are server sources
// and pass startup.
func TestLoadConfig_AcceptsServerRegistry(t *testing.T) {
	for _, reg := range []string{"http://127.0.0.1:8080", "https://podium.acme.com"} {
		t.Run(reg, func(t *testing.T) {
			hermetic(t)
			t.Setenv("PODIUM_REGISTRY", reg)
			cfg, err := loadConfig()
			if err != nil {
				t.Fatalf("server registry %q: %v", reg, err)
			}
			if cfg.registry != reg {
				t.Errorf("registry = %q, want %q", cfg.registry, reg)
			}
		})
	}
}

// spec: §6.2 / §7.5.2 / §6.11 — when PODIUM_REGISTRY is unset the bridge
// falls back to defaults.registry from the workspace sync.yaml (F-6.2.2).
func TestLoadConfig_RegistryFromWorkspaceSyncYAML(t *testing.T) {
	hermetic(t)
	t.Setenv("PODIUM_REGISTRY", "")
	ws, _ := os.Getwd()
	if err := os.MkdirAll(filepath.Join(ws, ".podium"), 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "defaults:\n  registry: https://podium.acme.com\n"
	if err := os.WriteFile(filepath.Join(ws, ".podium", "sync.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.registry != "https://podium.acme.com" {
		t.Errorf("registry = %q, want https://podium.acme.com", cfg.registry)
	}
}

// spec: §6.2 / §6.11 — the home-global ~/.podium/sync.yaml the standalone
// recipe bootstraps supplies the registry when no workspace overlay does.
func TestLoadConfig_RegistryFromHomeSyncYAML(t *testing.T) {
	hermetic(t)
	t.Setenv("PODIUM_REGISTRY", "")
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".podium"), 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "defaults:\n  registry: http://127.0.0.1:8080\n"
	if err := os.WriteFile(filepath.Join(home, ".podium", "sync.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.registry != "http://127.0.0.1:8080" {
		t.Errorf("registry = %q, want http://127.0.0.1:8080", cfg.registry)
	}
}

// spec: §6.2 / §6.5 — when PODIUM_CACHE_DIR is unset and the home
// directory cannot be resolved, the cache is disabled with a warning
// rather than silently (F-6.2.8). Startup still succeeds.
func TestLoadConfig_CacheDirWarnsWhenHomeUnresolvable(t *testing.T) {
	hermetic(t)
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
	t.Setenv("PODIUM_CACHE_DIR", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	var cfg *config
	out := captureStderr(t, func() {
		var err error
		cfg, err = loadConfig()
		if err != nil {
			t.Fatalf("loadConfig: %v", err)
		}
	})
	if cfg.cacheDir != "" {
		t.Errorf("cacheDir = %q, want empty (cache disabled)", cfg.cacheDir)
	}
	if !strings.Contains(out, "PODIUM_CACHE_DIR") || !strings.Contains(strings.ToLower(out), "cache disabled") {
		t.Errorf("expected a cache-disabled warning on stderr, got %q", out)
	}
}

// spec: §6.1 / §6.2 — command-line flags are accepted and override env
// vars (F-6.2.6).
func TestApplyFlagsAndConfig_FlagOverridesEnv(t *testing.T) {
	t.Parallel()
	c := &config{harness: "none", registry: "http://env"}
	if err := applyFlagsAndConfig(c, []string{"--harness=claude-code", "--registry", "http://flag"}); err != nil {
		t.Fatal(err)
	}
	if c.harness != "claude-code" {
		t.Errorf("harness = %q, want claude-code", c.harness)
	}
	if c.registry != "http://flag" {
		t.Errorf("registry = %q, want http://flag", c.registry)
	}
}

// spec: §6.1 / §6.2 — a config file is accepted; explicit flags override
// the config file, which overrides env (F-6.2.6).
func TestApplyFlagsAndConfig_ConfigFilePrecedence(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "podium.yaml")
	body := "harness: cursor\nverify-signatures: always\naudit-sink: /tmp/a.log\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c := &config{harness: "none"}
	// Config file sets harness=cursor; the explicit flag overrides it.
	if err := applyFlagsAndConfig(c, []string{"--config", path, "--harness=opencode"}); err != nil {
		t.Fatal(err)
	}
	if c.harness != "opencode" {
		t.Errorf("harness = %q, want opencode (flag over config)", c.harness)
	}
	if string(c.verifyPolicy) != "always" {
		t.Errorf("verifyPolicy = %q, want always (from config)", c.verifyPolicy)
	}
	if c.auditSink != "/tmp/a.log" || !c.auditSinkSet {
		t.Errorf("auditSink = %q set=%v, want /tmp/a.log true", c.auditSink, c.auditSinkSet)
	}
}

// spec: §6.2 — the flag parser ignores unrelated flags (such as the Go
// test runner's -test.* flags) so loadConfig stays callable under test.
func TestParseFlags_IgnoresUnknown(t *testing.T) {
	t.Parallel()
	flags, cfgPath := parseFlags([]string{"-test.v=true", "-test.run", "TestX", "--registry=http://r", "--config=/c.yaml"})
	if cfgPath != "/c.yaml" {
		t.Errorf("config path = %q, want /c.yaml", cfgPath)
	}
	if flags["registry"] != "http://r" {
		t.Errorf("registry flag = %q", flags["registry"])
	}
	c := &config{}
	for k, v := range flags {
		applyConfigKV(c, k, v)
	}
	if c.registry != "http://r" {
		t.Errorf("registry = %q after apply", c.registry)
	}
}

// spec: §6.2 — PODIUM_AUDIT_SINK: unset leaves auditing to the registry;
// "default" (or empty) selects ~/.podium/audit.log; a path selects that
// file (F-6.2.4).
func TestNewAuditSink(t *testing.T) {
	t.Run("unset is nil", func(t *testing.T) {
		sink, err := newAuditSink(&config{auditSinkSet: false})
		if err != nil {
			t.Fatal(err)
		}
		if sink != nil {
			t.Errorf("unset sink = %v, want nil", sink)
		}
	})
	t.Run("default path", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		sink, err := newAuditSink(&config{auditSinkSet: true, auditSink: "default"})
		if err != nil {
			t.Fatal(err)
		}
		fs, ok := sink.(*audit.FileSink)
		if !ok {
			t.Fatalf("sink type = %T", sink)
		}
		if want := filepath.Join(home, ".podium", "audit.log"); fs.Path() != want {
			t.Errorf("path = %q, want %q", fs.Path(), want)
		}
	})
	t.Run("explicit path", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "audit.log")
		sink, err := newAuditSink(&config{auditSinkSet: true, auditSink: path})
		if err != nil {
			t.Fatal(err)
		}
		fs := sink.(*audit.FileSink)
		if fs.Path() != path {
			t.Errorf("path = %q, want %q", fs.Path(), path)
		}
	})
}

// spec: §6.2 — a configured sink records a local audit event for a
// meta-tool call; an unset sink is a silent no-op (F-6.2.4).
func TestAuditMeta_AppendsEvent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &mcpServer{cfg: &config{}, audit: sink, sessionID: "sess-1"}
	s.auditMeta(audit.EventArtifactLoaded, "acme/widget")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "artifact.loaded") || !strings.Contains(string(data), "acme/widget") {
		t.Errorf("audit line missing event/target: %s", data)
	}
	// Nil sink: no panic, no write.
	(&mcpServer{cfg: &config{}}).auditMeta(audit.EventArtifactLoaded, "x")
}

// spec: §6.2 — callTool records the per-tool audit event before
// dispatching, so a local audit captures the call even when the
// downstream registry is unreachable (F-6.2.4).
func TestCallTool_EmitsAuditEvent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")
	sink, err := audit.NewFileSink(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &mcpServer{
		cfg:   &config{registry: "http://127.0.0.1:1"},
		http:  &http.Client{},
		audit: sink,
	}
	raw := []byte(`{"name":"load_domain","arguments":{"path":"acme/docs"}}`)
	_ = s.callTool(raw)
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "domain.loaded") || !strings.Contains(string(data), "acme/docs") {
		t.Errorf("expected domain.loaded audit for path acme/docs, got: %s", data)
	}
}

// spec: §6.2 / §6.6 — the per-call destination is read from the
// load_artifact arguments under destination/materialize_root/path
// (F-6.2.5).
func TestDestFromArgs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"destination": "/d"}, "/d"},
		{map[string]any{"materialize_root": "/m"}, "/m"},
		{map[string]any{"path": "/p"}, "/p"},
		{map[string]any{"destination": "/d", "path": "/p"}, "/d"},
		{map[string]any{}, ""},
		{map[string]any{"destination": ""}, ""},
		{map[string]any{"destination": 7}, ""},
	}
	for _, c := range cases {
		if got := destFromArgs(c.args); got != c.want {
			t.Errorf("destFromArgs(%v) = %q, want %q", c.args, got, c.want)
		}
	}
}

// spec: §6.2 — PODIUM_PRESIGN_TTL_SECONDS is a registry-side parameter;
// the MCP bridge neither requires nor consumes it. Setting it does not
// affect bridge startup (F-6.2.7).
func TestLoadConfig_IgnoresPresignTTL(t *testing.T) {
	hermetic(t)
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
	t.Setenv("PODIUM_PRESIGN_TTL_SECONDS", "120")
	if _, err := loadConfig(); err != nil {
		t.Fatalf("loadConfig with PODIUM_PRESIGN_TTL_SECONDS set: %v", err)
	}
}

// spec: §6.2 / §6.6 — when PODIUM_MATERIALIZE_ROOT is unset, a per-call
// `destination` argument drives materialization, so the host can supply
// the destination per load_artifact call (F-6.2.5). Without either, the
// artifact is returned but nothing is written to disk.
func TestLoadArtifact_PerCallDestinationMaterializes(t *testing.T) {
	t.Parallel()
	respBody := loadArtifactJSON(t, map[string]any{
		"id": "acme/widget", "type": "context", "version": "1.0.0",
		"manifest_body": "body", "frontmatter": "---\ntype: context\n---\n",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(respBody))
	}))
	defer srv.Close()
	// materializeRoot is intentionally empty: only the per-call
	// destination should drive the write.
	s := newTestServer(t, &config{registry: srv.URL, harness: "none", verifyPolicy: sign.PolicyNever})

	dest := t.TempDir()
	got := s.loadArtifact(map[string]any{"id": "acme/widget", "destination": dest})
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("type = %T (%v)", got, got)
	}
	paths, _ := m["materialized_at"].([]string)
	if len(paths) == 0 {
		t.Fatalf("materialized_at empty; per-call destination did not materialize: %v", m)
	}
	for _, p := range paths {
		if !strings.HasPrefix(p, dest) {
			t.Errorf("materialized path %q is not under per-call destination %q", p, dest)
		}
		if _, err := os.Stat(p); err != nil {
			t.Errorf("materialized file missing: %v", err)
		}
	}

	// Control: no destination and no PODIUM_MATERIALIZE_ROOT → returned
	// but not written.
	got2 := s.loadArtifact(map[string]any{"id": "acme/widget"})
	m2, _ := got2.(map[string]any)
	if paths2, _ := m2["materialized_at"].([]string); len(paths2) != 0 {
		t.Errorf("materialized_at = %v, want empty when no destination configured", paths2)
	}
}
