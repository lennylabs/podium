package adapter

import (
	"path"
	"sort"
	"strings"

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
func (c ClaudeCode) Adapt(src Source) ([]File, error) {
	ty := frontmatterType(src.ArtifactBytes)
	out := []File{}
	name := lastSegmentClaude(src.ArtifactID)

	switch ty {
	case "skill":
		skillRoot := path.Join(".claude", "skills", name)
		if len(src.SkillBytes) > 0 {
			// §4.4.2 — rewrite imported provenance blocks into
			// Claude Code <untrusted-data> regions so the host
			// can apply differential trust at read time.
			out = append(out, File{Path: path.Join(skillRoot, "SKILL.md"), Content: rewriteProvenanceForClaude(src.SkillBytes)})
		}
		for rel, data := range src.Resources {
			out = append(out, File{Path: path.Join(skillRoot, rel), Content: data})
		}
	case "rule":
		out = append(out, File{
			Path:    path.Join(".claude", "rules", name+".md"),
			Content: src.ArtifactBytes,
		})
	case "agent":
		out = append(out, File{
			Path:    path.Join(".claude", "agents", name+".md"),
			Content: src.ArtifactBytes,
		})
		for rel, data := range src.Resources {
			out = append(out, File{
				Path:    path.Join(".claude", "podium", src.ArtifactID, rel),
				Content: data,
			})
		}
	default:
		// context, command, hook, mcp-server, and extensions all land
		// under .claude/podium/<id>/ with the canonical layout.
		out = append(out, File{
			Path:    path.Join(".claude", "podium", src.ArtifactID, "ARTIFACT.md"),
			Content: src.ArtifactBytes,
		})
		for rel, data := range src.Resources {
			out = append(out, File{
				Path:    path.Join(".claude", "podium", src.ArtifactID, rel),
				Content: data,
			})
		}
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
