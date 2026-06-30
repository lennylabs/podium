package e2e

// End-to-end coverage for the §7.5.2 workflow-execution boundary and Decision 8
// of proposal 0005: a `workflow:` runs only when `podium sync` materializes a
// target. The MCP server reads sync.yaml for the registry, the profiles, and the
// version pin (cmd/podium-mcp/cliconfig.go: cfg.CheckServerVersion) and for the
// overlay watch (cmd/podium-mcp/overlay_watch.go), and it never iterates the
// targets list or executes a target's workflow. Because `workflow` is a field on
// any target, sync.yaml is execution-bearing, so the always-on multi-tenant
// bridge must never become a command-execution path.
//
// The test drives the real podium-mcp binary against a sync.yaml whose
// `kind: marketplace` target carries a workflow whose prepare and publish
// commands would each write a sentinel file. It boots the bridge against a
// working server-source registry, runs an initialize, and asserts the bridge
// starts normally and the sentinel files were never written.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/internal/testharness/cmdharness"
)

// writeMCPSyncYAMLWithWorkflow writes a sync.yaml under <home>/.podium/ that
// pins defaults.registry to a server source and declares one `kind: marketplace`
// target whose prepare and publish workflow commands each write a sentinel file.
// The bridge runs with HOME and cwd both pointing at home (helpers_test.go
// runBin), so the file is the workspace overlay scope and the home-global scope.
// It returns the prepare and publish sentinel paths the test asserts never exist.
func writeMCPSyncYAMLWithWorkflow(t *testing.T, home, registry string) (prepSentinel, pubSentinel string) {
	t.Helper()
	dir := filepath.Join(home, ".podium")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir .podium: %v", err)
	}
	prepSentinel = filepath.Join(home, "mcp-prepare-ran")
	pubSentinel = filepath.Join(home, "mcp-publish-ran")
	body := "" +
		"defaults:\n" +
		"  registry: " + registry + "\n" +
		"  identity: publisher@acme.com\n" +
		"targets:\n" +
		"  - id: acme-agents\n" +
		"    kind: marketplace\n" +
		"    target: " + filepath.Join(home, "build", "acme-agents") + "\n" +
		"    git:\n" +
		"      remote: git@github.com:acme/agent-marketplace.git\n" +
		"      branch: main\n" +
		"    harnesses: [claude-code]\n" +
		"    commit_message: \"Sync Podium catalog\"\n" +
		"    plugins:\n" +
		"      - name: finance-pack\n" +
		"        include: [\"finance/**\"]\n" +
		"    workflow:\n" +
		"      prepare:\n" +
		"        - sh: \"echo ran > " + prepSentinel + "\"\n" +
		"      publish:\n" +
		"        - sh: \"echo ran > " + pubSentinel + "\"\n"
	if err := os.WriteFile(filepath.Join(dir, "sync.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write sync.yaml: %v", err)
	}
	return prepSentinel, pubSentinel
}

// The MCP bridge boots against a sync.yaml carrying a `kind: marketplace` target
// with a workflow and never executes the workflow. The boot path parses the file
// for the version pin (cfg.CheckServerVersion) and starts the overlay watch;
// neither iterates targets nor runs a command. The bridge starts normally
// (initialize returns serverInfo) and the workflow's prepare and publish sentinel
// files are never written.
//
// spec: §7.5.2 (workflow-execution boundary), §7.8.
func TestMCP_NeverExecutesTargetWorkflow(t *testing.T) {
	t.Parallel()

	srv := startServer(t, writeRegistry(t, map[string]string{
		"finance/close/run-variance/ARTIFACT.md": pubSkillArtifact,
		"finance/close/run-variance/SKILL.md":    pubSkillBody("run-variance"),
	}))

	// The bridge runs with HOME and cwd both pinned to this isolated home, so the
	// sync.yaml here is both the workspace overlay and the home-global scope the
	// boot path reads (cmd/podium-mcp/cliconfig.go).
	home := cmdharness.IsolatedHome(t)
	prepSentinel, pubSentinel := writeMCPSyncYAMLWithWorkflow(t, home, srv.BaseURL)

	// An explicit PODIUM_REGISTRY is still passed so the boot path never depends
	// on the sync.yaml registry resolution; the file is parsed regardless for the
	// version pin and the overlay watch.
	env := mcpServerEnv(t, srv.BaseURL)
	res := mcpExec(t, env, rpcReq{ID: 1, Method: "initialize", Params: map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "host", "version": "1.0.0"},
	}})

	// The bridge started normally: initialize returned a result carrying
	// serverInfo, so the marketplace target and its workflow did not block boot.
	result := rpcResult(t, res.Stdout, 1)
	if result["serverInfo"] == nil {
		t.Fatalf("initialize did not complete; the marketplace target blocked boot: %v\nstderr:\n%s", result, res.Stderr)
	}

	// Decision 8: neither the version-pin parse nor the overlay watch executed the
	// target's workflow, so no prepare or publish command ran.
	if _, err := os.Stat(prepSentinel); !os.IsNotExist(err) {
		t.Errorf("the MCP bridge executed the target's prepare workflow command (sentinel %s exists, stat err=%v)", prepSentinel, err)
	}
	if _, err := os.Stat(pubSentinel); !os.IsNotExist(err) {
		t.Errorf("the MCP bridge executed the target's publish workflow command (sentinel %s exists, stat err=%v)", pubSentinel, err)
	}
}
