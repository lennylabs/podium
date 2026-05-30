package core

import (
	"context"
	"fmt"
	"sort"
	"strings"

	domainpkg "github.com/lennylabs/podium/pkg/domain"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/version"
)

// domainKnobs is the resolved §4.5.5 discovery configuration for one
// load_domain call: caller options override the merged DOMAIN.md
// discovery block, which overrides the package defaults.
type domainKnobs struct {
	maxDepth             int
	notableCount         int
	foldBelow            int
	foldPassthrough      bool
	targetResponseTokens int
	featured             []string
	deprioritize         []string
	include              []string
	exclude              []string
	keywords             []string
}

// resolveKnobs layers the §4.5.5 discovery configuration: package
// defaults, then the requested domain's merged DOMAIN.md discovery
// block (per-domain override), then the caller-supplied options (which
// win, per §4.5.5 "Caller overrides"). The tenant-scope registry.yaml
// discovery block and allow_per_domain_overrides gate are out of scope
// here (tracked separately).
func resolveKnobs(opts LoadDomainOptions, dom *manifest.Domain) domainKnobs {
	k := domainKnobs{
		maxDepth:        DefaultMaxDepth,
		notableCount:    DefaultNotableCount,
		foldPassthrough: true,
	}
	if dom != nil {
		k.include = dom.Include
		k.exclude = dom.Exclude
		if d := dom.Discovery; d != nil {
			if d.MaxDepth > 0 {
				k.maxDepth = d.MaxDepth
			}
			if d.NotableCount > 0 {
				k.notableCount = d.NotableCount
			}
			if d.FoldBelowArtifacts > 0 {
				k.foldBelow = d.FoldBelowArtifacts
			}
			if d.FoldPassthroughChains != nil {
				k.foldPassthrough = *d.FoldPassthroughChains
			}
			if d.TargetResponseTokens > 0 {
				k.targetResponseTokens = d.TargetResponseTokens
			}
			k.featured = d.Featured
			k.deprioritize = d.Deprioritize
			k.keywords = d.Keywords
		}
	}
	if opts.NotableCount > 0 {
		k.notableCount = opts.NotableCount
	}
	if opts.FoldBelowArtifacts > 0 {
		k.foldBelow = opts.FoldBelowArtifacts
	}
	if opts.FoldPassthroughChains != nil {
		k.foldPassthrough = *opts.FoldPassthroughChains
	}
	if opts.TargetResponseTokens > 0 {
		k.targetResponseTokens = opts.TargetResponseTokens
	}
	if len(opts.Featured) > 0 {
		k.featured = opts.Featured
	}
	return k
}

// mergedDomains loads every DOMAIN.md visible to id and merges the
// candidates for each path across layers per §4.5.4 (lowest precedence
// first). Malformed DOMAIN.md frontmatter is skipped here; the linter
// reports it at ingest. Returns a map keyed by canonical domain path.
func (r *Registry) mergedDomains(ctx context.Context, id layer.Identity) (map[string]*manifest.Domain, error) {
	recs, err := r.store.ListDomains(ctx, r.tenantID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	allowed := r.visibleLayerIDs(id)
	prec := map[string]int{}
	for _, l := range r.layers {
		prec[l.ID] = l.Precedence
	}
	byPath := map[string][]store.DomainRecord{}
	for _, rec := range recs {
		if allowed != nil && !allowed[rec.Layer] {
			continue
		}
		byPath[rec.Path] = append(byPath[rec.Path], rec)
	}
	out := make(map[string]*manifest.Domain, len(byPath))
	for path, group := range byPath {
		sort.SliceStable(group, func(i, j int) bool {
			return prec[group[i].Layer] < prec[group[j].Layer]
		})
		cands := make([]*manifest.Domain, 0, len(group))
		for _, rec := range group {
			d, perr := manifest.ParseDomain(rec.Raw)
			if perr != nil {
				continue
			}
			cands = append(cands, d)
		}
		if len(cands) > 0 {
			out[path] = domainpkg.MergeAcrossLayers(cands)
		}
	}
	return out, nil
}

// visibleLayerIDs returns the set of layer IDs visible to id, or nil
// when every layer is visible (public mode, filesystem-source, or no
// configured layer list). Mirrors visibleManifests so domain records
// and manifest records share one visibility view.
func (r *Registry) visibleLayerIDs(id layer.Identity) map[string]bool {
	if id.IsPublic || len(r.layers) == 0 {
		return nil
	}
	allowed := map[string]bool{}
	for _, l := range layer.EffectiveLayersWith(r.layers, id, r.resolveGroup) {
		allowed[l.ID] = true
	}
	return allowed
}

// unlistedAt reports whether path resolves under an unlisted folder:
// path itself or any ancestor has a merged DOMAIN.md with unlisted:
// true. §4.5.3 propagates unlisted to the whole subtree.
func unlistedAt(path string, merged map[string]*manifest.Domain) bool {
	for p := path; p != ""; p = parentPath(p) {
		if d := merged[p]; d != nil && d.Unlisted {
			return true
		}
	}
	return false
}

// requestedDescription resolves the §4.5.5 description for the requested
// domain: prose body if present, else the frontmatter description, else
// the synthesized basename fallback.
func requestedDescription(d *manifest.Domain, path string) string {
	if d != nil {
		if body := strings.TrimSpace(d.Body); body != "" {
			return body
		}
		if d.Description != "" {
			return d.Description
		}
	}
	return domainpkg.FallbackDescription(path)
}

// childDescription resolves the §4.5.5 description for a domain that
// appears as a child or sibling: the frontmatter description, else the
// synthesized fallback. The body is never returned for non-requested
// entries.
func childDescription(path string, merged map[string]*manifest.Domain) string {
	if d := merged[path]; d != nil && d.Description != "" {
		return d.Description
	}
	return domainpkg.FallbackDescription(path)
}

// renderSubtree returns the subdomain descriptors immediately under
// path (post-fold), each carrying its short description and a nested
// child tree up to depth levels. Unlisted subtrees are dropped.
// Per-domain fold_below_artifacts folding is applied only at the
// requested domain's level (where the folded artifacts have a notable
// list to surface in); deeper levels carry descriptions only per
// §4.5.5.
func (r *Registry) renderSubtree(under []store.ManifestRecord, merged map[string]*manifest.Domain, path string, depth int, foldPassthrough bool) []DomainDescriptor {
	if depth <= 0 {
		return nil
	}
	groups := groupByImmediateChild(under, path)
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []DomainDescriptor
	for _, name := range names {
		childPath := joinPath(path, name)
		if unlistedAt(childPath, merged) {
			continue
		}
		renderedPath := childPath
		if foldPassthrough {
			renderedPath = collapsePassthroughChain(under, childPath)
		}
		if unlistedAt(renderedPath, merged) {
			continue
		}
		out = append(out, DomainDescriptor{
			Path:        renderedPath,
			Name:        lastSegment(renderedPath),
			Description: childDescription(renderedPath, merged),
			Subdomains:  r.renderSubtree(under, merged, renderedPath, depth-1, foldPassthrough),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// manifestIDs returns the unique, sorted set of artifact IDs in the
// visible set, used as the catalog for §4.5.2 import resolution.
func manifestIDs(visible []store.ManifestRecord) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(visible))
	for _, m := range visible {
		if seen[m.ArtifactID] {
			continue
		}
		seen[m.ArtifactID] = true
		out = append(out, m.ArtifactID)
	}
	sort.Strings(out)
	return out
}

// latestByID maps each artifact ID to its latest-version record per
// §4.7.6, used to build descriptors for imported artifacts.
func latestByID(visible []store.ManifestRecord) map[string]store.ManifestRecord {
	byID := map[string][]store.ManifestRecord{}
	for _, m := range visible {
		byID[m.ArtifactID] = append(byID[m.ArtifactID], m)
	}
	out := make(map[string]store.ManifestRecord, len(byID))
	for id, recs := range byID {
		out[id] = latestRecord(recs)
	}
	return out
}

// dedupeLatest collapses a record slice to one record per artifact ID
// (the latest version), ordered by ID.
func dedupeLatest(records []store.ManifestRecord) []store.ManifestRecord {
	byID := map[string][]store.ManifestRecord{}
	ids := make([]string, 0, len(records))
	for _, m := range records {
		if _, ok := byID[m.ArtifactID]; !ok {
			ids = append(ids, m.ArtifactID)
		}
		byID[m.ArtifactID] = append(byID[m.ArtifactID], m)
	}
	sort.Strings(ids)
	out := make([]store.ManifestRecord, 0, len(ids))
	for _, id := range ids {
		out = append(out, latestRecord(byID[id]))
	}
	return out
}

// latestRecord returns the highest-version record per §4.7.6, falling
// back to the last record when no version parses as semver.
func latestRecord(recs []store.ManifestRecord) store.ManifestRecord {
	if len(recs) == 1 {
		return recs[0]
	}
	versions := make([]string, 0, len(recs))
	for _, m := range recs {
		versions = append(versions, m.Version)
	}
	latest, _ := version.ParsePin("latest")
	if resolved, err := version.Resolve(latest, versions); err == nil {
		for _, m := range recs {
			if m.Version == resolved {
				return m
			}
		}
	}
	return recs[len(recs)-1]
}

// visibleCount returns the number of distinct artifact IDs in records,
// the §4.5.5 visibility-aware count used for fold_below decisions.
func visibleCount(records []store.ManifestRecord) int {
	seen := map[string]bool{}
	for _, m := range records {
		seen[m.ArtifactID] = true
	}
	return len(seen)
}
