package ingest_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/ingest"
)

// upUnparseableSkill returns a SKILL.md named name whose description is a
// mapping, a kind §4.3.4 does not allow, so it does not parse.
func upUnparseableSkill(name string) string {
	return "---\nname: " + name + "\ndescription:\n  text: A mapping.\n---\n\nSkill body of " + name + ".\n"
}

// erAssertUnparseable fails unless res rejects id under the §4.1
// unparseable-manifest rule with exactly the manifest_unparseable constraint
// in details and a single-line reason that names file and the parse error.
func erAssertUnparseable(t *testing.T, res *ingest.Result, id, file string, parseErr error) {
	t.Helper()
	rj := erRejection(t, res, id)
	want := map[string]any{"constraint": layer.ManifestUnparseableConstraint}
	if rj.Code != layer.InvalidArtifactCode || !reflect.DeepEqual(rj.Details, want) {
		t.Errorf("rejection of %s = %+v, want %s and details %v", id, rj, layer.InvalidArtifactCode, want)
	}
	if wantReason := layer.UnparseableReason(file, parseErr); rj.Reason != wantReason || strings.Contains(rj.Reason, "\n") {
		t.Errorf("rejection of %s reason = %q, want %q", id, rj.Reason, wantReason)
	}
	if strings.Contains(rj.Reason, "team") {
		t.Errorf("rejection of %s reason %q names the layer", id, rj.Reason)
	}
}

// Spec: §4.1, §7.3.1 — an artifact whose ARTIFACT.md or SKILL.md frontmatter
// does not parse is refused on its own with ingest.invalid_artifact and
// exactly the manifest_unparseable constraint, is not stored, and leaves the
// valid sibling accepted. The rule runs before the §4.6 no-inheritance rule
// and the §4.1 removed-type rule, so a skill ARTIFACT.md that declares
// extends: beside an unparseable SKILL.md reports manifest_unparseable. A
// refused artifact is withheld from the catalog lint resolves prose
// references against, so a link to it fails lint.prose_reference while a
// link to the kept sibling resolves, and the refusal is never counted as a
// lint failure.
func TestIngest_UnparseableManifestRefusedPerArtifact(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	res := erIngest(t, st, "team", map[string]string{
		"ops/nested/SKILL.md":     upUnparseableSkill("nested"),
		"ops/badyaml/ARTIFACT.md": "---\ntype: agent\nweird: [unclosed\n---\n\nbody\n",
		"ops/nofm/ARTIFACT.md":    "no frontmatter here\n",
		"ops/ordered/ARTIFACT.md": erDoc("skill", "1.0.0", "extends: ops/sibling\n", ""),
		"ops/ordered/SKILL.md":    upUnparseableSkill("ordered"),
		"ops/sibling/ARTIFACT.md": erDoc("agent", "1.0.0", "", "Sibling body."),
		"ops/linker/SKILL.md": "---\nname: linker\ndescription: Fixture skill linker.\n---\n\n" +
			"See [the nested skill](ops/nested) and [the sibling](ops/sibling).\n",
	})
	if res.Accepted != 1 || len(res.Rejected) != 4 {
		t.Fatalf("result = %+v, want the sibling accepted and four refusals", res)
	}
	_, skillErr := manifest.ParseSkill([]byte(upUnparseableSkill("nested")))
	_, yamlErr := manifest.ParseArtifact([]byte("---\ntype: agent\nweird: [unclosed\n---\n\nbody\n"))
	if skillErr == nil || yamlErr == nil {
		t.Fatalf("fixtures parse: SKILL.md %v, ARTIFACT.md %v", skillErr, yamlErr)
	}
	erAssertUnparseable(t, res, "ops/nested", "ops/nested/SKILL.md", skillErr)
	erAssertUnparseable(t, res, "ops/badyaml", "ops/badyaml/ARTIFACT.md", yamlErr)
	erAssertUnparseable(t, res, "ops/nofm", "ops/nofm/ARTIFACT.md", manifest.ErrNoFrontmatter)
	erAssertUnparseable(t, res, "ops/ordered", "ops/ordered/SKILL.md", skillErr)
	for _, id := range []string{"ops/nested", "ops/badyaml", "ops/nofm", "ops/ordered"} {
		erAssertNotIngested(t, res, id)
		erAssertNotStored(t, st, id, "1.0.0")
	}
	if len(res.LintFailures) != 1 {
		t.Fatalf("LintFailures = %+v, want the one prose reference", res.LintFailures)
	}
	d := res.LintFailures[0]
	if d.ArtifactID != "ops/linker" || d.Code != "lint.prose_reference" ||
		!strings.Contains(d.Message, "ops/nested") || strings.Contains(d.Message, "ops/sibling") {
		t.Errorf("diagnostic = %+v, want lint.prose_reference on ops/linker naming ops/nested alone", d)
	}
}

// Spec: §4.1, §4.2 — the per-artifact refusal covers a manifest that does not
// parse and nothing else. A root-level SKILL.md has no canonical ID, so the
// directory cannot hold an artifact at all, and it still fails the call with
// ErrInvalidArtifact even beside an unparseable manifest the walk would
// otherwise refuse on its own.
func TestIngest_StructuralFailureStillFailsTheLayer(t *testing.T) {
	t.Parallel()
	_, err := ingest.Ingest(context.Background(), newStore(t), ingest.Request{
		TenantID: erTenant, LayerID: "team", Files: erFS(map[string]string{
			"SKILL.md":            erSkill("root", ""),
			"ops/nested/SKILL.md": upUnparseableSkill("nested"),
		}),
	})
	if !errors.Is(err, ingest.ErrInvalidArtifact) || !strings.Contains(err.Error(), "subdirectory") {
		t.Fatalf("Ingest = %v, want the root-level manifest to fail the call with ErrInvalidArtifact", err)
	}
}

// Spec: §4.1, §4.3.4 — a SKILL.md whose metadata nests a mapping or a sequence
// under a key Podium does not read parses, so ingest accepts and stores the
// skill with its authored bytes. A sequence under tags is ignored, so the
// stored row carries no tag. It fails against an ingest that refuses the
// nested value as manifest_unparseable.
func TestIngest_NestedMetadataAccepted(t *testing.T) {
	t.Parallel()
	st := newStore(t)
	skillMD := erSkill("nested", "metadata:\n  version: \"1.0.0\"\n  requires:\n    bins: [\"lark-cli\"]\n  tags: [a, b]\n")
	res := erIngest(t, st, "team", map[string]string{"ops/nested/SKILL.md": skillMD})
	if res.Accepted != 1 || len(res.Rejected) != 0 || len(res.LintFailures) != 0 {
		t.Fatalf("result = %+v, want the nested-metadata skill accepted", res)
	}
	rec, err := st.GetManifest(context.Background(), erTenant, "ops/nested", "1.0.0")
	if err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	if string(rec.SkillRaw) != skillMD {
		t.Errorf("stored SKILL.md = %q, want the authored bytes %q", rec.SkillRaw, skillMD)
	}
	if len(rec.Tags) != 0 {
		t.Errorf("stored tags = %q, want none from a nested tags value", rec.Tags)
	}
}
