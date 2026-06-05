package integration

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/internal/testharness/registryharness"
)

// loadArtifactover runs one load_artifact call through a freshly spawned MCP
// bridge process against the given registry URL and cache dir, returning the
// decoded result map.
func loadArtifactOver(t *testing.T, registry, cacheDir string, extraEnv ...string) map[string]any {
	t.Helper()
	bin := buildMCP(t)
	cmd := exec.Command(bin)
	cmd.Env = append(cmd.Env,
		"PODIUM_REGISTRY="+registry,
		"PODIUM_HARNESS=none",
		"PODIUM_CACHE_DIR="+cacheDir,
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	cmd.Stdin = bytes.NewReader(newlineDelimitedRequests([]rpcCall{
		{Method: "tools/call", ID: 1, Params: map[string]any{
			"name":      "load_artifact",
			"arguments": map[string]any{"id": "x"},
		}},
	}))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run mcp: %v\nstdout:\n%s", err, stdout.String())
	}
	var resp struct {
		Result map[string]any `json:"result"`
	}
	if err := json.NewDecoder(&stdout).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v\nstdout: %s", err, stdout.String())
	}
	return resp.Result
}

// Spec: §6.5 — a first load populates the content
// cache and the BoltDB resolution index, so a later offline-first load serves
// the artifact from the persisted index without contacting the registry. This
// exercises the (id, "latest") → semver → content_hash chain end to end.
func TestPodiumMCP_OfflineFirstServesFromPersistedIndex(t *testing.T) {
	t.Parallel()

	h := registryharness.New(t,
		testharness.WriteTreeOption{
			Path: "x/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 1.0.0\n" +
				"description: x\n---\n\nbody-text\n",
		},
	)
	cache := t.TempDir()

	// First load against the live registry populates the caches.
	first := loadArtifactOver(t, h.URL, cache)
	if first["id"] != "x" {
		t.Fatalf("first load id = %v, want x", first["id"])
	}

	// Take the registry down. A second load in offline-first mode must serve
	// from the persisted index + content cache. TTL is disabled so the test
	// never depends on wall-clock timing.
	h.Close()
	second := loadArtifactOver(t, "http://127.0.0.1:1", cache,
		"PODIUM_CACHE_MODE=offline-first",
		"PODIUM_CACHE_RESOLUTION_TTL_SECONDS=0",
	)
	if second["id"] != "x" {
		t.Fatalf("offline-first load id = %v (registry down), want x; result=%v", second["id"], second)
	}
	if second["manifest_body"] != "body-text\n" {
		t.Errorf("offline-first manifest_body = %v, want body-text", second["manifest_body"])
	}
}

// Spec: §6.5 — the default always-revalidate mode HEAD-revalidates a
// cached resolution against the live registry. A second load through a fresh
// bridge process reusing the same cache directory exercises the registry HEAD
// route and still returns the artifact.
func TestPodiumMCP_AlwaysRevalidateHeadRoundTrip(t *testing.T) {
	t.Parallel()

	h := registryharness.New(t,
		testharness.WriteTreeOption{
			Path: "x/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 1.0.0\n" +
				"description: x\n---\n\nbody-text\n",
		},
	)
	cache := t.TempDir()

	if first := loadArtifactOver(t, h.URL, cache); first["id"] != "x" {
		t.Fatalf("first load id = %v, want x", first["id"])
	}
	// Second load: registry up, cache warm → HEAD revalidation path.
	second := loadArtifactOver(t, h.URL, cache)
	if second["id"] != "x" {
		t.Fatalf("revalidated load id = %v, want x; result=%v", second["id"], second)
	}
	if second["manifest_body"] != "body-text\n" {
		t.Errorf("revalidated manifest_body = %v, want body-text", second["manifest_body"])
	}
}

// Spec: §6.5 / §7.4 — offline-only never contacts the registry and reports the
// structured network.offline_cache_miss error for content that was never
// cached (the code lives in the §6.10 network.* namespace; there is no cache.*
// namespace).
func TestPodiumMCP_OfflineOnlyMissReportsOfflineMiss(t *testing.T) {
	t.Parallel()

	res := loadArtifactOver(t, "http://127.0.0.1:1", t.TempDir(),
		"PODIUM_CACHE_MODE=offline-only",
	)
	errText, _ := res["error"].(string)
	if errText == "" || !bytes.Contains([]byte(errText), []byte("network.offline_cache_miss")) {
		t.Errorf("error = %v, want network.offline_cache_miss", res["error"])
	}
}
