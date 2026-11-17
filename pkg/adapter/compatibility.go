package adapter

import (
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/podium/pkg/manifest"
)

// skillCompatibilityMaxChars caps the derived compatibility string per
// spec §4.3.4 ("≤ 500 chars; human-readable string").
const skillCompatibilityMaxChars = 500

// deriveSkillCompatibility implements spec §4.3.4: when a skill's SKILL.md
// omits compatibility, every harness adapter that writes a skill's SKILL.md
// (§6.7) and every marketplace emitter that writes one (§7.8) derives it from
// runtime_requirements and sandbox_profile at materialization time. The none
// adapter writes SKILL.md without translation and derives nothing. A harness
// reads SKILL.md but not ARTIFACT.md, so the runtime constraints declared in
// ARTIFACT.md are otherwise invisible to it. The derived value is injected
// as the first SKILL.md frontmatter line, at the indentation of the top-level
// keys, so an indented block mapping still decodes. skillBytes is returned unchanged
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
	return injectFrontmatterLine(skillBytes, frontmatterIndent(skillBytes), "compatibility", yamlQuoteScalar(compat))
}

// frontmatterIndent returns the indentation of the top-level keys of src's
// frontmatter when it is a block mapping that blockKeyIndent accepts, and ""
// otherwise, which keeps the column-1 injection for every other layout.
func frontmatterIndent(src []byte) string {
	fm, _, err := manifest.SplitFrontmatter(src)
	if err != nil {
		return ""
	}
	var doc yaml.Node
	if yaml.Unmarshal(fm, &doc) != nil || len(doc.Content) == 0 {
		return ""
	}
	indent, _ := blockKeyIndent(strings.SplitAfter(string(src), "\n"), doc.Content[0])
	return indent
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

// injectFrontmatterLine inserts `key: scalar`, prefixed with indent, as the
// first line of src's YAML frontmatter, immediately after the opening "---"
// delimiter, and leaves every other line byte-identical. indent is the
// indentation of the frontmatter's top-level keys, so the line joins their
// mapping. The caller must
// have verified key is absent and passes scalar already rendered: the derived
// compatibility string goes through yamlQuoteScalar so a colon stays part of
// the string, and the user-only control passes the bare boolean `true`, which
// a quoted "true" would turn into a string a harness can reject. src is
// returned unchanged when it has no recognizable leading frontmatter delimiter.
//
// Spec: §4.3.4, §6.7
func injectFrontmatterLine(src []byte, indent, key, scalar string) []byte {
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
	line := indent + key + ": " + scalar + eol
	return []byte(delim + line + s[len(delim):])
}

// yamlQuoteScalar double-quotes value and escapes the characters that would
// otherwise break a YAML double-quoted scalar.
func yamlQuoteScalar(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}
