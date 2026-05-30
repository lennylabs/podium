// Package core implements the meta-tool operations against a Store
// (spec §2.2 shared library code, §5 meta-tools).
//
// The core sits between the storage layer (Store) and the consumer
// surfaces (HTTP server, MCP server, SDK, sync). It exposes typed
// methods for load_domain, search_domains, search_artifacts, and
// load_artifact, applies visibility filtering when an Identity is
// supplied (§4.6), and resolves "latest" version per §4.7.6.
package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode"

	domainpkg "github.com/lennylabs/podium/pkg/domain"
	"github.com/lennylabs/podium/pkg/embedding"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
	"github.com/lennylabs/podium/pkg/version"
)

// Errors returned by the core. Tests assert via errors.Is. Codes align
// with §6.10.
var (
	// ErrNotFound is returned when a resource is missing
	// (registry.not_found in §6.10).
	ErrNotFound = errors.New("registry.not_found")
	// ErrInvalidArgument signals a malformed argument
	// (registry.invalid_argument).
	ErrInvalidArgument = errors.New("registry.invalid_argument")
	// ErrDomainNotFound (domain.not_found) is returned by LoadDomain
	// for unknown paths.
	ErrDomainNotFound = errors.New("domain.not_found")
	// ErrUnavailable wraps store-layer failures so callers see the
	// documented §6.10 namespace. Maps to registry.unavailable.
	ErrUnavailable = errors.New("registry.unavailable")
)

// Registry is the core registry type. Construct one per tenant; the
// caller passes Identity in per request to drive visibility filtering.
type Registry struct {
	store      store.Store
	tenantID   string
	layers     []layer.Layer
	audit      AuditEmitter
	sessionsMu sync.Mutex
	sessions   map[sessionKey]string
	// vector and embedder enable §4.7 hybrid retrieval. When both are
	// set, SearchArtifacts blends BM25 with vector cosine via RRF.
	// When either is nil, search degrades to BM25-only and the
	// SearchResult carries Degraded=true so consumers can surface
	// the reduced fidelity.
	vector   vector.Provider
	embedder embedding.Provider
	// resolveGroup expands a layer's `groups:` filter via the §6.3.1
	// SCIM membership store. Nil disables expansion: visibility falls
	// back to JWT-claim group matching only.
	resolveGroup layer.GroupResolver
	// notifier delivers §9 operational notifications (ingest
	// failure, transparency-anchor failure, etc.). Nil = no
	// outbound notifications.
	notifier NotificationFunc
}

// NotificationFunc fires when the registry observes an operational
// event worth alerting an operator on. Wrappers exist in
// pkg/notification (Webhook, LogProvider, MultiProvider).
type NotificationFunc func(ctx context.Context, severity, title, body string, tags map[string]string)

// sessionKey identifies one (session, artifact) latest-resolution
// memo entry. §4.7.6: "the first latest lookup within a session is
// recorded and reused for all subsequent same-id lookups in that
// session".
type sessionKey struct {
	session string
	id      string
}

// AuditEmitter records audit events for every meta-tool call per §8.
// The default is a no-op; callers wire pkg/audit.Sink to persist.
type AuditEmitter func(ctx context.Context, e AuditEvent)

// AuditEvent is one structured event the registry emits. Maps to a
// pkg/audit.Event in callers that wire one in.
type AuditEvent struct {
	Type    string
	Caller  string
	Target  string
	Context map[string]string
}

// New returns a Registry backed by the given store, tenant, and layer
// list. The layer list governs visibility filtering (§4.6); empty means
// every record is visible.
func New(s store.Store, tenantID string, layers []layer.Layer) *Registry {
	return &Registry{store: s, tenantID: tenantID, layers: layers}
}

// WithAudit attaches an AuditEmitter so every meta-tool invocation
// produces an audit event per §8.1.
func (r *Registry) WithAudit(emit AuditEmitter) *Registry {
	r.audit = emit
	return r
}

// WithVectorSearch attaches the §4.7 hybrid-retrieval pieces.
// SearchArtifacts will RRF-fuse BM25 ranks with vector cosine ranks
// when both the vector store and the embedder are non-nil. Either
// argument left nil disables the vector path; search continues to
// work BM25-only and reports Degraded=true.
func (r *Registry) WithVectorSearch(v vector.Provider, e embedding.Provider) *Registry {
	r.vector = v
	r.embedder = e
	return r
}

// WithNotifier wires the §9 NotificationProvider so the registry
// can fire operational notifications (ingest failure, anchor
// failure) without callers having to subscribe to the audit log.
func (r *Registry) WithNotifier(fn NotificationFunc) *Registry {
	r.notifier = fn
	return r
}

// Notify is the registry-internal entry point that forwards to the
// configured notifier (no-op when none is wired). Exported so
// neighboring packages (ingest, audit anchoring) can fire events
// through the same channel.
func (r *Registry) Notify(ctx context.Context, severity, title, body string, tags map[string]string) {
	if r.notifier == nil {
		return
	}
	r.notifier(ctx, severity, title, body, tags)
}

// WithGroupResolver wires a §6.3.1 SCIM-backed expander so the
// visibility evaluator turns a layer's `groups:` filter into the
// underlying user set. Without one, visibility checks the
// identity's JWT claim list directly.
func (r *Registry) WithGroupResolver(fn layer.GroupResolver) *Registry {
	r.resolveGroup = fn
	return r
}

// VectorStore returns the configured vector store, or nil. Used by
// the ingest pipeline to upsert embeddings on content-hash change.
func (r *Registry) VectorStore() vector.Provider { return r.vector }

// Embedder returns the configured embedding provider, or nil. Used
// by the ingest pipeline.
func (r *Registry) Embedder() embedding.Provider { return r.embedder }

// emit fires the configured audit emitter, if any.
func (r *Registry) emit(ctx context.Context, e AuditEvent) {
	if r.audit == nil {
		return
	}
	r.audit(ctx, e)
}

// ----- LoadDomain ----------------------------------------------------------

// LoadDomainResult is the return value of LoadDomain.
type LoadDomainResult struct {
	Path        string
	Description string
	Keywords    []string
	Subdomains  []DomainDescriptor
	Notable     []ArtifactDescriptor
	Note        string
}

// DomainDescriptor describes one subdomain entry.
type DomainDescriptor struct {
	Path        string
	Name        string
	Description string
	// Subdomains is the nested child tree rendered when load_domain
	// expands more than one level (§4.5.5 depth). It is empty for leaf
	// subdomains and for entries at the deepest rendered level. Each
	// nested entry carries its short description only; bodies are
	// returned solely for the originally requested domain.
	Subdomains []DomainDescriptor
}

// ArtifactDescriptor describes one artifact entry. Used by LoadDomain's
// notable list and by the search responses.
type ArtifactDescriptor struct {
	ID          string
	Type        string
	Version     string
	Description string
	Tags        []string
	Score       float64
	// FoldedFrom records the relative subpath an artifact was lifted
	// from when fold_below_artifacts collapsed its sparse subdomain
	// into the parent's leaf set (§4.5.5 folding mechanics). Empty
	// when the artifact was a direct child of the requested domain.
	FoldedFrom string
}

// LoadDomainOptions are the optional knobs from §5.
type LoadDomainOptions struct {
	// Depth requests a deeper map than the configured default
	// (§4.5.5). A value of 0 uses the registry default; values
	// exceeding the resolved max_depth ceiling are capped.
	Depth int
	// NotableCount caps the notable list per §4.5.5; 0 uses the default.
	NotableCount int
	// Featured artifact IDs are surfaced first in the notable list.
	Featured []string
	// FoldBelowArtifacts collapses subdomains whose recursive visible
	// artifact count is below the threshold into the parent's leaf
	// set per §4.5.5. 0 (default) disables folding.
	FoldBelowArtifacts int
	// FoldPassthroughChains collapses single-child intermediate
	// domains into the deepest non-passthrough descendant per
	// §4.5.5. nil treats as the §4.5.5 default of true; pass a
	// pointer to false to opt out.
	FoldPassthroughChains *bool
	// TargetResponseTokens is the §4.5.5 soft response budget. The
	// renderer tightens notable count to fit, surfacing the reduction
	// in the rendering note. 0 disables budget enforcement.
	TargetResponseTokens int
}

// DefaultMaxDepth is the §4.5.5 tenant default.
const (
	DefaultMaxDepth     = 3
	DefaultNotableCount = 10
)

// LoadDomain returns the domain map for path. An empty path returns
// the registry root.
//
// Visibility: artifacts whose Layer is invisible to id are excluded.
// In standalone mode (no identity provider), pass an empty Identity
// with IsPublic=true to bypass visibility filtering, mirroring the
// §13.10 / §13.11 behavior.
func (r *Registry) LoadDomain(ctx context.Context, id layer.Identity, path string, opts LoadDomainOptions) (*LoadDomainResult, error) {
	r.emit(ctx, AuditEvent{
		Type:   "domain.loaded",
		Caller: callerOf(id),
		Target: path,
	})
	visible, err := r.visibleManifests(ctx, id)
	if err != nil {
		return nil, err
	}
	// §4.5.1 / §4.5.4 — load every visible DOMAIN.md and merge the
	// candidates for each path across layers. The merged set drives
	// description, keywords, unlisted, imports, and per-domain
	// discovery overrides below.
	merged, err := r.mergedDomains(ctx, id)
	if err != nil {
		return nil, err
	}

	// §4.5.3 / §4.5.5 unlisted: a path that resolves only under an
	// unlisted folder is indistinguishable from a typo and returns
	// domain.not_found, so unlisted folders are not detectable through
	// enumeration probing.
	if path != "" && unlistedAt(path, merged) {
		return nil, fmt.Errorf("%w: %s", ErrDomainNotFound, path)
	}

	requested := merged[path]
	knobs := resolveKnobs(opts, requested)

	// §4.5.5 caller overrides: depth overrides the configured default
	// (the resolved max_depth ceiling) and is silently capped at it.
	renderDepth := knobs.maxDepth
	depthCapped := false
	if opts.Depth > 0 {
		renderDepth = opts.Depth
		if renderDepth > knobs.maxDepth {
			renderDepth = knobs.maxDepth
			depthCapped = true
		}
	}

	res := &LoadDomainResult{Path: path}

	// §4.5.5 description rendering and keywords. The root has no
	// DOMAIN.md: description is omitted and keywords is the empty list.
	if path == "" {
		res.Keywords = []string{}
	} else {
		res.Description = requestedDescription(requested, path)
		res.Keywords = knobs.keywords
	}

	under := manifestsUnder(visible, path)
	allIDs := manifestIDs(visible)
	byID := latestByID(visible)

	// §4.5.5 candidate pool for the notable list: the requested
	// domain's direct artifacts plus those brought in by include:
	// (after exclude:), deduplicated by canonical ID.
	notable := make([]ArtifactDescriptor, 0)
	seen := map[string]bool{}
	addCandidate := func(d ArtifactDescriptor) {
		if seen[d.ID] {
			return
		}
		seen[d.ID] = true
		notable = append(notable, d)
	}
	for _, m := range dedupeLatest(directArtifactsOf(under, path)) {
		if domainpkg.MatchAny(knobs.exclude, m.ArtifactID) {
			continue
		}
		addCandidate(descriptorOf(m))
	}
	for _, iid := range domainpkg.ResolveImports(knobs.include, knobs.exclude, allIDs) {
		if rec, ok := byID[iid]; ok {
			addCandidate(descriptorOf(rec))
		}
	}

	// Immediate children: fold sparse subdomains into the notable list
	// (§4.5.5 folding mechanics) and render the rest as the subdomain
	// tree, dropping any unlisted subtree.
	groups := groupByImmediateChild(under, path)
	childNames := make([]string, 0, len(groups))
	for name := range groups {
		childNames = append(childNames, name)
	}
	sort.Strings(childNames)
	for _, name := range childNames {
		childPath := joinPath(path, name)
		if unlistedAt(childPath, merged) {
			continue // §4.5.3 unlisted subtree removed from enumeration
		}
		recursiveCount := visibleCount(groups[name])
		if knobs.foldBelow > 0 && recursiveCount < knobs.foldBelow {
			for _, m := range dedupeLatest(groups[name]) {
				if domainpkg.MatchAny(knobs.exclude, m.ArtifactID) {
					continue
				}
				d := descriptorOf(m)
				d.FoldedFrom = stripPrefix(parentPath(m.ArtifactID), path)
				if d.FoldedFrom == "" {
					d.FoldedFrom = name
				}
				addCandidate(d)
			}
			continue
		}
		renderedPath := childPath
		if knobs.foldPassthrough {
			renderedPath = collapsePassthroughChain(under, childPath)
		}
		if unlistedAt(renderedPath, merged) {
			continue // collapsed through an unlisted intermediate
		}
		res.Subdomains = append(res.Subdomains, DomainDescriptor{
			Path:        renderedPath,
			Name:        lastSegment(renderedPath),
			Description: childDescription(renderedPath, merged),
			Subdomains:  r.renderSubtree(under, merged, renderedPath, renderDepth-1, knobs.foldPassthrough),
		})
	}
	sort.Slice(res.Subdomains, func(i, j int) bool {
		return res.Subdomains[i].Path < res.Subdomains[j].Path
	})

	notable = orderNotable(notable, knobs.featured, knobs.deprioritize)
	originalNotable := len(notable)
	if len(notable) > knobs.notableCount {
		notable = notable[:knobs.notableCount]
	}

	// §4.5.5 target_response_tokens: if the soft budget is set and the
	// estimated response exceeds it, tighten the notable list further.
	tightenedTo := -1
	if knobs.targetResponseTokens > 0 {
		budget := knobs.targetResponseTokens
		for len(notable) > 0 && estimateResponseTokens(res.Subdomains, notable) > budget {
			notable = notable[:len(notable)-1]
			tightenedTo = len(notable)
		}
	}
	res.Notable = notable

	// §4.5.5 rendering note: cover the budget reduction and the
	// depth-cap cases. Folding decisions are not surfaced — folded
	// artifacts already carry FoldedFrom in the response.
	notes := []string{}
	if tightenedTo >= 0 {
		notes = append(notes, fmt.Sprintf(
			"Notable list reduced from %d to %d to fit the response budget.",
			originalNotable, tightenedTo,
		))
	}
	if depthCapped {
		notes = append(notes, fmt.Sprintf(
			"Requested depth %d capped at the configured ceiling of %d.",
			opts.Depth, knobs.maxDepth,
		))
	}
	res.Note = strings.Join(notes, " ")

	// §4.5.5 unknown paths: a non-root path with no subdomains, no
	// notable artifacts, and no DOMAIN.md does not resolve to a visible
	// domain.
	if path != "" && len(res.Subdomains) == 0 && len(res.Notable) == 0 && requested == nil {
		return nil, fmt.Errorf("%w: %s", ErrDomainNotFound, path)
	}
	return res, nil
}

// manifestsUnder returns the subset of manifests whose ArtifactID is
// strictly within prefix (excludes records that are not under the
// path at all). An empty prefix returns the input.
func manifestsUnder(all []store.ManifestRecord, prefix string) []store.ManifestRecord {
	if prefix == "" {
		return all
	}
	out := make([]store.ManifestRecord, 0, len(all))
	for _, m := range all {
		if inPrefix(m.ArtifactID, prefix) {
			out = append(out, m)
		}
	}
	return out
}

// groupByImmediateChild groups manifests under prefix by the first
// path segment beyond prefix. Direct children of prefix (no trailing
// segments) are not included; use directArtifactsOf for those.
func groupByImmediateChild(under []store.ManifestRecord, prefix string) map[string][]store.ManifestRecord {
	groups := map[string][]store.ManifestRecord{}
	for _, m := range under {
		if !inPrefix(m.ArtifactID, prefix) {
			continue
		}
		rest := stripPrefix(m.ArtifactID, prefix)
		if rest == "" || !strings.Contains(rest, "/") {
			continue
		}
		first := strings.SplitN(rest, "/", 2)[0]
		groups[first] = append(groups[first], m)
	}
	return groups
}

// directArtifactsOf returns manifests directly under prefix (no
// further nesting). The inPrefix guard keeps it correct when callers
// pass a record set scoped to an ancestor of prefix (the recursive
// subtree renderer reuses one slice across nested levels).
func directArtifactsOf(under []store.ManifestRecord, prefix string) []store.ManifestRecord {
	out := make([]store.ManifestRecord, 0, len(under))
	for _, m := range under {
		if !inPrefix(m.ArtifactID, prefix) {
			continue
		}
		rest := stripPrefix(m.ArtifactID, prefix)
		if rest != "" && !strings.Contains(rest, "/") {
			out = append(out, m)
		}
	}
	return out
}

// collapsePassthroughChain walks down a single-child chain that has
// no direct artifacts at the current level, returning the deepest
// non-passthrough path. Used by §4.5.5 fold_passthrough_chains.
func collapsePassthroughChain(under []store.ManifestRecord, path string) string {
	for {
		if len(directArtifactsOf(under, path)) > 0 {
			return path
		}
		groups := groupByImmediateChild(under, path)
		if len(groups) != 1 {
			return path
		}
		var only string
		for k := range groups {
			only = k
		}
		path = joinPath(path, only)
	}
}

// estimateResponseTokens approximates the token cost of a load_domain
// response for the §4.5.5 budget check. The estimate stays
// dependency-free; it counts identifier and description characters
// divided by the typical 4-bytes-per-token ratio.
func estimateResponseTokens(subs []DomainDescriptor, notable []ArtifactDescriptor) int {
	bytes := 0
	for _, s := range subs {
		bytes += len(s.Path) + len(s.Name) + len(s.Description) + 16
	}
	for _, n := range notable {
		bytes += len(n.ID) + len(n.Description) + len(n.Type) + 32
		for _, t := range n.Tags {
			bytes += len(t) + 4
		}
	}
	return bytes / 4
}

// orderNotable surfaces featured IDs first (in author-supplied order),
// then the remaining notable artifacts in alphabetical ID order, with
// any artifact matching a deprioritize glob ranked last (§4.5.5). Per
// §4.5.5 deduplication an artifact appearing in both featured and the
// alphabetical list keeps its featured position; featured always wins
// over deprioritize.
func orderNotable(notable []ArtifactDescriptor, featured, deprioritize []string) []ArtifactDescriptor {
	byID := map[string]ArtifactDescriptor{}
	for _, n := range notable {
		byID[n.ID] = n
	}
	out := make([]ArtifactDescriptor, 0, len(notable))
	used := map[string]bool{}
	for _, id := range featured {
		if d, ok := byID[id]; ok && !used[id] {
			out = append(out, d)
			used[id] = true
		}
	}
	rest := make([]ArtifactDescriptor, 0, len(notable))
	low := make([]ArtifactDescriptor, 0)
	for _, n := range notable {
		if used[n.ID] {
			continue
		}
		if len(deprioritize) > 0 && domainpkg.MatchAny(deprioritize, n.ID) {
			low = append(low, n)
			continue
		}
		rest = append(rest, n)
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i].ID < rest[j].ID })
	sort.Slice(low, func(i, j int) bool { return low[i].ID < low[j].ID })
	out = append(out, rest...)
	return append(out, low...)
}

// ----- SearchArtifacts ----------------------------------------------------

// SearchArtifactsOptions captures the §5 search_artifacts arguments.
type SearchArtifactsOptions struct {
	Query string
	Type  string
	Scope string
	Tags  []string
	TopK  int
	// IncludeDeprecated opts deprecated artifacts back into the
	// result set. Default search excludes them per §4.7.4.
	IncludeDeprecated bool
}

// SearchResult is the common envelope for both search functions.
type SearchResult struct {
	Query        string
	TotalMatched int
	Results      []ArtifactDescriptor
	Domains      []DomainDescriptor
	// Degraded is true when vector search was configured but failed
	// (provider unreachable, embedder error) or unconfigured;
	// search returned BM25-only ranks. Consumers surface this so
	// callers know the result quality is lexical-only.
	Degraded bool
}

// SearchArtifacts runs the §5 search over manifests visible to id.
//
// When a vector store and embedder are configured (WithVectorSearch),
// the implementation runs BM25 + vector cosine in parallel and
// fuses the rankings via RRF (§4.7). Otherwise it falls back to
// BM25-only and sets SearchResult.Degraded=true.
func (r *Registry) SearchArtifacts(ctx context.Context, id layer.Identity, opts SearchArtifactsOptions) (*SearchResult, error) {
	r.emit(ctx, AuditEvent{
		Type:    "artifacts.searched",
		Caller:  callerOf(id),
		Context: map[string]string{"query": opts.Query, "scope": opts.Scope, "type": opts.Type},
	})
	if opts.TopK > 50 {
		return nil, fmt.Errorf("%w: top_k > 50", ErrInvalidArgument)
	}
	if opts.TopK <= 0 {
		opts.TopK = 10
	}

	visible, err := r.visibleManifests(ctx, id)
	if err != nil {
		return nil, err
	}

	// Filter by type / scope / tags first.
	filtered := visible[:0:0]
	for _, m := range visible {
		if opts.Type != "" && m.Type != opts.Type {
			continue
		}
		if opts.Scope != "" && !inPrefix(m.ArtifactID, opts.Scope) {
			continue
		}
		if !tagsMatch(m.Tags, opts.Tags) {
			continue
		}
		// §4.7.4 lifecycle: deprecated artifacts are excluded from
		// default search results. Callers can opt in via
		// IncludeDeprecated when surfacing audit / migration views.
		if m.Deprecated && !opts.IncludeDeprecated {
			continue
		}
		// §4.3 / §4.5.3 search_visibility: a direct-only artifact does
		// not appear in search_artifacts results; it stays reachable
		// via load_artifact for a caller that knows its ID.
		if m.SearchVisibility == string(manifest.SearchVisibilityDirectOnly) {
			continue
		}
		filtered = append(filtered, m)
	}

	// Lexical: BM25 over manifest text. Empty query returns matched
	// records with score 0 (alphabetical by id).
	scored := scoreBM25(filtered, opts.Query)
	totalMatched := len(scored)

	// Vector: when configured + non-empty query, compute query
	// embedding, fetch top-K nearest by cosine, and RRF-fuse with the
	// BM25 ranks. Failures degrade to BM25-only without erroring.
	degraded := r.vector == nil || r.embedder == nil
	if !degraded && opts.Query != "" {
		vecRanks, vecErr := r.vectorRanks(ctx, opts.Query, filtered, opts.TopK)
		if vecErr != nil {
			degraded = true
		} else if len(vecRanks) > 0 {
			lexIDs := scoredIDs(scored)
			fused := RRFFuse(lexIDs, vecRanks)
			scored = reorderScored(scored, fused)
		}
	}

	res := &SearchResult{
		Query:        opts.Query,
		TotalMatched: totalMatched,
		Degraded:     degraded,
	}
	if len(scored) > opts.TopK {
		scored = scored[:opts.TopK]
	}
	res.Results = make([]ArtifactDescriptor, 0, len(scored))
	for _, sc := range scored {
		d := descriptorOf(sc.rec)
		d.Score = sc.score
		res.Results = append(res.Results, d)
	}
	return res, nil
}

// vectorRanks embeds the query and returns the top-K nearest
// artifact IDs from the vector store, restricted to the visible
// candidate set so a leaked vector outside the caller's view never
// surfaces.
func (r *Registry) vectorRanks(ctx context.Context, query string, candidates []store.ManifestRecord, topK int) ([]string, error) {
	vecs, err := r.embedder.Embed(ctx, []string{query})
	if err != nil || len(vecs) == 0 {
		return nil, fmt.Errorf("embed: %w", err)
	}
	matches, err := r.vector.Query(ctx, r.tenantID, vecs[0], topK*4)
	if err != nil {
		return nil, fmt.Errorf("vector: %w", err)
	}
	candidateIDs := map[string]bool{}
	for _, c := range candidates {
		candidateIDs[c.ArtifactID] = true
	}
	out := make([]string, 0, len(matches))
	seen := map[string]bool{}
	for _, m := range matches {
		if !candidateIDs[m.ArtifactID] || seen[m.ArtifactID] {
			continue
		}
		seen[m.ArtifactID] = true
		out = append(out, m.ArtifactID)
	}
	return out, nil
}

// scoredIDs flattens a BM25 score list to artifact IDs in rank order.
func scoredIDs(scored []scoredRecord) []string {
	out := make([]string, len(scored))
	for i, sc := range scored {
		out[i] = sc.rec.ArtifactID
	}
	return out
}

// reorderScored reorders the BM25 score slice to match the order of
// fused IDs, preserving any items that survive the fusion. Items
// that aren't in fused (because they came from neither list, which
// can't happen here) are dropped.
func reorderScored(scored []scoredRecord, fused []string) []scoredRecord {
	byID := map[string]scoredRecord{}
	for _, sc := range scored {
		byID[sc.rec.ArtifactID] = sc
	}
	out := make([]scoredRecord, 0, len(fused))
	for _, id := range fused {
		if sc, ok := byID[id]; ok {
			out = append(out, sc)
		}
	}
	return out
}

// ----- SearchDomains ------------------------------------------------------

// SearchDomainsOptions captures the §5 search_domains arguments.
type SearchDomainsOptions struct {
	Query string
	Scope string
	TopK  int
}

// SearchDomains returns ranked domain descriptors. Phase 12 enriches
// this with §4.5.5 keyword projections; for now, domains are derived
// from the unique paths of visible manifests.
func (r *Registry) SearchDomains(ctx context.Context, id layer.Identity, opts SearchDomainsOptions) (*SearchResult, error) {
	r.emit(ctx, AuditEvent{
		Type:    "domains.searched",
		Caller:  callerOf(id),
		Context: map[string]string{"query": opts.Query, "scope": opts.Scope},
	})
	if opts.TopK > 50 {
		return nil, fmt.Errorf("%w: top_k > 50", ErrInvalidArgument)
	}
	if opts.TopK <= 0 {
		opts.TopK = 10
	}

	visible, err := r.visibleManifests(ctx, id)
	if err != nil {
		return nil, err
	}

	domainSet := map[string]bool{}
	for _, m := range visible {
		if opts.Scope != "" && !inPrefix(m.ArtifactID, opts.Scope) {
			continue
		}
		dir := parentPath(m.ArtifactID)
		if dir == "" {
			continue
		}
		// All ancestor paths qualify as domains.
		for p := dir; p != ""; p = parentPath(p) {
			domainSet[p] = true
		}
	}
	q := strings.ToLower(opts.Query)
	matched := make([]string, 0, len(domainSet))
	for p := range domainSet {
		if q != "" && !strings.Contains(strings.ToLower(p), q) {
			continue
		}
		matched = append(matched, p)
	}
	sort.Strings(matched)
	res := &SearchResult{Query: opts.Query, TotalMatched: len(matched)}
	if len(matched) > opts.TopK {
		matched = matched[:opts.TopK]
	}
	for _, p := range matched {
		res.Domains = append(res.Domains, DomainDescriptor{
			Path: p, Name: lastSegment(p),
		})
	}
	return res, nil
}

// ----- LoadArtifact -------------------------------------------------------

// LoadArtifactResult bundles the manifest body and resources for the
// requested artifact (§5).
type LoadArtifactResult struct {
	ID           string
	Type         string
	Version      string
	ContentHash  string
	ManifestBody string
	Frontmatter  []byte
	Layer        string
	Resources    map[string][]byte
	Sensitivity  string
	// Deprecated reports whether the resolved manifest was marked
	// deprecated at ingest. Per §4.7.4 the registry continues to
	// serve deprecated artifacts but surfaces a warning.
	Deprecated bool
	// ReplacedBy carries the §4.7.4 upgrade target when the
	// deprecated artifact's manifest names one. Empty when not set.
	ReplacedBy string
	// DeprecationWarning is the human-readable warning the registry
	// emits when serving a deprecated artifact, per §4.7.4. Empty
	// when the artifact is live.
	DeprecationWarning string
	// Signature is the §4.7.9 envelope produced at ingest by the
	// configured SignatureProvider. Empty when ingest had no
	// signer wired. Consumers verify via sign.EnforceVerification
	// against PODIUM_VERIFY_SIGNATURES.
	Signature string
}

// LoadArtifactOptions captures §5 arguments. Empty Version means
// "latest" per §4.7.6.
type LoadArtifactOptions struct {
	Version string
	// SessionID enables consistent latest resolution within a session
	// (§4.7.6). The first latest lookup within a session is recorded
	// and reused for all subsequent same-id lookups in the session.
	SessionID string
}

// LoadArtifact returns the manifest body. When the resolved manifest
// declares extends:, the parent is fetched (server-side, hidden-parent
// semantics per §4.6) and field-merged per the §4.6 merge-semantics
// table. The returned body and frontmatter are the merged result.
func (r *Registry) LoadArtifact(ctx context.Context, id layer.Identity, artifactID string, opts LoadArtifactOptions) (*LoadArtifactResult, error) {
	r.emit(ctx, AuditEvent{
		Type:    "artifact.loaded",
		Caller:  callerOf(id),
		Target:  artifactID,
		Context: map[string]string{"version": opts.Version},
	})
	visible, err := r.visibleManifests(ctx, id)
	if err != nil {
		return nil, err
	}
	candidates := make([]store.ManifestRecord, 0, 4)
	for _, m := range visible {
		if m.ArtifactID == artifactID {
			candidates = append(candidates, m)
		}
	}
	if len(candidates) == 0 {
		// §8.1 visibility.denied: the artifact ID exists in the
		// store but its layer is not in the caller's effective
		// view. Emitting before the not-found return lets SIEM
		// pipelines tell "missing" from "filtered."
		if r.artifactExistsAnywhere(ctx, artifactID) {
			r.emit(ctx, AuditEvent{
				Type:   "visibility.denied",
				Caller: callerOf(id),
				Target: artifactID,
			})
		}
		return nil, fmt.Errorf("%w: artifact %s", ErrNotFound, artifactID)
	}
	pin, err := version.ParsePin(opts.Version)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	versions := make([]string, 0, len(candidates))
	byVersion := map[string]store.ManifestRecord{}
	for _, c := range candidates {
		if pin.Kind == version.PinContentHash {
			if c.ContentHash == "sha256:"+pin.Hash {
				return r.assembleResult(ctx, c)
			}
			continue
		}
		versions = append(versions, c.Version)
		byVersion[c.Version] = c
	}
	if pin.Kind == version.PinContentHash {
		return nil, fmt.Errorf("%w: no version with content hash sha256:%s", ErrNotFound, pin.Hash)
	}

	// §4.7.6 — `latest` resolves to the most recently ingested
	// non-deprecated version. Build a deprecation-filtered list;
	// fall back to the full list when every version is
	// deprecated so callers still get bytes (with the warning).
	resolveVersions := versions
	if pin.Kind == version.PinLatest {
		live := make([]string, 0, len(versions))
		for _, v := range versions {
			if rec, ok := byVersion[v]; ok && !rec.Deprecated {
				live = append(live, v)
			}
		}
		if len(live) > 0 {
			resolveVersions = live
		}
	}

	// §4.7.6 session-consistent latest: the first latest lookup within
	// a session pins, subsequent same-id lookups in the session
	// resolve to the same version regardless of newer ingests.
	if pin.Kind == version.PinLatest && opts.SessionID != "" {
		if pinned, ok := r.lookupSessionPin(opts.SessionID, artifactID); ok {
			if rec, ok := byVersion[pinned]; ok {
				return r.assembleResult(ctx, rec)
			}
		}
	}

	resolved, err := version.Resolve(pin, resolveVersions)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	if pin.Kind == version.PinLatest && opts.SessionID != "" {
		r.recordSessionPin(opts.SessionID, artifactID, resolved)
	}
	rec := byVersion[resolved]
	return r.assembleResult(ctx, rec)
}

// lookupSessionPin returns the version a session previously resolved
// to for artifactID, if any.
func (r *Registry) lookupSessionPin(session, id string) (string, bool) {
	r.sessionsMu.Lock()
	defer r.sessionsMu.Unlock()
	if r.sessions == nil {
		return "", false
	}
	v, ok := r.sessions[sessionKey{session: session, id: id}]
	return v, ok
}

// recordSessionPin remembers the version this session resolved to so
// later calls see the same answer (§4.7.6).
func (r *Registry) recordSessionPin(session, id, ver string) {
	r.sessionsMu.Lock()
	defer r.sessionsMu.Unlock()
	if r.sessions == nil {
		r.sessions = map[sessionKey]string{}
	}
	r.sessions[sessionKey{session: session, id: id}] = ver
}

// assembleResult turns one manifest record into a LoadArtifactResult.
// When the record declares ExtendsPin, the parent is loaded
// (privilege-bypassing the visibility filter — hidden-parent semantics
// per §4.6) and field-merged. Cycle detection prevents infinite loops.
func (r *Registry) assembleResult(ctx context.Context, rec store.ManifestRecord) (*LoadArtifactResult, error) {
	if rec.ExtendsPin == "" {
		return withDeprecationWarning(resultFromRecord(rec)), nil
	}
	chain, err := r.resolveExtendsChain(ctx, rec, map[string]bool{})
	if err != nil {
		return nil, err
	}
	merged := mergeChain(chain)
	return withDeprecationWarning(resultFromRecord(merged)), nil
}

// resolveExtendsChain returns the chain of records starting at rec and
// walking each parent via ExtendsPin. Order: parent first (lowest
// precedence) ... rec (highest). Cycles are detected and produce an
// error to prevent infinite loops, matching §4.6 "Cycle detection at
// ingest time" — we re-check at load time as defense in depth.
func (r *Registry) resolveExtendsChain(ctx context.Context, rec store.ManifestRecord, seen map[string]bool) ([]store.ManifestRecord, error) {
	key := rec.ArtifactID + "@" + rec.Version
	if seen[key] {
		return nil, fmt.Errorf("%w: extends cycle at %s", ErrInvalidArgument, key)
	}
	seen[key] = true

	if rec.ExtendsPin == "" {
		return []store.ManifestRecord{rec}, nil
	}
	parentID, parentVer := splitParentRef(rec.ExtendsPin)
	parent, err := r.store.GetManifest(ctx, r.tenantID, parentID, parentVer)
	if err != nil {
		return nil, fmt.Errorf("%w: parent %s: %v", ErrNotFound, rec.ExtendsPin, err)
	}
	parentChain, err := r.resolveExtendsChain(ctx, parent, seen)
	if err != nil {
		return nil, err
	}
	return append(parentChain, rec), nil
}

// mergeChain folds the chain parent → child per the §4.6 field-semantics
// table. It parses each record's stored frontmatter into a
// manifest.Artifact, applies manifest.MergeExtends across the chain, and
// re-serializes the merged frontmatter so every consumer that reads the
// served frontmatter (not only the indexed Description/Tags/Sensitivity
// record fields) observes the merged result. The merged record keeps the
// child's identity (id, version, content hash, layer, body) and surfaces
// the merged scalar fields back onto the record for callers that read
// them directly. spec: §4.6 field-semantics table.
func mergeChain(chain []store.ManifestRecord) store.ManifestRecord {
	if len(chain) == 0 {
		return store.ManifestRecord{}
	}
	out := chain[0]
	merged := parsedArtifact(out)
	for _, c := range chain[1:] {
		m := manifest.MergeExtends(*merged, *parsedArtifact(c))
		merged = &m
		// Identity fields preserve the child's coordinates so callers
		// see the child rather than the parent.
		out.ArtifactID = c.ArtifactID
		out.Version = c.Version
		out.ContentHash = c.ContentHash
		out.Layer = c.Layer
		out.IngestedAt = c.IngestedAt
		out.ExtendsPin = c.ExtendsPin
		// The body takes the child's; the parent's prose is not
		// concatenated — extends inherits structured fields, not the
		// markdown body.
		if len(c.Body) > 0 {
			out.Body = c.Body
		}
	}
	// Surface the merged structured fields back onto the record so search
	// descriptors, sensitivity gating, and deprecation reporting agree
	// with the served frontmatter.
	out.Type = string(merged.Type)
	out.Description = merged.Description
	out.Tags = append([]string(nil), merged.Tags...)
	out.Sensitivity = string(merged.Sensitivity)
	out.SearchVisibility = string(merged.SearchVisibility)
	out.Deprecated = merged.Deprecated
	out.ReplacedBy = merged.ReplacedBy
	// Strip the extends reference from the served manifest: the merge has
	// been applied server-side and the parent's ID must not be surfaced to
	// the requester (§4.6 hidden parents).
	merged.Extends = ""
	if fm, err := manifest.SerializeArtifact(merged); err == nil {
		out.Frontmatter = fm
	}
	return out
}

// parsedArtifact decodes a record's stored frontmatter into a
// manifest.Artifact. Ingested records always parse (ingest parsed them);
// the indexed-field fallback is defense in depth for a malformed record.
func parsedArtifact(rec store.ManifestRecord) *manifest.Artifact {
	if a, err := manifest.ParseArtifact(rec.Frontmatter); err == nil && a != nil {
		return a
	}
	return &manifest.Artifact{
		Type:        manifest.ArtifactType(rec.Type),
		Version:     rec.Version,
		Description: rec.Description,
		Tags:        append([]string(nil), rec.Tags...),
		Sensitivity: manifest.Sensitivity(rec.Sensitivity),
	}
}

func mostRestrictiveSensitivity(a, b string) string {
	rank := func(s string) int {
		switch s {
		case "high":
			return 3
		case "medium":
			return 2
		case "low":
			return 1
		}
		return 0
	}
	if rank(b) > rank(a) {
		return b
	}
	return a
}

// splitParentRef splits "<id>@<version>" into its components. It splits on
// the first "@" so it parses the §4.2 reference grammar identically to the
// other split helpers (ingest.splitRef/stripPin, composer.SplitArtifactRef);
// canonical-ID segments may not contain "@" (filesystem.ValidateCanonicalID),
// so the suffix always begins at the first "@".
func splitParentRef(ref string) (id, ver string) {
	if i := strings.Index(ref, "@"); i >= 0 {
		return ref[:i], ref[i+1:]
	}
	return ref, ""
}

// callerOf renders the caller identity per §8.1. Public-mode (or
// anonymous) calls record "system:public" so SIEM filters can scope
// without parsing identity strings.
func callerOf(id layer.Identity) string {
	if id.IsPublic || !id.IsAuthenticated {
		return "system:public"
	}
	if id.Sub != "" {
		return id.Sub
	}
	return "system:public"
}

func resultFromRecord(rec store.ManifestRecord) *LoadArtifactResult {
	return &LoadArtifactResult{
		ID:           rec.ArtifactID,
		Type:         rec.Type,
		Version:      rec.Version,
		ContentHash:  rec.ContentHash,
		ManifestBody: string(rec.Body),
		Frontmatter:  rec.Frontmatter,
		Layer:        rec.Layer,
		Sensitivity:  rec.Sensitivity,
		Deprecated:   rec.Deprecated,
		ReplacedBy:   rec.ReplacedBy,
		Signature:    rec.Signature,
	}
}

// withDeprecationWarning fills in DeprecationWarning when the
// artifact is deprecated. Lifted out of resultFromRecord so
// callers can wrap their own constructed results.
func withDeprecationWarning(r *LoadArtifactResult) *LoadArtifactResult {
	if r == nil || !r.Deprecated {
		return r
	}
	if r.ReplacedBy != "" {
		r.DeprecationWarning = "artifact is deprecated; replaced_by " + r.ReplacedBy
	} else {
		r.DeprecationWarning = "artifact is deprecated"
	}
	return r
}

// ----- Visibility ---------------------------------------------------------

// artifactExistsAnywhere reports whether artifactID has at least
// one manifest in the tenant store regardless of layer
// visibility. Used by visibility.denied emission to distinguish
// filtered records from genuine misses.
func (r *Registry) artifactExistsAnywhere(ctx context.Context, artifactID string) bool {
	all, err := r.store.ListManifests(ctx, r.tenantID)
	if err != nil {
		return false
	}
	for _, m := range all {
		if m.ArtifactID == artifactID {
			return true
		}
	}
	return false
}

// visibleManifests returns every manifest from the tenant whose
// originating layer is visible to id. In standalone / filesystem
// modes (id.IsPublic), every manifest is returned.
func (r *Registry) visibleManifests(ctx context.Context, id layer.Identity) ([]store.ManifestRecord, error) {
	all, err := r.store.ListManifests(ctx, r.tenantID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if id.IsPublic || len(r.layers) == 0 {
		return all, nil
	}
	visible := layer.EffectiveLayersWith(r.layers, id, r.resolveGroup)
	allowed := map[string]bool{}
	for _, l := range visible {
		allowed[l.ID] = true
	}
	out := make([]store.ManifestRecord, 0, len(all))
	for _, m := range all {
		if allowed[m.Layer] {
			out = append(out, m)
		}
	}
	return out, nil
}

// ----- helpers ------------------------------------------------------------

func descriptorOf(m store.ManifestRecord) ArtifactDescriptor {
	return ArtifactDescriptor{
		ID:          m.ArtifactID,
		Type:        m.Type,
		Version:     m.Version,
		Description: m.Description,
		Tags:        append([]string(nil), m.Tags...),
	}
}

func inPrefix(id, prefix string) bool {
	if prefix == "" {
		return true
	}
	if !strings.HasPrefix(id, prefix) {
		return false
	}
	if len(id) == len(prefix) {
		return true
	}
	return id[len(prefix)] == '/'
}

func stripPrefix(id, prefix string) string {
	if prefix == "" {
		return id
	}
	rest := strings.TrimPrefix(id, prefix)
	return strings.TrimPrefix(rest, "/")
}

func parentPath(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return ""
}

func joinPath(parts ...string) string {
	out := []string{}
	for _, p := range parts {
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return strings.Join(out, "/")
}

func lastSegment(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func tagsMatch(have, want []string) bool {
	if len(want) == 0 {
		return true
	}
	set := map[string]bool{}
	for _, t := range have {
		set[t] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}

// ----- BM25-style scoring -------------------------------------------------

type scoredRecord struct {
	rec   store.ManifestRecord
	score float64
}

// scoreBM25 ranks records by a small BM25 implementation over manifest
// text (id segments, description, tags, body). Empty query returns
// records sorted alphabetically by ID with score 0.
func scoreBM25(records []store.ManifestRecord, query string) []scoredRecord {
	if query == "" {
		out := make([]scoredRecord, len(records))
		for i, r := range records {
			out[i] = scoredRecord{rec: r}
		}
		sort.Slice(out, func(i, j int) bool { return out[i].rec.ArtifactID < out[j].rec.ArtifactID })
		return out
	}

	docs := make([][]string, len(records))
	totalLen := 0
	for i, r := range records {
		docs[i] = tokensFor(r)
		totalLen += len(docs[i])
	}
	avgLen := float64(totalLen) / float64(max1(len(records)))

	df := map[string]int{}
	for _, doc := range docs {
		seen := map[string]bool{}
		for _, term := range doc {
			if seen[term] {
				continue
			}
			seen[term] = true
			df[term]++
		}
	}

	queryTerms := tokenize(strings.ToLower(query))
	const (
		k1 = 1.5
		b  = 0.75
	)
	out := make([]scoredRecord, 0, len(records))
	for i, doc := range docs {
		score := 0.0
		tf := termFreqs(doc)
		for _, qt := range queryTerms {
			f := float64(tf[qt])
			if f == 0 {
				continue
			}
			n := float64(len(records))
			idf := 0.0
			if df[qt] > 0 {
				idf = logf((n-float64(df[qt])+0.5)/(float64(df[qt])+0.5) + 1)
			}
			dl := float64(len(doc))
			norm := f * (k1 + 1) / (f + k1*(1-b+b*dl/avgLen))
			score += idf * norm
		}
		if score > 0 {
			out = append(out, scoredRecord{rec: records[i], score: score})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].rec.ArtifactID < out[j].rec.ArtifactID
	})
	return out
}

// tokensFor lowercases and tokenizes the searchable text of a manifest.
func tokensFor(r store.ManifestRecord) []string {
	var b strings.Builder
	b.WriteString(r.ArtifactID)
	b.WriteByte(' ')
	b.WriteString(r.Description)
	b.WriteByte(' ')
	b.WriteString(strings.Join(r.Tags, " "))
	b.WriteByte(' ')
	b.Write(r.Body)
	return tokenize(strings.ToLower(b.String()))
}

func tokenize(s string) []string {
	out := []string{}
	cur := strings.Builder{}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(r)
			continue
		}
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func termFreqs(tokens []string) map[string]int {
	m := map[string]int{}
	for _, t := range tokens {
		m[t]++
	}
	return m
}

func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

// logf is a small natural-log wrapper so tests can override later if
// they need deterministic scoring.
func logf(x float64) float64 {
	if x <= 0 {
		return 0
	}
	// Series for ln(x): use Newton via float64 — math.Log is the
	// production path; we wrap here so the BM25 implementation stays
	// dependency-free at the package boundary.
	return naturalLog(x)
}

func naturalLog(x float64) float64 {
	// Simple wrapper over math.Log; abstracted so callers can read the
	// dependency surface without diving into stdlib.
	return logImpl(x)
}
