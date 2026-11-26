package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end coverage of the §5 rule that the MCP bridge keeps mcp-server
// artifacts out of its results. A host that consumes Podium through podium-mcp
// fixes its MCP server list at startup, so the bridge neither lists nor loads
// an mcp-server artifact; podium sync and the SDK still serve one.

// hiddenRegistry holds a rule and three mcp-server artifacts under acme/tools.
// The servers acme/tools/a/db and acme/tools/b/db are both named db, so on
// codex each would write a [mcp_servers.db] table into .codex/config.toml. The
// rule and acme/tools/cache sit directly in acme/tools, which lists them as
// notable entries.
func hiddenRegistry(t *testing.T) string {
	t.Helper()
	server := func(name, desc string) string {
		return "---\ntype: mcp-server\nname: " + name + "\nversion: 1.0.0\ndescription: " + desc + "\nserver_identifier: npx:@acme/" + name + "-mcp\n---\n\nbody\n"
	}
	return writeRegistry(t, map[string]string{
		"acme/tools/style/ARTIFACT.md": "---\ntype: rule\nversion: 1.0.0\nrule_mode: always\ndescription: Acme database style rule.\n---\n\nUse the acme database conventions.\n",
		"acme/tools/a/db/ARTIFACT.md":  server("db", "Acme database server A."),
		"acme/tools/b/db/ARTIFACT.md":  server("db", "Acme database server B."),
		"acme/tools/cache/ARTIFACT.md": server("cache", "Acme cache server."),
	})
}

// resultIDs returns the id of each descriptor in the list under key.
func resultIDs(result map[string]any, key string) map[string]bool {
	ids := map[string]bool{}
	list, _ := result[key].([]any)
	for _, el := range list {
		if d, ok := el.(map[string]any); ok {
			id, _ := d["id"].(string)
			ids[id] = true
		}
	}
	return ids
}

// assertNotFound fails unless the tools/call response with the given id is
// the §6.10 registry.not_found envelope.
func assertNotFound(t *testing.T, stdout string, id int, what string) {
	t.Helper()
	result := rpcResult(t, stdout, id)
	if code, _ := result["code"].(string); code != "registry.not_found" {
		t.Errorf("%s: code = %q, want registry.not_found; result=%v", what, code, result)
	}
}

// Spec: §5 — search_artifacts and load_domain list no mcp-server artifact,
// and load_artifact refuses one with registry.not_found and writes nothing.
// Loading two same-named servers on codex therefore cannot leave two
// [mcp_servers.db] tables in .codex/config.toml.
func TestMCPBridge_HidesMCPServerArtifacts(t *testing.T) {
	t.Parallel()
	reg := hiddenRegistry(t)
	mat := t.TempDir()
	env := append(chMCPEnv(t, reg, "PODIUM_HARNESS=codex"), "PODIUM_MATERIALIZE_ROOT="+mat)
	res := mcpExec(t, env,
		toolCall(1, "search_artifacts", map[string]any{"query": "acme database"}),
		toolCall(2, "search_artifacts", map[string]any{"type": "mcp-server"}),
		toolCall(3, "load_domain", map[string]any{"path": "acme/tools"}),
		toolCall(4, "load_artifact", map[string]any{"id": "acme/tools/a/db"}),
		toolCall(5, "load_artifact", map[string]any{"id": "acme/tools/b/db"}),
		toolCall(6, "load_artifact", map[string]any{"id": "acme/tools/style"}),
	)

	search := rpcResult(t, res.Stdout, 1)
	ids := resultIDs(search, "results")
	if ids["acme/tools/a/db"] || ids["acme/tools/b/db"] || !ids["acme/tools/style"] {
		t.Errorf("search_artifacts ids = %v, want the rule and no mcp-server", ids)
	}
	if total, _ := search["total_matched"].(float64); int(total) != len(ids) {
		t.Errorf("search_artifacts total_matched = %v, want %d", search["total_matched"], len(ids))
	}
	byType := rpcResult(t, res.Stdout, 2)
	if ids := resultIDs(byType, "results"); len(ids) != 0 || byType["total_matched"] != float64(0) {
		t.Errorf("search_artifacts(type=mcp-server) = %v, want no results and total_matched 0", byType)
	}
	domain := rpcResult(t, res.Stdout, 3)
	if notable := resultIDs(domain, "notable"); notable["acme/tools/cache"] || !notable["acme/tools/style"] {
		t.Errorf("load_domain notable = %v, want the rule and no mcp-server", notable)
	}

	assertNotFound(t, res.Stdout, 4, "load_artifact(acme/tools/a/db)")
	assertNotFound(t, res.Stdout, 5, "load_artifact(acme/tools/b/db)")
	if _, err := os.Stat(filepath.Join(mat, ".codex", "config.toml")); !os.IsNotExist(err) {
		t.Errorf(".codex/config.toml exists after refused mcp-server loads; stat err=%v", err)
	}
	if rule := rpcResult(t, res.Stdout, 6); rule["error"] != nil {
		t.Errorf("load_artifact(acme/tools/style) = %v, want a load", rule)
	}
	mustExist(t, filepath.Join(mat, "AGENTS.md"))
}

// Spec: §5 — a workspace-overlay mcp-server artifact is hidden from
// search_artifacts and load_domain and refused by load_artifact, and the §5.0
// resources mirror lists none and reads none.
func TestMCPBridge_HidesOverlayAndResourceMCPServers(t *testing.T) {
	t.Parallel()
	reg := hiddenRegistry(t)
	overlay := writeRegistry(t, map[string]string{
		"acme/local/cache/ARTIFACT.md": "---\ntype: mcp-server\nname: cache\nversion: 1.0.0\ndescription: Acme local cache server.\nserver_identifier: npx:@acme/cache-mcp\n---\n\nbody\n",
	})
	mat := t.TempDir()
	env := append(chMCPEnv(t, reg, "PODIUM_HARNESS=codex", "PODIUM_OVERLAY_PATH="+overlay), "PODIUM_MATERIALIZE_ROOT="+mat)
	res := mcpExec(t, env,
		toolCall(1, "search_artifacts", map[string]any{"query": "acme cache server"}),
		toolCall(2, "load_artifact", map[string]any{"id": "acme/local/cache"}),
		rpcReq{ID: 3, Method: "resources/list"},
		rpcReq{ID: 4, Method: "resources/read", Params: map[string]any{"uri": "podium://artifact/acme/tools/a/db"}},
		toolCall(5, "load_domain", map[string]any{"path": "acme/local"}),
	)
	if ids := resultIDs(rpcResult(t, res.Stdout, 1), "results"); ids["acme/local/cache"] || ids["acme/tools/a/db"] {
		t.Errorf("search_artifacts with an overlay ids = %v, want no mcp-server", ids)
	}
	assertNotFound(t, res.Stdout, 2, "load_artifact(acme/local/cache)")
	if _, err := os.Stat(filepath.Join(mat, ".codex", "config.toml")); !os.IsNotExist(err) {
		t.Errorf(".codex/config.toml exists after a refused overlay load; stat err=%v", err)
	}
	list := rpcResult(t, res.Stdout, 3)
	resources, _ := list["resources"].([]any)
	for _, r := range resources {
		if m, _ := r.(map[string]any); m["name"] == "acme/tools/a/db" || m["name"] == "acme/tools/b/db" {
			t.Errorf("resources/list names an mcp-server: %v", m)
		}
	}
	if read := rpcResult(t, res.Stdout, 4); read["code"] != "resources.not_found" {
		t.Errorf("resources/read of an mcp-server = %v, want resources.not_found", read)
	}
	if notable := resultIDs(rpcResult(t, res.Stdout, 5), "notable"); notable["acme/local/cache"] {
		t.Errorf("load_domain(acme/local) notable = %v, want no overlay mcp-server", notable)
	}
}

// Spec: §5 — dropping the mcp-server results does not empty or shorten a
// search page while visible matches exist, because the bridge asks the
// registry for its largest page and trims after the drop. A search for the
// mcp-server type alone matches nothing.
func TestMCPBridge_HiddenResultsLeavePageFilled(t *testing.T) {
	t.Parallel()
	reg := hiddenRegistry(t)
	env := chMCPEnv(t, reg, "PODIUM_HARNESS=codex")
	res := mcpExec(t, env,
		toolCall(1, "search_artifacts", map[string]any{"query": "acme", "top_k": 1}),
		toolCall(2, "search_artifacts", map[string]any{"type": "mcp-server", "top_k": 1}),
		toolCall(3, "search_artifacts", map[string]any{"scope": "acme/tools", "top_k": 2}),
	)
	one := rpcResult(t, res.Stdout, 1)
	if ids := resultIDs(one, "results"); len(ids) != 1 || !ids["acme/tools/style"] {
		t.Errorf("search_artifacts(acme, top_k 1) = %v, want the rule", one)
	}
	if total, _ := one["total_matched"].(float64); total != 1 {
		t.Errorf("search_artifacts(acme, top_k 1) total_matched = %v, want 1", one["total_matched"])
	}
	byType := rpcResult(t, res.Stdout, 2)
	if ids := resultIDs(byType, "results"); len(ids) != 0 || byType["total_matched"] != float64(0) {
		t.Errorf("search_artifacts(type=mcp-server, top_k 1) = %v, want no results and total_matched 0", byType)
	}
	if ids := resultIDs(rpcResult(t, res.Stdout, 3), "results"); len(ids) != 1 || !ids["acme/tools/style"] {
		t.Errorf("search_artifacts(scope acme/tools, top_k 2) ids = %v, want the rule", ids)
	}
}

// Spec: §5, §6.10 — a refused mcp-server load reads exactly as a load of an
// ID that is not in the caller's view, apart from the ID itself.
func TestMCPBridge_HiddenLoadMatchesMissingID(t *testing.T) {
	t.Parallel()
	reg := hiddenRegistry(t)
	env := chMCPEnv(t, reg, "PODIUM_HARNESS=codex")
	res := mcpExec(t, env,
		toolCall(1, "load_artifact", map[string]any{"id": "acme/tools/a/db"}),
		toolCall(2, "load_artifact", map[string]any{"id": "acme/tools/nope"}),
	)
	norm := func(id int, artifact string) string {
		b, err := json.Marshal(rpcEnvelope(t, res.Stdout, id)["result"])
		if err != nil {
			t.Fatal(err)
		}
		return strings.ReplaceAll(string(b), artifact, "<id>")
	}
	hidden, missing := norm(1, "acme/tools/a/db"), norm(2, "acme/tools/nope")
	if hidden != missing {
		t.Errorf("refused load and missing ID differ:\nhidden:  %s\nmissing: %s", hidden, missing)
	}
	if !strings.Contains(missing, "registry.not_found") {
		t.Errorf("missing ID result carries no registry.not_found: %s", missing)
	}
}
