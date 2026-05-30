package domain

import (
	"strings"

	"github.com/lennylabs/podium/pkg/manifest"
)

// DomainProjectionBodyTokens caps the DOMAIN.md prose body contribution
// to the embedding projection at the first 500 tokens per §4.7 "Domain
// embeddings".
const DomainProjectionBodyTokens = 500

// EmbeddingProjection builds the §4.7 domain text projection used for
// search_domains hybrid retrieval: the frontmatter description, the
// keywords joined, and the prose body truncated to the first 500 tokens.
// The full body is deliberately truncated; long bodies are noisy for
// retrieval and risk busting embedding-model context limits. Returns the
// empty string when the domain carries none of the three (e.g. a
// DOMAIN.md whose only content is include: patterns), in which case the
// domain has nothing to embed and does not surface in search_domains.
func EmbeddingProjection(d *manifest.Domain) string {
	if d == nil {
		return ""
	}
	parts := make([]string, 0, 3)
	if s := strings.TrimSpace(d.Description); s != "" {
		parts = append(parts, s)
	}
	if d.Discovery != nil && len(d.Discovery.Keywords) > 0 {
		parts = append(parts, strings.Join(d.Discovery.Keywords, " "))
	}
	if body := truncateTokens(d.Body, DomainProjectionBodyTokens); body != "" {
		parts = append(parts, body)
	}
	return strings.Join(parts, "\n")
}

// truncateTokens returns the first n whitespace-delimited tokens of s,
// rejoined with single spaces. Used to bound the DOMAIN.md body
// contribution to the embedding projection (§4.7).
func truncateTokens(s string, n int) string {
	fields := strings.Fields(s)
	if len(fields) > n {
		fields = fields[:n]
	}
	return strings.Join(fields, " ")
}
