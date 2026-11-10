package core

import (
	"fmt"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/store"
)

// plainSkillRow reports whether rec is a stored row of a skill with no
// ARTIFACT.md: its stored manifest is empty and its stored SKILL.md parses.
// It reads only the bytes admission recomputes the content hash over and
// never the Type column, so a metadata-store writer without the signing key
// cannot make a row satisfy the predicate or fail it. Ingest stores such a
// row exactly for a plain skill, because discovery leaves the manifest bytes
// empty only for a package whose SKILL.md parsed.
//
// Spec: §13.4, §4.3.4
func plainSkillRow(rec store.ManifestRecord) bool {
	_, ok := plainSkillOf(rec)
	return ok
}

// plainSkillOf returns the parsed SKILL.md of a plain-skill row, and false for
// any other row.
func plainSkillOf(rec store.ManifestRecord) (*manifest.Skill, bool) {
	if len(rec.Frontmatter) != 0 {
		return nil, false
	}
	s, err := manifest.ParseSkill(rec.SkillRaw)
	if err != nil {
		return nil, false
	}
	return s, true
}

// servedFrontmatter returns the ARTIFACT.md document the registry serves for
// rec: the synthesized document at the row's version for a plain-skill row,
// and the stored manifest otherwise. The synthesized document never enters
// the content hash and is never stored; the §4.7.10 delivery record frames it
// as the served document.
//
// Spec: §4.3.4, §4.7.10
func servedFrontmatter(rec store.ManifestRecord) ([]byte, error) {
	if !plainSkillRow(rec) {
		return rec.Frontmatter, nil
	}
	doc, err := manifest.SynthesizedSkillDocument(rec.Version)
	if err != nil {
		return nil, fmt.Errorf("serve %s@%s: synthesized ARTIFACT.md: %w", rec.ArtifactID, rec.Version, err)
	}
	return doc, nil
}
