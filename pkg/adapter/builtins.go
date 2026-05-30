package adapter

import (
	"context"
	"path"
	"sort"
	"strings"

	"github.com/lennylabs/podium/pkg/manifest"
)

// This file gathers the harness-specific built-in adapters whose
// placement logic is small enough to live alongside each other. Each
// adapter implements the §6.7 table for its target harness.
//
// Phase 13 adapters land here: claude-desktop, claude-cowork, cursor,
// gemini, opencode, pi, hermes. The full §6.7.1 capability matrix
// (frontmatter mapping, rule_mode handling, hook_event translation)
// is enforced incrementally through dedicated tests.

// ClaudeDesktop is the adapter for Anthropic Claude Desktop.
// Outputs a Claude Desktop extension layout: a manifest.json derived
// from canonical frontmatter, plus bundled resources alongside.
type ClaudeDesktop struct{}

// ID returns "claude-desktop".
func (ClaudeDesktop) ID() string { return "claude-desktop" }

// Adapt writes the canonical artifact under .claude-desktop/<id>/.
func (ClaudeDesktop) Adapt(ctx context.Context, src Source) ([]File, error) {
	return placeUnder(".claude-desktop/extensions", src), nil
}

// ClaudeCowork is the adapter for Anthropic Claude Cowork.
// Outputs a Claude Cowork plugin layout (marketplace.json plus
// per-plugin folders containing skills, commands, agents, hooks, and
// MCP server registrations).
type ClaudeCowork struct{}

// ID returns "claude-cowork".
func (ClaudeCowork) ID() string { return "claude-cowork" }

// Adapt writes the canonical artifact under .claude-cowork/plugins/<id>/.
func (ClaudeCowork) Adapt(ctx context.Context, src Source) ([]File, error) {
	return placeUnder(".claude-cowork/plugins", src), nil
}

// Cursor is the adapter for Cursor IDE.
// type: rule outputs go to .cursor/rules/<name>.mdc per the §6.7
// table; other types land under .cursor/extensions/<id>/.
type Cursor struct{}

// ID returns "cursor".
func (Cursor) ID() string { return "cursor" }

// Adapt translates per type.
func (c Cursor) Adapt(ctx context.Context, src Source) ([]File, error) {
	ty := frontmatterType(src.ArtifactBytes)
	if ty == "rule" {
		name := lastSeg(src.ArtifactID)
		return []File{{
			Path:    path.Join(".cursor", "rules", name+".mdc"),
			Content: cursorRuleBody(src),
		}}, nil
	}
	return placeUnder(".cursor/extensions", src), nil
}

// cursorRuleBody emits the .mdc content with rule_mode-derived
// alwaysApply / globs / description frontmatter, per spec §6.7 ("writes
// `.cursor/rules/<name>.mdc` with `alwaysApply` / `globs` / `description`
// per `rule_mode`"). Each canonical mode maps to the Cursor-native key
// that drives the same attach behavior:
//
//	always   -> alwaysApply: true
//	glob     -> globs: <rule_globs>
//	auto     -> description: <rule_description>
//	explicit -> (no auto-apply key; attaches on @-mention only)
//
// The rule prose follows the frontmatter. A parse quirk falls back to the
// canonical bytes so the adapter never emits an empty .mdc.
func cursorRuleBody(src Source) []byte {
	art, err := manifest.ParseArtifact(src.ArtifactBytes)
	if err != nil {
		return cursorMDC("", src.ArtifactBytes)
	}
	var fm strings.Builder
	switch art.RuleMode {
	case manifest.RuleModeAlways:
		fm.WriteString("alwaysApply: true\n")
	case manifest.RuleModeGlob:
		if art.RuleGlobs != "" {
			fm.WriteString("globs: " + art.RuleGlobs + "\n")
		}
	case manifest.RuleModeAuto:
		if art.RuleDescription != "" {
			fm.WriteString("description: " + art.RuleDescription + "\n")
		}
	case manifest.RuleModeExplicit:
		// Explicit rules attach only when @-mentioned; no auto-apply key.
	}
	return cursorMDC(fm.String(), []byte(art.Body))
}

// cursorMDC assembles a Cursor .mdc file: a YAML frontmatter block holding
// the native keys (possibly empty), then the rule prose.
func cursorMDC(frontmatter string, body []byte) []byte {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString(frontmatter)
	b.WriteString("---\n\n")
	b.Write(body)
	return []byte(b.String())
}

// Gemini is the adapter for Google Gemini CLI.
type Gemini struct{}

// ID returns "gemini".
func (Gemini) ID() string { return "gemini" }

// Adapt writes the canonical artifact under .gemini/extensions/<id>/.
func (Gemini) Adapt(ctx context.Context, src Source) ([]File, error) {
	return placeUnder(".gemini/extensions", src), nil
}

// OpenCode is the adapter for OpenCode.
// type: rule injects into AGENTS.md between markers; other types
// land under .opencode/packages/<id>/.
type OpenCode struct{}

// ID returns "opencode".
func (OpenCode) ID() string { return "opencode" }

// Adapt translates per type.
func (OpenCode) Adapt(ctx context.Context, src Source) ([]File, error) {
	ty := frontmatterType(src.ArtifactBytes)
	if ty == "rule" {
		name := lastSeg(src.ArtifactID)
		return []File{{
			Path:    path.Join(".opencode", "rules", name+".md"),
			Content: src.ArtifactBytes,
		}}, nil
	}
	return placeUnder(".opencode/packages", src), nil
}

// Pi is the adapter for the Pi coding agent.
// type: rule lands at .pi/rules/<name>.md (explicit-mode); others
// under .pi/packages/<id>/.
type Pi struct{}

// ID returns "pi".
func (Pi) ID() string { return "pi" }

// Adapt translates per type.
func (Pi) Adapt(ctx context.Context, src Source) ([]File, error) {
	ty := frontmatterType(src.ArtifactBytes)
	if ty == "rule" {
		name := lastSeg(src.ArtifactID)
		return []File{{
			Path:    path.Join(".pi", "rules", name+".md"),
			Content: src.ArtifactBytes,
		}}, nil
	}
	return placeUnder(".pi/packages", src), nil
}

// Hermes is the adapter for the Hermes Agent (Nous Research).
// Per §6.7, type: rule writes .claude/rules/<name>.md (Hermes natively
// reads .cursor/rules too, but the adapter prefers Claude's path).
type Hermes struct{}

// ID returns "hermes".
func (Hermes) ID() string { return "hermes" }

// Adapt translates per type.
func (Hermes) Adapt(ctx context.Context, src Source) ([]File, error) {
	ty := frontmatterType(src.ArtifactBytes)
	if ty == "rule" {
		name := lastSeg(src.ArtifactID)
		return []File{{
			Path:    path.Join(".claude", "rules", name+".md"),
			Content: src.ArtifactBytes,
		}}, nil
	}
	return placeUnder(".hermes/packages", src), nil
}

// placeUnder writes ARTIFACT.md, optional SKILL.md, and every bundled
// resource under <root>/<artifact-id>/, sorted for deterministic
// golden-file output.
func placeUnder(root string, src Source) []File {
	out := []File{}
	if len(src.ArtifactBytes) > 0 {
		out = append(out, File{
			Path:    path.Join(root, src.ArtifactID, "ARTIFACT.md"),
			Content: src.ArtifactBytes,
		})
	}
	if len(src.SkillBytes) > 0 {
		out = append(out, File{
			Path:    path.Join(root, src.ArtifactID, "SKILL.md"),
			Content: src.SkillBytes,
		})
	}
	for rel, data := range src.Resources {
		out = append(out, File{
			Path:    path.Join(root, src.ArtifactID, rel),
			Content: data,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func lastSeg(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
