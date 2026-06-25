package adapter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Spec: §7.8 marketplace emitters. These tests pin each plugin-marketplace
// emitter's manifest path, per-plugin manifest path, the once-per-plugin entry
// count, the PodiumOwnedKey-on-plugin-name reconciliation, and the Codex
// .app.json component the §6.7 distribution table mandates.

// finPlugin is the descriptor used across the emitter tests: a finance-pack
// plugin under the harness subtree.
func finPlugin(prefix string) PluginDescriptor {
	return PluginDescriptor{Name: "finance-pack", Prefix: prefix, Description: "Finance artifacts."}
}

// fileByPath returns the File at exactly p, or fails the test.
func fileByPath(t *testing.T, files []File, p string) File {
	t.Helper()
	for _, f := range files {
		if f.Path == p {
			return f
		}
	}
	t.Fatalf("no file at %q in output: %v", p, paths(files))
	return File{}
}

// pluginEntries unmarshals a marketplace fragment and returns its plugins array.
func pluginEntries(t *testing.T, frag []byte) []any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(frag, &m); err != nil {
		t.Fatalf("marketplace fragment is not valid JSON: %v\n%s", err, frag)
	}
	plugins, ok := m["plugins"].([]any)
	if !ok {
		t.Fatalf("marketplace fragment has no plugins array: %s", frag)
	}
	return plugins
}

// Spec: §7.8 — the Claude emitter writes the root marketplace manifest at
// .claude-plugin/marketplace.json and a per-plugin .claude-plugin/plugin.json
// under the plugin subtree.
func TestClaudeMarketplace_ManifestPaths(t *testing.T) {
	t.Parallel()
	out, err := ClaudeMarketplace{}.Manifest("acme-agents", finPlugin("claude"))
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	mkt := fileByPath(t, out, ".claude-plugin/marketplace.json")
	if mkt.Op != OpMergeJSON {
		t.Errorf("marketplace manifest must be OpMergeJSON, got %v", mkt.Op)
	}
	fileByPath(t, out, "claude/finance-pack/.claude-plugin/plugin.json")
}

// Spec: §7.8 — the Codex emitter writes .agents/plugins/marketplace.json and a
// per-plugin .codex-plugin/plugin.json under the plugin subtree.
func TestCodexMarketplace_ManifestPaths(t *testing.T) {
	t.Parallel()
	out, err := CodexMarketplace{}.Manifest("acme-agents", finPlugin("codex"))
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	mkt := fileByPath(t, out, ".agents/plugins/marketplace.json")
	if mkt.Op != OpMergeJSON {
		t.Errorf("marketplace manifest must be OpMergeJSON, got %v", mkt.Op)
	}
	fileByPath(t, out, "codex/finance-pack/.codex-plugin/plugin.json")
}

// Spec: §7.8 — the Cursor emitter writes .cursor-plugin/marketplace.json and a
// per-plugin .cursor-plugin/plugin.json under the plugin subtree.
func TestCursorMarketplace_ManifestPaths(t *testing.T) {
	t.Parallel()
	out, err := CursorMarketplace{}.Manifest("acme-agents", finPlugin("cursor"))
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	mkt := fileByPath(t, out, ".cursor-plugin/marketplace.json")
	if mkt.Op != OpMergeJSON {
		t.Errorf("marketplace manifest must be OpMergeJSON, got %v", mkt.Op)
	}
	fileByPath(t, out, "cursor/finance-pack/.cursor-plugin/plugin.json")
}

// Spec: §6.7, §7.8 — the Codex distribution table lists .app.json and .mcp.json
// in the component set. The per-plugin .app.json is part of the manifest, and a
// mcp-server artifact contributes .mcp.json under the plugin subtree.
func TestCodexMarketplace_AppJSONComponentPresent(t *testing.T) {
	t.Parallel()
	out, err := CodexMarketplace{}.Manifest("acme-agents", finPlugin("codex"))
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	app := fileByPath(t, out, "codex/finance-pack/.app.json")
	var m map[string]any
	if err := json.Unmarshal(app.Content, &m); err != nil {
		t.Fatalf(".app.json is not valid JSON: %v\n%s", err, app.Content)
	}
	if m["name"] != "finance-pack" {
		t.Errorf(".app.json name = %v, want finance-pack", m["name"])
	}
}

func TestCodexMarketplace_MCPComponentUnderSubtree(t *testing.T) {
	t.Parallel()
	src := Source{
		ArtifactID:    "finance/pay-mcp",
		ArtifactBytes: []byte("---\ntype: mcp-server\nversion: 1.0.0\nserver_identifier: https://mcp.acme.com\n---\n\nbody\n"),
		Plugin:        finPlugin("codex"),
	}
	out, err := CodexMarketplace{}.Component(context.Background(), src)
	if err != nil {
		t.Fatalf("Component: %v", err)
	}
	mcp := fileByPath(t, out, "codex/finance-pack/.mcp.json")
	if !strings.Contains(string(mcp.Content), "mcpServers") {
		t.Errorf(".mcp.json must carry mcpServers:\n%s", mcp.Content)
	}
}

// Spec: §7.8 — the marketplace entry is contributed once per plugin keyed by
// the plugin name, so a Manifest call yields exactly one plugin entry. The
// entry carries PodiumOwnedKey set to the plugin name (not an artifact ID), so
// an N-artifact plugin reconciles to a single entry under the array-concat
// merge.
func TestMarketplace_OncePerPluginEntryAndPodiumOwnedKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		emitter  MarketplaceEmitter
		prefix   string
		manifest string
	}{
		{"claude", ClaudeMarketplace{}, "claude", ".claude-plugin/marketplace.json"},
		{"codex", CodexMarketplace{}, "codex", ".agents/plugins/marketplace.json"},
		{"cursor", CursorMarketplace{}, "cursor", ".cursor-plugin/marketplace.json"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, err := tc.emitter.Manifest("acme-agents", finPlugin(tc.prefix))
			if err != nil {
				t.Fatalf("Manifest: %v", err)
			}
			frag := fileByPath(t, out, tc.manifest)
			entries := pluginEntries(t, frag.Content)
			if len(entries) != 1 {
				t.Fatalf("Manifest must contribute one plugin entry, got %d: %s", len(entries), frag.Content)
			}
			entry, ok := entries[0].(map[string]any)
			if !ok {
				t.Fatalf("plugin entry is not an object: %v", entries[0])
			}
			if entry["name"] != "finance-pack" {
				t.Errorf("plugin entry name = %v, want finance-pack", entry["name"])
			}
			if got := entry[PodiumOwnedKey]; got != "finance-pack" {
				t.Errorf("PodiumOwnedKey on plugin entry = %v, want the plugin name finance-pack (not an artifact ID)", got)
			}
			if entry["source"] != "./"+tc.prefix+"/finance-pack" {
				t.Errorf("plugin source = %v, want ./%s/finance-pack", entry["source"], tc.prefix)
			}
		})
	}
}

// Spec: §7.8 — the per-plugin manifest entry is contributed once per plugin, so
// rendering an N-artifact plugin (Component once per artifact + Manifest once)
// yields exactly one plugin entry rather than N. The fragment is identical
// regardless of how many artifacts the plugin holds.
func TestMarketplace_NArtifactPluginYieldsOneEntry(t *testing.T) {
	t.Parallel()
	plugin := finPlugin("claude")
	emitter := ClaudeMarketplace{}

	// Render three artifacts' components into the plugin.
	arts := []Source{
		{ArtifactID: "finance/a", ArtifactBytes: []byte("---\ntype: agent\nversion: 1.0.0\n---\n\nA\n"), Plugin: plugin},
		{ArtifactID: "finance/b", ArtifactBytes: []byte("---\ntype: command\nversion: 1.0.0\n---\n\nB\n"), Plugin: plugin},
		{ArtifactID: "finance/c", SkillBytes: []byte("---\nname: c\ndescription: d\n---\n\nC\n"), ArtifactBytes: []byte("---\ntype: skill\nversion: 1.0.0\n---\n\nC\n"), Plugin: plugin},
	}
	for _, a := range arts {
		comp, err := emitter.Component(context.Background(), a)
		if err != nil {
			t.Fatalf("Component(%s): %v", a.ArtifactID, err)
		}
		if len(comp) == 0 {
			t.Fatalf("Component(%s) produced no files", a.ArtifactID)
		}
	}

	// The manifest is contributed once for the whole plugin.
	out, err := emitter.Manifest("acme-agents", plugin)
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	frag := fileByPath(t, out, ".claude-plugin/marketplace.json")
	if got := len(pluginEntries(t, frag.Content)); got != 1 {
		t.Errorf("an N-artifact plugin must yield one manifest entry, got %d", got)
	}
}

// Spec: §7.8 — the Claude emitter routes each plugin-layout type to its native
// plugin component under the plugin subtree.
func TestClaudeMarketplace_ComponentRouting(t *testing.T) {
	t.Parallel()
	plugin := finPlugin("claude")
	cases := []struct {
		name     string
		src      Source
		wantPath string
	}{
		{
			"skill",
			Source{ArtifactID: "finance/sk", SkillBytes: []byte("---\nname: sk\ndescription: d\n---\n\nbody\n"), ArtifactBytes: []byte("---\ntype: skill\nversion: 1.0.0\n---\n\nbody\n"), Plugin: plugin},
			"claude/finance-pack/skills/sk/SKILL.md",
		},
		{
			"agent",
			Source{ArtifactID: "finance/ag", ArtifactBytes: []byte("---\ntype: agent\nversion: 1.0.0\n---\n\nbody\n"), Plugin: plugin},
			"claude/finance-pack/agents/ag.md",
		},
		{
			"command",
			Source{ArtifactID: "finance/cmd", ArtifactBytes: []byte("---\ntype: command\nversion: 1.0.0\n---\n\nbody\n"), Plugin: plugin},
			"claude/finance-pack/commands/cmd.md",
		},
		{
			"mcp-server",
			Source{ArtifactID: "finance/mcp", ArtifactBytes: []byte("---\ntype: mcp-server\nversion: 1.0.0\nserver_identifier: https://mcp.acme.com\n---\n\nbody\n"), Plugin: plugin},
			"claude/finance-pack/.mcp.json",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, err := ClaudeMarketplace{}.Component(context.Background(), tc.src)
			if err != nil {
				t.Fatalf("Component: %v", err)
			}
			fileByPath(t, out, tc.wantPath)
		})
	}
}

// Spec: §6.7.1 — a rule has no native plugin component, so the Claude emitter
// ships it as a skill with a synthesized SKILL.md that carries the rule
// description and body and does not leak the rule frontmatter.
func TestClaudeMarketplace_RuleShipsAsSkill(t *testing.T) {
	t.Parallel()
	src := Source{
		ArtifactID:    "finance/house-rule",
		ArtifactBytes: []byte("---\ntype: rule\nversion: 1.0.0\nrule_mode: always\ndescription: Be careful.\n---\n\nRule prose.\n"),
		Plugin:        finPlugin("claude"),
	}
	out, err := ClaudeMarketplace{}.Component(context.Background(), src)
	if err != nil {
		t.Fatalf("Component: %v", err)
	}
	skill := fileByPath(t, out, "claude/finance-pack/skills/house-rule/SKILL.md")
	body := string(skill.Content)
	if !strings.Contains(body, "Rule prose.") {
		t.Errorf("synthesized SKILL.md must carry the rule prose:\n%s", body)
	}
	if !strings.Contains(body, "description: Be careful.") {
		t.Errorf("synthesized SKILL.md must carry the rule description:\n%s", body)
	}
	if strings.Contains(body, "rule_mode") || strings.Contains(body, "type: rule") {
		t.Errorf("rule frontmatter leaked into the SKILL.md:\n%s", body)
	}
}

// Spec: §7.8 — the Cursor emitter writes a rule as rules/<name>.mdc and a
// mcp-server as mcp.json under the plugin subtree.
func TestCursorMarketplace_ComponentRouting(t *testing.T) {
	t.Parallel()
	plugin := finPlugin("cursor")
	rule := Source{
		ArtifactID:    "finance/rl",
		ArtifactBytes: []byte("---\ntype: rule\nversion: 1.0.0\nrule_mode: always\ndescription: d\n---\n\nRule prose.\n"),
		Plugin:        plugin,
	}
	out, err := CursorMarketplace{}.Component(context.Background(), rule)
	if err != nil {
		t.Fatalf("Component(rule): %v", err)
	}
	mdc := fileByPath(t, out, "cursor/finance-pack/rules/rl.mdc")
	if !strings.Contains(string(mdc.Content), "alwaysApply: true") {
		t.Errorf("cursor rule .mdc must carry alwaysApply: true:\n%s", mdc.Content)
	}

	mcp := Source{
		ArtifactID:    "finance/mcp",
		ArtifactBytes: []byte("---\ntype: mcp-server\nversion: 1.0.0\nserver_identifier: https://mcp.acme.com\n---\n\nbody\n"),
		Plugin:        plugin,
	}
	out, err = CursorMarketplace{}.Component(context.Background(), mcp)
	if err != nil {
		t.Fatalf("Component(mcp): %v", err)
	}
	fileByPath(t, out, "cursor/finance-pack/mcp.json")
}

// Spec: §7.8 — an empty plugin description omits the manifest description key
// rather than emitting a null, so a strict harness schema accepts the object.
func TestMarketplace_EmptyDescriptionOmitsKey(t *testing.T) {
	t.Parallel()
	out, err := ClaudeMarketplace{}.Manifest("acme-agents", PluginDescriptor{Name: "p", Prefix: "claude"})
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	pj := fileByPath(t, out, "claude/p/.claude-plugin/plugin.json")
	var m map[string]any
	if err := json.Unmarshal(pj.Content, &m); err != nil {
		t.Fatalf("plugin.json is not valid JSON: %v", err)
	}
	if _, ok := m["description"]; ok {
		t.Errorf("plugin.json must omit description when unset: %s", pj.Content)
	}
}

// hookSource builds a session_start hook artifact bundling a script, used to
// exercise each emitter's hook component branch.
func hookSource(prefix string) Source {
	return Source{
		ArtifactID:    "finance/notify",
		ArtifactBytes: []byte("---\ntype: hook\nversion: 1.0.0\nhook_event: session_start\nhook_action: scripts/notify.sh\n---\n\nbody\n"),
		Resources:     map[string][]byte{"scripts/notify.sh": []byte("#!/bin/sh\necho hi\n")},
		Plugin:        finPlugin(prefix),
	}
}

// Spec: §7.8 — the Claude emitter config-merges a hook into the plugin's
// hooks/hooks.json and lands the bundled script under the plugin subtree.
func TestClaudeMarketplace_HookComponent(t *testing.T) {
	t.Parallel()
	out, err := ClaudeMarketplace{}.Component(context.Background(), hookSource("claude"))
	if err != nil {
		t.Fatalf("Component: %v", err)
	}
	hooks := fileByPath(t, out, "claude/finance-pack/hooks/hooks.json")
	if !strings.Contains(string(hooks.Content), "SessionStart") {
		t.Errorf("hooks.json must carry the native SessionStart event:\n%s", hooks.Content)
	}
	fileByPath(t, out, "claude/finance-pack/hooks/scripts/notify.sh")
}

// Spec: §6.7.1, §7.8 — a hook whose canonical event has no Claude-native
// mapping produces no hook component (the fragment is nil), so no orphaned
// hooks.json or script is written.
func TestClaudeMarketplace_HookWithoutNativeEventEmitsNothing(t *testing.T) {
	t.Parallel()
	src := Source{
		ArtifactID:    "finance/odd",
		ArtifactBytes: []byte("---\ntype: hook\nversion: 1.0.0\nhook_event: not_a_real_event\nhook_action: x\n---\n\nbody\n"),
		Plugin:        finPlugin("claude"),
	}
	out, err := ClaudeMarketplace{}.Component(context.Background(), src)
	if err != nil {
		t.Fatalf("Component: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("a hook without a native Claude event must emit no files, got %v", paths(out))
	}
}

// Spec: §7.8 — the Codex emitter ships a skill under skills/<name>/ and
// config-merges a hook into hooks/hooks.json.
func TestCodexMarketplace_SkillAndHookComponents(t *testing.T) {
	t.Parallel()
	skill := Source{
		ArtifactID:    "finance/sk",
		SkillBytes:    []byte("---\nname: sk\ndescription: d\n---\n\nbody\n"),
		ArtifactBytes: []byte("---\ntype: skill\nversion: 1.0.0\n---\n\nbody\n"),
		Plugin:        finPlugin("codex"),
	}
	out, err := CodexMarketplace{}.Component(context.Background(), skill)
	if err != nil {
		t.Fatalf("Component(skill): %v", err)
	}
	fileByPath(t, out, "codex/finance-pack/skills/sk/SKILL.md")

	out, err = CodexMarketplace{}.Component(context.Background(), hookSource("codex"))
	if err != nil {
		t.Fatalf("Component(hook): %v", err)
	}
	fileByPath(t, out, "codex/finance-pack/hooks/hooks.json")
	fileByPath(t, out, "codex/finance-pack/hooks/scripts/notify.sh")
}

// Spec: §7.8 — the Cursor emitter ships a skill under skills/<name>/.
func TestCursorMarketplace_SkillComponent(t *testing.T) {
	t.Parallel()
	skill := Source{
		ArtifactID:    "finance/sk",
		SkillBytes:    []byte("---\nname: sk\ndescription: d\n---\n\nbody\n"),
		ArtifactBytes: []byte("---\ntype: skill\nversion: 1.0.0\n---\n\nbody\n"),
		Plugin:        finPlugin("cursor"),
	}
	out, err := CursorMarketplace{}.Component(context.Background(), skill)
	if err != nil {
		t.Fatalf("Component(skill): %v", err)
	}
	fileByPath(t, out, "cursor/finance-pack/skills/sk/SKILL.md")
}

// Spec: §7.8 — a plugin-layout type a harness has no marketplace component for
// returns no files: an agent on Codex (skills are the install unit) and a hook
// on Cursor (no Cursor marketplace hook component).
func TestMarketplace_UnsupportedComponentTypeEmitsNothing(t *testing.T) {
	t.Parallel()
	codexAgent := Source{ArtifactID: "finance/ag", ArtifactBytes: []byte("---\ntype: agent\nversion: 1.0.0\n---\n\nbody\n"), Plugin: finPlugin("codex")}
	out, err := CodexMarketplace{}.Component(context.Background(), codexAgent)
	if err != nil || len(out) != 0 {
		t.Errorf("Codex agent component = %v, %v; want no files", paths(out), err)
	}
	out, err = CursorMarketplace{}.Component(context.Background(), hookSource("cursor"))
	if err != nil || len(out) != 0 {
		t.Errorf("Cursor hook component = %v, %v; want no files", paths(out), err)
	}
}

// Spec: §6.7.1 — a rule with no description and no SKILL.md still ships as a
// skill, with a synthesized SKILL.md carrying the generic description fallback
// and the rule body.
func TestClaudeMarketplace_RuleSkillBodyDefaultDescription(t *testing.T) {
	t.Parallel()
	src := Source{
		ArtifactID:    "finance/bare-rule",
		ArtifactBytes: []byte("---\ntype: rule\nversion: 1.0.0\n---\n\nRule prose without trailing newline"),
		Plugin:        finPlugin("claude"),
	}
	out, err := ClaudeMarketplace{}.Component(context.Background(), src)
	if err != nil {
		t.Fatalf("Component: %v", err)
	}
	skill := fileByPath(t, out, "claude/finance-pack/skills/bare-rule/SKILL.md")
	body := string(skill.Content)
	if !strings.Contains(body, "description: Project rule shipped as a skill.") {
		t.Errorf("rule without a description must use the generic fallback:\n%s", body)
	}
	if !strings.HasSuffix(body, "\n") {
		t.Errorf("synthesized SKILL.md must end with a newline:\n%q", body)
	}
}

// Spec: §7.8 — the emitter IDs map to the harness families: the shared Claude
// marketplace, Codex, and Cursor.
func TestMarketplaceEmitter_IDs(t *testing.T) {
	t.Parallel()
	want := map[MarketplaceEmitter]string{
		ClaudeMarketplace{}: "claude",
		CodexMarketplace{}:  "codex",
		CursorMarketplace{}: "cursor",
	}
	for e, id := range want {
		if e.ID() != id {
			t.Errorf("%T.ID() = %q, want %q", e, e.ID(), id)
		}
	}
}
