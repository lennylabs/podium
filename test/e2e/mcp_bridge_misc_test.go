package e2e

import (
	"strings"
	"testing"
)

// unreachableServerSource is a syntactically valid HTTP server-source registry
// whose port has no listener, so the bridge starts but every registry call is a
// transport failure (the §7.4 degraded-network path).
const unreachableServerSource = "http://127.0.0.1:1"

// spec: §7.4 (F-7.4.4) — offline-first "serve cached results silently": the
// discovery meta-tools must not emit an explicit "offline" status field in
// offline-first mode. Driven through the real podium-mcp binary.
func TestMCP_OfflineFirstServesSilently(t *testing.T) {
	t.Parallel()
	env := []string{
		"PODIUM_REGISTRY=" + unreachableServerSource,
		"PODIUM_CACHE_MODE=offline-first",
		"PODIUM_CACHE_DIR=" + t.TempDir(),
	}
	res := mcpExec(t, env, toolCall(1, "search_domains", map[string]any{"query": "finance"}))
	cliWantExit(t, res, 0, "offline-first search_domains")
	result := rpcResult(t, res.Stdout, 1)
	if _, has := result["status"]; has {
		t.Errorf("offline-first must serve silently (no status field): %v", result)
	}
}

// spec: §7.4 (F-7.4.4) — always-revalidate (the default) keeps the explicit
// "offline" status the offline-first mode drops, confirming the per-mode
// distinction end-to-end.
func TestMCP_AlwaysRevalidateKeepsOfflineStatus(t *testing.T) {
	t.Parallel()
	env := []string{
		"PODIUM_REGISTRY=" + unreachableServerSource,
		"PODIUM_CACHE_MODE=always-revalidate",
		"PODIUM_CACHE_DIR=" + t.TempDir(),
	}
	res := mcpExec(t, env, toolCall(1, "search_domains", map[string]any{"query": "finance"}))
	cliWantExit(t, res, 0, "always-revalidate search_domains")
	result := rpcResult(t, res.Stdout, 1)
	if result["status"] != "offline" {
		t.Errorf("always-revalidate must surface offline status: %v", result)
	}
}

// spec: §6.9 (F-6.9.1) — "Binary version mismatch with host caller: refuse to
// start." With a configured host-version floor, an initialize from a host below
// the floor is refused with mcp.client_too_old so the host can prompt an update.
func TestMCP_RefusesOldHostCaller(t *testing.T) {
	t.Parallel()
	env := []string{
		"PODIUM_REGISTRY=" + unreachableServerSource,
		"PODIUM_MIN_CLIENT_VERSION=2.0.0",
		"PODIUM_CACHE_DIR=" + t.TempDir(),
	}
	res := mcpExec(t, env, rpcReq{ID: 1, Method: "initialize", Params: map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "old-host", "version": "1.0.0"},
	}})
	env2 := rpcEnvelope(t, res.Stdout, 1)
	rpcErr, ok := env2["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected an initialize error for an old host caller, got: %v", env2)
	}
	if msg, _ := rpcErr["message"].(string); !strings.Contains(msg, "mcp.client_too_old") {
		t.Errorf("error message = %q, want mcp.client_too_old", rpcErr["message"])
	}
}

// spec: §6.9 (F-6.9.1) — a host caller at or above the floor initializes
// normally, and with no floor configured (the default) the check is inert.
func TestMCP_AcceptsHostCallerAtFloorAndWhenUnset(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		floor string
		ver   string
	}{
		{"at floor", "2.0.0", "2.0.0"},
		{"above floor", "2.0.0", "2.5.1"},
		{"no floor", "", "0.0.1"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := []string{
				"PODIUM_REGISTRY=" + unreachableServerSource,
				"PODIUM_MIN_CLIENT_VERSION=" + tc.floor,
				"PODIUM_CACHE_DIR=" + t.TempDir(),
			}
			res := mcpExec(t, env, rpcReq{ID: 1, Method: "initialize", Params: map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{},
				"clientInfo":      map[string]any{"name": "host", "version": tc.ver},
			}})
			result := rpcResult(t, res.Stdout, 1) // fails the test if an error was returned
			if result["serverInfo"] == nil {
				t.Errorf("initialize did not complete: %v", result)
			}
		})
	}
}
