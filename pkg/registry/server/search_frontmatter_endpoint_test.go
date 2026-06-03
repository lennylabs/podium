package server_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness/registryharness"
	"github.com/lennylabs/podium/pkg/registry/server"
)

// spec: §5 / §7.6.1 — a /v1/search_artifacts result carries the artifact's
// frontmatter (the documented {id, type, version, score, frontmatter}
// read-CLI/SDK schema). Only the manifest body stays at the registry until
// load_artifact; the descriptor has no body field, so the frontmatter metadata
// rides along while the prose body does not. Regression for F-7.6.3, where the
// handler cleared the frontmatter on every result.
func TestSearchArtifacts_ResultCarriesFrontmatter(t *testing.T) {
	t.Parallel()
	h := registryharness.New(t,
		fixture("a/ARTIFACT.md", contextFor("Alpha description", "finance")),
	)
	body := mustGet(t, h.URL, "/v1/search_artifacts?query=")
	var resp server.SearchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Results) == 0 {
		t.Fatalf("expected at least one result, got none")
	}
	r := resp.Results[0]
	if r.Frontmatter == "" {
		t.Fatalf("search result dropped frontmatter (F-7.6.3 regression): %+v", r)
	}
	// The descriptive fields the §7.6.1 schema surfaces inside frontmatter.
	if !strings.Contains(r.Frontmatter, "type: context") ||
		!strings.Contains(r.Frontmatter, "Alpha description") {
		t.Errorf("frontmatter missing expected fields: %q", r.Frontmatter)
	}
	// The prose manifest body stays at the registry until load_artifact; it
	// must not ride along in the search descriptor's frontmatter.
	if strings.Contains(r.Frontmatter, "Body for") {
		t.Errorf("manifest body leaked into search frontmatter: %q", r.Frontmatter)
	}
}
