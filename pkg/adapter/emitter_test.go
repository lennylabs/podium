package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Spec: §7.8 marketplace emitters. These tests pin each plugin-marketplace
// emitter's manifest path, per-plugin manifest path, the once-per-plugin entry
// count, the PodiumOwnedKey-on-plugin-name reconciliation, and the Codex
// component set the §6.7 distribution table (spec/06-mcp-server.md:219) and the
// proposal 0003 distribution table (line 36) enumerate (skills/,
// hooks/hooks.json, .app.json, .mcp.json), which are the authoritative
// component set. The §7.8 emitter prose and the proposal 0003 emitter bullet
// (line 166) omit .app.json; the prose and the distribution tables disagree,
// the emitter renders the full distribution-table set including .app.json, and
// the inconsistency is flagged for spec reconciliation.

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
	out, err := ClaudeMarketplace{}.Manifest(context.Background(), "acme-agents", finPlugin("claude"))
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
	out, err := CodexMarketplace{}.Manifest(context.Background(), "acme-agents", finPlugin("codex"))
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
	out, err := CursorMarketplace{}.Manifest(context.Background(), "acme-agents", finPlugin("cursor"))
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	mkt := fileByPath(t, out, ".cursor-plugin/marketplace.json")
	if mkt.Op != OpMergeJSON {
		t.Errorf("marketplace manifest must be OpMergeJSON, got %v", mkt.Op)
	}
	fileByPath(t, out, "cursor/finance-pack/.cursor-plugin/plugin.json")
}

// Spec: §6.7 — the Codex distribution table (spec/06-mcp-server.md:219) and the
// proposal 0003 distribution table (line 36) list .app.json as a Codex
// component, so the Codex emitter renders a per-plugin .app.json under the
// plugin subtree alongside the .codex-plugin/plugin.json and the .mcp.json. The
// §7.8 emitter prose and the proposal 0003 Codex bullet (line 166) omit
// .app.json, so the prose and the distribution tables disagree; the emitter
// follows the authoritative distribution tables and renders .app.json. This
// test pins the .app.json component and flags the prose/table inconsistency for
// spec reconciliation. The .app.json carries the same verified plugin name and
// description the plugin.json carries.
func TestCodexMarketplace_ManifestWritesAppJSON(t *testing.T) {
	t.Parallel()
	out, err := CodexMarketplace{}.Manifest(context.Background(), "acme-agents", finPlugin("codex"))
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	app := fileByPath(t, out, "codex/finance-pack/.app.json")
	var m map[string]any
	if err := json.Unmarshal(app.Content, &m); err != nil {
		t.Fatalf(".app.json is not valid JSON: %v\n%s", err, app.Content)
	}
	if m["name"] != "finance-pack" {
		t.Errorf(".app.json name = %v, want the plugin name finance-pack", m["name"])
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

// Spec: §6.7 — rendering a Codex plugin (its mcp-server Component plus its
// once-per-plugin Manifest) populates the plugin subtree with both the
// distribution table's .app.json and .mcp.json components, so the published
// Codex plugin subtree carries the full authoritative component set.
func TestCodexMarketplace_SubtreeHasAppAndMCP(t *testing.T) {
	t.Parallel()
	plugin := finPlugin("codex")
	mcpSrc := Source{
		ArtifactID:    "finance/pay-mcp",
		ArtifactBytes: []byte("---\ntype: mcp-server\nversion: 1.0.0\nserver_identifier: https://mcp.acme.com\n---\n\nbody\n"),
		Plugin:        plugin,
	}
	comp, err := CodexMarketplace{}.Component(context.Background(), mcpSrc)
	if err != nil {
		t.Fatalf("Component: %v", err)
	}
	man, err := CodexMarketplace{}.Manifest(context.Background(), "acme-agents", plugin)
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	subtree := append(append([]File{}, comp...), man...)
	fileByPath(t, subtree, "codex/finance-pack/.app.json")
	fileByPath(t, subtree, "codex/finance-pack/.mcp.json")
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
			out, err := tc.emitter.Manifest(context.Background(), "acme-agents", finPlugin(tc.prefix))
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
	out, err := emitter.Manifest(context.Background(), "acme-agents", plugin)
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
	out, err := ClaudeMarketplace{}.Manifest(context.Background(), "acme-agents", PluginDescriptor{Name: "p", Prefix: "claude"})
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
// hooks/hooks.json as an OpMergeJSON fragment, lands the bundled script in the
// harness-neutral .podium/resources/<id>/ bucket, and the merged command points
// at that same script path so the rendered hook resolves.
func TestClaudeMarketplace_HookComponent(t *testing.T) {
	t.Parallel()
	out, err := ClaudeMarketplace{}.Component(context.Background(), hookSource("claude"))
	if err != nil {
		t.Fatalf("Component: %v", err)
	}
	hooks := fileByPath(t, out, "claude/finance-pack/hooks/hooks.json")
	if hooks.Op != OpMergeJSON {
		t.Errorf("hooks.json must be an OpMergeJSON fragment so the merge layer reconciles it, got %v", hooks.Op)
	}
	if !strings.Contains(string(hooks.Content), "SessionStart") {
		t.Errorf("hooks.json must carry the native SessionStart event:\n%s", hooks.Content)
	}
	// The bundled script lands at the .podium/resources/<id>/ bucket, the same
	// path hookActionFor rewrites the command to, so the command and the
	// materialized script agree.
	script := fileByPath(t, out, ".podium/resources/finance/notify/scripts/notify.sh")
	assertHookCommandResolves(t, hooks.Content, script.Path)
}

// assertHookCommandResolves checks that the command embedded in a hooks.json
// merge fragment points at the path where the bundled script is materialized, so
// the published hook does not reference a file that the plugin never writes.
func assertHookCommandResolves(t *testing.T, hooksJSON []byte, scriptPath string) {
	t.Helper()
	if !strings.Contains(string(hooksJSON), scriptPath) {
		t.Errorf("hooks.json command must reference the materialized script path %q:\n%s", scriptPath, hooksJSON)
	}
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
	hooks := fileByPath(t, out, "codex/finance-pack/hooks/hooks.json")
	if hooks.Op != OpMergeJSON {
		t.Errorf("hooks.json must be an OpMergeJSON fragment, got %v", hooks.Op)
	}
	script := fileByPath(t, out, ".podium/resources/finance/notify/scripts/notify.sh")
	assertHookCommandResolves(t, hooks.Content, script.Path)
}

// Spec: §6.7.1, §7.8 — the Codex emitter translates a hook through the
// Codex-native event map, not the Claude map. A canonical event Codex has its
// own name for (permission_request -> PermissionRequest) must render to the
// Codex name, matching how the project-files Codex adapter translates it.
func TestCodexMarketplace_HookUsesCodexEventMap(t *testing.T) {
	t.Parallel()
	src := Source{
		ArtifactID:    "finance/perm",
		ArtifactBytes: []byte("---\ntype: hook\nversion: 1.0.0\nhook_event: permission_request\nhook_action: scripts/check.sh\n---\n\nbody\n"),
		Resources:     map[string][]byte{"scripts/check.sh": []byte("#!/bin/sh\nexit 0\n")},
		Plugin:        finPlugin("codex"),
	}
	out, err := CodexMarketplace{}.Component(context.Background(), src)
	if err != nil {
		t.Fatalf("Component: %v", err)
	}
	hooks := fileByPath(t, out, "codex/finance-pack/hooks/hooks.json")
	body := string(hooks.Content)
	if !strings.Contains(body, "PermissionRequest") {
		t.Errorf("Codex hook must use the Codex-native PermissionRequest event:\n%s", body)
	}
	if strings.Contains(body, "PreToolUse") {
		t.Errorf("Codex hook must not use the Claude PreToolUse mapping for permission_request:\n%s", body)
	}
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
// marketplace, Codex, Cursor, the Gemini extension, the Pi package, and the
// Hermes tap.
func TestMarketplaceEmitter_IDs(t *testing.T) {
	t.Parallel()
	want := map[MarketplaceEmitter]string{
		ClaudeMarketplace{}: "claude",
		CodexMarketplace{}:  "codex",
		CursorMarketplace{}: "cursor",
		GeminiExtension{}:   "gemini",
		PiPackage{}:         "pi",
		HermesTap{}:         "hermes",
	}
	for e, id := range want {
		if e.ID() != id {
			t.Errorf("%T.ID() = %q, want %q", e, e.ID(), id)
		}
	}
}

// Spec: §6.7, §7.8 — the Gemini emitter collapses the output's plugin set into
// one extension. The Manifest writes a single root gemini-extension.json naming
// the extension and its contextFileName, with no per-plugin subtree, regardless
// of which plugin the call carries.
func TestGeminiExtension_ManifestIsRootExtension(t *testing.T) {
	t.Parallel()
	out, err := GeminiExtension{}.Manifest(context.Background(), "acme-gemini", finPlugin("gemini"))
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	ext := fileByPath(t, out, "gemini-extension.json")
	if ext.Op != OpMergeJSON {
		t.Errorf("gemini-extension.json must be OpMergeJSON so the mcpServers merge does not clobber it, got %v", ext.Op)
	}
	var m map[string]any
	if err := json.Unmarshal(ext.Content, &m); err != nil {
		t.Fatalf("gemini-extension.json is not valid JSON: %v\n%s", err, ext.Content)
	}
	if m["name"] != "acme-gemini" {
		t.Errorf("extension name = %v, want the output name acme-gemini", m["name"])
	}
	if m["contextFileName"] != "GEMINI.md" {
		t.Errorf("extension contextFileName = %v, want GEMINI.md", m["contextFileName"])
	}
	// One extension per repository: no per-plugin subtree manifest.
	for _, f := range out {
		if strings.Contains(f.Path, "gemini/finance-pack") {
			t.Errorf("Gemini collapses to one extension; no per-plugin subtree expected, got %q", f.Path)
		}
	}
}

// Spec: §6.7, §7.8 — the Gemini extension manifest is identical for every plugin
// call, so collapsing several plugins into one extension yields one stable
// gemini-extension.json under the idempotent-scalar merge rather than a
// per-plugin variant.
func TestGeminiExtension_ManifestIdempotentAcrossPlugins(t *testing.T) {
	t.Parallel()
	a, err := GeminiExtension{}.Manifest(context.Background(), "acme-gemini", PluginDescriptor{Name: "house-rules", Prefix: "gemini"})
	if err != nil {
		t.Fatalf("Manifest(house-rules): %v", err)
	}
	b, err := GeminiExtension{}.Manifest(context.Background(), "acme-gemini", PluginDescriptor{Name: "finance-pack", Prefix: "gemini"})
	if err != nil {
		t.Fatalf("Manifest(finance-pack): %v", err)
	}
	if string(fileByPath(t, a, "gemini-extension.json").Content) != string(fileByPath(t, b, "gemini-extension.json").Content) {
		t.Errorf("Gemini extension manifest must be identical across plugins (one extension per repository)")
	}
}

// Spec: §6.7, §7.8 — the Gemini extension component set is commands, the context
// file, and mcpServers. A command writes a root commands/<name>.toml, a rule
// injects into the root context file, and an mcp-server merges into the
// extension manifest's mcpServers, all at the repository root with no plugin
// subtree.
func TestGeminiExtension_ComponentRouting(t *testing.T) {
	t.Parallel()
	plugin := finPlugin("gemini")
	cmd := Source{
		ArtifactID:    "finance/review",
		ArtifactBytes: []byte("---\ntype: command\nversion: 1.0.0\ndescription: Review.\n---\n\nDo the review.\n"),
		Plugin:        plugin,
	}
	out, err := GeminiExtension{}.Component(context.Background(), cmd)
	if err != nil {
		t.Fatalf("Component(command): %v", err)
	}
	toml := fileByPath(t, out, "commands/review.toml")
	if !strings.Contains(string(toml.Content), "prompt =") {
		t.Errorf("Gemini command .toml must carry a prompt:\n%s", toml.Content)
	}

	rule := Source{
		ArtifactID:    "finance/house-rule",
		ArtifactBytes: []byte("---\ntype: rule\nversion: 1.0.0\nrule_mode: always\ndescription: d\n---\n\nRule prose.\n"),
		Plugin:        plugin,
	}
	out, err = GeminiExtension{}.Component(context.Background(), rule)
	if err != nil {
		t.Fatalf("Component(rule): %v", err)
	}
	ctxFile := fileByPath(t, out, "GEMINI.md")
	if ctxFile.Op != OpInject {
		t.Errorf("Gemini rule must inject into the context file, got Op %v", ctxFile.Op)
	}

	mcp := Source{
		ArtifactID:    "finance/pay-mcp",
		ArtifactBytes: []byte("---\ntype: mcp-server\nversion: 1.0.0\nserver_identifier: https://mcp.acme.com\n---\n\nbody\n"),
		Plugin:        plugin,
	}
	out, err = GeminiExtension{}.Component(context.Background(), mcp)
	if err != nil {
		t.Fatalf("Component(mcp): %v", err)
	}
	ext := fileByPath(t, out, "gemini-extension.json")
	if ext.Op != OpMergeJSON || !strings.Contains(string(ext.Content), "mcpServers") {
		t.Errorf("Gemini mcp-server must merge mcpServers into gemini-extension.json:\n%s", ext.Content)
	}
}

// Spec: §6.7, §7.8 — a skill, agent, or hook has no Gemini extension component
// (the extension surfaces commands, the context file, and mcpServers), so the
// component returns no files.
func TestGeminiExtension_UnsupportedTypeEmitsNothing(t *testing.T) {
	t.Parallel()
	skill := Source{
		ArtifactID:    "finance/sk",
		SkillBytes:    []byte("---\nname: sk\ndescription: d\n---\n\nbody\n"),
		ArtifactBytes: []byte("---\ntype: skill\nversion: 1.0.0\n---\n\nbody\n"),
		Plugin:        finPlugin("gemini"),
	}
	out, err := GeminiExtension{}.Component(context.Background(), skill)
	if err != nil || len(out) != 0 {
		t.Errorf("Gemini skill component = %v, %v; want no files", paths(out), err)
	}
}

// Spec: §6.7, §7.8 — the Pi emitter writes a root package.json carrying the
// pi-package keyword and a pi.skills array pointing at the skills subtree.
func TestPiPackage_ManifestPackageJSON(t *testing.T) {
	t.Parallel()
	out, err := PiPackage{}.Manifest(context.Background(), "acme-pi", finPlugin("pi"))
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	pkg := fileByPath(t, out, "package.json")
	var m map[string]any
	if err := json.Unmarshal(pkg.Content, &m); err != nil {
		t.Fatalf("package.json is not valid JSON: %v\n%s", err, pkg.Content)
	}
	if m["name"] != "acme-pi" {
		t.Errorf("package.json name = %v, want the output name acme-pi", m["name"])
	}
	kw, ok := m["keywords"].([]any)
	if !ok || len(kw) != 1 || kw[0] != "pi-package" {
		t.Errorf("package.json keywords = %v, want the pi-package keyword", m["keywords"])
	}
	pi, ok := m["pi"].(map[string]any)
	if !ok {
		t.Fatalf("package.json has no pi object: %s", pkg.Content)
	}
	skills, ok := pi["skills"].([]any)
	if !ok || len(skills) != 1 || skills[0] != "skills" {
		t.Errorf("pi.skills = %v, want an array pointing at the skills subtree", pi["skills"])
	}
}

// Spec: §6.7, §7.8 — the Pi emitter writes skills/<name>/SKILL.md per skill in
// the skills subtree the pi.skills array points at, and the install unit is the
// skill, so a non-skill type produces no component.
func TestPiPackage_ComponentSkillLayout(t *testing.T) {
	t.Parallel()
	skill := Source{
		ArtifactID:    "finance/sk",
		SkillBytes:    []byte("---\nname: sk\ndescription: d\n---\n\nbody\n"),
		ArtifactBytes: []byte("---\ntype: skill\nversion: 1.0.0\n---\n\nbody\n"),
		Resources:     map[string][]byte{"references/notes.md": []byte("notes\n")},
		Plugin:        finPlugin("pi"),
	}
	out, err := PiPackage{}.Component(context.Background(), skill)
	if err != nil {
		t.Fatalf("Component(skill): %v", err)
	}
	fileByPath(t, out, "skills/sk/SKILL.md")
	fileByPath(t, out, "skills/sk/references/notes.md")

	rule := Source{
		ArtifactID:    "finance/rl",
		ArtifactBytes: []byte("---\ntype: rule\nversion: 1.0.0\n---\n\nbody\n"),
		Plugin:        finPlugin("pi"),
	}
	out, err = PiPackage{}.Component(context.Background(), rule)
	if err != nil || len(out) != 0 {
		t.Errorf("Pi rule component = %v, %v; want no files (skills are the install unit)", paths(out), err)
	}
}

// Spec: §6.7, §7.8 — the Hermes tap writes skills/<name>/SKILL.md per skill with
// its references/, scripts/, and assets/ resources, and has no root manifest.
func TestHermesTap_ComponentSkillLayout(t *testing.T) {
	t.Parallel()
	skill := Source{
		ArtifactID:    "finance/sk",
		SkillBytes:    []byte("---\nname: sk\ndescription: d\n---\n\nbody\n"),
		ArtifactBytes: []byte("---\ntype: skill\nversion: 1.0.0\n---\n\nbody\n"),
		Resources: map[string][]byte{
			"references/notes.md": []byte("notes\n"),
			"scripts/run.sh":      []byte("#!/bin/sh\n"),
			"assets/logo.png":     []byte("png"),
		},
		Plugin: finPlugin("hermes"),
	}
	out, err := HermesTap{}.Component(context.Background(), skill)
	if err != nil {
		t.Fatalf("Component(skill): %v", err)
	}
	fileByPath(t, out, "skills/sk/SKILL.md")
	fileByPath(t, out, "skills/sk/references/notes.md")
	fileByPath(t, out, "skills/sk/scripts/run.sh")
	fileByPath(t, out, "skills/sk/assets/logo.png")
}

// Spec: §6.7, §7.8 — a Hermes tap has no root manifest, so Manifest returns no
// files and the skills subtree reconciles through the sync lock file.
func TestHermesTap_ManifestEmpty(t *testing.T) {
	t.Parallel()
	out, err := HermesTap{}.Manifest(context.Background(), "acme-hermes", finPlugin("hermes"))
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("Hermes tap has no root manifest, got %v", paths(out))
	}
}

// Spec: §6.7, §7.8 — a non-skill type has no Hermes tap component (the install
// unit is the skill), so the component returns no files.
func TestHermesTap_UnsupportedTypeEmitsNothing(t *testing.T) {
	t.Parallel()
	rule := Source{
		ArtifactID:    "finance/rl",
		ArtifactBytes: []byte("---\ntype: rule\nversion: 1.0.0\n---\n\nbody\n"),
		Plugin:        finPlugin("hermes"),
	}
	out, err := HermesTap{}.Component(context.Background(), rule)
	if err != nil || len(out) != 0 {
		t.Errorf("Hermes rule component = %v, %v; want no files", paths(out), err)
	}
}

// Spec: §7.8 — the publish-target selector maps each harness ID to its emitter.
// The three Claude surfaces resolve to the one shared Claude marketplace, and
// codex, cursor, gemini, pi, and hermes each resolve to their own emitter.
func TestEmitterForHarness_Mapping(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"claude-code":    "claude",
		"claude-desktop": "claude",
		"claude-cowork":  "claude",
		"codex":          "codex",
		"cursor":         "cursor",
		"gemini":         "gemini",
		"pi":             "pi",
		"hermes":         "hermes",
	}
	for harness, wantID := range cases {
		harness, wantID := harness, wantID
		t.Run(harness, func(t *testing.T) {
			t.Parallel()
			e, err := EmitterForHarness(harness)
			if err != nil {
				t.Fatalf("EmitterForHarness(%q): %v", harness, err)
			}
			if e.ID() != wantID {
				t.Errorf("EmitterForHarness(%q).ID() = %q, want %q", harness, e.ID(), wantID)
			}
		})
	}
}

// Spec: §7.8 — OpenCode (npm only), none (raw output), and an unknown harness
// have no git-repo distribution, so they are not publish targets and the
// selector rejects them with ErrNotPublishTarget, the error config validation
// reports for a harness set naming an excluded harness.
func TestEmitterForHarness_RejectsNonPublishTargets(t *testing.T) {
	t.Parallel()
	for _, harness := range []string{"opencode", "none", "", "not-a-harness"} {
		harness := harness
		t.Run(harness, func(t *testing.T) {
			t.Parallel()
			e, err := EmitterForHarness(harness)
			if err == nil {
				t.Fatalf("EmitterForHarness(%q) = %v, nil; want an error", harness, e)
			}
			if !errors.Is(err, ErrNotPublishTarget) {
				t.Errorf("EmitterForHarness(%q) error = %v, want ErrNotPublishTarget", harness, err)
			}
			if e != nil {
				t.Errorf("EmitterForHarness(%q) emitter = %v, want nil on rejection", harness, e)
			}
		})
	}
}
