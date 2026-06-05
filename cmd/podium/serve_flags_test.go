package main

import (
	"os"
	"path/filepath"
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
	t.Setenv("PODIUM_SIGN", "")
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

// Spec: §13.10 — an unrecognized --sign value is named at startup
// rather than silently leaving signing disabled.
func TestServeCmd_SignRejectsUnknownValue(t *testing.T) {
	t.Setenv("PODIUM_SIGN", "")
	t.Setenv("PODIUM_CONFIG_FILE", filepath.Join(t.TempDir(), "missing.yaml"))
	t.Setenv("PODIUM_REGISTRY_STORE", "sqlite")
	t.Setenv("PODIUM_SQLITE_PATH", filepath.Join(t.TempDir(), "podium.db"))
	t.Setenv("PODIUM_OBJECT_STORE", "none")

	code := serveCmd([]string{"--sign", "sigstore"})
	if code != 1 {
		t.Fatalf("serveCmd exit = %d, want 1 (validate rejects unknown sign mode)", code)
	}
}
