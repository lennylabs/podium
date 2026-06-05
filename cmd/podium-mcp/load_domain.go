package main

import (
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/lennylabs/podium/pkg/audit"
	domainpkg "github.com/lennylabs/podium/pkg/domain"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// defaultRenderDepth is the §4.5.5 max_depth default. The bridge does not know
// the tenant's resolved max_depth, so an overlay-introduced subtree is rendered
// to the caller's requested depth, falling back to this default.
const defaultRenderDepth = 3

// ldResponse mirrors the /v1/load_domain wire envelope (§5, server.go
// LoadDomainResponse) so the bridge can decode the registry result, compose the
// workspace overlay onto it, and re-emit the same schema. subdomains and
// notable are always present arrays, matching the registry.
type ldResponse struct {
	Path        string        `json:"path"`
	Description string        `json:"description,omitempty"`
	Keywords    []string      `json:"keywords,omitempty"`
	Subdomains  []ldSubdomain `json:"subdomains"`
	Notable     []ldArtifact  `json:"notable"`
	Note        string        `json:"note,omitempty"`
}

// ldSubdomain mirrors a load_domain subdomain entry.
type ldSubdomain struct {
	Path        string        `json:"path"`
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Subdomains  []ldSubdomain `json:"subdomains,omitempty"`
}

// ldArtifact mirrors a load_domain notable entry ({id, type, summary, source,
// folded_from}, §7.6.1). Overlay marks an entry surfaced from the workspace
// overlay, mirroring the search_artifacts overlay annotation; the registry
// never sets it.
type ldArtifact struct {
	ID         string `json:"id"`
	Type       string `json:"type,omitempty"`
	Version    string `json:"version,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Source     string `json:"source,omitempty"`
	FoldedFrom string `json:"folded_from,omitempty"`
	Overlay    bool   `json:"overlay,omitempty"`
}

// loadDomain serves the §5 load_domain meta-tool. It proxies the registry's
// rendered result and, when a workspace overlay is configured, composes the
// overlay DOMAIN.md set and overlay artifacts onto it client-side per §4.5.4
// (the overlay is the highest-precedence layer in the caller's effective view,
// §6.4). With no overlay the behavior is identical to the pre-merge proxy.
func (s *mcpServer) loadDomain(args map[string]any) any {
	path := argString(args, "path")
	s.auditMeta(audit.EventDomainLoaded, path)

	domains := s.overlayDomainsSnapshot()
	records := s.overlaySnapshot()
	if len(domains) == 0 && len(records) == 0 {
		// No overlay to merge: identical to the pre-merge dispatch.
		return s.proxyGet("/v1/load_domain", args, nil)
	}

	// §7.4 offline-only: discovery tools keep no registry tree to merge onto,
	// so the proxy's structured cache-miss error stands (no fetch is issued).
	if s.cfg.cacheMode == "offline-only" {
		return s.proxyGet("/v1/load_domain", args, nil)
	}

	body, err := s.fetchJSON("/v1/load_domain", args)
	if err != nil {
		// §7.4 / §12: a transient outage leaves no registry result to merge,
		// so surface the offline envelope (the overlay-only domain tree is not
		// reconstructed here).
		if isRegistryUnreachable(err) {
			return s.offlineEnvelope(nil)
		}
		// §4.5.2 / §6.4: a domain that exists only in the workspace overlay is
		// still part of the caller's effective view, but the registry 404s it
		// because it never sees the overlay. Synthesize an empty registry
		// result and compose the overlay onto it. Any other structured error
		// (auth, invalid argument) passes through as a §6.10 error.
		var re *registryError
		if errors.As(err, &re) && re.Code == "domain.not_found" && overlayHasDomainContent(path, domains, records) {
			return s.finishDomainMerge(ldResponse{Path: path}, path, args, domains, records)
		}
		return errorResultFrom(err)
	}
	var reg ldResponse
	if uerr := json.Unmarshal(body, &reg); uerr != nil {
		return errorResult("decode load_domain: " + uerr.Error())
	}
	return s.finishDomainMerge(reg, path, args, domains, records)
}

// finishDomainMerge composes the overlay onto reg, guarantees the subdomains
// and notable arrays are present, and re-encodes the wire envelope.
func (s *mcpServer) finishDomainMerge(reg ldResponse, path string, args map[string]any, domains map[string]*manifest.Domain, records []filesystem.ArtifactRecord) any {
	merged := s.mergeDomain(reg, path, args, domains, records)
	if merged.Subdomains == nil {
		merged.Subdomains = []ldSubdomain{}
	}
	if merged.Notable == nil {
		merged.Notable = []ldArtifact{}
	}
	out, merr := json.Marshal(merged)
	if merr != nil {
		return errorResult("encode load_domain: " + merr.Error())
	}
	return jsonAny(out)
}

// overlayHasDomainContent reports whether the workspace overlay carries
// anything at or under path: a DOMAIN.md at path, a deeper DOMAIN.md, or an
// artifact under path. It gates synthesizing a result for an overlay-only
// domain the registry does not know (§4.5.2, §6.4).
func overlayHasDomainContent(path string, domains map[string]*manifest.Domain, records []filesystem.ArtifactRecord) bool {
	if domains[path] != nil {
		return true
	}
	for dp := range domains {
		if dp != path && underRest(dp, path) != "" {
			return true
		}
	}
	for i := range records {
		if underRest(records[i].ID, path) != "" {
			return true
		}
	}
	return false
}

// mergeDomain composes the workspace overlay onto the registry's load_domain
// result for path, per §4.5.4. The overlay is the highest-precedence layer:
//
//   - description/body: the overlay's value wins when it supplies one (the
//     last-layer-wins rule with the overlay as the top layer); otherwise the
//     registry's resolved description stands. The bridge holds only the
//     registry's already-resolved description string, so a registry body is
//     preserved only when the overlay contributes neither body nor description.
//   - keywords: append-unique (registry ∪ overlay).
//   - notable: the candidate pool is extended with the overlay's direct child
//     artifacts and the overlay DOMAIN.md include: set resolved over the merged
//     view (registry catalog ∪ overlay), with overlay precedence on a shared id.
//   - subdomains: extended with overlay-only children (rendered to the
//     requested depth), descriptions overridden by an overlay DOMAIN.md, and
//     overlay-unlisted subtrees dropped.
func (s *mcpServer) mergeDomain(reg ldResponse, path string, args map[string]any, domains map[string]*manifest.Domain, records []filesystem.ArtifactRecord) ldResponse {
	od := domains[path]

	// §4.5.4 keywords append-unique. The root has no DOMAIN.md (§4.5.5), so a
	// description/keyword override applies only to a non-root requested path.
	if path != "" && od != nil && od.Discovery != nil && len(od.Discovery.Keywords) > 0 {
		reg.Keywords = uniqueAppend(reg.Keywords, od.Discovery.Keywords)
	}
	// §4.5.4 description/body, overlay highest precedence.
	if path != "" && od != nil {
		if b := strings.TrimSpace(od.Body); b != "" {
			reg.Description = b
		} else if od.Description != "" {
			reg.Description = od.Description
		}
	}

	// §4.5.5 notable candidate pool extension.
	candidates := s.overlayNotableCandidates(path, od, records)
	reg.Notable = mergeNotable(reg.Notable, candidates, mergedFeatured(reg.Notable, od), notableCap(od))

	// §4.5.5 subdomain enumeration extension and §4.5.3 unlisted pruning.
	reg.Subdomains = s.mergeSubdomains(reg.Subdomains, path, renderDepth(args), domains, records)
	return reg
}

// overlayNotableCandidates returns the overlay's contribution to the §4.5.5
// notable candidate pool for path: the overlay's direct child artifacts (after
// the merged DOMAIN.md exclude:) plus the overlay DOMAIN.md include: set
// resolved over the merged view.
func (s *mcpServer) overlayNotableCandidates(path string, od *manifest.Domain, records []filesystem.ArtifactRecord) []ldArtifact {
	var include, exclude []string
	if od != nil {
		include, exclude = od.Include, od.Exclude
	}
	out := []ldArtifact{}
	seen := map[string]bool{}
	for i := range records {
		rec := records[i]
		if parentOf(rec.ID) != path {
			continue
		}
		if domainpkg.MatchAny(exclude, rec.ID) {
			continue
		}
		if seen[rec.ID] {
			continue
		}
		seen[rec.ID] = true
		out = append(out, overlayArtifactDescriptor(rec))
	}
	if len(include) > 0 {
		for _, m := range s.resolveOverlayIncludes(include, exclude, records) {
			if seen[m.ID] {
				continue
			}
			seen[m.ID] = true
			out = append(out, m)
		}
	}
	return out
}

// resolveOverlayIncludes resolves the overlay DOMAIN.md include:/exclude: globs
// over the merged view (registry catalog ∪ overlay) per §4.5.2 and maps each
// match to a notable descriptor: an overlay record (highest precedence) when
// the id is in the overlay, otherwise the registry catalog descriptor.
func (s *mcpServer) resolveOverlayIncludes(include, exclude []string, records []filesystem.ArtifactRecord) []ldArtifact {
	catalog := s.fetchCatalogForIncludes(include)
	overlayByID := make(map[string]filesystem.ArtifactRecord, len(records))
	for i := range records {
		overlayByID[records[i].ID] = records[i]
	}
	ids := make([]string, 0, len(catalog)+len(overlayByID))
	seen := map[string]bool{}
	for id := range catalog {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for id := range overlayByID {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	out := []ldArtifact{}
	for _, id := range domainpkg.ResolveImports(include, exclude, ids) {
		if rec, ok := overlayByID[id]; ok {
			out = append(out, overlayArtifactDescriptor(rec))
			continue
		}
		if e, ok := catalog[id]; ok {
			out = append(out, e)
		}
	}
	return out
}

// fetchCatalogForIncludes fetches the registry catalog descriptors needed to
// resolve include over the merged view, keyed by id. It scopes each request to
// the literal path prefix of an include pattern so the registry returns only
// the relevant slice; an unanchored pattern (leading glob) widens the fetch to
// the whole visible catalog. A registry-side error degrades the merge to
// overlay-only resolution rather than failing the call.
func (s *mcpServer) fetchCatalogForIncludes(include []string) map[string]ldArtifact {
	prefixes := map[string]bool{}
	full := false
	for _, p := range include {
		pre := globLiteralPrefix(p)
		if pre == "" {
			full = true
			break
		}
		prefixes[pre] = true
	}
	out := map[string]ldArtifact{}
	fetch := func(scope string) {
		entries, err := s.fetchCatalog(scope)
		if err != nil {
			return
		}
		for _, e := range entries {
			out[e.ID] = ldArtifact{ID: e.ID, Type: e.Type, Summary: e.Summary}
		}
	}
	if full {
		fetch("")
		return out
	}
	for pre := range prefixes {
		fetch(pre)
	}
	return out
}

// catalogEntry mirrors one /v1/catalog artifact descriptor.
type catalogEntry struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Summary string `json:"summary"`
}

// fetchCatalog issues GET /v1/catalog?scope=<scope> and returns the visible
// artifact descriptors under that scope (§4.5.2 merged-view glob resolution).
func (s *mcpServer) fetchCatalog(scope string) ([]catalogEntry, error) {
	args := map[string]any{}
	if scope != "" {
		args["scope"] = scope
	}
	body, err := s.fetchJSON("/v1/catalog", args)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Artifacts []catalogEntry `json:"artifacts"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return resp.Artifacts, nil
}

// mergeSubdomains extends the registry subdomain list with overlay-only
// children, overrides a child's description from an overlay DOMAIN.md, and
// drops every overlay-unlisted subtree (§4.5.3, §4.5.5).
func (s *mcpServer) mergeSubdomains(reg []ldSubdomain, path string, depth int, domains map[string]*manifest.Domain, records []filesystem.ArtifactRecord) []ldSubdomain {
	out := pruneUnlisted(reg, domains)
	byPath := make(map[string]int, len(out))
	for i := range out {
		byPath[out[i].Path] = i
	}
	for _, name := range overlayImmediateChildren(path, domains, records) {
		childPath := joinSeg(path, name)
		if unlistedOverlay(childPath, domains) {
			continue
		}
		if i, ok := byPath[childPath]; ok {
			if od := domains[childPath]; od != nil && od.Description != "" {
				out[i].Description = od.Description
			}
			continue
		}
		out = append(out, ldSubdomain{
			Path:        childPath,
			Name:        name,
			Description: overlayChildDescription(childPath, domains),
			Subdomains:  s.renderOverlaySubtree(childPath, depth-1, domains, records),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// renderOverlaySubtree renders the overlay-only subdomain tree under path to
// depth levels, dropping unlisted subtrees. It does not apply pass-through
// folding (a registry-side rendering nicety); an overlay-introduced subtree
// renders its literal directory structure.
func (s *mcpServer) renderOverlaySubtree(path string, depth int, domains map[string]*manifest.Domain, records []filesystem.ArtifactRecord) []ldSubdomain {
	if depth <= 0 {
		return nil
	}
	out := []ldSubdomain{}
	for _, name := range overlayImmediateChildren(path, domains, records) {
		childPath := joinSeg(path, name)
		if unlistedOverlay(childPath, domains) {
			continue
		}
		out = append(out, ldSubdomain{
			Path:        childPath,
			Name:        name,
			Description: overlayChildDescription(childPath, domains),
			Subdomains:  s.renderOverlaySubtree(childPath, depth-1, domains, records),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// overlayImmediateChildren returns the immediate subdomain child names under
// path implied by the overlay: a first segment beyond path that has an overlay
// artifact below it (id has a deeper segment) or an overlay DOMAIN.md at or
// below it. A direct child artifact (no deeper segment) is a notable entry, not
// a subdomain, and is excluded here.
func overlayImmediateChildren(path string, domains map[string]*manifest.Domain, records []filesystem.ArtifactRecord) []string {
	seen := map[string]bool{}
	names := []string{}
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		names = append(names, name)
	}
	for i := range records {
		rest := underRest(records[i].ID, path)
		if rest == "" || !strings.Contains(rest, "/") {
			continue
		}
		add(strings.SplitN(rest, "/", 2)[0])
	}
	for dp := range domains {
		if dp == path {
			continue
		}
		rest := underRest(dp, path)
		if rest == "" {
			continue
		}
		add(strings.SplitN(rest, "/", 2)[0])
	}
	sort.Strings(names)
	return names
}

// overlayArtifactDescriptor maps an overlay artifact record to a load_domain
// notable descriptor, tagged as overlay-sourced.
func overlayArtifactDescriptor(rec filesystem.ArtifactRecord) ldArtifact {
	d := descriptorFor(rec)
	return ldArtifact{
		ID:      d.ID,
		Type:    d.Type,
		Version: d.Version,
		Summary: d.Description,
		Overlay: true,
	}
}

// mergeNotable appends the overlay candidates to the registry notable list with
// overlay precedence on a shared id, tags each entry's §4.5.5 notable source
// (featured wins), orders featured entries first, and caps the result when the
// overlay DOMAIN.md sets a notable_count. The registry already capped its own
// stream; overlay additions extend it unless an overlay notable_count applies.
func mergeNotable(reg, candidates []ldArtifact, featured map[string]bool, cap int) []ldArtifact {
	out := make([]ldArtifact, 0, len(reg)+len(candidates))
	idx := map[string]int{}
	for _, a := range reg {
		idx[a.ID] = len(out)
		out = append(out, a)
	}
	for _, c := range candidates {
		if i, ok := idx[c.ID]; ok {
			out[i] = c // overlay precedence shadows the registry descriptor
			continue
		}
		idx[c.ID] = len(out)
		out = append(out, c)
	}
	for i := range out {
		if featured[out[i].ID] {
			out[i].Source = "featured"
		} else if out[i].Source == "" {
			out[i].Source = "signal"
		}
	}
	feat := make([]ldArtifact, 0, len(out))
	rest := make([]ldArtifact, 0, len(out))
	for _, a := range out {
		if a.Source == "featured" {
			feat = append(feat, a)
		} else {
			rest = append(rest, a)
		}
	}
	merged := append(feat, rest...)
	if cap > 0 && len(merged) > cap {
		merged = merged[:cap]
	}
	return merged
}

// mergedFeatured is the union of the registry's featured ids (notable entries
// already tagged source: featured) and the overlay DOMAIN.md featured list
// (§4.5.4 featured append-unique).
func mergedFeatured(reg []ldArtifact, od *manifest.Domain) map[string]bool {
	m := map[string]bool{}
	for _, a := range reg {
		if a.Source == "featured" {
			m[a.ID] = true
		}
	}
	if od != nil && od.Discovery != nil {
		for _, f := range od.Discovery.Featured {
			m[f] = true
		}
	}
	return m
}

// notableCap returns the overlay DOMAIN.md notable_count, or 0 for no extra cap.
func notableCap(od *manifest.Domain) int {
	if od != nil && od.Discovery != nil {
		return od.Discovery.NotableCount
	}
	return 0
}

// pruneUnlisted recursively drops every subdomain the overlay marks unlisted
// (the path or an ancestor carries unlisted: true), per §4.5.3.
func pruneUnlisted(subs []ldSubdomain, domains map[string]*manifest.Domain) []ldSubdomain {
	out := make([]ldSubdomain, 0, len(subs))
	for _, sd := range subs {
		if unlistedOverlay(sd.Path, domains) {
			continue
		}
		sd.Subdomains = pruneUnlisted(sd.Subdomains, domains)
		out = append(out, sd)
	}
	return out
}

// unlistedOverlay reports whether path resolves under an overlay-unlisted
// folder (the path or any ancestor has an overlay DOMAIN.md with unlisted:
// true). Mirrors core.unlistedAt; the root ("") carries no DOMAIN.md.
func unlistedOverlay(path string, domains map[string]*manifest.Domain) bool {
	for p := path; p != ""; p = parentOf(p) {
		if d := domains[p]; d != nil && d.Unlisted {
			return true
		}
	}
	return false
}

// overlayChildDescription resolves a child/sibling subdomain's §4.5.5
// description: the overlay DOMAIN.md frontmatter description if set, otherwise
// the synthesized basename fallback. The prose body is never returned for a
// non-requested entry.
func overlayChildDescription(path string, domains map[string]*manifest.Domain) string {
	if d := domains[path]; d != nil && d.Description != "" {
		return d.Description
	}
	return domainpkg.FallbackDescription(path)
}

// renderDepth reads the caller's requested depth, falling back to the §4.5.5
// default when unset. Accepts the float64/int/string forms an MCP arg may take.
func renderDepth(args map[string]any) int {
	switch v := args["depth"].(type) {
	case float64:
		if int(v) > 0 {
			return int(v)
		}
	case int:
		if v > 0 {
			return v
		}
	case string:
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return defaultRenderDepth
}

// globLiteralPrefix returns the leading literal path segments of a §4.5.2 glob,
// stopping at the first segment that carries a glob metacharacter. It scopes a
// catalog fetch to the slice an include pattern can match ("finance/ap/*" ->
// "finance/ap"); a leading glob yields "" (the whole catalog).
func globLiteralPrefix(pattern string) string {
	out := []string{}
	for _, seg := range strings.Split(pattern, "/") {
		if strings.ContainsAny(seg, "*{},") {
			break
		}
		out = append(out, seg)
	}
	return strings.Join(out, "/")
}

// uniqueAppend appends src to dst, dropping values already present, preserving
// order (§4.5.4 append-unique).
func uniqueAppend(dst, src []string) []string {
	seen := make(map[string]bool, len(dst)+len(src))
	for _, v := range dst {
		seen[v] = true
	}
	for _, v := range src {
		if !seen[v] {
			seen[v] = true
			dst = append(dst, v)
		}
	}
	return dst
}

// parentOf returns the parent domain path of a canonical id ("" for a
// top-level id).
func parentOf(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return ""
}

// joinSeg joins a domain path and a child segment.
func joinSeg(path, seg string) string {
	if path == "" {
		return seg
	}
	return path + "/" + seg
}

// underRest returns id's remainder beyond prefix when id is at or under prefix
// (segment-aligned), or "" otherwise. An empty prefix returns id unchanged.
func underRest(id, prefix string) string {
	if prefix == "" {
		return id
	}
	if !strings.HasPrefix(id, prefix) {
		return ""
	}
	if len(id) == len(prefix) {
		return ""
	}
	if id[len(prefix)] != '/' {
		return ""
	}
	return id[len(prefix)+1:]
}
