package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Spec: §13.10 — the standalone serve
// flags map onto the PODIUM_* env vars serverboot.Run() reads. Run is forced
// to fail fast by selecting the postgres store with no DSN so validate()
// returns before any listener binds; the flag-to-env mapping happens first,
// which is the contract this test pins.
func TestServeCmd_StandaloneFlagsSetEnv(t *testing.T) {
	t.Setenv("PODIUM_WEB_UI", "")
	t.Setenv("PODIUM_WEB_UI_ALLOW_PUBLIC_BIND", "")
	t.Setenv("PODIUM_NO_EMBEDDINGS", "")
	// An unset PODIUM_SIGN already resolves to registry-key (§13.10), so the
	// fixture starts from none to make the flag's side effect observable.
	t.Setenv("PODIUM_SIGN", "none")
	t.Setenv("PODIUM_REGISTRY_STORE", "postgres")
	t.Setenv("PODIUM_POSTGRES_DSN", "")
	t.Setenv("PODIUM_CONFIG_FILE", filepath.Join(t.TempDir(), "missing.yaml"))

	code := serveCmd([]string{"--web-ui", "--web-ui-allow-public-bind", "--no-embeddings", "--sign", "registry-key"})
	if code != 1 {
		t.Fatalf("serveCmd exit = %d, want 1 (validate fails on missing PODIUM_POSTGRES_DSN)", code)
	}
	for _, c := range []struct{ env, want string }{
		{"PODIUM_WEB_UI", "true"},
		{"PODIUM_WEB_UI_ALLOW_PUBLIC_BIND", "true"},
		{"PODIUM_NO_EMBEDDINGS", "true"},
		{"PODIUM_SIGN", "registry-key"},
	} {
		if got := os.Getenv(c.env); got != c.want {
			t.Errorf("%s = %q, want %q (flag side effect)", c.env, got, c.want)
		}
	}
}

// Spec: §13.12 — `podium serve --config <path>` sets PODIUM_CONFIG_FILE
// for the process, replacing a value inherited from the environment. The
// inherited value names a missing file, so a run that kept it would print the
// "does not exist" refusal. Both runs exit 1 here (validate fails on the
// missing PODIUM_POSTGRES_DSN), so the test asserts the variable and the
// absence of the refusal rather than the exit status. captureStderr swaps
// the process-wide os.Stderr, so this test must not run in parallel.
func TestServeCmd_ConfigFlagOverridesConfigFileEnv(t *testing.T) {
	t.Setenv("PODIUM_CONFIG_FILE", filepath.Join(t.TempDir(), "missing.yaml"))
	t.Setenv("PODIUM_SIGN", "none")
	t.Setenv("PODIUM_REGISTRY_STORE", "postgres")
	t.Setenv("PODIUM_POSTGRES_DSN", "")
	named := filepath.Join(t.TempDir(), "registry.yaml")
	if err := os.WriteFile(named, []byte("registry:\n  layer_path: /from/flag\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var code int
	stderr := captureStderr(t, func() {
		code = serveCmd([]string{"--config", named})
	})
	if code != 1 {
		t.Fatalf("serveCmd exit = %d, want 1 (validate fails on missing PODIUM_POSTGRES_DSN); stderr:\n%s", code, stderr)
	}
	if got := os.Getenv("PODIUM_CONFIG_FILE"); got != named {
		t.Errorf("PODIUM_CONFIG_FILE = %q, want %q (--config override)", got, named)
	}
	if strings.Contains(stderr, "does not exist") {
		t.Errorf("stderr reports a missing config file, want the --config path read:\n%s", stderr)
	}
}

// Spec: §13.10 — an unrecognized --sign value is named at startup
// rather than silently read as a signing mode.
func TestServeCmd_SignRejectsUnknownValue(t *testing.T) {
	t.Setenv("PODIUM_SIGN", "none")
	t.Setenv("PODIUM_CONFIG_FILE", filepath.Join(t.TempDir(), "missing.yaml"))
	t.Setenv("PODIUM_REGISTRY_STORE", "sqlite")
	t.Setenv("PODIUM_SQLITE_PATH", filepath.Join(t.TempDir(), "podium.db"))
	t.Setenv("PODIUM_OBJECT_STORE", "none")

	code := serveCmd([]string{"--sign", "sigstore"})
	if code != 1 {
		t.Fatalf("serveCmd exit = %d, want 1 (validate rejects unknown sign mode)", code)
	}
}
