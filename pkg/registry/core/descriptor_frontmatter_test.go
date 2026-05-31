package core

import (
	"testing"

	"github.com/lennylabs/podium/pkg/store"
)

// spec: §7.6.1 — the search-result descriptor built from a manifest record
// carries the record's frontmatter so search_artifacts can return it in the
// documented JSON schema (F-7.6.11).
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
