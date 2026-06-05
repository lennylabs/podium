package core_test

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §4.3.4 / §11 — load_artifact surfaces a skill's verbatim SKILL.md so a
// server-source consumer materializes the authored file byte-for-byte. The
// authored SKILL.md frontmatter is distinct from ARTIFACT.md and cannot be
// reconstructed from the manifest frontmatter plus body, so it round-trips
// from the store through LoadArtifactResult.SkillRaw unchanged.
func TestLoadArtifact_SurfacesVerbatimSkillRaw(t *testing.T) {
	t.Parallel()
	reg, st := newRegistryWithStore(t)

	const skillMD = "---\nname: lint\ndescription: Run the project linter.\ncompatibility: \"py>=3.10\"\n---\n\nRun the linter.\n"
	if err := st.PutManifest(context.Background(), store.ManifestRecord{
		TenantID:    "t",
		ArtifactID:  "eng/lint",
		Version:     "1.0.0",
		ContentHash: "sha256:abc",
		Type:        "skill",
		Layer:       "L",
		Frontmatter: []byte("---\ntype: skill\nversion: 1.0.0\n---\n"),
		Body:        []byte("Run the linter.\n"),
		SkillRaw:    []byte(skillMD),
	}); err != nil {
		t.Fatalf("PutManifest: %v", err)
	}

	got, err := reg.LoadArtifact(context.Background(), publicID, "eng/lint", core.LoadArtifactOptions{})
	if err != nil {
		t.Fatalf("LoadArtifact: %v", err)
	}
	if string(got.SkillRaw) != skillMD {
		t.Errorf("SkillRaw = %q, want verbatim SKILL.md %q", got.SkillRaw, skillMD)
	}
	// The verbatim SKILL.md must differ from the reconstruction the old path
	// produced (ARTIFACT.md frontmatter + body), proving the fix matters.
	if string(got.SkillRaw) == string(got.Frontmatter)+got.ManifestBody {
		t.Errorf("SkillRaw equals frontmatter+body; the authored SKILL.md frontmatter was lost")
	}
}
