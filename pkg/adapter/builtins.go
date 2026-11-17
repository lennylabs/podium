package adapter

import (
	"context"
	"path"
	"strings"

	"github.com/lennylabs/podium/pkg/manifest"
)

// This file gathers the harness-specific built-in adapters. Each implements the
// §6.7 materialization matrix for its target harness; shared helpers live in
// layout.go. A type a harness has no project-level surface for returns no
// output (the §6.7.1 capability matrix grades it ✗ and the ingest lint and
// §6.9 guard act on that).

// ClaudeDesktop is the adapter for Anthropic Claude Desktop. Claude Desktop is
// a chat application whose only on-disk surface is the user/OS-scope MCP config
// (claude_desktop_config.json); it has no project-level materialization target,
// so the adapter emits nothing (§6.7).
type ClaudeDesktop struct{}

// ID returns "claude-desktop".
func (ClaudeDesktop) ID() string { return "claude-desktop" }

// Adapt produces no project-level output for Claude Desktop.
func (ClaudeDesktop) Adapt(ctx context.Context, src Source) ([]File, error) {
	return nil, nil
}

// ClaudeCowork is the adapter for Anthropic Claude Cowork. Cowork is a publish
// consumer of the Claude marketplace and has no project-scope materialization
// output, so the adapter value exists to select the shared Claude marketplace
// emitter (§7.8) and its Adapt emits nothing (§6.7).
type ClaudeCowork struct{}

// ID returns "claude-cowork".
func (ClaudeCowork) ID() string { return "claude-cowork" }

// Adapt produces no project-level output for any type. skill, agent, rule,
// hook, and mcp-server are ✗ cells in the §6.7.1 matrix for claude-cowork, so
// the §6.9 untranslatable guard fails them on both canonical-Adapt paths, and
// a cowork user obtains them by importing the published Claude marketplace.
//
// Spec: §6.7
func (ClaudeCowork) Adapt(ctx context.Context, src Source) ([]File, error) {
	return nil, nil
}

// Cursor is the adapter for Cursor IDE (§6.7).
type Cursor struct{}

// ID returns "cursor".
func (Cursor) ID() string { return "cursor" }

// Adapt routes each type to its Cursor-native location.
func (Cursor) Adapt(ctx context.Context, src Source) ([]File, error) {
	name := lastSeg(src.ArtifactID)
	switch frontmatterType(src.ArtifactBytes) {
	case "skill":
		return skillOut(path.Join(".cursor", "skills", name), src, controlFrontmatter), nil
	case "agent":
		return singleFileOut(path.Join(".cursor", "agents", name+".md"), src.ArtifactBytes, src), nil
	case "rule":
		return []File{{Path: path.Join(".cursor", "rules", name+".mdc"), Content: cursorRuleBody(src)}}, nil
	case "mcp-server":
		return []File{{Path: ".cursor/mcp.json", Op: OpMergeJSON, Content: mcpFragmentJSON(src)}}, nil
	case "hook":
		// §6.7 — config-merge into .cursor/hooks.json. Bundled scripts land in
		// the harness-neutral resource bucket (a config-merge has no native
		// home for them) and the merged command references them.
		return hookConfigOut(".cursor/hooks.json", cursorHookFragmentJSON(src), src), nil
	}
	return nil, nil
}

// cursorRuleBody emits the .mdc content with rule_mode-derived
// alwaysApply / globs / description frontmatter, per spec §6.7. Each canonical
// mode maps to the Cursor-native key that drives the same attach behavior:
//
//	always   -> alwaysApply: true
//	glob     -> globs: <rule_globs>
//	auto     -> description: <rule_description>
//	explicit -> (no auto-apply key; attaches on @-mention only)
func cursorRuleBody(src Source) []byte {
	art, err := manifest.ParseArtifact(src.ArtifactBytes)
	if err != nil {
		return cursorMDC("", src.ArtifactBytes)
	}
	// spec: 04-artifact-model.md §4.3 — rule_mode defaults to `always` when
	// unset ("rule_mode: always | glob | auto | explicit  # default: always"),
	// and an `always` rule loads when the session starts. Cursor needs an
	// explicit `alwaysApply: true` for that, so map an unset mode to always
	// rather than emitting empty frontmatter (which Cursor treats as a
	// non-always rule).
	mode := art.RuleMode
	if mode == "" {
		mode = manifest.RuleModeAlways
	}
	var fm strings.Builder
	switch mode {
	case manifest.RuleModeAlways:
		fm.WriteString("alwaysApply: true\n")
	case manifest.RuleModeGlob:
		if art.RuleGlobs != "" {
			// Quote the value: a glob like *.ts starts with `*`, which is a
			// YAML alias indicator and breaks the .mdc frontmatter parse if
			// emitted bare.
			fm.WriteString("globs: " + yamlDoubleQuote(art.RuleGlobs) + "\n")
		}
	case manifest.RuleModeAuto:
		if art.RuleDescription != "" {
			fm.WriteString("description: " + yamlDoubleQuote(art.RuleDescription) + "\n")
		}
	case manifest.RuleModeExplicit:
		// Explicit rules attach only when @-mentioned; no auto-apply key.
	}
	return cursorMDC(fm.String(), []byte(art.Body))
}

// cursorMDC assembles a Cursor .mdc file: a YAML frontmatter block holding the
// native keys (possibly empty), then the rule prose.
func cursorMDC(frontmatter string, body []byte) []byte {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString(frontmatter)
	b.WriteString("---\n\n")
	b.Write(body)
	return []byte(b.String())
}

// Gemini is the adapter for Google Gemini CLI (§6.7).
type Gemini struct{}

// ID returns "gemini".
func (Gemini) ID() string { return "gemini" }

// Adapt routes each type to its Gemini-native location. Gemini has no
// user-only skill control, so a user-invoked skill falls back to a TOML
// command file with its resources in .podium/resources/<id>/ (§6.7).
func (Gemini) Adapt(ctx context.Context, src Source) ([]File, error) {
	name := lastSeg(src.ArtifactID)
	switch frontmatterType(src.ArtifactBytes) {
	case "skill":
		if userInvoked(src) {
			return singleFileOut(path.Join(".gemini", "commands", name+".toml"), geminiSkillCommandTOML(src), src), nil
		}
		return skillOut(path.Join(".gemini", "skills", name), src, controlNone), nil
	case "agent":
		return singleFileOut(path.Join(".gemini", "agents", name+".md"), src.ArtifactBytes, src), nil
	case "rule":
		return []File{injectRule("GEMINI.md", src)}, nil
	case "hook":
		return hookConfigOut(".gemini/settings.json", hookFragmentJSON(geminiHooks, src), src), nil
	case "mcp-server":
		return []File{{Path: ".gemini/settings.json", Op: OpMergeJSON, Content: mcpFragmentJSON(src)}}, nil
	}
	return nil, nil
}

// OpenCode is the adapter for OpenCode. It uses plural component directories and
// injects rules into AGENTS.md (§6.7).
type OpenCode struct{}

// ID returns "opencode".
func (OpenCode) ID() string { return "opencode" }

// Adapt routes each type to its OpenCode-native location. OpenCode has no
// documented user-only skill control, so a user-invoked skill falls back to a
// command file holding the skill's SKILL.md, with compatibility derived as for
// every materialized SKILL.md, and its resources in .podium/resources/<id>/
// (§6.7).
func (OpenCode) Adapt(ctx context.Context, src Source) ([]File, error) {
	name := lastSeg(src.ArtifactID)
	switch frontmatterType(src.ArtifactBytes) {
	case "skill":
		if userInvoked(src) {
			return singleFileOut(path.Join(".opencode", "commands", name+".md"), skillMarkdown(src, controlNone), src), nil
		}
		return skillOut(path.Join(".opencode", "skills", name), src, controlNone), nil
	case "agent":
		return singleFileOut(path.Join(".opencode", "agents", name+".md"), src.ArtifactBytes, src), nil
	case "rule":
		return []File{injectRule("AGENTS.md", src)}, nil
	case "mcp-server":
		return []File{{Path: "opencode.json", Op: OpMergeJSON, Content: opencodeMCPJSON(src)}}, nil
	}
	// hook: OpenCode hooks are JS/TS plugin modules, not declarative config.
	return nil, nil
}

// Pi is the adapter for the Pi coding agent. Pi omits subagents, hooks, and
// MCP; rules inject into AGENTS.md, and a user-invoked skill carries
// disable-model-invocation: true (§6.7).
type Pi struct{}

// ID returns "pi".
func (Pi) ID() string { return "pi" }

// Adapt routes each supported type to its Pi-native location.
func (Pi) Adapt(ctx context.Context, src Source) ([]File, error) {
	name := lastSeg(src.ArtifactID)
	switch frontmatterType(src.ArtifactBytes) {
	case "skill":
		return skillOut(path.Join(".pi", "skills", name), src, controlFrontmatter), nil
	case "rule":
		return []File{injectRule("AGENTS.md", src)}, nil
	}
	// agent, hook, mcp-server: not supported by Pi.
	return nil, nil
}

// Hermes is the adapter for the Hermes Agent (Nous Research). Hermes natively
// reads .cursor/rules/*.mdc, AGENTS.md, and .cursorrules; its skill, hook, and
// MCP surfaces are user-scope, so they are out of project-level
// materialization (§6.7).
type Hermes struct{}

// ID returns "hermes".
func (Hermes) ID() string { return "hermes" }

// Adapt routes the one project-level type Hermes reads, rule.
func (Hermes) Adapt(ctx context.Context, src Source) ([]File, error) {
	if frontmatterType(src.ArtifactBytes) == "rule" {
		name := lastSeg(src.ArtifactID)
		return []File{{Path: path.Join(".cursor", "rules", name+".mdc"), Content: cursorRuleBody(src)}}, nil
	}
	// skill, agent, hook, mcp-server: user-scope (~/.hermes/), not
	// materialized at project level.
	return nil, nil
}

func lastSeg(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
