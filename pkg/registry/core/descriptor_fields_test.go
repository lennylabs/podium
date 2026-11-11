package core_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/lennylabs/podium/pkg/registry/core"
)

// Spec: §4.7.4 — an artifact surfaces its own declared sensitivity in search
// results.
func TestSearchArtifacts_SensitivityOwnValue(t *testing.T) {
	t.Parallel()
	body := "---\ntype: context\nversion: 1.0.0\ndescription: solo\nsensitivity: medium\n---\n\nbody\n"
	reg := setupRegistry(t, fstest.MapFS{
		"finance/solo/ARTIFACT.md": &fstest.MapFile{Data: []byte(body)},
	}, "L1")
	res, err := reg.SearchArtifacts(context.Background(), publicID, core.SearchArtifactsOptions{})
	if err != nil {
		t.Fatalf("SearchArtifacts: %v", err)
	}
	found := false
	for _, r := range res.Results {
		if r.ID == "finance/solo" {
			found = true
			if r.Sensitivity != "medium" {
				t.Errorf("Sensitivity = %q, want medium", r.Sensitivity)
			}
		}
	}
	if !found {
		t.Fatalf("search did not return finance/solo: %+v", res.Results)
	}
}

// Spec: §7.6.1 — a load_domain notable entry carries {id, type, summary,
// source, folded_from}. It carries neither a frontmatter block nor a
// sensitivity label, and its summary is the artifact's description.
func TestLoadDomain_NotableEntryCarriesNeitherFrontmatterNorSensitivity(t *testing.T) {
	t.Parallel()
	body := "---\ntype: agent\nversion: 1.0.0\ndescription: domain summary\nsensitivity: high\n---\n\nbody\n"
	reg := setupRegistry(t, fstest.MapFS{
		"finance/child/ARTIFACT.md": &fstest.MapFile{Data: []byte(body)},
	}, "L1")
	got, err := reg.LoadDomain(context.Background(), publicID, "finance", core.LoadDomainOptions{})
	if err != nil {
		t.Fatalf("LoadDomain: %v", err)
	}
	var entry *core.ArtifactDescriptor
	for i := range got.Notable {
		if got.Notable[i].ID == "finance/child" {
			entry = &got.Notable[i]
		}
	}
	if entry == nil {
		t.Fatalf("load_domain did not list finance/child as notable: %+v", got.Notable)
	}
	if entry.Frontmatter != "" {
		t.Errorf("notable Frontmatter = %q, want empty", entry.Frontmatter)
	}
	if entry.Sensitivity != "" {
		t.Errorf("notable Sensitivity = %q, want empty", entry.Sensitivity)
	}
	if entry.Summary != "domain summary" {
		t.Errorf("notable Summary = %q, want the description", entry.Summary)
	}
}
