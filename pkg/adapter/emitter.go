package adapter

import (
	"context"
	"encoding/json"
	"path"
)

// This file holds the §7.8 marketplace emitters for the plugin-marketplace
// formats (Claude, Codex, Cursor). Each emitter renders an artifact's component
// files under a plugin subtree and contributes a per-plugin manifest entry,
// reusing the §6.7 layout helpers (skillOut, mcpFragmentJSON, hookFragmentJSON,
// cursorRuleBody) so the marketplace and project-files modes translate a type
// the same way.
//
// The plugin-marketplace formats share a structure: a root marketplace.json
// listing the plugins, a per-plugin manifest under the plugin subtree, and the
// artifact's components inside that subtree. They differ in the manifest paths,
// the per-plugin manifest filename, and the component set each harness reads.

// MarketplaceEmitter renders an artifact into a harness's git-repo marketplace
// layout (§7.8). A plugin bundles several artifacts, so rendering is split in
// two so the per-plugin manifest entry is contributed once per plugin rather
// than once per artifact:
//
//   - Component renders one artifact's component files into a plugin subtree.
//     The publishing pipeline calls it once per artifact in the plugin.
//   - Manifest renders the root marketplace entry and the per-plugin manifest
//     for one plugin. The pipeline calls it once per plugin.
//
// The split is required because the OpMergeJSON merge concatenates same-key
// arrays without deduplication within a render (deepMerge, pkg/materialize/
// merge.go), so a per-artifact manifest fragment would emit N duplicate plugin
// entries for an N-artifact plugin. Contributing the entry once per plugin from
// the PluginDescriptor yields exactly one entry, tagged with PodiumOwnedKey on
// the plugin name so a re-render reconciles the listing.
type MarketplaceEmitter interface {
	// ID returns the harness identifier the emitter publishes for (e.g.,
	// "claude" for the shared Claude marketplace, "codex", "cursor").
	ID() string

	// Component returns the artifact's component files under the plugin subtree
	// named by src.Plugin. The artifact's type selects the component location.
	// A type the harness has no marketplace component for returns no files.
	Component(ctx context.Context, src Source) ([]File, error)

	// Manifest returns the root marketplace entry and the per-plugin manifest
	// for plugin. marketplaceName is the output's operator-chosen identifier.
	// The marketplace entry is an OpMergeJSON fragment keyed by the plugin name
	// and tagged with PodiumOwnedKey on the plugin name.
	Manifest(marketplaceName string, plugin PluginDescriptor) ([]File, error)
}

// pluginSubtree is the directory a plugin's content lives under within a
// marketplace repository: <prefix>/<name>. The prefix is the harness subtree
// from the PluginDescriptor (e.g., "claude") and the name is the plugin name.
func pluginSubtree(p PluginDescriptor) string {
	return path.Join(p.Prefix, p.Name)
}

// pluginJSON renders a per-plugin manifest object: the plugin name and, when
// set, its description. The harnesses read a `{"name": ..., "description": ...}`
// plugin manifest; the optional description is omitted when empty so a strict
// schema does not see a null.
func pluginJSON(p PluginDescriptor) []byte {
	obj := map[string]any{"name": p.Name}
	if p.Description != "" {
		obj["description"] = p.Description
	}
	out, _ := json.MarshalIndent(obj, "", "  ")
	return append(out, '\n')
}

// marketplaceEntryFragment builds the OpMergeJSON fragment that adds one plugin
// to a root marketplace manifest. The marketplace `name` scalar is idempotent
// across fragments, and the single plugin entry is tagged Podium-owned on the
// plugin name so a re-render reconciles the listing (stale plugins drop out).
// The plugin `source` is the project-relative plugin subtree the manifest
// references. description carries the plugin description when set.
func marketplaceEntryFragment(marketplaceName string, p PluginDescriptor) []byte {
	entry := map[string]any{
		"name":         p.Name,
		"source":       "./" + pluginSubtree(p),
		PodiumOwnedKey: p.Name,
	}
	if p.Description != "" {
		entry["description"] = p.Description
	}
	frag := map[string]any{
		"name":    marketplaceName,
		"plugins": []any{entry},
	}
	b, _ := json.Marshal(frag)
	return b
}

// pretty re-indents a compact JSON fragment for a standalone file. The callers
// pass bytes produced by json.Marshal, so the unmarshal and re-marshal error
// arms below are defensive against a future malformed caller and are not
// exercised by a contrived test; pretty returns the input unchanged then.
func pretty(b []byte) []byte {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return b
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return b
	}
	return append(out, '\n')
}

// --- Claude marketplace emitter ----------------------------------------------

// ClaudeMarketplace is the shared marketplace emitter for Claude Code, Claude
// Desktop, and Claude Cowork (§7.8). The three surfaces read the same
// .claude-plugin/marketplace.json, so one emitter serves all three. It writes
// the root marketplace manifest and a per-plugin .claude-plugin/plugin.json,
// with skills/, agents/, commands/, hooks/, and .mcp.json components.
type ClaudeMarketplace struct{}

// ID returns "claude".
func (ClaudeMarketplace) ID() string { return "claude" }

// Component renders one artifact into the Claude plugin layout under the plugin
// subtree. A rule has no native plugin component and ships as a skill (the
// §6.7.1 fallback), synthesizing a SKILL.md from the rule body.
func (ClaudeMarketplace) Component(ctx context.Context, src Source) ([]File, error) {
	root := pluginSubtree(src.Plugin)
	switch frontmatterType(src.ArtifactBytes) {
	case "skill", "rule":
		return claudePluginSkill(root, src), nil
	case "agent":
		return []File{{Path: path.Join(root, "agents", lastSeg(src.ArtifactID)+".md"), Content: src.ArtifactBytes}}, nil
	case "command":
		return []File{{Path: path.Join(root, "commands", lastSeg(src.ArtifactID)+".md"), Content: src.ArtifactBytes}}, nil
	case "hook":
		if frag := hookFragmentJSON(claudeHookEvents, src); frag != nil {
			out := []File{{Path: path.Join(root, "hooks", "hooks.json"), Content: pretty(frag)}}
			out = appendResources(out, path.Join(root, "hooks"), src.Resources)
			sortFiles(out)
			return out, nil
		}
		return nil, nil
	case "mcp-server":
		return []File{{Path: path.Join(root, ".mcp.json"), Content: pretty(mcpFragmentJSON(src))}}, nil
	}
	return nil, nil
}

// Manifest renders the root .claude-plugin/marketplace.json entry and the
// per-plugin .claude-plugin/plugin.json for one plugin.
func (ClaudeMarketplace) Manifest(marketplaceName string, plugin PluginDescriptor) ([]File, error) {
	out := []File{
		{Path: path.Join(".claude-plugin", "marketplace.json"), Op: OpMergeJSON, Content: marketplaceEntryFragment(marketplaceName, plugin)},
		{Path: path.Join(pluginSubtree(plugin), ".claude-plugin", "plugin.json"), Content: pluginJSON(plugin)},
	}
	sortFiles(out)
	return out, nil
}

// claudePluginSkill renders a skill (or a rule shipped as a skill) into the
// plugin's skills/<name>/ folder. A rule with no SKILL.md synthesizes one from
// the rule body so the fallback follows the skill format instead of leaking the
// rule's `type:`/`rule_mode:` frontmatter.
func claudePluginSkill(root string, src Source) []File {
	name := lastSeg(src.ArtifactID)
	dir := path.Join(root, "skills", name)
	body := src.SkillBytes
	if len(body) == 0 {
		if frontmatterType(src.ArtifactBytes) == "rule" {
			body = ruleSkillBody(src)
		} else {
			body = src.ArtifactBytes
		}
	}
	out := []File{{Path: path.Join(dir, "SKILL.md"), Content: body}}
	out = appendResources(out, dir, src.Resources)
	sortFiles(out)
	return out
}

// ruleSkillBody synthesizes a valid SKILL.md for a rule shipped as a plugin
// skill (the §6.7.1 fallback): the skill name is the artifact's leaf name, the
// description prefers the rule's own description and falls back to a generic
// line, and the body is the rule's prose. This keeps the fallback output in the
// SKILL.md format the harness expects instead of leaking the rule frontmatter.
func ruleSkillBody(src Source) []byte {
	art := parsed(src)
	desc := art.RuleDescription
	if desc == "" {
		desc = art.Description
	}
	if desc == "" {
		desc = "Project rule shipped as a skill."
	}
	var b []byte
	b = append(b, "---\nname: "+lastSeg(src.ArtifactID)+"\ndescription: "+desc+"\n---\n\n"...)
	b = append(b, art.Body...)
	if len(art.Body) == 0 || art.Body[len(art.Body)-1] != '\n' {
		b = append(b, '\n')
	}
	return b
}

// --- Codex marketplace emitter -----------------------------------------------

// CodexMarketplace is the marketplace emitter for Codex (§7.8). It writes the
// root .agents/plugins/marketplace.json and a per-plugin .codex-plugin/
// plugin.json, with skills/, hooks/hooks.json, .app.json, and .mcp.json
// components, matching the §6.7 distribution table.
type CodexMarketplace struct{}

// ID returns "codex".
func (CodexMarketplace) ID() string { return "codex" }

// Component renders one artifact into the Codex plugin layout under the plugin
// subtree. A skill ships natively; a hook config-merges into hooks/hooks.json;
// an mcp-server writes .mcp.json. agent, command, and rule have no Codex
// marketplace component (skills are the Codex install unit), so they return no
// files.
func (CodexMarketplace) Component(ctx context.Context, src Source) ([]File, error) {
	root := pluginSubtree(src.Plugin)
	switch frontmatterType(src.ArtifactBytes) {
	case "skill":
		return skillOut(path.Join(root, "skills", lastSeg(src.ArtifactID)), src), nil
	case "hook":
		if frag := hookFragmentJSON(claudeHookEvents, src); frag != nil {
			out := []File{{Path: path.Join(root, "hooks", "hooks.json"), Content: pretty(frag)}}
			out = appendResources(out, path.Join(root, "hooks"), src.Resources)
			sortFiles(out)
			return out, nil
		}
		return nil, nil
	case "mcp-server":
		return []File{{Path: path.Join(root, ".mcp.json"), Content: pretty(mcpFragmentJSON(src))}}, nil
	}
	return nil, nil
}

// Manifest renders the root .agents/plugins/marketplace.json entry, the
// per-plugin .codex-plugin/plugin.json, and the plugin's .app.json. The Codex
// distribution table (§6.7) lists .app.json in the component set, so the
// per-plugin .app.json is written from the plugin descriptor once per plugin.
func (CodexMarketplace) Manifest(marketplaceName string, plugin PluginDescriptor) ([]File, error) {
	root := pluginSubtree(plugin)
	out := []File{
		{Path: path.Join(".agents", "plugins", "marketplace.json"), Op: OpMergeJSON, Content: marketplaceEntryFragment(marketplaceName, plugin)},
		{Path: path.Join(root, ".codex-plugin", "plugin.json"), Content: pluginJSON(plugin)},
		{Path: path.Join(root, ".app.json"), Content: pluginJSON(plugin)},
	}
	sortFiles(out)
	return out, nil
}

// --- Cursor marketplace emitter ----------------------------------------------

// CursorMarketplace is the marketplace emitter for Cursor (§7.8). It writes the
// root .cursor-plugin/marketplace.json and a per-plugin .cursor-plugin/
// plugin.json, with skills/, rules/*.mdc, and mcp.json components.
type CursorMarketplace struct{}

// ID returns "cursor".
func (CursorMarketplace) ID() string { return "cursor" }

// Component renders one artifact into the Cursor plugin layout under the plugin
// subtree. A skill ships natively; a rule writes rules/<name>.mdc with the
// rule_mode-derived frontmatter; an mcp-server writes mcp.json. agent, command,
// and hook have no Cursor marketplace component, so they return no files.
func (CursorMarketplace) Component(ctx context.Context, src Source) ([]File, error) {
	root := pluginSubtree(src.Plugin)
	name := lastSeg(src.ArtifactID)
	switch frontmatterType(src.ArtifactBytes) {
	case "skill":
		return skillOut(path.Join(root, "skills", name), src), nil
	case "rule":
		return []File{{Path: path.Join(root, "rules", name+".mdc"), Content: cursorRuleBody(src)}}, nil
	case "mcp-server":
		return []File{{Path: path.Join(root, "mcp.json"), Content: pretty(mcpFragmentJSON(src))}}, nil
	}
	return nil, nil
}

// Manifest renders the root .cursor-plugin/marketplace.json entry and the
// per-plugin .cursor-plugin/plugin.json for one plugin.
func (CursorMarketplace) Manifest(marketplaceName string, plugin PluginDescriptor) ([]File, error) {
	out := []File{
		{Path: path.Join(".cursor-plugin", "marketplace.json"), Op: OpMergeJSON, Content: marketplaceEntryFragment(marketplaceName, plugin)},
		{Path: path.Join(pluginSubtree(plugin), ".cursor-plugin", "plugin.json"), Content: pluginJSON(plugin)},
	}
	sortFiles(out)
	return out, nil
}

// compile-time guard: the plugin-marketplace emitters satisfy the interface.
var (
	_ MarketplaceEmitter = ClaudeMarketplace{}
	_ MarketplaceEmitter = CodexMarketplace{}
	_ MarketplaceEmitter = CursorMarketplace{}
)
