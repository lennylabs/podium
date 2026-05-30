package domain

import (
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/manifest"
)

// spec: §4.7 "Domain embeddings" — the projection is the frontmatter
// description, the keywords joined, and the prose body, in that order.
func TestEmbeddingProjection_DescriptionKeywordsBody(t *testing.T) {
	t.Parallel()
	d := &manifest.Domain{
		Description: "AP-related operations",
		Body:        "Long-form context about accounts payable.",
		Discovery:   &manifest.DomainDiscovery{Keywords: []string{"invoice", "remittance"}},
	}
	got := EmbeddingProjection(d)
	for _, want := range []string{"AP-related operations", "invoice", "remittance", "accounts payable"} {
		if !strings.Contains(got, want) {
			t.Errorf("projection %q missing %q", got, want)
		}
	}
	// Order: description precedes keywords precedes body.
	if i, j := strings.Index(got, "AP-related"), strings.Index(got, "invoice"); i > j {
		t.Errorf("description should precede keywords: %q", got)
	}
	if i, j := strings.Index(got, "invoice"), strings.Index(got, "Long-form"); i > j {
		t.Errorf("keywords should precede body: %q", got)
	}
}

// spec: §4.7 — a DOMAIN.md whose only content is include: patterns has no
// projectable text and returns the empty string, so it does not surface
// in search_domains (§4.5.1).
func TestEmbeddingProjection_IncludeOnlyIsEmpty(t *testing.T) {
	t.Parallel()
	d := &manifest.Domain{Include: []string{"finance/ap/*"}}
	if got := EmbeddingProjection(d); got != "" {
		t.Errorf("projection = %q, want empty for an include-only DOMAIN.md", got)
	}
	if got := EmbeddingProjection(nil); got != "" {
		t.Errorf("projection(nil) = %q, want empty", got)
	}
}

// spec: §4.7 "Domain embeddings" — the prose body is truncated to the
// first 500 tokens; content past the cap does not enter the projection.
func TestEmbeddingProjection_BodyTruncatedAt500Tokens(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	for i := 0; i < DomainProjectionBodyTokens; i++ {
		b.WriteString("keepme ")
	}
	b.WriteString("DROPME_SENTINEL")
	d := &manifest.Domain{Body: b.String()}
	got := EmbeddingProjection(d)
	if strings.Contains(got, "DROPME_SENTINEL") {
		t.Errorf("token past the 500-token cap leaked into the projection")
	}
	if want := DomainProjectionBodyTokens; len(strings.Fields(got)) != want {
		t.Errorf("projected token count = %d, want %d", len(strings.Fields(got)), want)
	}
}
