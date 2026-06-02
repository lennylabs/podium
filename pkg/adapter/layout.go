package adapter

import (
	"encoding/json"
	"path"
	"sort"
	"strings"

	"github.com/lennylabs/podium/pkg/manifest"
)

// This file holds the shared §6.7 materialization helpers used by the
// per-harness adapters: type routing to native locations, the harness-neutral
// context bucket, skill folders, rule injection into shared context files, the
// hook and mcp-server config-merge fragments, and the small format
// translations (Gemini command TOML, Codex agent TOML).

func sortFiles(out []File) {
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
}

func appendResources(out []File, dir string, res map[string][]byte) []File {
	for rel, data := range res {
		out = append(out, File{Path: path.Join(dir, rel), Content: data})
	}
	return out
}

// parsed returns the parsed artifact or an empty one on error, so callers can
// read fields without nil checks.
func parsed(src Source) *manifest.Artifact {
	art, err := manifest.ParseArtifact(src.ArtifactBytes)
	if err != nil || art == nil {
		return &manifest.Artifact{}
	}
	return art
}

// contextOut materializes a `type: context` artifact to the harness-neutral
// `.podium/context/<id>/` directory (§6.7), identical for every adapter.
func contextOut(src Source) []File {
	dir := path.Join(".podium", "context", src.ArtifactID)
	out := []File{}
	if len(src.ArtifactBytes) > 0 {
		out = append(out, File{Path: path.Join(dir, "ARTIFACT.md"), Content: src.ArtifactBytes})
	}
	out = appendResources(out, dir, src.Resources)
	sortFiles(out)
	return out
}

// skillOut materializes a skill folder at dir: SKILL.md plus the bundled
// scripts/, references/, and assets/ resources alongside it.
func skillOut(dir string, src Source) []File {
	out := []File{}
	if len(src.SkillBytes) > 0 {
		out = append(out, File{Path: path.Join(dir, "SKILL.md"), Content: src.SkillBytes})
	}
	out = appendResources(out, dir, src.Resources)
	sortFiles(out)
	return out
}

// singleFileOut writes content to primaryPath and any bundled resources to the
// harness-neutral `.podium/resources/<id>/` bucket (non-skill single-file types
// have no native home for resources).
func singleFileOut(primaryPath string, content []byte, src Source) []File {
	out := []File{{Path: primaryPath, Content: content}}
	out = appendResources(out, path.Join(".podium", "resources", src.ArtifactID), src.Resources)
	sortFiles(out)
	return out
}

// ruleBody returns the rule's prose body for injection, stripping the Podium
// frontmatter. It falls back to the full bytes on a parse error.
func ruleBody(src Source) []byte {
	art, err := manifest.ParseArtifact(src.ArtifactBytes)
	if err != nil || art == nil {
		return src.ArtifactBytes
	}
	return []byte(art.Body)
}

// injectRule returns an OpInject File that reconciles the rule's body into the
// shared context file (AGENTS.md / GEMINI.md) under a Podium-managed block.
func injectRule(file string, src Source) File {
	return File{Path: file, Op: OpInject, Key: src.ArtifactID, Content: ruleBody(src)}
}

// --- format translations -----------------------------------------------------

// geminiCommandTOML renders a command as Gemini CLI's TOML custom-command
// format: a one-line description and a prompt multiline string.
func geminiCommandTOML(src Source) []byte {
	art := parsed(src)
	var b strings.Builder
	if art.Description != "" {
		b.WriteString("description = " + tomlBasic(art.Description) + "\n")
	}
	b.WriteString("prompt = " + tomlMultiline(art.Body) + "\n")
	return []byte(b.String())
}

// codexAgentTOML renders an agent as Codex's TOML subagent format: name,
// description, and developer_instructions (the body).
func codexAgentTOML(src Source, name string) []byte {
	art := parsed(src)
	var b strings.Builder
	b.WriteString("name = " + tomlBasic(name) + "\n")
	if art.Description != "" {
		b.WriteString("description = " + tomlBasic(art.Description) + "\n")
	}
	b.WriteString("developer_instructions = " + tomlMultiline(art.Body) + "\n")
	return []byte(b.String())
}

func tomlBasic(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func tomlMultiline(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"""`, `\"\"\"`)
	return "\"\"\"\n" + s + "\n\"\"\""
}

// --- mcp-server config-merge -------------------------------------------------

// mcpServerConfig derives a launch config from a `server_identifier`. A `://`
// scheme is an HTTP endpoint; a `transport:rest` prefix (npx:@scope/pkg) is a
// command with one argument; a bare value is a command.
func mcpServerConfig(serverID string) map[string]any {
	if strings.Contains(serverID, "://") {
		return map[string]any{"url": serverID}
	}
	if i := strings.Index(serverID, ":"); i > 0 {
		return map[string]any{"command": serverID[:i], "args": []string{serverID[i+1:]}}
	}
	return map[string]any{"command": serverID}
}

func mcpName(src Source) string {
	if n := parsed(src).Name; n != "" {
		return n
	}
	return lastSeg(src.ArtifactID)
}

// mcpFragmentJSON builds the {"mcpServers": {...}} OpMergeJSON fragment. The
// server entry is tagged Podium-owned so the merge layer can reconcile it.
func mcpFragmentJSON(src Source) []byte {
	cfg := mcpServerConfig(parsed(src).ServerIdentifier)
	cfg[PodiumOwnedKey] = src.ArtifactID
	frag := map[string]any{"mcpServers": map[string]any{mcpName(src): cfg}}
	b, _ := json.Marshal(frag)
	return b
}

// opencodeMCPJSON builds the OpenCode `mcp` config entry, which uses a
// type/command-array shape distinct from the mcpServers object.
func opencodeMCPJSON(src Source) []byte {
	cfg := mcpServerConfig(parsed(src).ServerIdentifier)
	entry := map[string]any{"enabled": true, PodiumOwnedKey: src.ArtifactID}
	if url, ok := cfg["url"].(string); ok {
		entry["type"], entry["url"] = "remote", url
	} else {
		entry["type"] = "local"
		cmdline := []string{cfg["command"].(string)}
		if args, _ := cfg["args"].([]string); len(args) > 0 {
			cmdline = append(cmdline, args...)
		}
		entry["command"] = cmdline
	}
	frag := map[string]any{"mcp": map[string]any{mcpName(src): entry}}
	b, _ := json.Marshal(frag)
	return b
}

// codexMCPTOML renders an [mcp_servers.<name>] table for Codex's config.toml.
func codexMCPTOML(src Source) []byte {
	cfg := mcpServerConfig(parsed(src).ServerIdentifier)
	name := mcpName(src)
	var b strings.Builder
	b.WriteString("[mcp_servers." + name + "]\n")
	if url, ok := cfg["url"].(string); ok {
		b.WriteString("url = " + tomlBasic(url) + "\n")
	} else {
		b.WriteString("command = " + tomlBasic(cfg["command"].(string)) + "\n")
		if args, _ := cfg["args"].([]string); len(args) > 0 {
			b.WriteString("args = [" + tomlBasic(args[0]) + "]\n")
		}
	}
	return []byte(b.String())
}

// --- hook config-merge -------------------------------------------------------

// hookResourceBucket is the harness-neutral directory a hook's bundled scripts
// materialize into. A config-merge has no native home for the script, so it
// lands here and the merged command is rewritten to reference it (§6.7).
func hookResourceBucket(src Source) string {
	return path.Join(".podium", "resources", src.ArtifactID)
}

// hookActionFor returns the hook_action with each bundled-resource reference
// rewritten from its registry-relative path to the materialized
// .podium/resources/<id>/ path, so the command a config-merge installs resolves
// from the project root. Longer keys are rewritten first so a key that is a
// suffix of another does not corrupt the longer path.
func hookActionFor(src Source) string {
	action := parsed(src).HookAction
	if len(src.Resources) == 0 {
		return action
	}
	keys := make([]string, 0, len(src.Resources))
	for k := range src.Resources {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	bucket := hookResourceBucket(src)
	for _, k := range keys {
		action = strings.ReplaceAll(action, k, path.Join(bucket, k))
	}
	return action
}

// hookConfigOut assembles a config-merge hook's output: the OpMergeJSON
// fragment at configPath plus the bundled scripts in the .podium/resources/<id>/
// bucket. A nil fragment (the harness has no native event for this hook_event)
// produces no output, so the bundled scripts are not orphaned.
func hookConfigOut(configPath string, frag []byte, src Source) []File {
	if frag == nil {
		return nil
	}
	out := []File{{Path: configPath, Op: OpMergeJSON, Content: frag}}
	out = appendResources(out, hookResourceBucket(src), src.Resources)
	sortFiles(out)
	return out
}

// hookFragmentJSON builds the {"hooks": {"<nativeEvent>": [...]}} OpMergeJSON
// fragment. The §4.3.5 canonical event is translated to the harness-native
// event name by eventMap. Returns nil when the harness has no mapping for the
// event (the adapter then emits no hook output for it).
func hookFragmentJSON(eventMap map[string]string, src Source) []byte {
	art := parsed(src)
	native, ok := eventMap[art.HookEvent]
	if !ok || native == "" {
		return nil
	}
	handler := map[string]any{"type": "command", "command": hookActionFor(src)}
	entry := map[string]any{"hooks": []any{handler}, PodiumOwnedKey: src.ArtifactID}
	frag := map[string]any{"hooks": map[string]any{native: []any{entry}}}
	b, _ := json.Marshal(frag)
	return b
}

// claudeHookEvents maps the §4.3.5 canonical events to Claude Code's native
// hook event names. Specific subtypes fall back to the generic tool event when
// Claude Code has no finer event. spec: §6.7.1 (hook_event translation lives in
// the adapter).
var claudeHookEvents = map[string]string{
	"session_start":         "SessionStart",
	"session_end":           "SessionEnd",
	"user_prompt_submit":    "UserPromptSubmit",
	"pre_tool_use":          "PreToolUse",
	"post_tool_use":         "PostToolUse",
	"post_tool_use_failure": "PostToolUse",
	"pre_shell_execution":   "PreToolUse",
	"post_shell_execution":  "PostToolUse",
	"pre_mcp_execution":     "PreToolUse",
	"post_mcp_execution":    "PostToolUse",
	"pre_read_file":         "PreToolUse",
	"post_file_edit":        "PostToolUse",
	"permission_request":    "PreToolUse",
	"permission_denied":     "PreToolUse",
	"subagent_start":        "PreToolUse",
	"subagent_stop":         "SubagentStop",
	"stop":                  "Stop",
	"pre_compact":           "PreCompact",
	"post_compact":          "PreCompact",
	"notification":          "Notification",
}

// cursorHookEvents maps the canonical §4.3.5 events to Cursor's native hook
// events. Cursor exposes per-category subtype events rather than the generic
// tool events, so pre_tool_use/post_tool_use have no native target and the
// adapter emits no hook for them (partial coverage, graded ⚠ in §6.7.1).
var cursorHookEvents = map[string]string{
	"user_prompt_submit":  "beforeSubmitPrompt",
	"pre_shell_execution": "beforeShellExecution",
	"pre_mcp_execution":   "beforeMCPExecution",
	"pre_read_file":       "beforeReadFile",
	"post_file_edit":      "afterFileEdit",
	"stop":                "stop",
}

// cursorHookFragmentJSON builds the Cursor .cursor/hooks.json config-merge
// fragment. Cursor's schema is `{"version": 1, "hooks": {"<event>": [{"command":
// "..."}]}}`: a required top-level schema version plus a `hooks` object keyed by
// the native event, each holding an array of `{command}` entries (§6.7
// config-merge). Returns nil when Cursor has no native event for the canonical
// hook_event. The version scalar is idempotent across merges.
func cursorHookFragmentJSON(src Source) []byte {
	art := parsed(src)
	native, ok := cursorHookEvents[art.HookEvent]
	if !ok || native == "" {
		return nil
	}
	entry := map[string]any{"command": hookActionFor(src), PodiumOwnedKey: src.ArtifactID}
	frag := map[string]any{
		"version": 1,
		"hooks":   map[string]any{native: []any{entry}},
	}
	b, _ := json.Marshal(frag)
	return b
}

// codexHookEvents maps the canonical events to Codex's native hook events.
var codexHookEvents = map[string]string{
	"session_start":        "SessionStart",
	"user_prompt_submit":   "UserPromptSubmit",
	"pre_tool_use":         "PreToolUse",
	"post_tool_use":        "PostToolUse",
	"pre_shell_execution":  "PreToolUse",
	"post_shell_execution": "PostToolUse",
	"pre_mcp_execution":    "PreToolUse",
	"post_mcp_execution":   "PostToolUse",
	"permission_request":   "PermissionRequest",
	"subagent_start":       "SubagentStart",
	"subagent_stop":        "SubagentStop",
	"stop":                 "Stop",
	"pre_compact":          "PreCompact",
	"post_compact":         "PostCompact",
}

// geminiHookEvents maps the canonical events to Gemini CLI's native hook events.
var geminiHookEvents = map[string]string{
	"session_start":        "SessionStart",
	"session_end":          "SessionEnd",
	"subagent_start":       "BeforeAgent",
	"subagent_stop":        "AfterAgent",
	"pre_tool_use":         "BeforeTool",
	"post_tool_use":        "AfterTool",
	"pre_shell_execution":  "BeforeTool",
	"post_shell_execution": "AfterTool",
	"pre_mcp_execution":    "BeforeTool",
	"post_mcp_execution":   "AfterTool",
	"pre_compact":          "PreCompress",
	"notification":         "Notification",
}

// --- Claude Cowork plugin layout ---------------------------------------------

// coworkPlugin materializes an artifact into the Claude Cowork plugin layout:
// one plugin per artifact under plugins/<id>/, with the component in its native
// plugin location plus a plugin.json manifest. rule and context types ship as
// skills (the plugin format has no native component for them). Each plugin also
// contributes its entry to the repository-root .claude-plugin/marketplace.json
// via a config-merge fragment, so a `podium sync` writes a complete, reconciled
// marketplace listing without a separate aggregation pass.
func coworkPlugin(src Source) []File {
	id := src.ArtifactID
	pluginRoot := path.Join("plugins", id)
	name := lastSeg(id)
	out := []File{
		{Path: path.Join(pluginRoot, ".claude-plugin", "plugin.json"), Content: []byte(`{"name":"` + name + `"}` + "\n")},
		{Path: path.Join(".claude-plugin", "marketplace.json"), Op: OpMergeJSON, Content: coworkMarketplaceFragment(src)},
	}
	switch frontmatterType(src.ArtifactBytes) {
	case "skill", "rule":
		// A skill ships natively; a rule has no native plugin component, so it
		// ships as a skill (the §6.7.1 fallback). context is handled by the
		// caller via the harness-neutral .podium/context/ bucket.
		body := src.SkillBytes
		if len(body) == 0 {
			if frontmatterType(src.ArtifactBytes) == "rule" {
				// Synthesize a valid SKILL.md (name + description + the rule
				// body) so the fallback follows the skill format rather than
				// emitting a SKILL.md with raw `type: rule` frontmatter.
				body = coworkRuleSkillBody(src)
			} else {
				body = src.ArtifactBytes
			}
		}
		out = append(out, File{Path: path.Join(pluginRoot, "skills", name, "SKILL.md"), Content: body})
		out = appendResources(out, path.Join(pluginRoot, "skills", name), src.Resources)
	case "agent":
		out = append(out, File{Path: path.Join(pluginRoot, "agents", name+".md"), Content: src.ArtifactBytes})
	case "command":
		out = append(out, File{Path: path.Join(pluginRoot, "commands", name+".md"), Content: src.ArtifactBytes})
	case "hook":
		if frag := hookFragmentJSON(claudeHookEvents, src); frag != nil {
			out = append(out, File{Path: path.Join(pluginRoot, "hooks", "hooks.json"), Content: pretty(frag)})
		}
	case "mcp-server":
		out = append(out, File{Path: path.Join(pluginRoot, ".mcp.json"), Content: pretty(mcpFragmentJSON(src))})
	}
	sortFiles(out)
	return out
}

// coworkRuleSkillBody synthesizes a valid SKILL.md for a rule shipped as a
// Claude Cowork skill (the §6.7.1 fallback): the skill name is the artifact's
// leaf name, the description prefers the rule's own description and falls back
// to a generic line, and the body is the rule's prose. This keeps the fallback
// output in the SKILL.md format the harness expects instead of leaking the
// rule's `type:`/`rule_mode:` frontmatter.
func coworkRuleSkillBody(src Source) []byte {
	art := parsed(src)
	desc := art.RuleDescription
	if desc == "" {
		desc = art.Description
	}
	if desc == "" {
		desc = "Project rule shipped as a Claude Cowork skill."
	}
	var b strings.Builder
	b.WriteString("---\nname: " + lastSeg(src.ArtifactID) + "\ndescription: " + desc + "\n---\n\n")
	b.WriteString(art.Body)
	if !strings.HasSuffix(art.Body, "\n") {
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// coworkMarketplaceFragment builds the OpMergeJSON fragment that adds this
// plugin to the repository-root .claude-plugin/marketplace.json. The single
// plugin entry is tagged Podium-owned so a re-sync reconciles the listing
// (stale plugins drop out); the marketplace `name` scalar is idempotent across
// fragments. The plugin `source` is the project-relative plugin directory.
func coworkMarketplaceFragment(src Source) []byte {
	entry := map[string]any{
		"name":         lastSeg(src.ArtifactID),
		"source":       "./" + path.Join("plugins", src.ArtifactID),
		PodiumOwnedKey: src.ArtifactID,
	}
	frag := map[string]any{
		"name":    "podium",
		"plugins": []any{entry},
	}
	b, _ := json.Marshal(frag)
	return b
}

// pretty re-indents a compact JSON fragment for a standalone file.
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
