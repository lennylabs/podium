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

// ClaudeCowork is the adapter for Anthropic Claude Cowork. At project scope it
// materializes only a type: context artifact into the harness-neutral
// .podium/context/<id>/ bucket; the plugin and marketplace layout for the other
// types ships through marketplace publishing (§6.7, §7.8) rather than through
// the project-files materialization that podium sync and load_artifact run.
type ClaudeCowork struct{}

// ID returns "claude-cowork".
func (ClaudeCowork) ID() string { return "claude-cowork" }

// Adapt writes the harness-neutral context bucket for a type: context artifact
// (§6.7), identical to every other adapter. For skill, agent, command, rule,
// hook, and mcp-server it emits nothing: those types are ✗ cells in the §6.7.1
// matrix for claude-cowork, so the §6.9 untranslatable guard fails them on both
// canonical-Adapt paths, and a cowork user obtains them by importing the
// published Claude marketplace.
func (ClaudeCowork) Adapt(ctx context.Context, src Source) ([]File, error) {
	if frontmatterType(src.ArtifactBytes) == "context" {
		return contextOut(src), nil
	}
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
		return skillOut(path.Join(".cursor", "skills", name), src), nil
	case "agent":
		return singleFileOut(path.Join(".cursor", "agents", name+".md"), src.ArtifactBytes, src), nil
	case "command":
		return singleFileOut(path.Join(".cursor", "commands", name+".md"), src.ArtifactBytes, src), nil
	case "rule":
		return []File{{Path: path.Join(".cursor", "rules", name+".mdc"), Content: cursorRuleBody(src)}}, nil
	case "context":
		return contextOut(src), nil
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

// Adapt routes each type to its Gemini-native location.
func (Gemini) Adapt(ctx context.Context, src Source) ([]File, error) {
	name := lastSeg(src.ArtifactID)
	switch frontmatterType(src.ArtifactBytes) {
	case "skill":
		return skillOut(path.Join(".gemini", "skills", name), src), nil
	case "agent":
		return singleFileOut(path.Join(".gemini", "agents", name+".md"), src.ArtifactBytes, src), nil
	case "command":
		return singleFileOut(path.Join(".gemini", "commands", name+".toml"), geminiCommandTOML(src), src), nil
	case "rule":
		return []File{injectRule("GEMINI.md", src)}, nil
	case "context":
		return contextOut(src), nil
	case "hook":
		return hookConfigOut(".gemini/settings.json", hookFragmentJSON(geminiHookEvents, src), src), nil
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

// Adapt routes each type to its OpenCode-native location.
func (OpenCode) Adapt(ctx context.Context, src Source) ([]File, error) {
	name := lastSeg(src.ArtifactID)
	switch frontmatterType(src.ArtifactBytes) {
	case "skill":
		return skillOut(path.Join(".opencode", "skills", name), src), nil
	case "agent":
		return singleFileOut(path.Join(".opencode", "agents", name+".md"), src.ArtifactBytes, src), nil
	case "command":
		return singleFileOut(path.Join(".opencode", "commands", name+".md"), src.ArtifactBytes, src), nil
	case "rule":
		return []File{injectRule("AGENTS.md", src)}, nil
	case "context":
		return contextOut(src), nil
	case "mcp-server":
		return []File{{Path: "opencode.json", Op: OpMergeJSON, Content: opencodeMCPJSON(src)}}, nil
	}
	// hook: OpenCode hooks are JS/TS plugin modules, not declarative config.
	return nil, nil
}

// Pi is the adapter for the Pi coding agent. Pi omits subagents, hooks, and
// MCP; rules inject into AGENTS.md and commands are prompt templates (§6.7).
type Pi struct{}

// ID returns "pi".
func (Pi) ID() string { return "pi" }

// Adapt routes each supported type to its Pi-native location.
func (Pi) Adapt(ctx context.Context, src Source) ([]File, error) {
	name := lastSeg(src.ArtifactID)
	switch frontmatterType(src.ArtifactBytes) {
	case "skill":
		return skillOut(path.Join(".pi", "skills", name), src), nil
	case "command":
		return singleFileOut(path.Join(".pi", "prompts", name+".md"), src.ArtifactBytes, src), nil
	case "rule":
		return []File{injectRule("AGENTS.md", src)}, nil
	case "context":
		return contextOut(src), nil
	}
	// agent, hook, mcp-server: not supported by Pi.
	return nil, nil
}

// Hermes is the adapter for the Hermes Agent (Nous Research). Hermes natively
// reads .cursor/rules/*.mdc, AGENTS.md, and .cursorrules; its skill, command,
// hook, and MCP surfaces are user-scope, so they are out of project-level
// materialization (§6.7).
type Hermes struct{}

// ID returns "hermes".
func (Hermes) ID() string { return "hermes" }

// Adapt routes the project-level types Hermes reads.
func (Hermes) Adapt(ctx context.Context, src Source) ([]File, error) {
	name := lastSeg(src.ArtifactID)
	switch frontmatterType(src.ArtifactBytes) {
	case "rule":
		return []File{{Path: path.Join(".cursor", "rules", name+".mdc"), Content: cursorRuleBody(src)}}, nil
	case "context":
		return contextOut(src), nil
	}
	// skill, agent, command, hook, mcp-server: user-scope (~/.hermes/), not
	// materialized at project level.
	return nil, nil
}

func lastSeg(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
