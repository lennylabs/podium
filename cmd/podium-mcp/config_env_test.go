package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
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
// ~/.podium/sync.yaml nor a workspace sync.yaml leaks into a config test. It
// also sets PODIUM_VERIFY_SIGNATURES=never and clears the signing variables,
// because the empty home holds no registry key file and a policy above never
// with no verification material refuses the start; a case whose subject is
// the signature policy sets its own policy and material.
func hermetic(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PODIUM_CONFIG", "")
	t.Setenv("PODIUM_VERIFY_SIGNATURES", "never")
	for _, k := range []string{"PODIUM_SIGNATURE_PROVIDER", "PODIUM_SIGNATURE_VERIFY_KEY", "PODIUM_SIGNATURE_KEY_ID", "PODIUM_SIGN_KEY_PATH", "PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE"} {
		t.Setenv(k, "")
	}
	chdirTemp(t)
}

// testVerifyKey returns a fresh base64 Ed25519 public key for a case that
// needs verification material under a policy above never.
func testVerifyKey(t *testing.T) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(pub)
}

// writeHomeKeyFile writes a registry key file at the sign.KeyFilePath default
// under the current HOME, in the format the registry writes, and returns its
// public half.
func writeHomeKeyFile(t *testing.T) ed25519.PublicKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	path, err := sign.KeyFilePath("")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "private: " + base64.StdEncoding.EncodeToString(priv) + "\n" +
		"public: " + base64.StdEncoding.EncodeToString(pub) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return pub
}

// writeSyncFile writes body to path, creating its directory.
func writeSyncFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// captureStderr redirects os.Stderr for the duration of fn and returns what fn
// wrote.
//
// The caller must not be a parallel test. os.Stderr is process-wide, so the
// swap is visible to every goroutine, and Go releases parallel tests only once
// the sequential ones have finished. A sequential capture therefore runs while
// nothing else does, while a capture from a parallel test runs alongside every
// other parallel test and races with any that writes to the same stream. That
// combination produced intermittent CI failures in cmd/podium, in whichever
// test lost the interleaving rather than in the one that captured.
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
// An unknown value is refused at startup instead of silently
// disabling signature verification.
func TestLoadConfig_RejectsUnknownVerifyPolicy(t *testing.T) {
	hermetic(t)
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
	t.Setenv("PODIUM_VERIFY_SIGNATURES", "medium-and-aboe")
	if _, err := loadConfig(); err == nil {
		t.Fatal("unknown PODIUM_VERIFY_SIGNATURES: no error")
	}
}

// spec: §6.2 — never and always are the accepted values. Each arm supplies a
// verification key, which the always arm needs and the never arm ignores.
func TestLoadConfig_AcceptsKnownVerifyPolicies(t *testing.T) {
	for _, p := range []string{"never", "always"} {
		t.Run(p, func(t *testing.T) {
			hermetic(t)
			t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:1")
			t.Setenv("PODIUM_VERIFY_SIGNATURES", p)
			t.Setenv("PODIUM_SIGNATURE_VERIFY_KEY", testVerifyKey(t))
			if _, err := loadConfig(); err != nil {
				t.Fatalf("policy %q: %v", p, err)
			}
		})
	}
}

// spec: §6.2 — PODIUM_IDENTITY_PROVIDER selects a built-in provider; an
// unrecognized value is refused at startup.
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
// unknown harness lazily on the first load_artifact materialization.
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
// starts cleanly.
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
// to start rather than failing opaquely on the first tool call.
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
// falls back to defaults.registry from the workspace sync.yaml.
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

// spec: §4.7.9 / §7.5.2 — an operator-set defaults.verify_signatures: never
// in the home-global ~/.podium/sync.yaml wins over the always default when
// PODIUM_VERIFY_SIGNATURES is unset, and the bridge announces it with one
// stderr line naming the file and the key to remove. No material is resolved
// under never.
func TestLoadConfig_VerifySignaturesFromSyncYAML(t *testing.T) {
	hermetic(t)
	t.Setenv("PODIUM_REGISTRY", "")
	t.Setenv("PODIUM_VERIFY_SIGNATURES", "")
	path := filepath.Join(os.Getenv("HOME"), ".podium", "sync.yaml")
	writeSyncFile(t, path, "defaults:\n  registry: http://127.0.0.1:8080\n  verify_signatures: never\n")
	var cfg *config
	out := captureStderr(t, func() {
		var err error
		if cfg, err = loadConfig(); err != nil {
			t.Fatalf("loadConfig: %v", err)
		}
	})
	if cfg.verifyPolicy != "never" {
		t.Errorf("verifyPolicy = %q, want never (from sync.yaml)", cfg.verifyPolicy)
	}
	if cfg.verifier != nil {
		t.Errorf("verifier = %v, want nil under never", cfg.verifier)
	}
	if !strings.Contains(out, path) || !strings.Contains(out, "defaults.verify_signatures") {
		t.Errorf("stderr %q does not name %s and defaults.verify_signatures", out, path)
	}
}

// spec: §4.7.9 / §6.2 — a never the environment supplied logs nothing, even
// when a sync.yaml carries never too, because the file did not supply it.
func TestLoadConfig_EnvNeverLogsNoWarning(t *testing.T) {
	hermetic(t)
	t.Setenv("PODIUM_REGISTRY", "")
	path := filepath.Join(os.Getenv("HOME"), ".podium", "sync.yaml")
	writeSyncFile(t, path, "defaults:\n  registry: http://127.0.0.1:8080\n  verify_signatures: never\n")
	out := captureStderr(t, func() {
		if _, err := loadConfig(); err != nil {
			t.Fatalf("loadConfig: %v", err)
		}
	})
	if strings.Contains(out, "verify_signatures") {
		t.Errorf("stderr %q carries the stale-never warning for an environment-set never", out)
	}
}

// spec: §7.5.2 — the workspace's sync.local.yaml outranks its sync.yaml for
// defaults.verify_signatures, read from a subdirectory of the workspace.
func TestLoadConfig_VerifySignaturesLocalOutranksShared(t *testing.T) {
	hermetic(t)
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:8080")
	t.Setenv("PODIUM_VERIFY_SIGNATURES", "")
	t.Setenv("PODIUM_SIGNATURE_VERIFY_KEY", testVerifyKey(t))
	ws, _ := os.Getwd()
	writeSyncFile(t, filepath.Join(ws, ".podium", "sync.yaml"), "defaults:\n  verify_signatures: never\n")
	writeSyncFile(t, filepath.Join(ws, ".podium", "sync.local.yaml"), "defaults:\n  verify_signatures: always\n")
	chdirSub(t, ws)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.verifyPolicy != sign.PolicyAlways {
		t.Errorf("verifyPolicy = %q, want always from sync.local.yaml", cfg.verifyPolicy)
	}
}

// spec: §7.5.2 — the workspace is discovered by walking up from the working
// directory, so a never in the workspace's sync.local.yaml applies from a
// subdirectory, and the warning names that file.
func TestLoadConfig_VerifySignaturesWalksUpToTheWorkspace(t *testing.T) {
	hermetic(t)
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:8080")
	t.Setenv("PODIUM_VERIFY_SIGNATURES", "")
	ws, _ := os.Getwd()
	local := filepath.Join(ws, ".podium", "sync.local.yaml")
	writeSyncFile(t, local, "defaults:\n  verify_signatures: never\n")
	chdirSub(t, ws)
	var cfg *config
	out := captureStderr(t, func() {
		var err error
		if cfg, err = loadConfig(); err != nil {
			t.Fatalf("loadConfig: %v", err)
		}
	})
	if cfg.verifyPolicy != sign.PolicyNever {
		t.Errorf("verifyPolicy = %q, want never from the workspace sync.local.yaml", cfg.verifyPolicy)
	}
	if !strings.Contains(out, local) {
		t.Errorf("stderr %q does not name %s", out, local)
	}
}

// chdirSub moves the working directory into a fresh subdirectory of dir. The
// chdirTemp cleanup registered by hermetic restores the original directory.
func chdirSub(t *testing.T, dir string) {
	t.Helper()
	sub := filepath.Join(dir, "nested", "deeper")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(sub); err != nil {
		t.Fatal(err)
	}
}

// spec: §6.2 / §7.5.2 — an explicit PODIUM_VERIFY_SIGNATURES overrides a
// sync.yaml value.
func TestLoadConfig_VerifySignaturesEnvOverridesSyncYAML(t *testing.T) {
	hermetic(t)
	t.Setenv("PODIUM_REGISTRY", "")
	t.Setenv("PODIUM_VERIFY_SIGNATURES", "always")
	t.Setenv("PODIUM_SIGNATURE_VERIFY_KEY", testVerifyKey(t))
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".podium"), 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "defaults:\n  registry: http://127.0.0.1:8080\n  verify_signatures: never\n"
	if err := os.WriteFile(filepath.Join(home, ".podium", "sync.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.verifyPolicy != "always" {
		t.Errorf("verifyPolicy = %q, want always (env overrides sync.yaml)", cfg.verifyPolicy)
	}
}

// spec: §4.7.9 / §6.2 — with neither env nor sync.yaml setting them, the
// policy is always and the provider is registry-managed.
func TestLoadConfig_VerifySignaturesDefaultsToAlways(t *testing.T) {
	hermetic(t)
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:8080")
	t.Setenv("PODIUM_VERIFY_SIGNATURES", "")
	t.Setenv("PODIUM_SIGNATURE_VERIFY_KEY", testVerifyKey(t))
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.verifyPolicy != sign.PolicyAlways {
		t.Errorf("verifyPolicy = %q, want always (default)", cfg.verifyPolicy)
	}
	if cfg.signatureProvider != "registry-managed" {
		t.Errorf("signatureProvider = %q, want registry-managed (default)", cfg.signatureProvider)
	}
}

// spec: §6.2 — medium-and-above is no longer a policy value. The environment
// value refuses naming never and always, and a sync.yaml value refuses naming
// the file too.
func TestLoadConfig_RejectsMediumAndAbove(t *testing.T) {
	t.Run("env", func(t *testing.T) {
		hermetic(t)
		t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:8080")
		t.Setenv("PODIUM_VERIFY_SIGNATURES", "medium-and-above")
		_, err := loadConfig()
		if err == nil || !strings.Contains(err.Error(), "never | always") {
			t.Fatalf("loadConfig = %v, want an error naming never | always", err)
		}
	})
	t.Run("sync.yaml", func(t *testing.T) {
		hermetic(t)
		t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:8080")
		t.Setenv("PODIUM_VERIFY_SIGNATURES", "")
		path := filepath.Join(os.Getenv("HOME"), ".podium", "sync.yaml")
		writeSyncFile(t, path, "defaults:\n  verify_signatures: medium-and-above\n")
		_, err := loadConfig()
		if err == nil || !strings.Contains(err.Error(), "never | always") || !strings.Contains(err.Error(), path) {
			t.Fatalf("loadConfig = %v, want an error naming never | always and %s", err, path)
		}
	})
}

// spec: §6.2 / §6.5 — when PODIUM_CACHE_DIR is unset and the home
// directory cannot be resolved, the cache is disabled with a warning
// rather than silently. Startup still succeeds.
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
// vars.
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
// the config file, which overrides env.
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
// file.
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
// meta-tool call; an unset sink is a silent no-op.
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
// downstream registry is unreachable.
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
// load_artifact arguments under destination/materialize_root/path.
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
// affect bridge startup.
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
// the destination per load_artifact call. Without either, the
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

// spec: §4.7.9, §6.2, §6.9 — loadConfig resolves the verification material
// once, through resolveVerifier, with one case per cell of the resolution
// table: an unrecognized provider name refuses with config.invalid under every
// policy, never resolves no material, noop under always refuses, a set
// PODIUM_SIGNATURE_VERIFY_KEY is authoritative over the key file, the key file
// answers only when the variable is unset, and sigstore-keyless needs a
// readable trust root.
func TestLoadConfig_VerifierResolution(t *testing.T) {
	type outcome struct {
		code string        // refusal code, or "" when loadConfig returns
		id   string        // resolved verifier ID, or "" for a nil verifier
		pub  func() []byte // expected registry-managed public key, when set
		msg  []string      // strings the refusal names
	}
	var keyA ed25519.PublicKey
	keyB := testVerifyKey(t)
	rootFile := filepath.Join(t.TempDir(), "root.pem")
	if err := os.WriteFile(rootFile, []byte("trust root"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		policy   string
		provider string
		env      map[string]string
		keyFile  bool
		want     outcome
	}{
		{name: "unknown name under never", policy: "never", provider: "bogus", want: outcome{code: "config.invalid:", msg: []string{"bogus"}}},
		{name: "unknown name under always", policy: "always", provider: "bogus", keyFile: true, want: outcome{code: "config.invalid:", msg: []string{"bogus"}}},
		{name: "never resolves nothing", policy: "never", provider: "registry-managed", want: outcome{}},
		{name: "never with noop", policy: "never", provider: "noop", want: outcome{}},
		{name: "noop under always", policy: "always", provider: "noop", keyFile: true, want: outcome{code: "config.signature_provider_unavailable", msg: []string{"noop"}}},
		{
			name: "verify key set and decodes", policy: "always", provider: "registry-managed", keyFile: true,
			env:  map[string]string{"PODIUM_SIGNATURE_VERIFY_KEY": keyB},
			want: outcome{id: "registry-managed", pub: func() []byte { b, _ := base64.StdEncoding.DecodeString(keyB); return b }},
		},
		{
			name: "verify key set and malformed", policy: "always", provider: "registry-managed", keyFile: true,
			env:  map[string]string{"PODIUM_SIGNATURE_VERIFY_KEY": "!!!not base64"},
			want: outcome{code: "config.signature_provider_unavailable", msg: []string{"PODIUM_SIGNATURE_VERIFY_KEY"}},
		},
		{
			name: "key file resolves", policy: "always", provider: "registry-managed", keyFile: true,
			want: outcome{id: "registry-managed", pub: func() []byte { return keyA }},
		},
		{
			name: "nothing resolves", policy: "always", provider: "registry-managed",
			want: outcome{code: "config.signature_provider_unavailable", msg: []string{"PODIUM_SIGNATURE_VERIFY_KEY", "PODIUM_SIGN_KEY_PATH", "registry-signing.key"}},
		},
		{
			name: "sigstore trust root readable", policy: "always", provider: "sigstore-keyless",
			env:  map[string]string{"PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE": rootFile},
			want: outcome{id: "sigstore-keyless"},
		},
		{
			name: "sigstore trust root unset", policy: "always", provider: "sigstore-keyless",
			want: outcome{code: "config.signature_provider_unavailable", msg: []string{"PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE"}},
		},
		{
			name: "sigstore trust root unreadable", policy: "always", provider: "sigstore-keyless",
			env:  map[string]string{"PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE": filepath.Join(t.TempDir(), "absent.pem")},
			want: outcome{code: "config.signature_provider_unavailable", msg: []string{"PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hermetic(t)
			t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:8080")
			t.Setenv("PODIUM_VERIFY_SIGNATURES", c.policy)
			t.Setenv("PODIUM_SIGNATURE_PROVIDER", c.provider)
			for k, v := range c.env {
				t.Setenv(k, v)
			}
			if c.keyFile {
				keyA = writeHomeKeyFile(t)
			}
			cfg, err := loadConfig()
			if c.want.code != "" {
				if err == nil || !strings.HasPrefix(err.Error(), c.want.code) {
					t.Fatalf("loadConfig = %v, want a refusal with %s", err, c.want.code)
				}
				for _, m := range c.want.msg {
					if !strings.Contains(err.Error(), m) {
						t.Errorf("refusal %q does not name %q", err, m)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("loadConfig: %v", err)
			}
			if c.want.id == "" {
				if cfg.verifier != nil {
					t.Fatalf("verifier = %v, want nil", cfg.verifier)
				}
				return
			}
			if cfg.verifier == nil || cfg.verifier.ID() != c.want.id {
				t.Fatalf("verifier = %v, want %s", cfg.verifier, c.want.id)
			}
			if c.want.pub != nil {
				got := cfg.verifier.(sign.RegistryManagedKey).PublicKey
				if !got.Equal(ed25519.PublicKey(c.want.pub())) {
					t.Errorf("verifier public key = %x, want %x", got, c.want.pub())
				}
			}
		})
	}
	// An empty name reaches resolveVerifier only from a flag or config value
	// that set it explicitly; it names no provider under any policy.
	for _, p := range []sign.VerificationPolicy{sign.PolicyNever, sign.PolicyAlways} {
		if _, err := resolveVerifier(p, ""); err == nil || !strings.HasPrefix(err.Error(), "config.invalid:") {
			t.Errorf("resolveVerifier(%s, \"\") = %v, want config.invalid", p, err)
		}
	}
}

// spec: §4.7.9, §6.9 — a bridge under the always default with no verification
// material refuses to start with config.signature_provider_unavailable rather
// than starting and refusing every load.
// Matrix: §6.10 (config.signature_provider_unavailable)
func TestLoadConfig_RefusesWithoutVerificationMaterial(t *testing.T) {
	hermetic(t)
	t.Setenv("PODIUM_REGISTRY", "http://127.0.0.1:8080")
	t.Setenv("PODIUM_VERIFY_SIGNATURES", "")
	_, err := loadConfig()
	if err == nil || !strings.HasPrefix(err.Error(), "config.signature_provider_unavailable:") {
		t.Fatalf("loadConfig = %v, want config.signature_provider_unavailable", err)
	}
}
