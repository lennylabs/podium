package adapter

import (
	"context"
	"path"
	"sort"

	"github.com/lennylabs/podium/pkg/manifest"
)

// None is the default HarnessAdapter (PODIUM_HARNESS=none), per §6.7. It
// writes the canonical layout as-is: ARTIFACT.md, SKILL.md (when present),
// and bundled resources land under <dest>/<artifact-id>/. A served
// ARTIFACT.md that is the §4.3.4 synthesized document is omitted, so a skill
// with no authored ARTIFACT.md materializes as a plain Agent Skills directory.
type None struct{}

// ID returns "none".
func (None) ID() string { return "none" }

// Adapt copies the canonical artifact layout, rooted at <artifact-id>/,
// into the destination. Files are returned in deterministic order
// (alphabetical by path) so golden-file comparisons remain stable across
// platforms.
func (None) Adapt(ctx context.Context, src Source) ([]File, error) {
	out := []File{}
	// Spec: §6.7, §4.3.4: the synthesized document carries the
	// registry-assigned version, so writing it would make the output differ
	// from the source package it reads back as. Byte equality also covers an
	// authored ARTIFACT.md identical to that document.
	if len(src.ArtifactBytes) > 0 && !manifest.IsSynthesizedSkillDocument(src.ArtifactBytes) {
		out = append(out, File{
			Path:    path.Join(src.ArtifactID, "ARTIFACT.md"),
			Content: src.ArtifactBytes,
		})
	}
	if len(src.SkillBytes) > 0 {
		// Spec: §6.6, §6.7: none writes SKILL.md without translation, so it
		// does not derive the §4.3.4 compatibility field. ARTIFACT.md, written
		// above, carries runtime_requirements and sandbox_profile directly.
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
