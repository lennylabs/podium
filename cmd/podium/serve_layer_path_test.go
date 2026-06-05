package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/lennylabs/podium/internal/serverboot"
	"github.com/lennylabs/podium/internal/testharness"
)

// Spec: §13.10 — `podium serve --layer-path <path>` is the CLI
// flag the spec prescribes. The flag is implemented by setting
// PODIUM_LAYER_PATH, which serverboot.Run() reads.
//
// Run() is forced to fail fast by pointing it at the postgres
// store without a DSN; validate() returns an error before any
// listener binds. The flag-to-env mapping happens before the
// failure, which is the contract this test pins.
func TestServeCmd_LayerPathFlagSetsEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PODIUM_LAYER_PATH", "")
	t.Setenv("PODIUM_REGISTRY_STORE", "postgres")
	t.Setenv("PODIUM_POSTGRES_DSN", "")
	t.Setenv("PODIUM_CONFIG_FILE", emptyServerConfig(t))

	code := serveCmd([]string{"--layer-path", dir})
	if code != 1 {
		t.Fatalf("serveCmd exit = %d, want 1 (validate fails on missing PODIUM_POSTGRES_DSN)", code)
	}
	if got := os.Getenv("PODIUM_LAYER_PATH"); got != dir {
		t.Errorf("PODIUM_LAYER_PATH = %q, want %q (flag side effect)", got, dir)
	}
}

// Spec: §13.10 — when the --layer-path flag is omitted, the
// previous PODIUM_LAYER_PATH value is preserved (the empty flag
// branch does not unset the env var).
func TestServeCmd_LayerPathFlagOmittedPreservesEnv(t *testing.T) {
	t.Setenv("PODIUM_LAYER_PATH", "/preexisting")
	t.Setenv("PODIUM_REGISTRY_STORE", "postgres")
	t.Setenv("PODIUM_POSTGRES_DSN", "")
	t.Setenv("PODIUM_CONFIG_FILE", emptyServerConfig(t))

	code := serveCmd([]string{})
	if code != 1 {
		t.Fatalf("serveCmd exit = %d, want 1", code)
	}
	if got := os.Getenv("PODIUM_LAYER_PATH"); got != "/preexisting" {
		t.Errorf("PODIUM_LAYER_PATH = %q, want /preexisting (env preserved when flag omitted)", got)
	}
}

// Spec: §13.10 / §7.3.1 — when PODIUM_LAYER_PATH is set, the
// standalone server ingests the filesystem registry at boot,
// persists a LayerConfig per layer, and serves the ingested
// content via the meta-tool endpoints. End-to-end check that all
// three contracts hold against a real HTTP listener.
func TestServe_LayerPathBootstrap_E2E(t *testing.T) {
	port := freePort(t)
	tmp := t.TempDir()
	layerDir := t.TempDir()

	testharness.WriteTree(t, layerDir,
		testharness.WriteTreeOption{
			Path:    ".registry-config",
			Content: "multi_layer: true\n",
		},
		testharness.WriteTreeOption{
			Path:    "alice-personal/notes/welcome/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 1.0.0\ndescription: hello-from-alice\nsensitivity: low\n---\n\nbody\n",
		},
	)

	t.Setenv("PODIUM_BIND", fmt.Sprintf("127.0.0.1:%d", port))
	t.Setenv("PODIUM_REGISTRY_STORE", "memory")
	t.Setenv("PODIUM_OBJECT_STORE", "none")
	t.Setenv("PODIUM_CONFIG_FILE", emptyServerConfig(t))
	t.Setenv("PODIUM_FILESYSTEM_ROOT", tmp)
	t.Setenv("PODIUM_VECTOR_BACKEND", "")
	t.Setenv("PODIUM_LAYER_PATH", layerDir)

	go func() { _ = serverboot.Run() }()
	waitForServer(t, port)

	// /v1/layers must surface the bootstrap layer so /v1/layers/reingest
	// and the admin UI can address it. store.LayerConfig has no JSON
	// tags so the marshaller emits the Go field names verbatim.
	layers := getJSON(t, port, "/v1/layers")
	list, _ := layers["layers"].([]any)
	if len(list) != 1 {
		t.Fatalf("/v1/layers returned %d layers, want 1: %v", len(list), layers)
	}
	first, _ := list[0].(map[string]any)
	if first["ID"] != "alice-personal" {
		t.Errorf("layer ID = %v, want alice-personal (full: %v)", first["ID"], first)
	}
	if first["SourceType"] != "local" {
		t.Errorf("SourceType = %v, want local", first["SourceType"])
	}
	if first["LocalPath"] != layerDir+"/alice-personal" {
		t.Errorf("LocalPath = %v, want %s/alice-personal", first["LocalPath"], layerDir)
	}

	// /v1/search_artifacts must return the ingested manifest so the
	// reason §13.10 standalone exists (content-serving) holds.
	search := getJSON(t, port, "/v1/search_artifacts?query=hello-from-alice")
	results, _ := search["results"].([]any)
	if len(results) == 0 {
		t.Fatalf("search returned no results: %v", search)
	}
}

// Spec: §6.2 — the presigned-URL TTL accepts a `--flag` form, not
// just the env var. `podium serve --presign-ttl-seconds <n>` sets
// PODIUM_PRESIGN_TTL_SECONDS, the value serverboot reads. Run() is forced to
// fail fast (postgres store, no DSN) so the flag-to-env mapping is the
// contract under test without binding a listener.
func TestServeCmd_PresignTTLFlagSetsEnv(t *testing.T) {
	t.Setenv("PODIUM_PRESIGN_TTL_SECONDS", "")
	t.Setenv("PODIUM_REGISTRY_STORE", "postgres")
	t.Setenv("PODIUM_POSTGRES_DSN", "")
	t.Setenv("PODIUM_CONFIG_FILE", emptyServerConfig(t))

	code := serveCmd([]string{"--presign-ttl-seconds", "7200"})
	if code != 1 {
		t.Fatalf("serveCmd exit = %d, want 1 (validate fails on missing PODIUM_POSTGRES_DSN)", code)
	}
	if got := os.Getenv("PODIUM_PRESIGN_TTL_SECONDS"); got != "7200" {
		t.Errorf("PODIUM_PRESIGN_TTL_SECONDS = %q, want 7200 (flag side effect)", got)
	}
}

// Spec: §6.2 — when the flag is omitted, an existing
// PODIUM_PRESIGN_TTL_SECONDS is preserved (the empty-flag branch does not
// clear it).
func TestServeCmd_PresignTTLFlagOmittedPreservesEnv(t *testing.T) {
	t.Setenv("PODIUM_PRESIGN_TTL_SECONDS", "1800")
	t.Setenv("PODIUM_REGISTRY_STORE", "postgres")
	t.Setenv("PODIUM_POSTGRES_DSN", "")
	t.Setenv("PODIUM_CONFIG_FILE", emptyServerConfig(t))

	code := serveCmd([]string{})
	if code != 1 {
		t.Fatalf("serveCmd exit = %d, want 1", code)
	}
	if got := os.Getenv("PODIUM_PRESIGN_TTL_SECONDS"); got != "1800" {
		t.Errorf("PODIUM_PRESIGN_TTL_SECONDS = %q, want 1800 (env preserved when flag omitted)", got)
	}
}

func waitForServer(t *testing.T, port int) {
	t.Helper()
	url := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := http.DefaultClient.Do(req)
		cancel()
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("server never came up on port %d", port)
}

func getJSON(t *testing.T, port int, path string) map[string]any {
	t.Helper()
	url := fmt.Sprintf("http://127.0.0.1:%d%s", port, path)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s status = %d: %s", url, resp.StatusCode, body)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
	return out
}
