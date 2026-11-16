package core_test

import (
	"context"
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/core"
)

// plainSearchSkill is the SKILL.md of the plain skill name whose metadata
// carries version 1.0.0 and the extra metadata lines in their §4.3.4
// encodings.
func plainSearchSkill(name, extra string) []byte {
	return []byte("---\nname: " + name + "\ndescription: " + name + " skill\nmetadata:\n  version: \"1.0.0\"\n" +
		extra + "---\n\n" + name + " prose\n")
}

// resultIDs returns the IDs of res in result order.
func resultIDs(res *core.SearchResult) []string {
	ids := []string{}
	for _, d := range res.Results {
		ids = append(ids, d.ID)
	}
	return ids
}

// Spec: §4.7.4, §4.3.4 — a tag filter matches a plain skill by its SKILL.md
// metadata.tags. Its descriptor carries the resolved tags and sensitivity and
// the synthesized frontmatter of the version alone, and a plain skill whose
// metadata.deprecated is "true" is absent from default results.
func TestSearch_PlainSkillMetadataTagsFilter(t *testing.T) {
	t.Parallel()
	reg := setupRegistry(t, fstest.MapFS{
		"ops/notes/SKILL.md": &fstest.MapFile{Data: plainSearchSkill("notes",
			"  tags: \"release, notes\"\n  sensitivity: medium\n")},
		"ops/old/SKILL.md": &fstest.MapFile{Data: plainSearchSkill("old",
			"  tags: release\n  deprecated: \"true\"\n  replaced_by: ops/notes\n")},
	}, "L1")
	ctx := context.Background()

	res, err := reg.SearchArtifacts(ctx, publicID, core.SearchArtifactsOptions{Tags: []string{"notes"}})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	if got := resultIDs(res); !reflect.DeepEqual(got, []string{"ops/notes"}) {
		t.Fatalf("tag notes = %v, want [ops/notes]", got)
	}
	d := res.Results[0]
	if !reflect.DeepEqual(d.Tags, []string{"release", "notes"}) || d.Sensitivity != "medium" {
		t.Errorf("descriptor tags=%v sensitivity=%q, want the metadata [release notes] and medium", d.Tags, d.Sensitivity)
	}
	doc, err := manifest.SynthesizedSkillDocument("1.0.0")
	if err != nil {
		t.Fatalf("SynthesizedSkillDocument: %v", err)
	}
	fm, _, err := manifest.SplitFrontmatter(doc)
	if err != nil {
		t.Fatalf("SplitFrontmatter: %v", err)
	}
	if want := "---\n" + string(fm) + "\n---\n"; d.Frontmatter != want {
		t.Errorf("descriptor frontmatter = %q, want the synthesized block %q", d.Frontmatter, want)
	}

	res, err = reg.SearchArtifacts(ctx, publicID, core.SearchArtifactsOptions{Tags: []string{"release"}})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	if got := resultIDs(res); !reflect.DeepEqual(got, []string{"ops/notes"}) {
		t.Errorf("default tag release = %v, want [ops/notes] without the deprecated ops/old", got)
	}

	res, err = reg.SearchArtifacts(ctx, publicID, core.SearchArtifactsOptions{Tags: []string{"release"}, IncludeDeprecated: true})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	if got := len(res.Results); got != 2 {
		t.Errorf("tag release with deprecated = %v, want both skills, so the default omission is the deprecation filter", resultIDs(res))
	}
}
