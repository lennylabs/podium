package sync_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/sync"
)

// a several-hundred-artifact catalog is materialized for claude-code,
// then dozens of artifacts spanning standalone files, hooks, and mcp-server
// entries are dropped and the catalog re-synced. The test asserts the §7.5
// stale-cleanup and §6.7 config-merge reconciliation contracts at scale:
//
//   - removed standalone files (agents, commands, deeply-nested context trees)
//     are deleted and their emptied parent directories pruned;
//   - the shared config-merge files (.claude/settings.json hooks,
//     .mcp.json mcpServers) keep the operator's own entries and every surviving
//     Podium entry while the dropped artifacts' entries disappear;
//   - a config-merge file whose every contributing Podium artifact is dropped
//     is reconciled in place (Podium entries stripped, operator content kept),
//     not deleted;
//   - a second re-sync is idempotent and the lock reflects exactly the current
//     artifact set.
//
// This exercises the same removeStalePaths / reconcileOrphanConfig /
// mergeJSON-with-prior-strip path the unit cleanup tests cover, but at a scale
// (40-plus drops across every materialization class) the unit tests do not.

// scaleArtifact builds a minimal valid ARTIFACT.md for the given type and a
// searchable description. Standalone classes (agent, command, context) and the
// two config-merge classes (hook, mcp-server) are all produced here.
func scaleArtifact(typ, name, desc string) string {
	switch typ {
	case "hook":
		// A claude-code hook config-merges into .claude/settings.json. The
		// command text carries a per-artifact marker so the test can assert the
		// exact surviving / removed entries.
		return fmt.Sprintf(
			"---\ntype: hook\nname: %s\nversion: 1.0.0\ndescription: %s\nhook_event: stop\nhook_action: |\n  echo HOOKMARK-%s\n---\n\nbody\n",
			name, desc, name)
	case "mcp-server":
		// An mcp-server config-merges into .mcp.json under mcpServers.<name>.
		return fmt.Sprintf(
			"---\ntype: mcp-server\nname: %s\nversion: 1.0.0\ndescription: %s\nserver_identifier: npx:@acme/%s\n---\n\nbody\n",
			name, desc, name)
	default:
		return fmt.Sprintf("---\ntype: %s\nversion: 1.0.0\ndescription: %s\nsensitivity: low\n---\n\n%s body.\n", typ, desc, desc)
	}
}

// scaleSyncCatalog writes a several-hundred-artifact registry under root and
// returns the set of materialized-file relative paths the test will later
// assert on, keyed by artifact ID. The catalog spans:
//   - standalone agents          -> .claude/agents/<name>.md
//   - standalone commands        -> .claude/commands/<name>.md
//   - deeply-nested context dirs -> .podium/context/<id>/ARTIFACT.md
//   - hooks                      -> .claude/settings.json (config-merge)
//   - mcp-server entries         -> .mcp.json (config-merge)
func scaleSyncCatalog(t *testing.T, root string) (agentIDs, commandIDs, contextIDs, hookIDs, mcpIDs []string) {
	t.Helper()
	write := func(rel, content string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", full, err)
		}
	}

	// 120 standalone agents under area-bucketed subdirectories.
	for i := 0; i < 120; i++ {
		name := fmt.Sprintf("agent-%03d", i)
		id := fmt.Sprintf("agents/grp%02d/%s", i%12, name)
		write(id+"/ARTIFACT.md", scaleArtifact("agent", name, "Agent "+name+" for scale resync."))
		agentIDs = append(agentIDs, id)
	}
	// 80 standalone commands.
	for i := 0; i < 80; i++ {
		name := fmt.Sprintf("cmd-%03d", i)
		id := fmt.Sprintf("commands/grp%02d/%s", i%8, name)
		write(id+"/ARTIFACT.md", scaleArtifact("command", name, "Command "+name+" for scale resync."))
		commandIDs = append(commandIDs, id)
	}
	// 80 context artifacts, each materializing into its own nested
	// .podium/context/<id>/ tree so emptied-parent pruning is exercised.
	for i := 0; i < 80; i++ {
		name := fmt.Sprintf("ctx-%03d", i)
		id := fmt.Sprintf("context/grp%02d/%s", i%10, name)
		write(id+"/ARTIFACT.md", scaleArtifact("context", name, "Context "+name+" for scale resync."))
		contextIDs = append(contextIDs, id)
	}
	// 30 hooks, all config-merging into the single .claude/settings.json.
	for i := 0; i < 30; i++ {
		name := fmt.Sprintf("hook-%03d", i)
		id := fmt.Sprintf("hooks/%s", name)
		write(id+"/ARTIFACT.md", scaleArtifact("hook", name, "Hook "+name+"."))
		hookIDs = append(hookIDs, id)
	}
	// 30 mcp-server entries, all config-merging into the single .mcp.json.
	for i := 0; i < 30; i++ {
		name := fmt.Sprintf("srv-%03d", i)
		id := fmt.Sprintf("mcp/%s", name)
		write(id+"/ARTIFACT.md", scaleArtifact("mcp-server", name, "Server "+name+"."))
		mcpIDs = append(mcpIDs, id)
	}
	return agentIDs, commandIDs, contextIDs, hookIDs, mcpIDs
}

func TestScale_LargeCatalogResyncCleansAndReconciles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	registry := filepath.Join(dir, "registry")
	target := filepath.Join(dir, "out")

	agentIDs, commandIDs, contextIDs, hookIDs, mcpIDs := scaleSyncCatalog(t, registry)
	total := len(agentIDs) + len(commandIDs) + len(contextIDs) + len(hookIDs) + len(mcpIDs)
	if total < 300 {
		t.Fatalf("catalog has %d artifacts, want several hundred", total)
	}

	// ---- first sync: full catalog -----------------------------------------
	if _, err := sync.Run(sync.Options{RegistryPath: registry, Target: target, AdapterID: "claude-code"}); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	settings := filepath.Join(target, ".claude", "settings.json")
	mcpFile := filepath.Join(target, ".mcp.json")
	mustExistFile(t, settings)
	mustExistFile(t, mcpFile)

	// The operator adds their own entries to both shared config files after the
	// first sync. These untagged entries must survive every later reconcile.
	injectOperatorJSON(t, settings, "operatorHookKey", "operator-hook-value")
	injectOperatorJSON(t, mcpFile, "operatorMcpKey", "operator-mcp-value")

	// Sanity: a sampling of standalone files materialized.
	mustExistFile(t, filepath.Join(target, ".claude", "agents", "agent-000.md"))
	mustExistFile(t, filepath.Join(target, ".claude", "commands", "cmd-000.md"))
	mustExistFile(t, filepath.Join(target, ".podium", "context", contextIDs[0], "ARTIFACT.md"))
	// Every hook and mcp-server entry is present in the shared files.
	for i := 0; i < 30; i++ {
		assertContains(t, settings, fmt.Sprintf("HOOKMARK-hook-%03d", i))
		assertContains(t, mcpFile, fmt.Sprintf("srv-%03d", i))
	}

	// ---- drop 40-plus artifacts across every class ------------------------
	// 15 agents, 10 commands, 8 context trees, 5 hooks, and ALL 30 mcp-servers
	// are removed from the registry. Dropping every mcp-server makes .mcp.json
	// an orphaned config-merge that must be reconciled (not deleted), while the
	// surviving hooks keep .claude/settings.json a live merge target.
	droppedAgents := dropIDs(t, registry, agentIDs[:15])
	droppedCommands := dropIDs(t, registry, commandIDs[:10])
	droppedContext := dropIDs(t, registry, contextIDs[:8])
	droppedHooks := hookIDs[:5]
	dropIDs(t, registry, droppedHooks)
	dropIDs(t, registry, mcpIDs) // every mcp-server gone
	dropped := len(droppedAgents) + len(droppedCommands) + len(droppedContext) + len(droppedHooks) + len(mcpIDs)
	if dropped < 40 {
		t.Fatalf("dropped %d artifacts, want >= 40", dropped)
	}

	// ---- second sync: cleanup + reconcile ---------------------------------
	if _, err := sync.Run(sync.Options{RegistryPath: registry, Target: target, AdapterID: "claude-code"}); err != nil {
		t.Fatalf("second sync: %v", err)
	}

	// Removed standalone files are deleted.
	for _, id := range droppedAgents {
		mustNotExistFile(t, filepath.Join(target, ".claude", "agents", filepath.Base(id)+".md"))
	}
	for _, id := range droppedCommands {
		mustNotExistFile(t, filepath.Join(target, ".claude", "commands", filepath.Base(id)+".md"))
	}
	// A dropped context artifact's whole nested tree is gone, including the
	// emptied .podium/context/<id> parent directory.
	for _, id := range droppedContext {
		mustNotExistFile(t, filepath.Join(target, ".podium", "context", id, "ARTIFACT.md"))
		mustNotExistFile(t, filepath.Join(target, ".podium", "context", id))
	}

	// Surviving standalone files remain.
	mustExistFile(t, filepath.Join(target, ".claude", "agents", filepath.Base(agentIDs[20])+".md"))
	mustExistFile(t, filepath.Join(target, ".podium", "context", contextIDs[20], "ARTIFACT.md"))

	// settings.json keeps the operator key and every surviving Podium hook, and
	// drops exactly the removed hooks' entries.
	mustExistFile(t, settings)
	assertContains(t, settings, "operatorHookKey")
	for i := 0; i < 5; i++ { // dropped
		assertNotContains(t, settings, fmt.Sprintf("HOOKMARK-hook-%03d", i))
	}
	for i := 5; i < 30; i++ { // surviving
		assertContains(t, settings, fmt.Sprintf("HOOKMARK-hook-%03d", i))
	}

	// .mcp.json had every contributing artifact dropped, so it is reconciled in
	// place: the operator key survives, and no Podium mcpServers entry or
	// ownership index remains.
	mustExistFile(t, mcpFile)
	assertContains(t, mcpFile, "operatorMcpKey")
	for i := 0; i < 30; i++ {
		assertNotContains(t, mcpFile, fmt.Sprintf("srv-%03d", i))
	}
	// The Podium ownership index is gone once no Podium entry remains.
	assertNotContains(t, mcpFile, "x-podium")
	// The reconciled file is still valid JSON carrying the operator's value.
	assertJSONHasKey(t, mcpFile, "operatorMcpKey", "operator-mcp-value")

	// ---- third sync: idempotence ------------------------------------------
	settingsBefore := readFileString(t, settings)
	mcpBefore := readFileString(t, mcpFile)
	if _, err := sync.Run(sync.Options{RegistryPath: registry, Target: target, AdapterID: "claude-code"}); err != nil {
		t.Fatalf("third sync: %v", err)
	}
	if got := readFileString(t, settings); got != settingsBefore {
		t.Errorf("settings.json changed on idempotent re-sync:\nbefore=%s\nafter=%s", settingsBefore, got)
	}
	if got := readFileString(t, mcpFile); got != mcpBefore {
		t.Errorf(".mcp.json changed on idempotent re-sync:\nbefore=%s\nafter=%s", mcpBefore, got)
	}

	// The lock reflects exactly the current artifact set: every dropped ID is
	// absent and a sampling of survivors is present.
	lock, err := sync.ReadLock(target)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	lockIDs := map[string]bool{}
	for _, a := range lock.Artifacts {
		lockIDs[a.ID] = true
	}
	for _, id := range append(append([]string{}, droppedAgents...), droppedContext...) {
		if lockIDs[id] {
			t.Errorf("dropped artifact %q still present in lock", id)
		}
	}
	for _, id := range mcpIDs {
		if lockIDs[id] {
			t.Errorf("dropped mcp-server %q still present in lock", id)
		}
	}
	for _, id := range []string{agentIDs[20], commandIDs[20], contextIDs[20], hookIDs[20]} {
		if !lockIDs[id] {
			t.Errorf("surviving artifact %q missing from lock", id)
		}
	}
	// No mcp-server entry remains in the lock (all dropped).
	for id := range lockIDs {
		if strings.HasPrefix(id, "mcp/") {
			t.Errorf("lock retains an mcp-server entry %q after all were dropped", id)
		}
	}
}

// dropIDs removes each artifact directory from the registry and returns the IDs
// it dropped (for convenience in building assertion lists).
func dropIDs(t *testing.T, registry string, ids []string) []string {
	t.Helper()
	for _, id := range ids {
		if err := os.RemoveAll(filepath.Join(registry, id)); err != nil {
			t.Fatalf("RemoveAll %s: %v", id, err)
		}
	}
	return ids
}

// injectOperatorJSON adds a top-level operator-owned key to a JSON config file,
// preserving the existing (Podium-merged) content. The key carries no
// x-podium-id / x-podium ownership tag, so it must survive every reconcile.
func injectOperatorJSON(t *testing.T, path, key, value string) {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(readFileString(t, path)), &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	doc[key] = value
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal %s: %v", path, err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustExistFile(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected %s to exist: %v", path, err)
	}
}

func mustNotExistFile(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected %s to be gone; stat err=%v", path, err)
	}
}

func assertContains(t *testing.T, path, want string) {
	t.Helper()
	if got := readFileString(t, path); !strings.Contains(got, want) {
		t.Errorf("%s missing %q:\n%s", path, want, got)
	}
}

func assertNotContains(t *testing.T, path, notWant string) {
	t.Helper()
	if got := readFileString(t, path); strings.Contains(got, notWant) {
		t.Errorf("%s still contains %q:\n%s", path, notWant, got)
	}
}

func assertJSONHasKey(t *testing.T, path, key, value string) {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(readFileString(t, path)), &doc); err != nil {
		t.Fatalf("parse %s as JSON: %v", path, err)
	}
	if doc[key] != value {
		t.Errorf("%s[%q] = %v, want %q", path, key, doc[key], value)
	}
}
