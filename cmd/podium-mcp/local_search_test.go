package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// Spec: §6.4.1 — local BM25 over overlay records returns the
// best match for a query that matches description / tags /
// id-segment text.
func TestLocalSearch_DescriptionMatch(t *testing.T) {
	t.Parallel()
	records := []filesystem.ArtifactRecord{
		{
			ID: "team/finance/variance-analysis",
			Artifact: &manifest.Artifact{
				Type:        manifest.TypeSkill,
				Name:        "variance-analysis",
				Description: "compute variance against last quarter",
				Version:     "1.0.0",
			},
		},
		{
			ID: "team/ops/restart-runner",
			Artifact: &manifest.Artifact{
				Type:        manifest.TypeCommand,
				Name:        "restart-runner",
				Description: "restart the build runner",
				Version:     "1.0.0",
			},
		},
	}
	got := localSearch(records, "variance", "", "", nil, 10)
	if len(got) == 0 {
		t.Fatalf("no results for 'variance'")
	}
	if got[0].ID != "team/finance/variance-analysis" {
		t.Errorf("top hit = %q, want team/finance/variance-analysis", got[0].ID)
	}
}

// Spec: §6.4.1 — type filter drops records of the wrong type.
func TestLocalSearch_TypeFilter(t *testing.T) {
	t.Parallel()
	records := []filesystem.ArtifactRecord{
		{ID: "x/skill",
			Artifact: &manifest.Artifact{Type: manifest.TypeSkill, Name: "skill", Description: "x"}},
		{ID: "x/cmd",
			Artifact: &manifest.Artifact{Type: manifest.TypeCommand, Name: "cmd", Description: "x"}},
	}
	got := localSearch(records, "x", "command", "", nil, 10)
	if len(got) != 1 || got[0].ID != "x/cmd" {
		t.Errorf("got %+v, want only x/cmd", got)
	}
}

// Spec: §6.4.1 — empty query returns every matching record sorted
// alphabetically (deterministic for tests + UI).
func TestLocalSearch_EmptyQueryAlphabetical(t *testing.T) {
	t.Parallel()
	records := []filesystem.ArtifactRecord{
		{ID: "z/last", Artifact: &manifest.Artifact{Type: manifest.TypeContext}},
		{ID: "a/first", Artifact: &manifest.Artifact{Type: manifest.TypeContext}},
	}
	got := localSearch(records, "", "", "", nil, 10)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID != "a/first" || got[1].ID != "z/last" {
		t.Errorf("order = [%s, %s], want [a/first, z/last]", got[0].ID, got[1].ID)
	}
}

// Spec: §6.4.1 — scope filter keeps only IDs under the prefix.
func TestLocalSearch_ScopeFilter(t *testing.T) {
	t.Parallel()
	records := []filesystem.ArtifactRecord{
		{ID: "team/a", Artifact: &manifest.Artifact{Type: manifest.TypeContext, Description: "match"}},
		{ID: "other/b", Artifact: &manifest.Artifact{Type: manifest.TypeContext, Description: "match"}},
	}
	got := localSearch(records, "match", "", "team/", nil, 10)
	if len(got) != 1 || got[0].ID != "team/a" {
		t.Errorf("got %+v, want only team/a", got)
	}
}

// Spec: §6.4, §4.3.4 — the Go workspace overlay resolves a plain skill's
// SKILL.md metadata.tags through the filesystem walk, so a tag query matches
// the skill and its descriptor carries the tags. A plain skill without
// metadata.tags does not match.
func TestLocalSearch_OverlayPlainSkillMetadataTags(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for rel, content := range map[string]string{
		"team/notes/SKILL.md": "---\nname: notes\ndescription: Month-end notes\nmetadata:\n  tags: \"finance, close\"\n---\n\nKeep notes.\n",
		"team/other/SKILL.md": "---\nname: other\ndescription: Other notes\n---\n\nclose finance\n",
	} {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	records, _, err := resolveOverlayAll(dir)
	if err != nil {
		t.Fatalf("resolveOverlayAll: %v", err)
	}
	got := localSearch(records, "", "", "", []string{"close"}, 10)
	if len(got) != 1 || got[0].ID != "team/notes" {
		t.Fatalf("tag close = %+v, want only team/notes", got)
	}
	if !reflect.DeepEqual(got[0].Tags, []string{"finance", "close"}) {
		t.Errorf("descriptor tags = %v, want the metadata [finance close]", got[0].Tags)
	}
}
