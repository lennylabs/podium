package integration

import (
	"bytes"
	"encoding/json"
	"os/exec"
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

// Spec: §12 (F-12.0.3) — "Fresh load_domain / search_domains / search_artifacts
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
