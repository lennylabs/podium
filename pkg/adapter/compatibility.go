package adapter

import (
	"strings"

	"github.com/lennylabs/podium/pkg/manifest"
)

// skillCompatibilityMaxChars caps the derived compatibility string per
// spec §4.3.4 ("≤ 500 chars; human-readable string").
const skillCompatibilityMaxChars = 500

// deriveSkillCompatibility implements spec §4.3.4: when a skill's SKILL.md
// omits compatibility, "the Podium adapter derives it from
// runtime_requirements and sandbox_profile at materialization time for
// harnesses that consume only the agentskills.io subset." Those harnesses read
// SKILL.md but not ARTIFACT.md, so the runtime constraints declared in
// ARTIFACT.md are otherwise invisible to them. The derived value is injected
// as the first SKILL.md frontmatter line. skillBytes is returned unchanged
// when the author already authored compatibility, when ARTIFACT.md carries
// nothing to derive from, or when either manifest fails to parse.
func deriveSkillCompatibility(skillBytes, artifactBytes []byte) []byte {
	if len(skillBytes) == 0 {
		return skillBytes
	}
	skill, err := manifest.ParseSkill(skillBytes)
	if err != nil || skill.Compatibility != "" {
		return skillBytes
	}
	art, err := manifest.ParseArtifact(artifactBytes)
	if err != nil {
		return skillBytes
	}
	compat := buildSkillCompatibility(art.RuntimeRequirements, art.SandboxProfile)
	if compat == "" {
		return skillBytes
	}
	return injectFrontmatterLine(skillBytes, "compatibility", compat)
}

// buildSkillCompatibility renders a human-readable compatibility string from
// the ARTIFACT.md runtime_requirements and sandbox_profile. Returns "" when
// neither field carries information to convey.
func buildSkillCompatibility(req *manifest.RuntimeRequirements, sandbox manifest.SandboxProfile) string {
	var clauses []string
	if req != nil {
		var parts []string
		if req.Python != "" {
			parts = append(parts, "Python "+req.Python)
		}
		if req.Node != "" {
			parts = append(parts, "Node "+req.Node)
		}
		if len(req.SystemPackages) > 0 {
			parts = append(parts, "system packages: "+strings.Join(req.SystemPackages, ", "))
		}
		if len(parts) > 0 {
			clauses = append(clauses, "Requires "+strings.Join(parts, ", "))
		}
	}
	if sandbox != "" {
		clauses = append(clauses, "sandbox: "+string(sandbox))
	}
	s := strings.Join(clauses, "; ")
	if len(s) > skillCompatibilityMaxChars {
		s = strings.TrimSpace(s[:skillCompatibilityMaxChars])
	}
	return s
}

// injectFrontmatterLine inserts `key: "value"` as the first line of src's YAML
// frontmatter, immediately after the opening "---" delimiter. The caller must
// have verified key is absent. value is double-quoted and escaped so a colon
// or other YAML metacharacter stays part of the scalar. src is returned
// unchanged when it has no recognizable leading frontmatter delimiter.
func injectFrontmatterLine(src []byte, key, value string) []byte {
	s := string(src)
	var delim, eol string
	switch {
	case strings.HasPrefix(s, "---\r\n"):
		delim, eol = "---\r\n", "\r\n"
	case strings.HasPrefix(s, "---\n"):
		delim, eol = "---\n", "\n"
	default:
		return src
	}
	line := key + ": " + yamlQuoteScalar(value) + eol
	return []byte(delim + line + s[len(delim):])
}

// yamlQuoteScalar double-quotes value and escapes the characters that would
// otherwise break a YAML double-quoted scalar.
func yamlQuoteScalar(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}
