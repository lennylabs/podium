package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end coverage of how podium-mcp's load_artifact reconciles the shared
// files several artifacts write into. On the gemini harness a rule injects a
// block into GEMINI.md, and a hook config-merges an entry into
// .gemini/settings.json. A load carries one artifact, so it reconciles only
// that artifact's block and entry: loads accumulate, and a re-load replaces the
// loaded artifact's own block and entry without touching the others or the
// operator's content. §5 keeps mcp-server artifacts out of the bridge, so the
// keyed mcpServers entries are covered by the writer's tests in
// pkg/materialize.

// accRule and accHook build the manifests of the shared-file artifacts the
// case below loads.
func accRule(ver, body string) string {
	return "---\ntype: rule\nversion: " + ver + "\nrule_mode: always\n---\n\n" + body + "\n"
}

func accHook(ver, action string) string {
	return "---\ntype: hook\nversion: " + ver + "\ndescription: Log session starts.\nhook_event: session_start\nhook_action: |\n  " + action + "\n---\n\nbody\n"
}

// accLoadAll calls load_artifact once per id in one podium-mcp session and
// fails on any error result.
func accLoadAll(t *testing.T, env []string, ids ...string) {
	t.Helper()
	reqs := make([]rpcReq, len(ids))
	for i, id := range ids {
		reqs[i] = toolCall(i+1, "load_artifact", map[string]any{"id": id})
	}
	res := mcpExec(t, env, reqs...)
	for i, id := range ids {
		result := rpcResult(t, res.Stdout, i+1)
		if result["error"] != nil {
			t.Fatalf("load_artifact(%s) returned an error result: %v (stderr=%s)", id, result, res.Stderr)
		}
		if mat, _ := result["materialized_at"].([]any); len(mat) == 0 {
			t.Fatalf("load_artifact(%s) materialized nothing: %v (stderr=%s)", id, result, res.Stderr)
		}
	}
}

// accSettings reads .gemini/settings.json under root and returns the command
// of each SessionStart hook entry keyed by its owning artifact ID, and the
// whole document.
func accSettings(t *testing.T, root string) (hooks map[string][]string, doc map[string]any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, ".gemini", "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("settings.json is not JSON: %v\n%s", err, raw)
	}
	hooks = map[string][]string{}
	hooksObj, _ := doc["hooks"].(map[string]any)
	entries, _ := hooksObj["SessionStart"].([]any)
	for _, e := range entries {
		m, _ := e.(map[string]any)
		owner, _ := m["x-podium-id"].(string)
		handlers, _ := m["hooks"].([]any)
		for _, h := range handlers {
			hm, _ := h.(map[string]any)
			cmd, _ := hm["command"].(string)
			hooks[owner] = append(hooks[owner], strings.TrimSpace(cmd))
		}
	}
	return hooks, doc
}

// accAssertOperatorSettings fails unless the operator's own settings.json
// content, a theme key and an untagged mcpServers entry, is intact.
func accAssertOperatorSettings(t *testing.T, doc map[string]any) {
	t.Helper()
	if doc["theme"] != "acme-dark" {
		t.Errorf("operator key theme = %v, want acme-dark", doc["theme"])
	}
	servers, _ := doc["mcpServers"].(map[string]any)
	entry, _ := servers["alice-local"].(map[string]any)
	if entry["command"] != "alice-server" {
		t.Errorf("operator entry mcpServers.alice-local = %v, want command alice-server", servers["alice-local"])
	}
}

// Spec: §6.6 step 5, §6.7 — a load_artifact reconciles only the loaded
// artifact's block and entries in a shared inject or config-merge file. Loading
// a second artifact leaves the first one's GEMINI.md block and settings.json
// hook entry in place, and re-loading an artifact at a new version replaces
// only its own block and entry. Both load paths are driven:
// the registry-served path in the first session and the §6.4 workspace overlay
// path in the second.
func TestMCPLoad_AccumulatesSharedFileContributions(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"acme/alpha-rule/ARTIFACT.md": accRule("1.0.0", "ALPHA RULE V1."),
		"acme/beta-rule/ARTIFACT.md":  accRule("1.0.0", "BETA RULE V1."),
		"acme/alpha-hook/ARTIFACT.md": accHook("1.0.0", "echo alpha-v1"),
		"acme/beta-hook/ARTIFACT.md":  accHook("1.0.0", "echo beta-v1"),
	})
	mat := t.TempDir()
	// The operator's own content in both shared files must survive every load.
	operatorMD := "# Notes from alice\n\nKeep this line.\n"
	if err := os.WriteFile(filepath.Join(mat, "GEMINI.md"), []byte(operatorMD), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(mat, ".gemini"), 0o755); err != nil {
		t.Fatal(err)
	}
	operatorJSON := `{"theme": "acme-dark", "mcpServers": {"alice-local": {"command": "alice-server"}}}`
	if err := os.WriteFile(filepath.Join(mat, ".gemini", "settings.json"), []byte(operatorJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	// Session 1: load the alpha artifacts, then the beta artifacts, from the
	// registry. Each beta load must keep the alpha contribution in place.
	env := append(chMCPEnv(t, reg, "PODIUM_HARNESS=gemini"), "PODIUM_MATERIALIZE_ROOT="+mat)
	accLoadAll(t, env,
		"acme/alpha-rule", "acme/alpha-hook", "acme/beta-rule", "acme/beta-hook")

	md := readFile(t, filepath.Join(mat, "GEMINI.md"))
	for _, want := range []string{"Keep this line.", "<!-- podium:begin:acme/alpha-rule -->", "ALPHA RULE V1.", "<!-- podium:begin:acme/beta-rule -->", "BETA RULE V1."} {
		if !strings.Contains(md, want) {
			t.Errorf("after loading both rules, GEMINI.md lacks %q:\n%s", want, md)
		}
	}
	hooks, doc := accSettings(t, mat)
	if got := hooks["acme/alpha-hook"]; len(got) != 1 || got[0] != "echo alpha-v1" {
		t.Errorf("alpha hook entries = %v, want [echo alpha-v1]; all=%v", got, hooks)
	}
	if got := hooks["acme/beta-hook"]; len(got) != 1 || got[0] != "echo beta-v1" {
		t.Errorf("beta hook entries = %v, want [echo beta-v1]; all=%v", got, hooks)
	}
	accAssertOperatorSettings(t, doc)

	// Session 2: re-load the alpha artifacts at a new version from the
	// workspace overlay. Only the alpha block and entry change.
	overlay := writeRegistry(t, map[string]string{
		"acme/alpha-rule/ARTIFACT.md": accRule("2.0.0", "ALPHA RULE V2."),
		"acme/alpha-hook/ARTIFACT.md": accHook("2.0.0", "echo alpha-v2"),
	})
	env2 := append(chMCPEnv(t, reg, "PODIUM_HARNESS=gemini", "PODIUM_OVERLAY_PATH="+overlay), "PODIUM_MATERIALIZE_ROOT="+mat)
	accLoadAll(t, env2, "acme/alpha-rule", "acme/alpha-hook")

	md = readFile(t, filepath.Join(mat, "GEMINI.md"))
	if n := strings.Count(md, "<!-- podium:begin:acme/alpha-rule -->"); n != 1 {
		t.Errorf("GEMINI.md holds %d alpha blocks after the re-load, want 1:\n%s", n, md)
	}
	if strings.Contains(md, "ALPHA RULE V1.") || !strings.Contains(md, "ALPHA RULE V2.") {
		t.Errorf("the re-load did not replace the alpha block with V2:\n%s", md)
	}
	if strings.Count(md, "<!-- podium:begin:acme/beta-rule -->") != 1 || !strings.Contains(md, "BETA RULE V1.") {
		t.Errorf("the alpha re-load changed the beta block:\n%s", md)
	}
	if !strings.HasPrefix(md, operatorMD) {
		t.Errorf("the operator's GEMINI.md content changed:\n%s", md)
	}
	hooks, doc = accSettings(t, mat)
	if got := hooks["acme/alpha-hook"]; len(got) != 1 || got[0] != "echo alpha-v2" {
		t.Errorf("alpha hook entries after the re-load = %v, want [echo alpha-v2]; all=%v", got, hooks)
	}
	if got := hooks["acme/beta-hook"]; len(got) != 1 || got[0] != "echo beta-v1" {
		t.Errorf("beta hook entries after the alpha re-load = %v, want [echo beta-v1]; all=%v", got, hooks)
	}
	accAssertOperatorSettings(t, doc)
}
