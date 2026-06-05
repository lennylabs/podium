package e2e

// End-to-end tests for the harness materialization matrix journeys.
// Each test
// drives the real `podium sync` CLI over a filesystem registry and asserts the
// on-disk per-type outputs, the §6.7 config-merge reconciliation (OpMergeJSON
// JSON files and OpInject markdown/TOML files), re-sync idempotency, the
// stale-output cleanup (standalone files, empty parent directories, and shared
// config files), and the §6.7.1 unsupported-type diagnostics.
//
// These journeys span every config-merge surface (.claude/settings.json,
// .mcp.json, .cursor/hooks.json, .cursor/mcp.json, .gemini/settings.json,
// opencode.json, .codex/config.toml, AGENTS.md, and the claude-cowork
// marketplace.json) and the harnesses that own them.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
)

// ---- shared helpers ---------------------------------------------------------

// mzReadJSON reads path and decodes it into a generic map, failing the test on
// a read or parse error.
func mzReadJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("parse %s as JSON: %v\n%s", path, err, b)
	}
	return m
}

// mzWriteJSON marshals m and writes it to path (used to seed an operator entry
// into a config-merge file between syncs).
func mzWriteJSON(t *testing.T, path string, m map[string]any) {
	t.Helper()
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", path, err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// mzCount returns the number of non-overlapping occurrences of sub in s.
func mzCount(s, sub string) int { return strings.Count(s, sub) }

// mzPollContains waits until the file at path exists and contains sub, returning
// whether it did within the deadline. Used by the watch-loop test.
func mzPollContains(path, sub string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && strings.Contains(string(b), sub) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// mzPollGone waits until the file at path no longer exists, returning whether it
// was removed within the deadline.
func mzPollGone(path string, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// ------------------------------------------------------------------------

// a single `podium sync --harness claude-code` over a registry
// holding one artifact of each first-class type writes every per-type output
// path together, including the .claude/settings.json hook config-merge and the
// .mcp.json server entry derived from server_identifier, each carrying its
// ownership marker. spec: §6.7 (type routing), §6.7.1 (config-merge ownership).
func TestMaterialize_AllFirstClassTypesClaudeCodeSinglePass(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		// skill (folder + SKILL.md), agent, context, command, rule (always),
		// hook (config-merge into settings.json), mcp-server (config-merge into
		// .mcp.json).
		"tools/analyzer/ARTIFACT.md":   "---\ntype: skill\nversion: 1.0.0\ndescription: A skill.\n---\n\nbody\n",
		"tools/analyzer/SKILL.md":      skillBody("analyzer"),
		"agents/planner/ARTIFACT.md":   "---\ntype: agent\nname: planner\nversion: 1.0.0\ndescription: An agent.\n---\n\nAgent body.\n",
		"context/glossary/ARTIFACT.md": contextArtifact("glossary"),
		"commands/deploy/ARTIFACT.md":  "---\ntype: command\nname: deploy\nversion: 1.0.0\ndescription: Deploy.\n---\n\n$ARGUMENTS\n",
		"rules/house/ARTIFACT.md":      "---\ntype: rule\nname: house\nversion: 1.0.0\nrule_mode: always\ndescription: House rule.\n---\n\nHouse style.\n",
		"hooks/stop-hook/ARTIFACT.md":  "---\ntype: hook\nname: stop-hook\nversion: 1.0.0\ndescription: Stop hook.\nhook_event: stop\nhook_action: echo done\n---\n\nbody\n",
		"servers/finance/ARTIFACT.md":  "---\ntype: mcp-server\nname: finance-warehouse\nversion: 1.0.0\ndescription: Finance MCP.\nserver_identifier: npx:@company/finance-mcp\n---\n\nbody\n",
	})
	target := t.TempDir()
	chSync(t, reg, target, "claude-code")

	// Standalone per-type outputs.
	mustExist(t, filepath.Join(target, ".claude", "skills", "analyzer", "SKILL.md"))
	mustExist(t, filepath.Join(target, ".claude", "agents", "planner.md"))
	mustExist(t, filepath.Join(target, ".podium", "context", "context", "glossary", "ARTIFACT.md"))
	mustExist(t, filepath.Join(target, ".claude", "commands", "deploy.md"))
	mustExist(t, filepath.Join(target, ".claude", "rules", "house.md"))

	// Hook config-merge into .claude/settings.json, with the ownership marker.
	settings := mzReadJSON(t, filepath.Join(target, ".claude", "settings.json"))
	settingsRaw := readFile(t, filepath.Join(target, ".claude", "settings.json"))
	if _, ok := settings["hooks"]; !ok {
		t.Errorf(".claude/settings.json missing hooks key:\n%s", settingsRaw)
	}
	if !strings.Contains(settingsRaw, "echo done") {
		t.Errorf(".claude/settings.json missing the hook action:\n%s", settingsRaw)
	}
	if !strings.Contains(settingsRaw, "x-podium-id") || !strings.Contains(settingsRaw, "hooks/stop-hook") {
		t.Errorf(".claude/settings.json hook entry missing the x-podium-id ownership marker for hooks/stop-hook:\n%s", settingsRaw)
	}

	// mcp-server config-merge into .mcp.json: a Podium-owned server entry keyed
	// by name=finance-warehouse, derived from server_identifier, with the
	// top-level x-podium ownership index recording the entry's location.
	mcp := mzReadJSON(t, filepath.Join(target, ".mcp.json"))
	mcpRaw := readFile(t, filepath.Join(target, ".mcp.json"))
	servers, ok := mcp["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf(".mcp.json missing mcpServers object:\n%s", mcpRaw)
	}
	entry, ok := servers["finance-warehouse"].(map[string]any)
	if !ok {
		t.Fatalf(".mcp.json missing the finance-warehouse server entry:\n%s", mcpRaw)
	}
	if entry["command"] != "npx" {
		t.Errorf(".mcp.json finance-warehouse command=%v, want npx (from server_identifier):\n%s", entry["command"], mcpRaw)
	}
	idx, ok := mcp["x-podium"].(map[string]any)
	if !ok {
		t.Fatalf(".mcp.json missing the x-podium ownership index:\n%s", mcpRaw)
	}
	if _, ok := idx["servers/finance"]; !ok {
		t.Errorf(".mcp.json x-podium index missing the servers/finance entry:\n%s", mcpRaw)
	}
}

// ------------------------------------------------------------------------

// running `podium sync --harness cursor` twice over a rule, a
// hook, and an mcp-server is idempotent: a non-Podium entry the operator adds to
// .cursor/hooks.json and .cursor/mcp.json between runs survives, the second run
// leaves exactly one Podium entry per artifact, the lock content_hash values are
// unchanged, and the materialized rule file is byte-identical. spec: §6.7
// (config-merge), §7.5.3 (lock content_hash), §7.5 (idempotent re-sync).
func TestMaterialize_CursorResyncConfigMergeReconcileIdempotent(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"style/naming/ARTIFACT.md":  "---\ntype: rule\nname: naming\nversion: 1.0.0\nrule_mode: always\ndescription: Naming.\n---\n\nUse snake_case.\n",
		"audit/shell/ARTIFACT.md":   "---\ntype: hook\nname: shell\nversion: 1.0.0\ndescription: Shell guard.\nhook_event: pre_shell_execution\nhook_action: echo guard\n---\n\nbody\n",
		"tools/finance/ARTIFACT.md": "---\ntype: mcp-server\nname: finance-warehouse\nversion: 1.0.0\ndescription: Finance MCP.\nserver_identifier: npx:@company/finance-mcp\n---\n\nbody\n",
	})
	target := t.TempDir()
	chSync(t, reg, target, "cursor")

	hooksPath := filepath.Join(target, ".cursor", "hooks.json")
	mcpPath := filepath.Join(target, ".cursor", "mcp.json")
	rulePath := filepath.Join(target, ".cursor", "rules", "naming.mdc")
	lockPath := filepath.Join(target, ".podium", "sync.lock")

	// Operator adds non-Podium entries to both config-merge files between runs.
	hooks := mzReadJSON(t, hooksPath)
	hookEvents := hooks["hooks"].(map[string]any)
	hookEvents["afterFileEdit"] = []any{map[string]any{"command": "operator-format"}}
	mzWriteJSON(t, hooksPath, hooks)

	mcp := mzReadJSON(t, mcpPath)
	mcp["mcpServers"].(map[string]any)["operator-db"] = map[string]any{"command": "operator-mcp"}
	mzWriteJSON(t, mcpPath, mcp)

	ruleBefore := readFile(t, rulePath)
	lockBefore := readFile(t, lockPath)

	// Second sync over the same registry.
	chSync(t, reg, target, "cursor")

	// The materialized rule file is byte-identical.
	if got := readFile(t, rulePath); got != ruleBefore {
		t.Errorf("rule .mdc changed across re-sync:\nbefore:\n%s\nafter:\n%s", ruleBefore, got)
	}

	// Lock content_hash entries are unchanged (idempotent re-sync). The
	// last_synced_at timestamp legitimately changes; the content_hash lines do
	// not.
	assertLockHashesStable(t, lockBefore, readFile(t, lockPath))

	// Exactly one Podium hook entry; the operator's afterFileEdit entry survives.
	hooksAfter := readFile(t, hooksPath)
	if n := mzCount(hooksAfter, `"audit/shell"`); n != 1 {
		t.Errorf(".cursor/hooks.json has %d Podium entries for audit/shell, want exactly 1:\n%s", n, hooksAfter)
	}
	if !strings.Contains(hooksAfter, "operator-format") {
		t.Errorf(".cursor/hooks.json lost the operator afterFileEdit entry:\n%s", hooksAfter)
	}

	// Exactly one Podium mcp entry; the operator's operator-db entry survives.
	mcpAfter := readFile(t, mcpPath)
	mcpAfterObj := mzReadJSON(t, mcpPath)
	servers := mcpAfterObj["mcpServers"].(map[string]any)
	if _, ok := servers["finance-warehouse"]; !ok {
		t.Errorf(".cursor/mcp.json missing the Podium finance-warehouse entry:\n%s", mcpAfter)
	}
	if _, ok := servers["operator-db"]; !ok {
		t.Errorf(".cursor/mcp.json lost the operator operator-db entry:\n%s", mcpAfter)
	}
	// Exactly the two expected servers (the Podium one and the operator one); a
	// re-sync must not accumulate a duplicate finance-warehouse entry.
	if len(servers) != 2 {
		t.Errorf(".cursor/mcp.json has %d mcpServers entries, want exactly 2 (finance-warehouse + operator-db):\n%s", len(servers), mcpAfter)
	}
	// The x-podium ownership index names the Podium server exactly once.
	if idx, ok := mcpAfterObj["x-podium"].(map[string]any); ok {
		if _, named := idx["tools/finance"]; !named || len(idx) != 1 {
			t.Errorf(".cursor/mcp.json x-podium index should name tools/finance exactly once, got %v:\n%s", idx, mcpAfter)
		}
	} else {
		t.Errorf(".cursor/mcp.json missing the x-podium ownership index after re-sync:\n%s", mcpAfter)
	}
}

// assertLockHashesStable fails when the set of content_hash lines differs
// between two lock-file renderings. The timestamp and last_synced_by fields may
// change across a re-sync; the per-artifact content_hash must not.
func assertLockHashesStable(t *testing.T, before, after string) {
	t.Helper()
	hashes := func(s string) []string {
		var out []string
		for _, line := range strings.Split(s, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "content_hash:") {
				out = append(out, line)
			}
		}
		return out
	}
	hb, ha := hashes(before), hashes(after)
	if strings.Join(hb, "\n") != strings.Join(ha, "\n") {
		t.Errorf("lock content_hash entries changed across re-sync:\nbefore:\n%s\nafter:\n%s",
			strings.Join(hb, "\n"), strings.Join(ha, "\n"))
	}
}

// ------------------------------------------------------------------------

// `podium sync --watch` rematerializes on an in-place rule-body
// edit and cleans a deleted artifact's standalone output within one watch
// session, then exits 0 on SIGINT. spec: §7.5 (watch loop), §7.5 (stale
// cleanup).
func TestMaterialize_WatchEditAndStaleCleanupInOneSession(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/house/ARTIFACT.md":     "---\ntype: rule\nname: house\nversion: 1.0.0\nrule_mode: always\ndescription: House rule.\n---\n\nORIGINAL house style.\n",
		"commands/deploy/ARTIFACT.md": "---\ntype: command\nname: deploy\nversion: 1.0.0\ndescription: Deploy.\n---\n\n$ARGUMENTS\n",
	})
	target := t.TempDir()
	w := startWatch(t, reg, target, "claude-code")

	rulePath := filepath.Join(target, ".claude", "rules", "house.md")
	cmdPath := filepath.Join(target, ".claude", "commands", "deploy.md")
	if !mzPollContains(rulePath, "ORIGINAL house style", 15*time.Second) {
		t.Fatalf("initial sync did not materialize the rule\nlog:\n%s", w.log())
	}
	if !pollFile(cmdPath, 15*time.Second) {
		t.Fatalf("initial sync did not materialize the command\nlog:\n%s", w.log())
	}

	// Edit the rule body in place; the watcher must rematerialize the new body.
	edited := "---\ntype: rule\nname: house\nversion: 1.1.0\nrule_mode: always\ndescription: House rule.\n---\n\nUPDATED house style.\n"
	solofsWriteFile(t, filepath.Join(reg, "rules", "house", "ARTIFACT.md"), edited)
	if !mzPollContains(rulePath, "UPDATED house style", 15*time.Second) {
		t.Errorf("watcher did not rematerialize the edited rule body\nlog:\n%s", w.log())
	}

	// Delete the command artifact; the watcher must clean its standalone output.
	if err := os.RemoveAll(filepath.Join(reg, "commands", "deploy")); err != nil {
		t.Fatalf("remove command artifact: %v", err)
	}
	if !mzPollGone(cmdPath, 15*time.Second) {
		t.Errorf("watcher did not remove the deleted command's output\nlog:\n%s", w.log())
	}

	if code := w.stop(t); code != 0 {
		t.Errorf("watch exit=%d on SIGINT, want 0\nlog:\n%s", code, w.log())
	}
}

// ------------------------------------------------------------------------

// the §6.7.1 unsupported-type matrix is enforced through real
// sync and lint for pi and claude-desktop: pi materializes a command to
// .pi/prompts/<name>.md while emitting no agent output, claude-desktop writes
// nothing project-level, and a declared-but-unsupported combination
// (agent target_harnesses:[pi]) surfaces a lint.harness_capability diagnostic.
// spec: §6.7 (pi type routing), §6.7.1 (capability matrix), §4.3.5.
func TestMaterialize_UnsupportedTypeMatrixPiAndClaudeDesktop(t *testing.T) {
	t.Parallel()
	// pi: an agent declares target_harnesses:[pi] (✗ cell) alongside a command.
	piReg := writeRegistry(t, map[string]string{
		"agents/planner/ARTIFACT.md":  "---\ntype: agent\nname: planner\nversion: 1.0.0\ndescription: An agent.\ntarget_harnesses: [pi]\n---\n\nAgent body.\n",
		"commands/deploy/ARTIFACT.md": "---\ntype: command\nname: deploy\nversion: 1.0.0\ndescription: Deploy.\n---\n\n$ARGUMENTS\n",
	})
	piTarget := t.TempDir()
	chSync(t, piReg, piTarget, "pi")
	// pi writes only the command prompt; no agent output exists.
	mustExist(t, filepath.Join(piTarget, ".pi", "prompts", "deploy.md"))
	mustNotExist(t, filepath.Join(piTarget, ".pi", "agents"))
	mustNotExist(t, filepath.Join(piTarget, ".pi", "prompts", "planner.md"))

	// The declared-but-unsupported agent/pi combination surfaces a diagnostic
	// through podium lint (the §6.7.1 capability lint over target_harnesses).
	lint := runPodium(t, "", nil, "lint", "--registry", piReg)
	if lint.Exit == 0 {
		t.Errorf("lint exit=0, want non-zero for the agent/pi ✗ cell\nstdout=%s", lint.Stdout)
	}
	if !strings.Contains(lint.Stdout, "lint.harness_capability") ||
		!strings.Contains(lint.Stdout, `adapter "pi" cannot translate`) {
		t.Errorf("lint missing the agent/pi capability diagnostic:\n%s", lint.Stdout)
	}

	// claude-desktop has no project-level surface: sync writes nothing for an
	// agent or a command (only the lock under .podium/).
	cdReg := writeRegistry(t, map[string]string{
		"agents/planner/ARTIFACT.md":  "---\ntype: agent\nname: planner\nversion: 1.0.0\ndescription: An agent.\n---\n\nAgent body.\n",
		"commands/deploy/ARTIFACT.md": "---\ntype: command\nname: deploy\nversion: 1.0.0\ndescription: Deploy.\n---\n\n$ARGUMENTS\n",
	})
	cdTarget := t.TempDir()
	chSync(t, cdReg, cdTarget, "claude-desktop")
	mustNotExist(t, filepath.Join(cdTarget, ".claude"))
	mustNotExist(t, filepath.Join(cdTarget, ".claude-desktop"))
	mustNotExist(t, filepath.Join(cdTarget, "AGENTS.md"))
	// Only .podium/ (the lock) is written project-level.
	entries, err := os.ReadDir(cdTarget)
	if err != nil {
		t.Fatalf("readdir target: %v", err)
	}
	for _, e := range entries {
		if e.Name() != ".podium" {
			t.Errorf("claude-desktop wrote an unexpected project-level entry: %s", e.Name())
		}
	}
}

// ------------------------------------------------------------------------

// changing a hook's hook_event and bumping its version, then
// re-syncing for gemini, reconciles the prior translated event entry before the
// re-merge: the old native event entry is gone, the new translated event appears
// exactly once, and any operator key in .gemini/settings.json survives. spec:
// §6.7 (config-merge strip-before-merge), §6.7.1 (event translation).
func TestMaterialize_GeminiHookEventChangeReconciles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	reg := filepath.Join(dir, "registry")
	hookDir := filepath.Join(reg, "audit", "guard")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// pre_tool_use translates to BeforeTool on gemini.
	v1 := "---\ntype: hook\nname: guard\nversion: 1.0.0\ndescription: Guard.\nhook_event: pre_tool_use\nhook_action: echo guard\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(hookDir, "ARTIFACT.md"), []byte(v1), 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	target := t.TempDir()
	chSync(t, reg, target, "gemini")

	settingsPath := filepath.Join(target, ".gemini", "settings.json")
	first := mzReadJSON(t, settingsPath)
	hooks := first["hooks"].(map[string]any)
	if _, ok := hooks["BeforeTool"]; !ok {
		t.Fatalf(".gemini/settings.json missing the BeforeTool event for pre_tool_use:\n%s", readFile(t, settingsPath))
	}
	// Operator adds a settings key the re-sync must preserve.
	first["theme"] = "dark"
	mzWriteJSON(t, settingsPath, first)

	// Change the event to post_tool_use, which gemini translates to AfterTool (a
	// distinct native event from pre_tool_use's BeforeTool, so the reconciliation
	// is observable as the old key disappearing and the new one appearing), and
	// bump the version.
	v2 := "---\ntype: hook\nname: guard\nversion: 2.0.0\ndescription: Guard.\nhook_event: post_tool_use\nhook_action: echo guard\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(hookDir, "ARTIFACT.md"), []byte(v2), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	chSync(t, reg, target, "gemini")

	after := mzReadJSON(t, settingsPath)
	afterRaw := readFile(t, settingsPath)
	afterHooks, ok := after["hooks"].(map[string]any)
	if !ok {
		t.Fatalf(".gemini/settings.json missing hooks after re-sync:\n%s", afterRaw)
	}
	// The prior translated event (BeforeTool) is gone.
	if _, ok := afterHooks["BeforeTool"]; ok {
		t.Errorf(".gemini/settings.json still carries the stale BeforeTool entry after the event change:\n%s", afterRaw)
	}
	// The new translated event (AfterTool) appears exactly once.
	afterToolList, ok := afterHooks["AfterTool"].([]any)
	if !ok || len(afterToolList) != 1 {
		t.Errorf(".gemini/settings.json AfterTool entry count=%d, want exactly 1:\n%s", len(afterToolList), afterRaw)
	}
	// The operator key survives.
	if after["theme"] != "dark" {
		t.Errorf(".gemini/settings.json lost the operator theme key:\n%s", afterRaw)
	}
}

// ------------------------------------------------------------------------

// removing an agent and an mcp-server, then re-syncing for
// claude-code, deletes the agent's standalone file, cleans the now-empty
// .claude/agents/ parent directory, strips the Podium mcp-server entry from
// .mcp.json, and preserves an operator-authored .mcp.json entry. spec: §6.7,
// §7.5 (stale cleanup), §6.7.1 (config-merge reconciliation).
func TestMaterialize_RemoveAgentAndMCPServerCleansAndReconciles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	reg := filepath.Join(dir, "registry")
	agentDir := filepath.Join(reg, "agents", "planner")
	srvDir := filepath.Join(reg, "tools", "finance")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("mkdir agent: %v", err)
	}
	if err := os.MkdirAll(srvDir, 0o755); err != nil {
		t.Fatalf("mkdir server: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "ARTIFACT.md"),
		[]byte("---\ntype: agent\nname: planner\nversion: 1.0.0\ndescription: An agent.\n---\n\nAgent body.\n"), 0o644); err != nil {
		t.Fatalf("write agent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srvDir, "ARTIFACT.md"),
		[]byte("---\ntype: mcp-server\nname: finance-warehouse\nversion: 1.0.0\ndescription: MCP.\nserver_identifier: npx:@company/finance-mcp\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatalf("write server: %v", err)
	}
	target := t.TempDir()
	chSync(t, reg, target, "claude-code")

	agentPath := filepath.Join(target, ".claude", "agents", "planner.md")
	mcpPath := filepath.Join(target, ".mcp.json")
	mustExist(t, agentPath)

	// Operator adds their own server entry to .mcp.json.
	mcp := mzReadJSON(t, mcpPath)
	mcp["mcpServers"].(map[string]any)["operator-db"] = map[string]any{"command": "operator-mcp"}
	mzWriteJSON(t, mcpPath, mcp)

	// Remove both artifacts and re-sync.
	if err := os.RemoveAll(agentDir); err != nil {
		t.Fatalf("remove agent: %v", err)
	}
	if err := os.RemoveAll(srvDir); err != nil {
		t.Fatalf("remove server: %v", err)
	}
	chSync(t, reg, target, "claude-code")

	// The agent's standalone file is deleted.
	mustNotExist(t, agentPath)
	// The now-empty parent directory is cleaned.
	if _, err := os.Stat(filepath.Join(target, ".claude", "agents")); !os.IsNotExist(err) {
		t.Errorf(".claude/agents/ should be cleaned after the agent is removed; stat err=%v", err)
	}
	// The Podium mcp-server entry is stripped; the operator entry survives.
	after := mzReadJSON(t, mcpPath)
	afterRaw := readFile(t, mcpPath)
	servers, ok := after["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf(".mcp.json missing mcpServers after reconcile:\n%s", afterRaw)
	}
	if _, ok := servers["finance-warehouse"]; ok {
		t.Errorf(".mcp.json still carries the Podium finance-warehouse entry:\n%s", afterRaw)
	}
	if _, ok := servers["operator-db"]; !ok {
		t.Errorf(".mcp.json lost the operator operator-db entry:\n%s", afterRaw)
	}
	if _, ok := after["x-podium"]; ok {
		t.Errorf(".mcp.json still carries the x-podium ownership index after the last Podium entry is gone:\n%s", afterRaw)
	}
}

// ------------------------------------------------------------------------

// re-syncing for codex over a rule and a hook reconciles the
// OpInject markdown block in AGENTS.md and the OpInject TOML block in
// .codex/config.toml in place: an operator line hand-edited into each file
// survives, each prior Podium block is replaced exactly once when the rule body
// changes and the version bumps, and the TOML stays parseable. spec: §6.7
// (OpInject strip-before-inject), §6.7.1.
func TestMaterialize_CodexResyncInjectAndTomlReconcile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	reg := filepath.Join(dir, "registry")
	ruleDir := filepath.Join(reg, "style", "house")
	hookDir := filepath.Join(reg, "audit", "guard")
	if err := os.MkdirAll(ruleDir, 0o755); err != nil {
		t.Fatalf("mkdir rule: %v", err)
	}
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatalf("mkdir hook: %v", err)
	}
	rv1 := "---\ntype: rule\nname: house\nversion: 1.0.0\nrule_mode: always\ndescription: House.\n---\n\nORIGINAL house style.\n"
	if err := os.WriteFile(filepath.Join(ruleDir, "ARTIFACT.md"), []byte(rv1), 0o644); err != nil {
		t.Fatalf("write rule v1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hookDir, "ARTIFACT.md"),
		[]byte("---\ntype: hook\nname: guard\nversion: 1.0.0\ndescription: Guard.\nhook_event: stop\nhook_action: echo guard\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	target := t.TempDir()
	chSync(t, reg, target, "codex")

	agentsPath := filepath.Join(target, "AGENTS.md")
	tomlPath := filepath.Join(target, ".codex", "config.toml")
	if !strings.Contains(readFile(t, agentsPath), "ORIGINAL house style") {
		t.Fatalf("AGENTS.md missing the initial rule body:\n%s", readFile(t, agentsPath))
	}

	// Operator hand-edits a non-Podium line into each file (outside the Podium
	// blocks).
	appendLine(t, agentsPath, "\nOperator note: keep imports sorted.\n")
	appendLine(t, tomlPath, "\n[operator]\nkeep = true\n")

	// Change the rule body and bump the version, then re-sync.
	rv2 := "---\ntype: rule\nname: house\nversion: 2.0.0\nrule_mode: always\ndescription: House.\n---\n\nUPDATED house style.\n"
	if err := os.WriteFile(filepath.Join(ruleDir, "ARTIFACT.md"), []byte(rv2), 0o644); err != nil {
		t.Fatalf("write rule v2: %v", err)
	}
	chSync(t, reg, target, "codex")

	agentsAfter := readFile(t, agentsPath)
	// The Podium block is replaced once in place: the new body present, the old
	// gone, and exactly one begin marker for the rule.
	if !strings.Contains(agentsAfter, "UPDATED house style") {
		t.Errorf("AGENTS.md missing the updated rule body:\n%s", agentsAfter)
	}
	if strings.Contains(agentsAfter, "ORIGINAL house style") {
		t.Errorf("AGENTS.md still carries the stale rule body:\n%s", agentsAfter)
	}
	if n := mzCount(agentsAfter, "podium:begin:style/house"); n != 1 {
		t.Errorf("AGENTS.md has %d Podium blocks for style/house, want exactly 1:\n%s", n, agentsAfter)
	}
	if !strings.Contains(agentsAfter, "Operator note: keep imports sorted.") {
		t.Errorf("AGENTS.md lost the operator line:\n%s", agentsAfter)
	}

	tomlAfter := readFile(t, tomlPath)
	if n := mzCount(tomlAfter, "podium:begin:audit/guard"); n != 1 {
		t.Errorf(".codex/config.toml has %d Podium blocks for audit/guard, want exactly 1:\n%s", n, tomlAfter)
	}
	if !strings.Contains(tomlAfter, "[operator]") {
		t.Errorf(".codex/config.toml lost the operator [operator] table:\n%s", tomlAfter)
	}
	// The TOML stays parseable.
	assertValidTOML(t, tomlPath)
}

// ------------------------------------------------------------------------

// `podium sync --harness opencode --type rule,context` over an
// all-types registry materializes only the selected types and stays idempotent:
// AGENTS.md injected rules and .podium/context buckets exist while the excluded
// .opencode/commands and opencode.json mcp outputs are absent, and a second run
// is byte-identical. The §7.5.1 / spec §7.5 --type flag is comma-separated
// (`--type rule,context`). spec: §7.5.1 (type filter), §7.5.
func TestMaterialize_OpenCodeTypeFilteredIdempotent(t *testing.T) {
	t.Parallel()
	reg := writeRegistry(t, map[string]string{
		"rules/house/ARTIFACT.md":      "---\ntype: rule\nname: house\nversion: 1.0.0\nrule_mode: always\ndescription: House.\n---\n\nHouse style.\n",
		"context/glossary/ARTIFACT.md": contextArtifact("glossary"),
		"commands/deploy/ARTIFACT.md":  "---\ntype: command\nname: deploy\nversion: 1.0.0\ndescription: Deploy.\n---\n\n$ARGUMENTS\n",
		"tools/finance/ARTIFACT.md":    "---\ntype: mcp-server\nname: finance-warehouse\nversion: 1.0.0\ndescription: MCP.\nserver_identifier: npx:@company/finance-mcp\n---\n\nbody\n",
	})
	target := t.TempDir()
	res := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", "opencode", "--type", "rule,context")
	if res.Exit != 0 {
		t.Fatalf("sync exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	// Selected types present.
	agents := readFile(t, filepath.Join(target, "AGENTS.md"))
	if !strings.Contains(agents, "House style") || !strings.Contains(agents, "podium:begin:rules/house") {
		t.Errorf("AGENTS.md missing the filtered rule injection:\n%s", agents)
	}
	mustExist(t, filepath.Join(target, ".podium", "context", "context", "glossary", "ARTIFACT.md"))
	// Excluded types absent.
	mustNotExist(t, filepath.Join(target, ".opencode", "commands", "deploy.md"))
	mustNotExist(t, filepath.Join(target, ".opencode", "commands"))
	mustNotExist(t, filepath.Join(target, "opencode.json"))

	// Re-run with the same filter: the whole tree (excluding the lock, whose
	// timestamp changes) is byte-identical.
	before := snapshotTree(t, target)
	res2 := runPodium(t, "", nil, "sync", "--registry", reg, "--target", target, "--harness", "opencode", "--type", "rule,context")
	if res2.Exit != 0 {
		t.Fatalf("re-sync exit=%d stderr=%s", res2.Exit, res2.Stderr)
	}
	after := snapshotTree(t, target)
	if before != after {
		t.Errorf("type-filtered re-sync was not idempotent:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// snapshotTree renders a deterministic path->content listing of every file under
// root, excluding the .podium/sync.lock (whose last_synced_at changes on every
// run). It is used to assert byte-level idempotency of a re-sync.
func snapshotTree(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if rel == filepath.Join(".podium", "sync.lock") {
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		b.WriteString(rel)
		b.WriteString("\n")
		b.Write(data)
		b.WriteString("\n---\n")
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return b.String()
}

// ------------------------------------------------------------------------

// removing a skill, an mcp-server, and a hook from a
// claude-cowork target, then re-syncing, strips the Podium marketplace.json and
// plugins/<id>/.mcp.json entries, cleans the emptied plugins/<id> directories
// (including nested skills/ and hooks/ subtrees), and preserves an operator
// marketplace entry. spec: §6.7 (cowork plugin layout), §7.5 (stale cleanup),
// §6.7.1 (config-merge reconciliation).
func TestMaterialize_ClaudeCoworkRemovePluginReconcilesAndCleans(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	reg := filepath.Join(dir, "registry")
	skillDir := filepath.Join(reg, "tools", "greet")
	srvDir := filepath.Join(reg, "tools", "finance")
	hookDir := filepath.Join(reg, "audit", "guard")
	for _, d := range []string{skillDir, srvDir, hookDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	if err := os.WriteFile(filepath.Join(skillDir, "ARTIFACT.md"),
		[]byte("---\ntype: skill\nversion: 1.0.0\ndescription: A skill.\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatalf("write skill artifact: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillBody("greet")), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srvDir, "ARTIFACT.md"),
		[]byte("---\ntype: mcp-server\nname: finance-warehouse\nversion: 1.0.0\ndescription: MCP.\nserver_identifier: npx:@company/finance-mcp\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatalf("write server: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hookDir, "ARTIFACT.md"),
		[]byte("---\ntype: hook\nname: guard\nversion: 1.0.0\ndescription: Guard.\nhook_event: stop\nhook_action: echo guard\n---\n\nbody\n"), 0o644); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	target := t.TempDir()
	chSync(t, reg, target, "claude-cowork")

	marketPath := filepath.Join(target, ".claude-plugin", "marketplace.json")
	// Verify the initial plugin trees exist.
	mustExist(t, filepath.Join(target, "plugins", "tools/greet", "skills", "greet", "SKILL.md"))
	mustExist(t, filepath.Join(target, "plugins", "tools/finance", ".mcp.json"))
	mustExist(t, filepath.Join(target, "plugins", "audit/guard", "hooks", "hooks.json"))

	// Operator appends their own marketplace entry.
	market := mzReadJSON(t, marketPath)
	plugins := market["plugins"].([]any)
	market["plugins"] = append(plugins, map[string]any{"name": "operator-plugin", "source": "./plugins/operator"})
	mzWriteJSON(t, marketPath, market)

	// Remove every artifact and re-sync.
	for _, d := range []string{skillDir, srvDir, hookDir} {
		if err := os.RemoveAll(d); err != nil {
			t.Fatalf("remove %s: %v", d, err)
		}
	}
	chSync(t, reg, target, "claude-cowork")

	// The plugin trees are cleaned, including nested subtrees and the per-id
	// parent directories.
	for _, leftover := range []string{
		filepath.Join(target, "plugins", "tools/greet"),
		filepath.Join(target, "plugins", "tools/finance"),
		filepath.Join(target, "plugins", "audit/guard"),
	} {
		if _, err := os.Stat(leftover); !os.IsNotExist(err) {
			t.Errorf("plugin directory not cleaned: %s (stat err=%v)", leftover, err)
		}
	}

	// The marketplace.json Podium entries are stripped; the operator entry
	// survives.
	after := mzReadJSON(t, marketPath)
	afterRaw := readFile(t, marketPath)
	afterPlugins, ok := after["plugins"].([]any)
	if !ok {
		t.Fatalf("marketplace.json missing plugins array after reconcile:\n%s", afterRaw)
	}
	if len(afterPlugins) != 1 {
		t.Errorf("marketplace.json has %d plugins after reconcile, want exactly 1 (the operator's):\n%s", len(afterPlugins), afterRaw)
	}
	operatorEntry, ok := afterPlugins[0].(map[string]any)
	if !ok || operatorEntry["name"] != "operator-plugin" {
		t.Errorf("marketplace.json lost the operator-plugin entry:\n%s", afterRaw)
	}
	if strings.Contains(afterRaw, "x-podium-id") {
		t.Errorf("marketplace.json still carries a Podium-owned plugin entry:\n%s", afterRaw)
	}
}

// assertValidTOML parses the file at path with the same TOML library the codex
// adapter relies on, failing the test on a parse error.
func assertValidTOML(t *testing.T, path string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var v map[string]any
	if err := toml.Unmarshal(b, &v); err != nil {
		t.Errorf("%s is not valid TOML: %v\n%s", path, err, b)
	}
}
