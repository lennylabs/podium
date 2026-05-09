package adapter

import (
	"path"
	"sort"
)

// None is the default HarnessAdapter (PODIUM_HARNESS=none), per §6.7. It
// writes the canonical layout as-is: ARTIFACT.md, SKILL.md (when present),
// and bundled resources land under <dest>/<artifact-id>/.
type None struct{}

// ID returns "none".
func (None) ID() string { return "none" }

// Adapt copies the canonical artifact layout, rooted at <artifact-id>/,
// into the destination. Files are returned in deterministic order
// (alphabetical by path) so golden-file comparisons remain stable across
// platforms.
func (None) Adapt(src Source) ([]File, error) {
	out := []File{}
	if len(src.ArtifactBytes) > 0 {
		out = append(out, File{
			Path:    path.Join(src.ArtifactID, "ARTIFACT.md"),
			Content: src.ArtifactBytes,
		})
	}
	if len(src.SkillBytes) > 0 {
		out = append(out, File{
			Path:    path.Join(src.ArtifactID, "SKILL.md"),
			Content: src.SkillBytes,
		})
	}
	for rel, content := range src.Resources {
		out = append(out, File{
			Path:    path.Join(src.ArtifactID, rel),
			Content: content,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}
