package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	Manifest(ctx context.Context, marketplaceName string, plugin PluginDescriptor) ([]File, error)
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
		return hookConfigOut(path.Join(root, "hooks", "hooks.json"), hookFragmentJSON(claudeHookEvents, src), src), nil
	case "mcp-server":
		return []File{{Path: path.Join(root, ".mcp.json"), Op: OpMergeJSON, Content: mcpFragmentJSON(src)}}, nil
	}
	return nil, nil
}

// Manifest renders the root .claude-plugin/marketplace.json entry and the
// per-plugin .claude-plugin/plugin.json for one plugin.
func (ClaudeMarketplace) Manifest(ctx context.Context, marketplaceName string, plugin PluginDescriptor) ([]File, error) {
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
// components, matching the §6.7 distribution table (spec/06-mcp-server.md:219)
// and the proposal distribution table (proposal 0003 line 36), which are the
// authoritative component set for the Codex marketplace layout.
//
// The two normative distribution tables agree that the Codex component set
// includes .app.json. The §7.8 "Marketplace emitters" prose
// (spec/07-external-integration.md:789) and the proposal's Codex emitter bullet
// (proposal 0003 line 166) omit .app.json, so the prose and the distribution
// tables disagree. This emitter renders the full distribution-table set,
// including .app.json, and the .app.json/§7.8-prose inconsistency is flagged
// for spec reconciliation; the spec is read-only in this phase. The .app.json
// schema is unverified against the Codex marketplace format, which is part of
// that open reconciliation item; the emitter writes a minimal manifest carrying
// the plugin name and, when set, its description, the same fields the verified
// per-plugin plugin.json carries.
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
		return hookConfigOut(path.Join(root, "hooks", "hooks.json"), hookFragmentJSON(codexHookEvents, src), src), nil
	case "mcp-server":
		return []File{{Path: path.Join(root, ".mcp.json"), Op: OpMergeJSON, Content: mcpFragmentJSON(src)}}, nil
	}
	return nil, nil
}

// Manifest renders the root .agents/plugins/marketplace.json entry and the
// per-plugin .codex-plugin/plugin.json and .app.json for one plugin. The
// distribution table (§6.7, proposal line 36) lists .app.json as a Codex
// component; it is per-plugin like plugin.json, so it is rendered here once per
// plugin rather than once per artifact. It carries the verified plugin name and
// description; its full schema is part of the §6.7-vs-§7.8 reconciliation item.
func (CodexMarketplace) Manifest(ctx context.Context, marketplaceName string, plugin PluginDescriptor) ([]File, error) {
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
		return []File{{Path: path.Join(root, "mcp.json"), Op: OpMergeJSON, Content: mcpFragmentJSON(src)}}, nil
	}
	return nil, nil
}

// Manifest renders the root .cursor-plugin/marketplace.json entry and the
// per-plugin .cursor-plugin/plugin.json for one plugin.
func (CursorMarketplace) Manifest(ctx context.Context, marketplaceName string, plugin PluginDescriptor) ([]File, error) {
	out := []File{
		{Path: path.Join(".cursor-plugin", "marketplace.json"), Op: OpMergeJSON, Content: marketplaceEntryFragment(marketplaceName, plugin)},
		{Path: path.Join(pluginSubtree(plugin), ".cursor-plugin", "plugin.json"), Content: pluginJSON(plugin)},
	}
	sortFiles(out)
	return out, nil
}

// --- Gemini extension emitter ------------------------------------------------

// geminiContextFile is the context file a Gemini extension loads. The extension
// manifest names it through contextFileName, and a rule injects into it.
const geminiContextFile = "GEMINI.md"

// GeminiExtension is the marketplace emitter for the Gemini extension format
// (§7.8). A Gemini repository holds one extension, so the emitter collapses the
// output's plugin set into one extension: it writes a single root
// gemini-extension.json, root commands/*.toml, the root context file, and merges
// mcp-server entries into the manifest's mcpServers, with no per-plugin subtree.
// The plugin descriptor groups the selection but does not split the repository.
type GeminiExtension struct{}

// ID returns "gemini".
func (GeminiExtension) ID() string { return "gemini" }

// Component renders one artifact into the root extension layout, ignoring the
// plugin subtree because a Gemini repository holds one extension. A command
// writes commands/<name>.toml; a rule injects into the context file; an
// mcp-server merges into the manifest's mcpServers. The extension component set
// is commands, the context file, and mcpServers, so skill, agent, and hook have
// no Gemini extension component and return no files.
func (GeminiExtension) Component(ctx context.Context, src Source) ([]File, error) {
	name := lastSeg(src.ArtifactID)
	switch frontmatterType(src.ArtifactBytes) {
	case "command":
		return []File{{Path: path.Join("commands", name+".toml"), Content: geminiCommandTOML(src)}}, nil
	case "rule":
		return []File{injectRule(geminiContextFile, src)}, nil
	case "mcp-server":
		return []File{{Path: "gemini-extension.json", Op: OpMergeJSON, Content: mcpFragmentJSON(src)}}, nil
	}
	return nil, nil
}

// Manifest writes the root gemini-extension.json. The plugin set collapses into
// one extension, so the manifest carries the output's operator-chosen name once
// and is identical for every plugin call (the merge replaces the idempotent
// scalars). The manifest is an OpMergeJSON fragment so an mcp-server Component
// merging mcpServers into the same file does not clobber the scalars.
func (GeminiExtension) Manifest(ctx context.Context, marketplaceName string, plugin PluginDescriptor) ([]File, error) {
	frag := map[string]any{
		"name":            marketplaceName,
		"contextFileName": geminiContextFile,
	}
	b, _ := json.Marshal(frag)
	return []File{{Path: "gemini-extension.json", Op: OpMergeJSON, Content: b}}, nil
}

// --- Pi package emitter ------------------------------------------------------

// piSkillsDir is the subtree a Pi package's skills live under. The root
// package.json pi.skills array points at it, and each skill renders to
// <piSkillsDir>/<name>/SKILL.md.
const piSkillsDir = "skills"

// PiPackage is the marketplace emitter for the Pi git-package format (§7.8). It
// writes a root package.json carrying the pi-package keyword and a pi.skills
// array pointing at the skills subtree, with skills/<name>/SKILL.md per skill.
// The install unit is the individual skill, so the plugin descriptor groups
// skills into the subtree without changing the install unit, and the package
// manifest is one per output rather than one per plugin.
type PiPackage struct{}

// ID returns "pi".
func (PiPackage) ID() string { return "pi" }

// Component renders one skill into the package's skills subtree. The install
// unit is the skill, so only type: skill contributes a component; the other
// types have no Pi package component and return no files.
func (PiPackage) Component(ctx context.Context, src Source) ([]File, error) {
	if frontmatterType(src.ArtifactBytes) == "skill" {
		return skillOut(path.Join(piSkillsDir, lastSeg(src.ArtifactID)), src), nil
	}
	return nil, nil
}

// Manifest writes the root package.json. The package is one per output, so the
// manifest is identical for every plugin call: it carries the output's
// operator-chosen name, the pi-package keyword, and a pi.skills array pointing
// at the skills subtree. The skills subtree carries no merged manifest and
// reconciles through the sync lock file, so the package.json is a plain write of
// stable, idempotent content rather than a per-skill merge fragment.
func (PiPackage) Manifest(ctx context.Context, marketplaceName string, plugin PluginDescriptor) ([]File, error) {
	pkg := map[string]any{
		"name":     marketplaceName,
		"keywords": []any{"pi-package"},
		"pi":       map[string]any{"skills": []any{piSkillsDir}},
	}
	// json.MarshalIndent cannot fail on this static map of strings and arrays,
	// matching the other manifest emitters that ignore the marshal error.
	b, _ := json.MarshalIndent(pkg, "", "  ")
	return []File{{Path: "package.json", Content: append(b, '\n')}}, nil
}

// --- Hermes tap emitter ------------------------------------------------------

// hermesSkillsDir is the root directory a Hermes tap discovers skills under.
const hermesSkillsDir = "skills"

// HermesTap is the marketplace emitter for the Hermes skills-tap format (§7.8).
// A tap has no root manifest; the harness discovers skills under the root
// skills/ directory. The emitter writes skills/<name>/SKILL.md per skill with
// its references/, scripts/, and assets/ resources, and contributes no manifest.
// The install unit is the individual skill, so the plugin descriptor groups
// skills without changing the install unit, and the tap reconciles through the
// sync lock file.
type HermesTap struct{}

// ID returns "hermes".
func (HermesTap) ID() string { return "hermes" }

// Component renders one skill into the tap's skills directory with its bundled
// resources. The install unit is the skill, so only type: skill contributes a
// component; the other types have no Hermes tap component and return no files.
func (HermesTap) Component(ctx context.Context, src Source) ([]File, error) {
	if frontmatterType(src.ArtifactBytes) == "skill" {
		return skillOut(path.Join(hermesSkillsDir, lastSeg(src.ArtifactID)), src), nil
	}
	return nil, nil
}

// Manifest returns no files: a Hermes tap has no root manifest, and the skills
// subtree reconciles through the sync lock file.
func (HermesTap) Manifest(ctx context.Context, marketplaceName string, plugin PluginDescriptor) ([]File, error) {
	return nil, nil
}

// --- publish-target selector -------------------------------------------------

// marketplaceEmitters maps each publish-target harness ID to its emitter. The
// three Claude surfaces share one emitter, so claude-code, claude-desktop, and
// claude-cowork all resolve to the shared Claude marketplace (§7.8). OpenCode
// (npm only) and none (raw canonical output) are not publish targets, so they
// are absent and EmitterForHarness rejects them.
var marketplaceEmitters = map[string]MarketplaceEmitter{
	"claude-code":    ClaudeMarketplace{},
	"claude-desktop": ClaudeMarketplace{},
	"claude-cowork":  ClaudeMarketplace{},
	"codex":          CodexMarketplace{},
	"cursor":         CursorMarketplace{},
	"gemini":         GeminiExtension{},
	"pi":             PiPackage{},
	"hermes":         HermesTap{},
}

// EmitterForHarness returns the marketplace emitter for a publish-target harness
// ID (§7.8). The three Claude surfaces resolve to the one shared Claude
// marketplace, so a harness set naming more than one of them yields one Claude
// marketplace rather than a collision. A harness without a git-repo distribution
// (opencode, none) or an unknown ID is not a publish target and returns an
// error, so a publish output whose harness set names an excluded harness is
// rejected at config validation.
func EmitterForHarness(harnessID string) (MarketplaceEmitter, error) {
	e, ok := marketplaceEmitters[harnessID]
	if !ok {
		return nil, fmt.Errorf("adapter: harness %q is not a publish target (no git-repo distribution): %w", harnessID, ErrNotPublishTarget)
	}
	return e, nil
}

// ErrNotPublishTarget reports that a harness has no git-repo distribution, so it
// cannot be a marketplace publish target (§7.8). EmitterForHarness wraps it for
// opencode, none, and any unknown harness ID.
var ErrNotPublishTarget = errors.New("harness is not a publish target")

// compile-time guard: the marketplace emitters satisfy the interface.
var (
	_ MarketplaceEmitter = ClaudeMarketplace{}
	_ MarketplaceEmitter = CodexMarketplace{}
	_ MarketplaceEmitter = CursorMarketplace{}
	_ MarketplaceEmitter = GeminiExtension{}
	_ MarketplaceEmitter = PiPackage{}
	_ MarketplaceEmitter = HermesTap{}
)
