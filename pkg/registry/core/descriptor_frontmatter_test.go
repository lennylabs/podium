package core

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/store"
)

// spec: §7.6.1 — the search-result descriptor built from a manifest record
// carries the record's frontmatter so search_artifacts can return it in the
// documented JSON schema.
func TestDescriptorOf_SearchCarriesFrontmatter(t *testing.T) {
	fm := "---\ntype: skill\nversion: 1.0.0\n---\n"
	d := descriptorOf(store.ManifestRecord{
		ArtifactID:  "finance/run",
		Type:        "skill",
		Version:     "1.0.0",
		Frontmatter: []byte(fm),
	})
	if d.Frontmatter != fm {
		t.Errorf("Frontmatter = %q, want %q", d.Frontmatter, fm)
	}
}

// spec: §5 / §7.6.1 — the stored manifest bytes are the full ARTIFACT.md
// source (frontmatter plus prose body). A search descriptor carries only the
// frontmatter block; the body stays at the registry until load_artifact, so it
// must not ride along. Regression test (the prior fix cleared the whole
// frontmatter to keep the body out, which also dropped the documented field).
func TestDescriptorOf_SearchDropsBody(t *testing.T) {
	full := "---\ntype: skill\nversion: 1.0.0\ndescription: do the thing\n---\n\nThe full prose body lives here.\n"
	d := descriptorOf(store.ManifestRecord{
		ArtifactID:  "finance/run",
		Type:        "skill",
		Version:     "1.0.0",
		Frontmatter: []byte(full),
	})
	if d.Frontmatter == "" {
		t.Fatal("search descriptor dropped the frontmatter entirely")
	}
	if got := d.Frontmatter; got != "---\ntype: skill\nversion: 1.0.0\ndescription: do the thing\n---\n" {
		t.Errorf("frontmatter block wrong: %q", got)
	}
	if strings.Contains(d.Frontmatter, "prose body") {
		t.Errorf("manifest body leaked into search frontmatter: %q", d.Frontmatter)
	}
}
