package adapter

import (
	"path"
	"sort"
	"strings"
)

// Codex is the adapter for OpenAI Codex (§6.7). Stage 3 places skills
// alongside agents under a Codex-native package layout. The full
// frontmatter mapping plus the AGENTS.md injection for type: rule lands
// in Phase 13 per the §6.7.1 capability matrix.
type Codex struct{}

// ID returns "codex".
func (Codex) ID() string { return "codex" }

// Adapt produces a Codex-flavored layout. Stage 3 ships placement only;
// frontmatter rewriting and AGENTS.md generation come in Phase 13.
func (c Codex) Adapt(src Source) ([]File, error) {
	ty := frontmatterType(src.ArtifactBytes)
	out := []File{}
	name := lastSegmentCodex(src.ArtifactID)
	root := path.Join(".codex", "packages", src.ArtifactID)

	if len(src.ArtifactBytes) > 0 {
		out = append(out, File{Path: path.Join(root, "ARTIFACT.md"), Content: src.ArtifactBytes})
	}
	if ty == "skill" && len(src.SkillBytes) > 0 {
		out = append(out, File{Path: path.Join(root, "SKILL.md"), Content: src.SkillBytes})
	}
	for rel, data := range src.Resources {
		out = append(out, File{Path: path.Join(root, rel), Content: data})
	}
	if ty == "rule" {
		// Rules also land at .codex/rules/<name>.md so Codex's native
		// rule loader picks them up directly.
		out = append(out, File{
			Path:    path.Join(".codex", "rules", name+".md"),
			Content: src.ArtifactBytes,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func lastSegmentCodex(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
