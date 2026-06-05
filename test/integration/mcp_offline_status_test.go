package integration

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// callToolOver runs one meta-tool call through a freshly spawned MCP bridge
// process against the given registry URL, returning the decoded result map.
func callToolOver(t *testing.T, registry, cacheDir, tool string, args map[string]any, extraEnv ...string) map[string]any {
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
			"name":      tool,
			"arguments": args,
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

// Spec: §12 — "Fresh load_domain / search_domains / search_artifacts
// returns an explicit 'offline' status that hosts can surface." With the
// registry unreachable, each discovery/search meta-tool returns status
// "offline" rather than an error envelope so the host can tell a transient
// outage from a request rejection.
func TestPodiumMCP_DiscoveryToolsReportOfflineWhenRegistryDown(t *testing.T) {
	t.Parallel()
	const down = "http://127.0.0.1:1" // unbound port → connect refused

	cases := []struct {
		tool string
		args map[string]any
	}{
		{"load_domain", map[string]any{"path": "finance"}},
		{"search_domains", map[string]any{"query": "variance"}},
		{"search_artifacts", map[string]any{"query": "variance"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.tool, func(t *testing.T) {
			t.Parallel()
			res := callToolOver(t, down, t.TempDir(), tc.tool, tc.args)
			if res["status"] != "offline" {
				t.Errorf("%s status = %v, want offline (result=%v)", tc.tool, res["status"], res)
			}
			if _, hasErr := res["error"]; hasErr {
				t.Errorf("%s offline result must not carry an error key: %v", tc.tool, res)
			}
		})
	}
}

// Spec: §7.4 — offline-only "never contact the registry; structured
// error if cache miss." Through the real bridge, an offline-only discovery /
// search call against a down registry returns the structured
// network.offline_cache_miss error rather than the offline status the
// transient-outage modes produce. The discovery tools keep no content cache,
// so every offline-only call is a miss.
func TestPodiumMCP_OfflineOnlyDiscoveryReportsMiss(t *testing.T) {
	t.Parallel()
	const down = "http://127.0.0.1:1" // unbound port → connect refused if dialed

	cases := []struct {
		tool string
		args map[string]any
	}{
		{"load_domain", map[string]any{"path": "finance"}},
		{"search_domains", map[string]any{"query": "variance"}},
		{"search_artifacts", map[string]any{"query": "variance"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.tool, func(t *testing.T) {
			t.Parallel()
			res := callToolOver(t, down, t.TempDir(), tc.tool, tc.args, "PODIUM_CACHE_MODE=offline-only")
			errText, _ := res["error"].(string)
			if errText == "" || !strings.Contains(errText, "network.offline_cache_miss") {
				t.Errorf("%s error = %v, want network.offline_cache_miss (result=%v)", tc.tool, res["error"], res)
			}
			if res["status"] == "offline" {
				t.Errorf("%s offline-only miss must be a structured error, not an offline status: %v", tc.tool, res)
			}
		})
	}
}
