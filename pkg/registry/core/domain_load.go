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

// resolveKnobs layers the §4.5.5 discovery configuration in precedence
// order: package defaults, then the §13.12 tenant registry.yaml
// `discovery:` block, then the requested domain's merged DOMAIN.md
// `discovery:` block (per-domain override), then the caller-supplied
// options (which win, per §4.5.5 "Caller overrides"). The per-domain
// `discovery:` block is applied only when allowPerDomain is true; a
// tenant that sets discovery.allow_per_domain_overrides: false disables
// per-domain discovery overrides registry-wide. The DOMAIN.md
// include:/exclude: lists are top-level fields, not discovery overrides,
// so they apply regardless of the gate.
func resolveKnobs(opts LoadDomainOptions, dom *manifest.Domain, td DiscoveryDefaults, allowPerDomain bool) domainKnobs {
	k := domainKnobs{
		maxDepth:             DefaultMaxDepth,
		notableCount:         DefaultNotableCount,
		targetResponseTokens: DefaultTargetResponseTokens,
		foldPassthrough:      true,
	}
	// Tenant-scope registry.yaml defaults override the package defaults.
	if td.MaxDepth > 0 {
		k.maxDepth = td.MaxDepth
	}
	if td.NotableCount > 0 {
		k.notableCount = td.NotableCount
	}
	if td.FoldBelowArtifacts > 0 {
		k.foldBelow = td.FoldBelowArtifacts
	}
	if td.TargetResponseTokens > 0 {
		k.targetResponseTokens = td.TargetResponseTokens
	}
	if td.FoldPassthroughChains != nil {
		k.foldPassthrough = *td.FoldPassthroughChains
	}
	if dom != nil {
		k.include = dom.Include
		k.exclude = dom.Exclude
		if d := dom.Discovery; d != nil && allowPerDomain {
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
	recs, err := r.store.ListDomains(ctx, r.tenantFor(ctx))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	// spec: §4.6 — resolve the caller's layer list (admin + runtime-registered)
	// per request so load_domain composes user-defined layers too.
	resolved := r.resolveLayers(ctx)
	allowed := r.allowedLayers(resolved, id)
	prec := map[string]int{}
	for _, l := range resolved {
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

// allowedLayers returns the set of layer IDs from resolved that id may see,
// or nil when every layer is visible (public mode, filesystem-source, or no
// configured layer list). Mirrors visibleManifests so domain records and
// manifest records share one visibility view. Callers pass the per-request
// resolved layer list from resolveLayers (§4.6).
func (r *Registry) allowedLayers(resolved []layer.Layer, id layer.Identity) map[string]bool {
	if id.IsPublic || len(resolved) == 0 {
		return nil
	}
	allowed := map[string]bool{}
	for _, l := range layer.EffectiveLayersWith(resolved, id, r.resolveGroup) {
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
func (r *Registry) renderSubtree(under []store.ManifestRecord, merged map[string]*manifest.Domain, path string, depth int, foldPassthrough bool, curated map[string]bool) []DomainDescriptor {
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
			renderedPath = collapsePassthroughChain(under, childPath, curated)
		}
		if unlistedAt(renderedPath, merged) {
			continue
		}
		out = append(out, DomainDescriptor{
			Path:        renderedPath,
			Name:        lastSegment(renderedPath),
			Description: childDescription(renderedPath, merged),
			Subdomains:  r.renderSubtree(under, merged, renderedPath, depth-1, foldPassthrough, curated),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// CatalogEntry is one visible artifact in the §4.5.2 catalog: the canonical
// ID, type, and short summary, with no manifest body. It carries exactly what
// a client-side load_domain merge needs to resolve a workspace-local
// DOMAIN.md's include:/exclude: globs over the merged view and to render an
// imported registry artifact in the notable list without fetching its body.
type CatalogEntry struct {
	ID      string
	Type    string
	Summary string
}

// Catalog returns the visible artifacts whose canonical ID falls under scope
// (a path-segment prefix; an empty scope returns the whole visible catalog),
// one latest-version entry per artifact, ordered by ID. It is the cheap
// descriptor surface a consumer that exposes load_domain uses to resolve a
// workspace-local DOMAIN.md's include:/exclude: globs over the merged view
// (registry ∪ overlay) per §4.5.2 and §6.4, and to render registry artifacts
// a local include: pulls in without a load_artifact round-trip. Visibility is
// filtered per caller through visibleManifests, so the catalog never widens
// the caller's read surface.
func (r *Registry) Catalog(ctx context.Context, id layer.Identity, scope string) ([]CatalogEntry, error) {
	visible, err := r.visibleManifests(ctx, id)
	if err != nil {
		return nil, err
	}
	under := dedupeLatest(manifestsUnder(visible, scope))
	out := make([]CatalogEntry, 0, len(under))
	for _, m := range under {
		out = append(out, CatalogEntry{ID: m.ArtifactID, Type: m.Type, Summary: m.Description})
	}
	return out, nil
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
