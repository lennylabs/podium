package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/registry/core"
)

// spec: §7.6.1 — a search_artifacts result carries the artifact's frontmatter
// (the documented {id, type, version, score, frontmatter} schema) and omits the
// notable-only summary field (F-7.6.11).
func TestDescriptorOf_FrontmatterOnSearch(t *testing.T) {
	w := descriptorOf(core.ArtifactDescriptor{
		ID: "finance/run", Type: "skill", Version: "1.0.0",
		Frontmatter: "---\ntype: skill\n---\n",
	})
	if w.Frontmatter != "---\ntype: skill\n---\n" {
		t.Fatalf("frontmatter not mapped: %q", w.Frontmatter)
	}
	b, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"frontmatter"`) {
		t.Errorf("json missing frontmatter: %s", b)
	}
	if strings.Contains(string(b), `"summary"`) {
		t.Errorf("search result should omit summary: %s", b)
	}
}

// spec: §7.6.1 — a load_domain notable entry carries summary, source, and
// folded_from (the documented {id, type, summary, source, folded_from} schema)
// and omits the search-only frontmatter field (F-7.6.11).
func TestDescriptorOf_SummarySourceFoldedOnNotable(t *testing.T) {
	w := descriptorOf(core.ArtifactDescriptor{
		ID: "finance/run", Type: "skill",
		Summary: "Variance analysis", Source: "featured", FoldedFrom: "close",
	})
	if w.Summary != "Variance analysis" {
		t.Fatalf("summary not mapped: %q", w.Summary)
	}
	b, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, k := range []string{`"summary"`, `"source"`, `"folded_from"`} {
		if !strings.Contains(s, k) {
			t.Errorf("json missing %s: %s", k, s)
		}
	}
	if strings.Contains(s, `"frontmatter"`) {
		t.Errorf("notable entry should omit frontmatter: %s", s)
	}
}
