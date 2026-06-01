package adapter

import (
	"context"
	"path"
	"sort"
	"strings"

	"github.com/lennylabs/podium/pkg/manifest"
	"gopkg.in/yaml.v3"
)

// ClaudeCode is the adapter for the Anthropic Claude Code CLI (§6.7).
// Outputs:
//
//   - .claude/agents/<name>.md          for type: agent
//   - .claude/skills/<name>/SKILL.md    for type: skill (agentskills.io layout)
//   - .claude/skills/<name>/<resource>  for skill bundled resources
//   - .claude/rules/<name>.md           for type: rule
//   - .claude/podium/<artifact-id>/...  for non-skill bundled resources
//
// Frontmatter mapping follows the §6.7.1 capability matrix.
type ClaudeCode struct{}

// ID returns "claude-code".
func (ClaudeCode) ID() string { return "claude-code" }

// Adapt translates src into the Claude Code layout. Outputs are sorted
// alphabetically for golden-file stability.
func (c ClaudeCode) Adapt(ctx context.Context, src Source) ([]File, error) {
	ty := frontmatterType(src.ArtifactBytes)
	out := []File{}
	name := lastSegmentClaude(src.ArtifactID)

	switch ty {
	case "skill":
		skillRoot := path.Join(".claude", "skills", name)
		if len(src.SkillBytes) > 0 {
			// Claude Code consumes only the agentskills.io subset
			// (SKILL.md, not ARTIFACT.md). §4.3.4 — derive
			// compatibility from runtime_requirements and
			// sandbox_profile when the author omits it so the
			// runtime constraints survive into SKILL.md.
			skill := deriveSkillCompatibility(src.SkillBytes, src.ArtifactBytes)
			// §4.4.2 — rewrite imported provenance blocks into
			// Claude Code <untrusted-data> regions so the host
			// can apply differential trust at read time.
			out = append(out, File{Path: path.Join(skillRoot, "SKILL.md"), Content: rewriteProvenanceForClaude(skill)})
		}
		for rel, data := range src.Resources {
			out = append(out, File{Path: path.Join(skillRoot, rel), Content: data})
		}
	// §4.4.2 — every type's materialized body has its imported
	// provenance blocks rewritten into Claude Code <untrusted-data>
	// regions, not just skills. context bodies in particular aggregate
	// external knowledge, so the prompt-injection defense must cover
	// them. rewriteProvenanceForClaude only touches imported blocks, so
	// passing the full ARTIFACT.md (frontmatter included) is a no-op when
	// none are present.
	case "rule":
		// A Claude Code rule file carries the rule prose under Claude-native
		// scoping frontmatter (paths for glob, description for auto). The
		// Podium-internal fields (type, version, rule_mode, rule_globs, ...) are
		// catalog metadata, not Claude Code rule fields, so they are dropped.
		// Provenance markers in the body are rewritten to <untrusted-data>
		// regions (§4.4.2).
		out = append(out, File{
			Path:    path.Join(".claude", "rules", name+".md"),
			Content: rewriteProvenanceForClaude(claudeRuleBody(src)),
		})
	case "agent":
		out = append(out, File{
			Path:    path.Join(".claude", "agents", name+".md"),
			Content: rewriteProvenanceForClaude(src.ArtifactBytes),
		})
		for rel, data := range src.Resources {
			out = append(out, File{
				Path:    path.Join(".claude", "podium", src.ArtifactID, rel),
				Content: data,
			})
		}
	case "command":
		out = append(out, File{
			Path:    path.Join(".claude", "commands", name+".md"),
			Content: rewriteProvenanceForClaude(src.ArtifactBytes),
		})
		out = appendResources(out, path.Join(".podium", "resources", src.ArtifactID), src.Resources)
	case "context":
		return contextOut(src), nil
	case "hook":
		// §6.7 — a hook config-merges its registration into the shared
		// settings.json; bundled scripts materialize to the harness-neutral
		// .podium/resources/<id>/ bucket and the merged command references them.
		return hookConfigOut(".claude/settings.json", hookFragmentJSON(claudeHookEvents, src), src), nil
	case "mcp-server":
		return []File{{Path: ".mcp.json", Op: OpMergeJSON, Content: mcpFragmentJSON(src)}}, nil
	default:
		// extension types land under .claude/podium/<id>/ with the canonical
		// layout.
		out = append(out, File{
			Path:    path.Join(".claude", "podium", src.ArtifactID, "ARTIFACT.md"),
			Content: rewriteProvenanceForClaude(src.ArtifactBytes),
		})
		out = appendResources(out, path.Join(".claude", "podium", src.ArtifactID), src.Resources)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// frontmatterType extracts the type field from the leading YAML
// frontmatter block of an ARTIFACT.md without paying for the full
// manifest parser. Returns "" when the frontmatter is missing.
func frontmatterType(src []byte) string {
	s := string(src)
	if !strings.HasPrefix(s, "---\n") && !strings.HasPrefix(s, "---\r\n") {
		return ""
	}
	end := strings.Index(s[3:], "\n---")
	if end < 0 {
		return ""
	}
	fm := s[3 : 3+end]
	var holder struct {
		Type string `yaml:"type"`
	}
	if err := yaml.Unmarshal([]byte(fm), &holder); err != nil {
		return ""
	}
	return holder.Type
}

func lastSegmentClaude(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// claudeRuleBody renders a Claude Code rule file: the rule prose under a
// Claude-native scoping frontmatter derived from rule_mode. A glob rule writes
// `paths: <rule_globs>`; an auto rule writes `description: <rule_description>`
// (falling back to the general description); always and explicit rules carry no
// scoping key. The Podium-internal frontmatter is dropped. On a parse error the
// raw ARTIFACT.md is returned so nothing is silently lost.
func claudeRuleBody(src Source) []byte {
	art, err := manifest.ParseArtifact(src.ArtifactBytes)
	if err != nil || art == nil {
		return src.ArtifactBytes
	}
	var fm strings.Builder
	switch art.RuleMode {
	case manifest.RuleModeGlob:
		if art.RuleGlobs != "" {
			fm.WriteString("paths: " + art.RuleGlobs + "\n")
		}
	case manifest.RuleModeAuto:
		desc := art.RuleDescription
		if desc == "" {
			desc = art.Description
		}
		if desc != "" {
			fm.WriteString("description: " + desc + "\n")
		}
	}
	var b strings.Builder
	if fm.Len() > 0 {
		b.WriteString("---\n")
		b.WriteString(fm.String())
		b.WriteString("---\n\n")
	}
	b.WriteString(art.Body)
	return []byte(b.String())
}
