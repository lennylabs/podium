package core

import (
	"context"
	"fmt"
	"sort"
	"strings"

	domainpkg "github.com/lennylabs/podium/pkg/domain"
	"github.com/lennylabs/podium/pkg/layer"
)

// DomainVectorVersion is the reserved version sentinel under which a
// domain's §4.7 projection embedding is stored in the shared vector
// index. Artifact versions are semver, so this value cannot collide with
// one: search_artifacts skips it (vectorRanks) and search_domains keeps
// only it (domainVectorRanks), partitioning the two indexes within one
// vector.Provider per §4.7 ("The same EmbeddingProvider and storage
// backend serve both artifact and domain indexes").
const DomainVectorVersion = "@domain"

// SearchDomainsOptions captures the §5 search_domains arguments.
type SearchDomainsOptions struct {
	Query string
	Scope string
	TopK  int
}

// domainCandidate is one searchable domain: a path with a merged
// DOMAIN.md, so it has a §4.7 projection to rank over. description and
// keywords are returned in the descriptor; projection is the text the
// lexical and vector rankers score.
type domainCandidate struct {
	path        string
	description string
	keywords    []string
	projection  string
}

// SearchDomains runs §3.2 Layer 1 hybrid retrieval over domain
// projections. Per §4.7 "Domain embeddings", a domain's projection is its
// DOMAIN.md frontmatter description, keywords, and truncated prose body;
// search runs BM25 over that text and, when a vector store and embedder
// are configured (WithVectorSearch) and the query is non-empty, fuses the
// lexical ranks with the domain vector ranks via RRF, mirroring
// SearchArtifacts. Without the vector path it degrades to BM25-only and
// sets SearchResult.Degraded=true.
//
// Only domains with a DOMAIN.md surface: a domain without one has no
// projection to embed and is reachable by load_domain enumeration only
// (§4.5.1). Visibility filtering applies through mergedDomains: a domain
// whose DOMAIN.md was ingested only under a layer the caller cannot see
// does not appear (§4.7). Unlisted domains are excluded so they stay
// undetectable through discovery probing (§4.5.3), consistent with
// load_domain returning domain.not_found for them.
func (r *Registry) SearchDomains(ctx context.Context, id layer.Identity, opts SearchDomainsOptions) (*SearchResult, error) {
	// spec: §4.7.5 — log the read with resolved layer composition and
	// result size; deferred so the size lands on the success path.
	ev := AuditEvent{
		Type:           "domains.searched",
		Caller:         callerOf(id),
		Context:        map[string]string{"query": opts.Query, "scope": opts.Scope},
		ResolvedLayers: r.effectiveLayerComposition(id),
	}
	defer func() { r.emit(ctx, ev) }()
	if opts.TopK > 50 {
		return nil, fmt.Errorf("%w: top_k > 50", ErrInvalidArgument)
	}
	if opts.TopK <= 0 {
		opts.TopK = 10
	}

	merged, err := r.mergedDomains(ctx, id)
	if err != nil {
		return nil, err
	}

	cands := make([]domainCandidate, 0, len(merged))
	for path, dom := range merged {
		if dom == nil {
			continue
		}
		if opts.Scope != "" && !inPrefix(path, opts.Scope) {
			continue
		}
		if unlistedAt(path, merged) {
			continue
		}
		proj := domainpkg.EmbeddingProjection(dom)
		if strings.TrimSpace(proj) == "" {
			continue
		}
		var keywords []string
		if dom.Discovery != nil {
			keywords = dom.Discovery.Keywords
		}
		cands = append(cands, domainCandidate{
			path:        path,
			description: childDescription(path, merged),
			keywords:    keywords,
			projection:  proj,
		})
	}
	byPath := make(map[string]domainCandidate, len(cands))
	for _, c := range cands {
		byPath[c.path] = c
	}

	// Lexical: BM25 over the domain projections. Empty query returns every
	// candidate (alphabetical), the §3.2 "browse all domains" move. The
	// lexical score feeds the descriptor's score field (consistent with
	// search_artifacts); a vector-only match keeps score 0.
	var ordered []string
	scoreByPath := map[string]float64{}
	if opts.Query == "" {
		ordered = make([]string, 0, len(cands))
		for _, c := range cands {
			ordered = append(ordered, c.path)
		}
		sort.Strings(ordered)
	} else {
		docs := make([][]string, len(cands))
		tiebreak := make([]string, len(cands))
		for i, c := range cands {
			docs[i] = tokenize(strings.ToLower(c.projection))
			tiebreak[i] = c.path
		}
		for _, ri := range bm25Rank(docs, tiebreak, opts.Query) {
			p := cands[ri.idx].path
			ordered = append(ordered, p)
			scoreByPath[p] = ri.score
		}
	}

	// Vector: when configured and the query is non-empty, fuse the domain
	// vector ranks with the lexical ranks via RRF. RRFFuse unions both
	// lists, so a vector-only domain (semantically related, no lexical
	// overlap) surfaces — every fused path maps back through byPath.
	degraded := r.vector == nil || r.embedder == nil
	if !degraded && opts.Query != "" {
		vecRanks, vecErr := r.domainVectorRanks(ctx, opts.Query, byPath, opts.TopK)
		if vecErr != nil {
			degraded = true
		} else if len(vecRanks) > 0 {
			ordered = RRFFuse(ordered, vecRanks)
		}
	}

	res := &SearchResult{Query: opts.Query, TotalMatched: len(ordered), Degraded: degraded}
	if len(ordered) > opts.TopK {
		ordered = ordered[:opts.TopK]
	}
	for _, path := range ordered {
		c, ok := byPath[path]
		if !ok {
			continue
		}
		res.Domains = append(res.Domains, DomainDescriptor{
			Path:        c.path,
			Name:        lastSegment(c.path),
			Description: c.description,
			Keywords:    c.keywords,
			Score:       scoreByPath[path],
		})
	}
	ev.ResultSize = len(res.Domains)
	return res, nil
}

// domainVectorRanks embeds the query and returns the nearest domain paths
// from the shared vector index, restricted to the domain partition
// (Version == DomainVectorVersion) and to the visible candidate set
// (byPath) so an artifact vector or a domain the caller cannot see never
// surfaces. Mirrors vectorRanks for §3.2 Layer 1.
func (r *Registry) domainVectorRanks(ctx context.Context, query string, byPath map[string]domainCandidate, topK int) ([]string, error) {
	vecs, err := r.embedder.Embed(ctx, []string{query})
	if err != nil || len(vecs) == 0 {
		return nil, fmt.Errorf("embed: %w", err)
	}
	matches, err := r.vector.Query(ctx, r.tenantID, vecs[0], topK*4)
	if err != nil {
		return nil, fmt.Errorf("vector: %w", err)
	}
	out := make([]string, 0, len(matches))
	seen := map[string]bool{}
	for _, m := range matches {
		if m.Version != DomainVectorVersion {
			continue
		}
		// The domain projection is stored with the path as the key, so a
		// match's ArtifactID is the domain path.
		if _, ok := byPath[m.ArtifactID]; !ok || seen[m.ArtifactID] {
			continue
		}
		seen[m.ArtifactID] = true
		out = append(out, m.ArtifactID)
	}
	return out, nil
}
