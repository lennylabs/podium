package ingest

import (
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/store"
)

// spec: §4.7 "Artifact embeddings" — the projection is name +
// description + when_to_use (joined with newlines) + tags (joined),
// built from frontmatter only. The prose body is NOT embedded.
func TestComposeEmbeddingText_ProjectionFields(t *testing.T) {
	mr := store.ManifestRecord{
		Name:        "run-variance-analysis",
		Description: "flag unusual variance",
		WhenToUse:   []string{"after month-end close", "before board review"},
		Tags:        []string{"finance", "variance"},
		Body:        []byte("SECRET BODY PROSE that must not be embedded"),
	}
	got := composeEmbeddingText(mr)
	want := "run-variance-analysis\nflag unusual variance\nafter month-end close\nbefore board review\nfinance variance"
	if got != want {
		t.Fatalf("composeEmbeddingText =\n%q\nwant\n%q", got, want)
	}
	if strings.Contains(got, "SECRET BODY") {
		t.Errorf("projection must not embed the prose body; got %q", got)
	}
}

// spec: §4.7 — empty optional fields leave no blank lines, and the
// artifact id is never substituted for a missing name.
func TestComposeEmbeddingText_SkipsEmptyParts(t *testing.T) {
	mr := store.ManifestRecord{
		ArtifactID:  "finance/x",
		Name:        "x",
		Description: "",
		Tags:        []string{"t"},
	}
	got := composeEmbeddingText(mr)
	if got != "x\nt" {
		t.Fatalf("composeEmbeddingText = %q, want %q", got, "x\nt")
	}
	if strings.Contains(got, "finance/x") {
		t.Errorf("artifact id must not appear in the projection; got %q", got)
	}
}

// spec: §4.7 — manifestRecordFor must persist Name and WhenToUse so the
// projection can be rebuilt at reembed time. For skills the name and
// description come from SKILL.md (§4.3.4).
func TestManifestRecordFor_PersistsNameAndWhenToUse(t *testing.T) {
	rec := filesystem.ArtifactRecord{
		ID: "finance/run-variance",
		Artifact: &manifest.Artifact{
			Type:      manifest.TypeSkill,
			Version:   "1.0.0",
			WhenToUse: []string{"when X"},
		},
		ArtifactBytes: []byte("---\ntype: skill\nversion: 1.0.0\nwhen_to_use:\n  - when X\n---\n"),
		Skill: &manifest.Skill{
			Name:        "run-variance",
			Description: "from skill",
		},
	}
	mr, err := manifestRecordFor(rec, "acme", "L", time.Now())
	if err != nil {
		t.Fatalf("manifestRecordFor: %v", err)
	}
	if mr.Name != "run-variance" {
		t.Errorf("Name = %q, want run-variance (from SKILL.md)", mr.Name)
	}
	if len(mr.WhenToUse) != 1 || mr.WhenToUse[0] != "when X" {
		t.Errorf("WhenToUse = %v, want [when X]", mr.WhenToUse)
	}
	got := composeEmbeddingText(mr)
	if !strings.Contains(got, "run-variance") || !strings.Contains(got, "when X") {
		t.Errorf("projection %q missing name or when_to_use", got)
	}
}

// spec: §4.7.3 — the server identifier is derived as
// <command>:<first non-flag arg>, matching the spec example
// npx:@company/finance-warehouse-mcp.
func TestServerIdentifierFor_DerivesFromCommandAndArgs(t *testing.T) {
	cases := []struct {
		ref  manifest.MCPServerRef
		want string
	}{
		{manifest.MCPServerRef{Command: "npx", Args: []string{"-y", "@company/finance-warehouse-mcp"}}, "npx:@company/finance-warehouse-mcp"},
		{manifest.MCPServerRef{Command: "uvx", Args: []string{"some-pkg"}}, "uvx:some-pkg"},
		{manifest.MCPServerRef{Command: "node"}, "node"},
		{manifest.MCPServerRef{Transport: "stdio"}, "stdio"},
	}
	for _, c := range cases {
		if got := serverIdentifierFor(c.ref); got != c.want {
			t.Errorf("serverIdentifierFor(%+v) = %q, want %q", c.ref, got, c.want)
		}
	}
}

// spec: §4.7.3 — an mcpServers reference resolves to the mcp-server
// artifact that declares the matching server_identifier; the edge To is
// that artifact's canonical ID, not the consumer-side local name.
func TestEdgesFor_MCPServerResolvesViaServerIdentifier(t *testing.T) {
	consumer := &manifest.Artifact{
		Type: manifest.TypeAgent,
		MCPServers: []manifest.MCPServerRef{
			{Name: "warehouse", Command: "npx", Args: []string{"-y", "@company/finance-warehouse-mcp"}},
		},
	}
	serverIDs := map[string]string{
		"npx:@company/finance-warehouse-mcp": "infra/mcp/finance-warehouse",
	}
	edges := edgesFor(consumer, "finance/agent", serverIDs)
	var got *store.DependencyEdge
	for i := range edges {
		if edges[i].Kind == "mcpServers" {
			got = &edges[i]
		}
	}
	if got == nil {
		t.Fatalf("no mcpServers edge produced; edges=%+v", edges)
	}
	if got.To != "infra/mcp/finance-warehouse" {
		t.Errorf("edge To = %q, want infra/mcp/finance-warehouse (resolved artifact id)", got.To)
	}
}

// spec: §4.7.3 — when no mcp-server artifact declares the referenced
// server_identifier, no edge is produced (rather than a dangling edge
// to the local name).
func TestEdgesFor_MCPServerNoMatchEmitsNoEdge(t *testing.T) {
	consumer := &manifest.Artifact{
		Type: manifest.TypeAgent,
		MCPServers: []manifest.MCPServerRef{
			{Name: "warehouse", Command: "npx", Args: []string{"@company/unknown"}},
		},
	}
	edges := edgesFor(consumer, "finance/agent", map[string]string{})
	for _, e := range edges {
		if e.Kind == "mcpServers" {
			t.Errorf("expected no mcpServers edge, got %+v", e)
		}
	}
}

// spec: §4.7.3 — serverIdentifierIndex maps each mcp-server artifact's
// server_identifier to its canonical id, skipping other types and
// entries without an identifier.
func TestServerIdentifierIndex_MapsMCPServersOnly(t *testing.T) {
	records := []filesystem.ArtifactRecord{
		{ID: "infra/mcp/warehouse", Artifact: &manifest.Artifact{Type: manifest.TypeMCPServer, ServerIdentifier: "npx:@company/warehouse"}},
		{ID: "infra/mcp/no-id", Artifact: &manifest.Artifact{Type: manifest.TypeMCPServer}},
		{ID: "finance/agent", Artifact: &manifest.Artifact{Type: manifest.TypeAgent, ServerIdentifier: "ignored"}},
	}
	idx := serverIdentifierIndex(records)
	if len(idx) != 1 {
		t.Fatalf("index size = %d, want 1; idx=%v", len(idx), idx)
	}
	if idx["npx:@company/warehouse"] != "infra/mcp/warehouse" {
		t.Errorf("index[npx:@company/warehouse] = %q, want infra/mcp/warehouse", idx["npx:@company/warehouse"])
	}
}
