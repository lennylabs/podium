package integration

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
)

// Spec: §4.3.4, §4.4, §7.3.1 — registry ingest blocks an artifact only on an
// error-severity lint diagnostic. A plain Agent Skill that declares no
// version (info), carries a top-level version (warning), links to a sibling
// skill through ../ (warning), and links to a section of a bundled file
// (resolved once the fragment is removed) is ingested, while a link to a
// missing bundled file still blocks its artifact.
func TestIngest_PublishedSkillPatternsAreIngested(t *testing.T) {
	t.Parallel()

	greet := "---\nname: greet\ndescription: Greet the user by name.\nversion: 1.0.0\n---\n\n" +
		"Read [the errors](references/api.md#errors), then see [farewell](../farewell/SKILL.md).\n\n" +
		"Write a link as `[text](missing.md)`.\n"
	farewell := "---\nname: farewell\ndescription: Say goodbye to the user.\n---\n\nSay goodbye.\n"
	broken := "---\nname: broken\ndescription: Link to a missing file.\n---\n\nSee [gone](references/gone.md#top).\n"
	files := fstest.MapFS{
		"ops/greet/SKILL.md":          &fstest.MapFile{Data: []byte(greet)},
		"ops/greet/references/api.md": &fstest.MapFile{Data: []byte("# API\n\n## Errors\n")},
		"ops/farewell/SKILL.md":       &fstest.MapFile{Data: []byte(farewell)},
		"ops/broken/SKILL.md":         &fstest.MapFile{Data: []byte(broken)},
	}

	ctx := context.Background()
	st := store.NewMemory()
	if err := st.CreateTenant(ctx, store.Tenant{ID: "t"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	res, err := ingest.Ingest(ctx, st, ingest.Request{
		TenantID: "t",
		LayerID:  "L",
		Files:    files,
		Linter:   lint.NewIngestLinter(true),
	})
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	if res.Accepted != 2 {
		t.Errorf("Accepted = %d, want 2 (ops/greet and ops/farewell)", res.Accepted)
	}
	brokenBlocked := false
	for _, d := range res.LintFailures {
		switch d.ArtifactID {
		case "ops/broken":
			if d.Code == "lint.prose_reference" && strings.Contains(d.Message, "references/gone.md#top") {
				brokenBlocked = true
			}
		default:
			t.Errorf("only ops/broken may fail lint, got %+v", d)
		}
	}
	if !brokenBlocked {
		t.Errorf("a link to a missing bundled file should block ops/broken, got LintFailures=%v", res.LintFailures)
	}
	rows, err := st.ListManifests(ctx, "t")
	if err != nil {
		t.Fatalf("ListManifests: %v", err)
	}
	stored := map[string]bool{}
	for _, r := range rows {
		stored[r.ArtifactID] = true
		// §4.3.4: Podium ignores a top-level SKILL.md version, so ops/greet's
		// authored `version: 1.0.0` must not become the stored version.
		if r.ArtifactID == "ops/greet" && r.Version != "0.0.0-auto.0" {
			t.Errorf("ops/greet stored at %q, want 0.0.0-auto.0 (the top-level version is ignored)", r.Version)
		}
	}
	for _, id := range []string{"ops/greet", "ops/farewell"} {
		if !stored[id] {
			t.Errorf("%s was not stored", id)
		}
	}
	if stored["ops/broken"] {
		t.Errorf("ops/broken was stored despite its lint error")
	}
}
