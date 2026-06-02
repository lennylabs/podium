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
	"strconv"
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
	// ErrScopePreviewDisabled signals the §3.5 tenant gate is closed
	// (expose_scope_preview: false). The HTTP layer maps it to
	// 403 scope_preview_disabled.
	ErrScopePreviewDisabled = errors.New("scope_preview_disabled")
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
	// discoveryDefaults are the §13.12 tenant-scope `discovery:` knobs
	// from registry.yaml. They override the package defaults and are in
	// turn overridden by a per-domain DOMAIN.md `discovery:` block
	// (§4.5.5 "Where they're configured"). The zero value leaves the
	// package defaults in force.
	discoveryDefaults DiscoveryDefaults
	// allowPerDomainOverrides gates whether a per-domain DOMAIN.md
	// `discovery:` block overrides the tenant defaults (§4.5.5). The
	// default is true; a tenant that sets
	// discovery.allow_per_domain_overrides: false disables per-domain
	// discovery overrides registry-wide.
	allowPerDomainOverrides bool
	// importCache memoizes §4.5.2 DOMAIN.md include/exclude glob expansion
	// per visible artifact-ID snapshot (§12 "Glob expansion is cached
	// server-side per artifact-version snapshot; cache invalidation is
	// keyed on ingest events"). Nil falls back to uncached resolution.
	importCache *importCache
	// usage carries the §3.3 learn-from-usage signal. When set, load_artifact
	// records each access and search_artifacts / load_domain blend the
	// access-frequency ranking into their ordering (§12 "learn-from-usage
	// reranking surfaces signal-based ordering"). Nil leaves ranking on its
	// lexical, vector, and author-curated order alone.
	usage UsageSignals
}

// DiscoveryDefaults carries the §13.12 tenant-scope discovery knobs from
// registry.yaml. A zero field means "unset" and leaves the §4.5.5 package
// default in force (max_depth 3, notable_count 10, target_response_tokens
// 4000, fold_below_artifacts 0, fold_passthrough_chains true).
type DiscoveryDefaults struct {
	MaxDepth              int
	NotableCount          int
	FoldBelowArtifacts    int
	TargetResponseTokens  int
	FoldPassthroughChains *bool
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
	// ResolvedLayers is the ordered set of layer IDs composing the
	// caller's effective view (§4.6 precedence order), recorded on every
	// read call per §4.7.5 "resolved layer composition". Empty when no
	// layer list is configured (filesystem source) or the call is not a
	// read. spec: §4.7.5, §8.1.
	ResolvedLayers []string
	// ResultSize is the number of result items the read call returned to
	// the caller per §4.7.5 "result size": matched artifacts/domains for
	// a search, rendered subdomains plus notable entries for load_domain,
	// and 1 for a resolved load_artifact. Zero on the error paths and for
	// non-read events. spec: §4.7.5, §8.1.
	ResultSize int
	// RedactKeys lists the context keys the resolved artifact's manifest
	// named in audit_redact (§8.2 manifest-declared redaction). The audit
	// adapter replaces those context values with [redacted] before writing
	// to a sink. Eligible context keys on artifact.loaded are version,
	// content_hash, and layer (the same keys the publish event carries),
	// so a directive that names one of them is honored on reads as well as
	// on ingest. Empty for events that do not reference a single resolved
	// manifest. spec: §8.2.
	RedactKeys []string
}

// New returns a Registry backed by the given store, tenant, and layer
// list. The layer list governs visibility filtering (§4.6); empty means
// every record is visible.
func New(s store.Store, tenantID string, layers []layer.Layer) *Registry {
	// §4.5.5: per-domain discovery overrides are allowed unless a tenant
	// opts out via discovery.allow_per_domain_overrides: false.
	return &Registry{store: s, tenantID: tenantID, layers: layers, allowPerDomainOverrides: true, importCache: newImportCache()}
}

// WithDiscoveryDefaults wires the §13.12 tenant-scope `discovery:` knobs
// (from registry.yaml) and the allow_per_domain_overrides gate. These
// override the §4.5.5 package defaults; a per-domain DOMAIN.md
// `discovery:` block overrides them in turn unless allowPerDomain is
// false. Returns the registry for chaining.
func (r *Registry) WithDiscoveryDefaults(d DiscoveryDefaults, allowPerDomain bool) *Registry {
	r.discoveryDefaults = d
	r.allowPerDomainOverrides = allowPerDomain
	return r
}

// WithAudit attaches an AuditEmitter so every meta-tool invocation
// produces an audit event per §8.1.
func (r *Registry) WithAudit(emit AuditEmitter) *Registry {
	r.audit = emit
	return r
}

// WithCacheObserver attaches a callback that fires once per server-side
// import-glob cache lookup, reporting whether the result was served from the
// cache (true) or recomputed (false). It feeds the §13.8 cache-hit/miss
// counters without giving the core a metrics dependency. nil disables it.
// Returns the registry for chaining.
func (r *Registry) WithCacheObserver(observe func(hit bool)) *Registry {
	if r.importCache != nil {
		r.importCache.observe = observe
	}
	return r
}

// WithUsageSignals attaches the §3.3 learn-from-usage signal store so
// load_artifact records accesses and search_artifacts / load_domain rerank by
// access frequency (§12). Nil disables the feature; ranking stays on its
// lexical, vector, and author-curated order. Returns the registry for chaining.
func (r *Registry) WithUsageSignals(u UsageSignals) *Registry {
	r.usage = u
	return r
}

// WithVectorSearch attaches the §4.7 hybrid-retrieval pieces.
// SearchArtifacts RRF-fuses BM25 ranks with vector cosine ranks when the
// vector path is active. The embedder may be nil when the vector backend
// self-embeds (§13.12 F-13.12.6): the registry then sends raw text through
// the backend's TextVectorizer methods. A nil vector store, or a nil
// embedder against a backend that does not self-embed, disables the vector
// path; search continues BM25-only and reports Degraded=true.
func (r *Registry) WithVectorSearch(v vector.Provider, e embedding.Provider) *Registry {
	r.vector = v
	r.embedder = e
	return r
}

// vectorSearchActive reports whether the §4.7 vector path is wired and can
// run: a vector store plus either a local embedder or a self-embedding
// backend (§13.12). When false, search and reembed degrade to BM25-only.
func (r *Registry) vectorSearchActive() bool {
	return r.vector != nil && (r.embedder != nil || vector.SelfEmbeds(r.vector))
}

// queryVector returns the top-K nearest matches for the query string. With a
// self-embedding backend (§13.12 F-13.12.6) it sends the raw text via
// QueryText; otherwise it embeds the query locally and queries by vector.
func (r *Registry) queryVector(ctx context.Context, query string, topK int) ([]vector.Match, error) {
	if r.embedder == nil && vector.SelfEmbeds(r.vector) {
		return r.vector.(vector.TextVectorizer).QueryText(ctx, r.tenantID, query, topK)
	}
	vecs, err := r.embedder.Embed(ctx, []string{query})
	if err != nil || len(vecs) == 0 {
		return nil, fmt.Errorf("embed: %w", err)
	}
	// §4.7 model versioning: on a model-versioned backend, restrict results to
	// the currently-configured model so a transient mixed-model state during a
	// re-embed never scores the stale model's vectors.
	if mv, ok := vector.ModelVersionedOf(r.vector); ok {
		return mv.QueryModel(ctx, r.tenantID, vecs[0], topK, r.embedder.Model())
	}
	return r.vector.Query(ctx, r.tenantID, vecs[0], topK)
}

// upsertVector persists the embedding for one (tenant, id, version) row from
// its composed text. With a self-embedding backend it sends the text via
// PutText; otherwise it embeds locally and upserts the vector. An empty text
// is a no-op. spec: §4.7 / §13.12 (F-13.12.6).
func (r *Registry) upsertVector(ctx context.Context, tenantID, artifactID, version, text string) error {
	if text == "" {
		return nil
	}
	if r.embedder == nil && vector.SelfEmbeds(r.vector) {
		if err := r.vector.(vector.TextVectorizer).PutText(ctx, tenantID, artifactID, version, text); err != nil {
			return fmt.Errorf("vector put: %w", err)
		}
		return nil
	}
	vecs, err := r.embedder.Embed(ctx, []string{text})
	if err != nil {
		return fmt.Errorf("embed: %w", err)
	}
	if len(vecs) != 1 {
		return fmt.Errorf("embed: expected 1 vector, got %d", len(vecs))
	}
	// §4.7 model versioning: tag the row with the embedding model so a query
	// during a later model switch can restrict to the current model.
	if mv, ok := vector.ModelVersionedOf(r.vector); ok {
		if err := mv.PutModel(ctx, tenantID, artifactID, version, vecs[0], r.embedder.Model()); err != nil {
			return fmt.Errorf("vector put: %w", err)
		}
		return nil
	}
	if err := r.vector.Put(ctx, tenantID, artifactID, version, vecs[0]); err != nil {
		return fmt.Errorf("vector put: %w", err)
	}
	return nil
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

// effectiveLayerComposition returns the ordered layer IDs composing id's
// effective view (§4.6 precedence order, lowest first), for the §4.7.5
// audit "resolved layer composition" field. Returns nil when no layer
// list is configured (filesystem source) so the audit field stays empty
// rather than carrying a misleading value.
func (r *Registry) effectiveLayerComposition(ctx context.Context, id layer.Identity) []string {
	eff := layer.EffectiveLayersWith(r.resolveLayers(ctx), id, r.resolveGroup)
	if len(eff) == 0 {
		return nil
	}
	out := make([]string, len(eff))
	for i, l := range eff {
		out[i] = l.ID
	}
	return out
}

// resolveLayers builds the tenant's ordered layer list for one read call,
// composing admin-defined layers (config order, lowest precedence) below the
// caller's user-defined layers, per the §4.6 composition order. Runtime-
// registered layers — user-defined layers (§7.3.1) and admin-defined layers
// added through the API after boot — are persisted as store.LayerConfig rows,
// so resolving the list per request is what brings them into the effective
// view; a static slice fixed at construction never sees them (F-4.6.1). The
// boot-time slice is the fallback when the store carries no layer configs (the
// standalone filesystem path does not persist them and bypasses visibility in
// public mode anyway).
//
// spec: §4.6 — "Resolution of layers 1 and 2 happens at the registry on every
// load_domain, search_domains, search_artifacts, and load_artifact call."
func (r *Registry) resolveLayers(ctx context.Context) []layer.Layer {
	cfgs, err := r.store.ListLayerConfigs(ctx, r.tenantID)
	if err != nil || len(cfgs) == 0 {
		return r.layers
	}
	var admin, user []store.LayerConfig
	for _, c := range cfgs {
		if c.UserDefined {
			user = append(user, c)
		} else {
			admin = append(admin, c)
		}
	}
	// ListLayerConfigs already orders by Order ascending; sort defensively so
	// precedence does not depend on a backend's iteration order.
	sort.SliceStable(admin, func(i, j int) bool { return admin[i].Order < admin[j].Order })
	sort.SliceStable(user, func(i, j int) bool { return user[i].Order < user[j].Order })
	out := make([]layer.Layer, 0, len(cfgs))
	prec := 1
	for _, c := range admin {
		out = append(out, layerFromConfig(c, prec))
		prec++
	}
	// §4.6 composition order places every user-defined layer above every
	// admin-defined layer, regardless of the stored Order values.
	for _, c := range user {
		out = append(out, layerFromConfig(c, prec))
		prec++
	}
	return out
}

// layerFromConfig projects a stored layer config onto the visibility layer
// the §4.6 evaluator consumes, at the given precedence.
func layerFromConfig(c store.LayerConfig, precedence int) layer.Layer {
	return layer.Layer{
		ID:         c.ID,
		Precedence: precedence,
		Visibility: layer.Visibility{
			Public:       c.Public,
			Organization: c.Organization,
			Groups:       c.Groups,
			Users:        c.Users,
		},
	}
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

// DomainDescriptor describes one subdomain entry (load_domain) or one
// ranked domain result (search_domains).
type DomainDescriptor struct {
	Path        string
	Name        string
	Description string
	// Keywords carries the domain's author-curated keywords. Populated for
	// search_domains results per the §3.2 Layer 1 descriptor (path, name,
	// description, keywords, score); empty for load_domain subdomain
	// entries, which carry path/name/description only.
	Keywords []string
	// Score is the retrieval relevance of a search_domains result (the
	// lexical BM25 score, consistent with search_artifacts). Zero for
	// load_domain subdomain entries and for a vector-only match.
	Score float64
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
	// Sensitivity is the artifact's classification label, surfaced in
	// search_artifacts results for filtering and display (§4.7.4). For an
	// artifact that declares extends:, it carries the most-restrictive value
	// across the chain (§4.6 field-semantics) so search agrees with
	// load_artifact. Empty when the artifact declares no sensitivity.
	Sensitivity string
	// FoldedFrom records the relative subpath an artifact was lifted
	// from when fold_below_artifacts collapsed its sparse subdomain
	// into the parent's leaf set (§4.5.5 folding mechanics). Empty
	// when the artifact was a direct child of the requested domain.
	FoldedFrom string
	// Source discriminates the §4.5.5 notable-selection sources for an
	// entry in load_domain's notable list: "featured" for an artifact
	// named in the domain's featured: list, "signal" otherwise. Per
	// §4.5.5 an artifact present in both sources is tagged "featured"
	// (featured wins). The learn-from-usage signal source (§3.3) is not
	// yet wired, so the "signal" slot is currently filled by domain
	// enumeration ranking. Empty outside load_domain (search results do
	// not carry a notable source).
	Source string
	// Frontmatter is the artifact's full ARTIFACT.md frontmatter, surfaced
	// in search_artifacts results per the §7.6.1 read-CLI/SDK JSON schema
	// ({id, type, version, score, frontmatter}). Empty for load_domain
	// notable entries, which carry Summary instead.
	Frontmatter string
	// Summary is the artifact's short description, surfaced in load_domain
	// notable entries per the §7.6.1 schema ({id, type, summary, source,
	// folded_from}). Empty for search results, which carry Frontmatter.
	Summary string
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

// DefaultMaxDepth, DefaultNotableCount, and DefaultTargetResponseTokens
// are the §4.5.5 tenant defaults applied when neither the tenant
// registry.yaml `discovery:` block nor a per-domain DOMAIN.md sets them.
const (
	DefaultMaxDepth             = 3
	DefaultNotableCount         = 10
	DefaultTargetResponseTokens = 4000
)

// LoadDomain returns the domain map for path. An empty path returns
// the registry root.
//
// Visibility: artifacts whose Layer is invisible to id are excluded.
// In standalone mode (no identity provider), pass an empty Identity
// with IsPublic=true to bypass visibility filtering, mirroring the
// §13.10 / §13.11 behavior.
func (r *Registry) LoadDomain(ctx context.Context, id layer.Identity, path string, opts LoadDomainOptions) (res *LoadDomainResult, err error) {
	// spec: §4.7.5 — log the read with resolved layer composition and
	// result size (rendered subdomains plus notable entries). Deferred
	// over a named return so the size lands on the success path.
	ev := AuditEvent{
		Type:           "domain.loaded",
		Caller:         callerOf(id),
		Target:         path,
		ResolvedLayers: r.effectiveLayerComposition(ctx, id),
	}
	defer func() {
		if res != nil {
			ev.ResultSize = len(res.Subdomains) + len(res.Notable)
		}
		r.emit(ctx, ev)
	}()
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
	knobs := resolveKnobs(opts, requested, r.discoveryDefaults, r.allowPerDomainOverrides)

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

	res = &LoadDomainResult{Path: path}

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
	// §12 glob-expansion cache: every include/exclude resolution in this
	// load_domain runs over the same allIDs snapshot, so route them through
	// the per-snapshot importCache. resolveImports memoizes the expansion
	// and invalidates when an ingest changes the visible artifact-ID set.
	resolveImports := func(include, exclude []string) []string {
		return r.resolveImports(include, exclude, allIDs)
	}
	// §4.5.5 / F-4.5.13: domains that carry curated content (a DOMAIN.md
	// description/body/keywords or resolved include: members) are not bare
	// pass-throughs and must not be collapsed away. Precomputed once and
	// shared by the fold/collapse logic at every level.
	curated := curatedDomainPaths(merged, resolveImports)

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
		// spec: §7.6.1 — a load_domain notable entry carries a short summary
		// ({id, type, summary, source, folded_from}); the search-only
		// frontmatter field is cleared so the two response schemas stay
		// distinct (output drift is a bug, §7.6.1).
		d.Summary = d.Description
		d.Frontmatter = ""
		notable = append(notable, d)
	}
	for _, m := range dedupeLatest(directArtifactsOf(under, path)) {
		if domainpkg.MatchAny(knobs.exclude, m.ArtifactID) {
			continue
		}
		addCandidate(descriptorOf(m))
	}
	for _, iid := range resolveImports(knobs.include, knobs.exclude) {
		if rec, ok := byID[iid]; ok {
			addCandidate(descriptorOf(rec))
		}
	}

	// Immediate children: fold sparse subdomains into the notable list
	// (§4.5.5 folding mechanics) and render the rest as the subdomain
	// tree, dropping any unlisted subtree.
	// foldedSubdomains counts how many sparse subdomains fold_below_artifacts
	// collapses here, for the §4.5.5 / §8 domain.loaded fold summary (F-4.5.1).
	foldedSubdomains := 0
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
		// §4.5.5 visibility-aware count: canonical descendants plus any
		// members a DOMAIN.md in the subtree pulls in via include: (F-4.5.13).
		recursiveCount := subtreeMemberCount(groups[name], childPath, merged, resolveImports)
		if knobs.foldBelow > 0 && recursiveCount < knobs.foldBelow {
			foldedSubdomains++
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
			renderedPath = collapsePassthroughChain(under, childPath, curated)
		}
		if unlistedAt(renderedPath, merged) {
			continue // collapsed through an unlisted intermediate
		}
		res.Subdomains = append(res.Subdomains, DomainDescriptor{
			Path:        renderedPath,
			Name:        lastSegment(renderedPath),
			Description: childDescription(renderedPath, merged),
			Subdomains:  r.renderSubtree(under, merged, renderedPath, renderDepth-1, knobs.foldPassthrough, curated),
		})
	}
	sort.Slice(res.Subdomains, func(i, j int) bool {
		return res.Subdomains[i].Path < res.Subdomains[j].Path
	})

	// §4.5.5 / §8 (F-4.5.1): whether pass-through chain collapse fired, read
	// off the rendered tree before the budget pass trims it. A rendered
	// subdomain sitting more than one path segment below its parent is the
	// signature of a collapsed single-child chain.
	passthroughCollapsed := knobs.foldPassthrough && passthroughFired(path, res.Subdomains)

	// §12 learn-from-usage reranking restricted to the notable pool so the
	// signal orders the candidates within their tier without pulling in
	// artifacts outside this domain's view.
	notableIDs := make(map[string]bool, len(notable))
	for _, n := range notable {
		notableIDs[n.ID] = true
	}
	notable = orderNotable(notable, knobs.featured, knobs.deprioritize, r.usageRanking(ctx, notableIDs))
	if len(notable) > knobs.notableCount {
		notable = notable[:knobs.notableCount]
	}

	// §4.5.5 target_response_tokens soft budget: when the estimated
	// response exceeds the budget, tighten the rendered subtree depth
	// first (coarse) and then the notable list (fine), surfacing each
	// reduction in the rendering note. Depth reduction drops the deepest
	// nested levels; the immediate children (level 1) and the curated
	// notable list survive as long as the budget allows.
	notableFrom, notableTo := len(notable), -1
	depthFrom, depthTo := 0, 0
	if knobs.targetResponseTokens > 0 {
		budget := knobs.targetResponseTokens
		startDepth := renderedDepth(res.Subdomains)
		eff := startDepth
		for eff > 1 && estimateResponseTokens(res.Subdomains, notable) > budget {
			eff--
			res.Subdomains = truncateToDepth(res.Subdomains, eff)
		}
		if eff < startDepth {
			depthFrom, depthTo = startDepth, eff
		}
		for len(notable) > 0 && estimateResponseTokens(res.Subdomains, notable) > budget {
			notable = notable[:len(notable)-1]
			notableTo = len(notable)
		}
	}
	res.Notable = notable

	// §4.5.5 rendering note: budget reductions (notable and depth, as one
	// sentence) and the depth-cap case. Folding decisions are not surfaced
	// — folded artifacts already carry FoldedFrom in the response.
	notes := []string{}
	clauses := []string{}
	if notableTo >= 0 {
		clauses = append(clauses, fmt.Sprintf("notable list reduced from %d to %d", notableFrom, notableTo))
	}
	if depthFrom > 0 {
		clauses = append(clauses, fmt.Sprintf("subtree depth reduced from %d to %d", depthFrom, depthTo))
	}
	if len(clauses) > 0 {
		s := strings.Join(clauses, "; ") + " to fit the response budget."
		notes = append(notes, strings.ToUpper(s[:1])+s[1:])
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

	// spec: §4.5.5 line 540 — "Audit events (§8) record the resolved depth
	// and fold decisions per call." Record the resolved render depth (after
	// caller-override capping), whether the requested depth was capped at the
	// ceiling, the count of sparse subdomains folded into the leaf set, and
	// whether pass-through chain collapse fired. The deferred emitter reads
	// ev.Context, so set it before the success return (F-4.5.1).
	ev.Context = map[string]string{
		"depth":                 strconv.Itoa(renderDepth),
		"depth_capped":          strconv.FormatBool(depthCapped),
		"folded_subdomains":     strconv.Itoa(foldedSubdomains),
		"passthrough_collapsed": strconv.FormatBool(passthroughCollapsed),
	}
	return res, nil
}

// passthroughFired reports whether any rendered subdomain sits more than one
// path segment below its parent, the signature of a §4.5.5 pass-through chain
// collapse. It walks the rendered tree so a collapse at any depth is detected.
// Used for the domain.loaded audit fold summary (F-4.5.1).
func passthroughFired(parent string, subs []DomainDescriptor) bool {
	for _, s := range subs {
		if segmentCount(s.Path)-segmentCount(parent) > 1 {
			return true
		}
		if passthroughFired(s.Path, s.Subdomains) {
			return true
		}
	}
	return false
}

// segmentCount returns the number of "/"-delimited path segments; the empty
// (root) path has zero segments.
func segmentCount(path string) int {
	if path == "" {
		return 0
	}
	return strings.Count(path, "/") + 1
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

// collapsePassthroughChain walks down a single-child chain that has no
// direct artifacts and no curated DOMAIN.md at the current level,
// returning the deepest non-passthrough path. A level stops the collapse
// when it has direct artifacts, is curated (a DOMAIN.md description,
// body, keywords, or resolved include: members — F-4.5.13), or has more
// than one immediate child. Used by §4.5.5 fold_passthrough_chains.
func collapsePassthroughChain(under []store.ManifestRecord, path string, curated map[string]bool) string {
	for {
		if len(directArtifactsOf(under, path)) > 0 {
			return path
		}
		if curated[path] {
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

// curatedDomainPaths returns the set of domain paths that must not be
// collapsed away as bare pass-throughs (§4.5.5 / F-4.5.13). A path is
// curated when its merged DOMAIN.md carries a description, a prose body,
// or keywords, or when its include: (after exclude:) resolves at least
// one imported member. Collapsing past such a domain would drop its
// description and curated member set from the rendered tree.
func curatedDomainPaths(merged map[string]*manifest.Domain, resolveImports func(include, exclude []string) []string) map[string]bool {
	out := map[string]bool{}
	for p, dom := range merged {
		if dom == nil {
			continue
		}
		if strings.TrimSpace(dom.Description) != "" || strings.TrimSpace(dom.Body) != "" {
			out[p] = true
			continue
		}
		if dom.Discovery != nil && len(dom.Discovery.Keywords) > 0 {
			out[p] = true
			continue
		}
		if len(resolveImports(dom.Include, dom.Exclude)) > 0 {
			out[p] = true
		}
	}
	return out
}

// subtreeMemberCount is the §4.5.5 visibility-aware count for the subtree
// rooted at subtreePath: the distinct canonical artifacts in records,
// unioned with every artifact pulled in by a DOMAIN.md include: (after
// exclude:) anywhere in the subtree. Imported members count toward the
// fold_below_artifacts decision so a domain whose members arrive only
// through include: is not treated as sparse (F-4.5.13).
func subtreeMemberCount(records []store.ManifestRecord, subtreePath string, merged map[string]*manifest.Domain, resolveImports func(include, exclude []string) []string) int {
	seen := map[string]bool{}
	for _, m := range records {
		seen[m.ArtifactID] = true
	}
	for p, dom := range merged {
		if dom == nil || !inPrefix(p, subtreePath) {
			continue
		}
		for _, id := range resolveImports(dom.Include, dom.Exclude) {
			seen[id] = true
		}
	}
	return len(seen)
}

// resolveImports expands the §4.5.2 include/exclude globs against the
// visible artifact-ID snapshot, serving the §12 per-snapshot importCache
// when one is wired. A nil cache (a Registry built without New) falls back
// to uncached resolution so the result is identical either way.
func (r *Registry) resolveImports(include, exclude, allIDs []string) []string {
	if r.importCache == nil {
		return domainpkg.ResolveImports(include, exclude, allIDs)
	}
	return r.importCache.resolve(include, exclude, allIDs)
}

// estimateResponseTokens approximates the token cost of a load_domain
// response for the §4.5.5 budget check. The estimate stays
// dependency-free; it counts identifier and description characters
// divided by the typical 4-bytes-per-token ratio. The subtree term
// recurses into nested children so the budget pass's depth reduction
// measurably shrinks the estimate.
func estimateResponseTokens(subs []DomainDescriptor, notable []ArtifactDescriptor) int {
	bytes := subdomainBytes(subs)
	for _, n := range notable {
		bytes += len(n.ID) + len(n.Description) + len(n.Type) + 32
		for _, t := range n.Tags {
			bytes += len(t) + 4
		}
	}
	return bytes / 4
}

// subdomainBytes sums the descriptor bytes for a subdomain tree,
// recursing into the nested children so the estimate tracks the full
// rendered depth (§4.5.5 budget).
func subdomainBytes(subs []DomainDescriptor) int {
	bytes := 0
	for _, s := range subs {
		bytes += len(s.Path) + len(s.Name) + len(s.Description) + 16
		bytes += subdomainBytes(s.Subdomains)
	}
	return bytes
}

// renderedDepth returns the number of levels present in a rendered
// subdomain tree: 0 for an empty slice, 1 for a flat list of children
// with no nested subdomains, and so on. The §4.5.5 budget pass uses it to
// know how far the subtree can be compressed before only the immediate
// children remain.
func renderedDepth(subs []DomainDescriptor) int {
	max := 0
	for _, s := range subs {
		if d := 1 + renderedDepth(s.Subdomains); d > max {
			max = d
		}
	}
	return max
}

// truncateToDepth returns subs with nesting limited to d levels (the
// immediate children are level 1). Levels deeper than d have their child
// lists dropped. Used by the §4.5.5 budget pass to compress the rendered
// subtree depth without dropping the immediate children.
func truncateToDepth(subs []DomainDescriptor, d int) []DomainDescriptor {
	out := make([]DomainDescriptor, len(subs))
	for i, s := range subs {
		if d <= 1 {
			s.Subdomains = nil
		} else {
			s.Subdomains = truncateToDepth(s.Subdomains, d-1)
		}
		out[i] = s
	}
	return out
}

// §4.5.5 notable-selection source tags. featured marks an author-curated
// entry; signal marks every other entry (currently surfaced by domain
// enumeration ranking pending the §3.3 learn-from-usage signal source).
const (
	sourceFeatured = "featured"
	sourceSignal   = "signal"
)

// orderNotable surfaces featured IDs first (in author-supplied order), then
// the remaining notable artifacts, with any artifact matching a deprioritize
// glob ranked last (§4.5.5). Per §4.5.5 deduplication an artifact appearing in
// both featured and the rest keeps its featured position; featured always wins
// over deprioritize. Within the non-deprioritized "signal" tier the §3.3
// learn-from-usage ranking (usageRank, most-accessed first) orders the
// artifacts agents actually load ahead of the rest, which fall back to
// alphabetical ID order; this is the §12 "learn-from-usage reranking surfaces
// signal-based ordering" applied to the load_domain notable pool. Each entry
// is tagged with its §4.5.5 source: "featured" for a featured ID, "signal"
// otherwise.
func orderNotable(notable []ArtifactDescriptor, featured, deprioritize, usageRank []string) []ArtifactDescriptor {
	byID := map[string]ArtifactDescriptor{}
	for _, n := range notable {
		byID[n.ID] = n
	}
	out := make([]ArtifactDescriptor, 0, len(notable))
	used := map[string]bool{}
	for _, id := range featured {
		if d, ok := byID[id]; ok && !used[id] {
			d.Source = sourceFeatured
			out = append(out, d)
			used[id] = true
		}
	}
	// usagePos maps an artifact ID to its rank in the usage signal (lower is
	// more accessed); IDs absent from the signal sort after ranked ones.
	usagePos := make(map[string]int, len(usageRank))
	for i, id := range usageRank {
		if _, ok := usagePos[id]; !ok {
			usagePos[id] = i
		}
	}
	rest := make([]ArtifactDescriptor, 0, len(notable))
	low := make([]ArtifactDescriptor, 0)
	for _, n := range notable {
		if used[n.ID] {
			continue
		}
		n.Source = sourceSignal
		if len(deprioritize) > 0 && domainpkg.MatchAny(deprioritize, n.ID) {
			low = append(low, n)
			continue
		}
		rest = append(rest, n)
	}
	sort.Slice(rest, func(i, j int) bool { return lessByUsageThenID(rest[i].ID, rest[j].ID, usagePos) })
	sort.Slice(low, func(i, j int) bool { return low[i].ID < low[j].ID })
	out = append(out, rest...)
	return append(out, low...)
}

// lessByUsageThenID orders two artifact IDs by their §3.3 usage rank first
// (lower position = more accessed = earlier), with unranked IDs after ranked
// ones, then alphabetically. spec: §12.
func lessByUsageThenID(a, b string, usagePos map[string]int) bool {
	pa, oka := usagePos[a]
	pb, okb := usagePos[b]
	if oka != okb {
		return oka // a ranked, b not -> a first
	}
	if oka && pa != pb {
		return pa < pb
	}
	return a < b
}

// ----- SearchArtifacts ----------------------------------------------------

// SearchArtifactsOptions captures the §5 search_artifacts arguments.
type SearchArtifactsOptions struct {
	Query string
	Type  string
	Scope string
	Tags  []string
	TopK  int
	// SessionID carries the §7.6 session_id filter for session-consistent
	// retrieval. It is threaded through so search shares the load path's
	// session surface; latest resolution itself is applied at load time.
	SessionID string
	// IncludeDeprecated opts deprecated artifacts back into the
	// result set. Default search excludes them per §4.7.4.
	IncludeDeprecated bool
	// AsAdmin requests the §4.7.2 admin diagnostic override: the search
	// ignores per-layer visibility so an admin can enumerate artifacts in
	// layers their own identity cannot otherwise see. The caller must hold
	// the admin role; the override is itself audited via
	// admin.visibility_override.
	AsAdmin bool
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
	// spec: §4.7.5 — log the read with the caller's resolved layer
	// composition and result size. Deferred so the size is captured on
	// the success path while the event still fires on every early return.
	ev := AuditEvent{
		Type:           "artifacts.searched",
		Caller:         callerOf(id),
		Context:        map[string]string{"query": opts.Query, "scope": opts.Scope, "type": opts.Type},
		ResolvedLayers: r.effectiveLayerComposition(ctx, id),
	}
	defer func() { r.emit(ctx, ev) }()
	if opts.TopK > 50 {
		return nil, fmt.Errorf("%w: top_k > 50", ErrInvalidArgument)
	}
	if opts.TopK <= 0 {
		opts.TopK = 10
	}

	// spec: §4.7.2 — an admin diagnostic search overrides per-layer
	// visibility (audited inside the helper); otherwise it enumerates only
	// the caller's effective view.
	var (
		visible []store.ManifestRecord
		err     error
	)
	if opts.AsAdmin {
		visible, err = r.adminVisibleManifests(ctx, id, opts.Query)
	} else {
		visible, err = r.visibleManifests(ctx, id)
	}
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
	// BM25 ranks. Failures degrade to BM25-only without erroring. The
	// embedder may be nil when the backend self-embeds (§13.12 F-13.12.6).
	degraded := !r.vectorSearchActive()
	if !degraded && opts.Query != "" {
		vecRanks, vecErr := r.vectorRanks(ctx, opts.Query, filtered, opts.TopK)
		if vecErr != nil {
			degraded = true
		} else if len(vecRanks) > 0 {
			lexIDs := scoredIDs(scored)
			fused := RRFFuse(lexIDs, vecRanks)
			scored = reorderScored(scored, latestByID(filtered), fused)
		}
	}

	// §12 learn-from-usage reranking: blend the access-frequency signal into
	// the ranked order so artifacts agents actually load rise above
	// equally-relevant but unused ones. Applied after lexical/vector fusion
	// and only for a non-empty query (an empty-query browse stays the
	// deterministic alphabetical listing of §4.7). The usage ranking is
	// restricted to the current result IDs so it reorders matches without
	// injecting non-matching artifacts.
	if opts.Query != "" && len(scored) > 1 {
		curIDs := scoredIDs(scored)
		if usageRank := r.usageRanking(ctx, idSet(curIDs)); len(usageRank) > 0 {
			fused := RRFFuse(curIDs, usageRank)
			scored = reorderScored(scored, latestByID(filtered), fused)
		}
	}

	// §4.7.3 search ranking signal: frequently-depended-on artifacts surface
	// higher. Blend the reverse-dependency in-degree into the ranked order,
	// restricted to the current result IDs (like the usage rerank) so it
	// reorders matches without injecting non-matching artifacts. Empty-query
	// browse keeps the deterministic §4.7 listing.
	if opts.Query != "" && len(scored) > 1 {
		curIDs := scoredIDs(scored)
		if depRank := r.dependencyRanking(ctx, idSet(curIDs)); len(depRank) > 0 {
			fused := RRFFuse(curIDs, depRank)
			scored = reorderScored(scored, latestByID(filtered), fused)
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
		// spec: §4.7.4 — surface the sensitivity label in search results,
		// resolved most-restrictive across the extends chain (§4.6) so the
		// search and load_artifact surfaces report the same value.
		d.Sensitivity = r.mergedSensitivity(ctx, sc.rec)
		res.Results = append(res.Results, d)
	}
	ev.ResultSize = len(res.Results)
	return res, nil
}

// mergedSensitivity returns rec's effective sensitivity. When rec declares
// extends:, it folds the chain most-restrictively (§4.6 field-semantics
// table) to match what assembleResult/load_artifact reports; otherwise it
// returns the record's own value. A chain that fails to resolve falls back
// to the record's own sensitivity rather than erroring, since this feeds a
// display field on an already-authorized result.
func (r *Registry) mergedSensitivity(ctx context.Context, rec store.ManifestRecord) string {
	if rec.ExtendsPin == "" {
		return rec.Sensitivity
	}
	chain, err := r.resolveExtendsChain(ctx, rec, map[string]bool{})
	if err != nil {
		return rec.Sensitivity
	}
	s := ""
	for _, c := range chain {
		s = mostRestrictiveSensitivity(s, c.Sensitivity)
	}
	return s
}

// vectorRanks embeds the query and returns the top-K nearest
// artifact IDs from the vector store, restricted to the visible
// candidate set so a leaked vector outside the caller's view never
// surfaces.
func (r *Registry) vectorRanks(ctx context.Context, query string, candidates []store.ManifestRecord, topK int) ([]string, error) {
	matches, err := r.queryVector(ctx, query, topK*4)
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
		// §3.2: domain projection vectors share this index under a
		// reserved version sentinel (DomainVectorVersion). Skip them so
		// an artifact search never returns a domain.
		if m.Version == DomainVectorVersion {
			continue
		}
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

// idSet builds a membership set over a slice of artifact IDs.
func idSet(ids []string) map[string]bool {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}

// reorderScored reorders the BM25 score slice to match the order of
// fused IDs. A fused ID that BM25 scored keeps its lexical score; a
// fused ID that only the vector ranker found (semantically related but
// with no query-term overlap, so absent from scored) is materialized
// from the candidate set with score 0. Without this, the embeddings
// half of hybrid retrieval could only reorder lexical hits and never
// surface a new candidate, defeating the point of fusing vectors in
// (§3.2 Layer 2 / F-3.2.3). candidates maps each visible artifact ID to
// its latest record.
func reorderScored(scored []scoredRecord, candidates map[string]store.ManifestRecord, fused []string) []scoredRecord {
	byID := map[string]scoredRecord{}
	for _, sc := range scored {
		byID[sc.rec.ArtifactID] = sc
	}
	out := make([]scoredRecord, 0, len(fused))
	for _, id := range fused {
		if sc, ok := byID[id]; ok {
			out = append(out, sc)
			continue
		}
		if rec, ok := candidates[id]; ok {
			out = append(out, scoredRecord{rec: rec})
		}
	}
	return out
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
	// SkillRaw is the verbatim SKILL.md for a type: skill artifact (§4.3.4),
	// surfaced so server-source delivery reproduces the authored skill file
	// byte-for-byte (§11 filesystem ↔ server equivalence). Empty for
	// non-skills.
	SkillRaw []byte
	Layer    string
	// Resources are the §4.4 bundled resources for the artifact,
	// resolved from the persisted manifest record (§7.2 data plane).
	// Small resources carry their bytes inline; large ones carry a
	// content-hash reference the HTTP layer presigns. Nil when the
	// package bundles no resources.
	Resources   []store.ResourceRef
	Sensitivity string
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
	// Merged is true when the served Frontmatter is an extends-merged
	// re-serialization with the hidden parent stripped (§4.6) rather than
	// the original child bytes the ContentHash was computed over. The
	// consumer reads RawFrontmatter (below) to reproduce the §4.7.6 hash.
	Merged bool
	// RawFrontmatter carries the leaf child's original (pre-merge) ARTIFACT.md
	// bytes when Merged is set, so the consumer can reproduce the §4.7.6
	// content hash (computed over these bytes, the verbatim SKILL.md, and the
	// bundled resources) instead of skipping the §6.6 step 2 check. Empty for
	// a non-merged result, where Frontmatter already holds the hashed bytes.
	RawFrontmatter []byte
}

// LoadArtifactOptions captures §5 arguments. Empty Version means
// "latest" per §4.7.6.
type LoadArtifactOptions struct {
	Version string
	// SessionID enables consistent latest resolution within a session
	// (§4.7.6). The first latest lookup within a session is recorded
	// and reused for all subsequent same-id lookups in the session.
	SessionID string
	// AsAdmin requests the §4.7.2 admin diagnostic override: the read
	// ignores per-layer visibility so an admin can inspect an artifact in
	// a layer their own identity cannot otherwise see. The caller must hold
	// the admin role (adminVisibleManifests re-checks); the override is
	// itself audited via admin.visibility_override.
	AsAdmin bool
}

// LoadArtifact returns the manifest body. When the resolved manifest
// declares extends:, the parent is fetched (server-side, hidden-parent
// semantics per §4.6) and field-merged per the §4.6 merge-semantics
// table. The returned body and frontmatter are the merged result.
func (r *Registry) LoadArtifact(ctx context.Context, id layer.Identity, artifactID string, opts LoadArtifactOptions) (res *LoadArtifactResult, err error) {
	// spec: §4.7.5 — log the read with resolved layer composition and
	// result size. Deferred over a named return so a resolved artifact
	// records size 1 across every resolution path (latest, exact, hash,
	// session pin) while a not-found/denied call still logs at size 0.
	ev := AuditEvent{
		Type:           "artifact.loaded",
		Caller:         callerOf(id),
		Target:         artifactID,
		Context:        map[string]string{"version": opts.Version},
		ResolvedLayers: r.effectiveLayerComposition(ctx, id),
	}
	defer func() {
		if res != nil {
			ev.ResultSize = 1
			// §3.3 / §12 learn-from-usage: a resolved load is the access
			// signal that feeds search and load_domain reranking. Recorded
			// only on success so denied/not-found calls do not inflate counts.
			r.recordUsage(ctx, artifactID, opts.SessionID)
		}
		r.emit(ctx, ev)
	}()
	// spec: §4.7.2 — an admin diagnostic load overrides per-layer
	// visibility (the override is itself audited inside the helper);
	// otherwise the read sees only the caller's effective view.
	var visible []store.ManifestRecord
	if opts.AsAdmin {
		visible, err = r.adminVisibleManifests(ctx, id, artifactID)
	} else {
		visible, err = r.visibleManifests(ctx, id)
	}
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
	// §6.3.1 fine-grained scopes: a load grant (with any "@version" pin)
	// must cover the resolved record, otherwise the load is outside the
	// caller's scope surface. assemble gates every successful resolution
	// path through this check and mirrors the visibility-denial response
	// so it does not leak that a version the caller may not load exists.
	scopes := layer.ParseScopes(id.Scopes)
	assemble := func(rec store.ManifestRecord) (*LoadArtifactResult, error) {
		if scopes.Active() && !scopes.AllowsLoad(artifactID, rec.Version) {
			if r.artifactExistsAnywhere(ctx, artifactID) {
				r.emit(ctx, AuditEvent{
					Type:   "visibility.denied",
					Caller: callerOf(id),
					Target: artifactID,
				})
			}
			return nil, fmt.Errorf("%w: artifact %s", ErrNotFound, artifactID)
		}
		res, err := r.assembleResult(ctx, rec)
		if err == nil {
			// §8.2 manifest-declared redaction + §4.7.5 read-event context:
			// record the resolved version/content_hash/layer and carry the
			// manifest's audit_redact key set so the audit adapter masks any
			// named eligible key before the event lands in a sink. This is
			// the read-side counterpart of the ingest publish event, which
			// already redacts these same keys.
			ev.Context = map[string]string{
				"version":      rec.Version,
				"content_hash": rec.ContentHash,
				"layer":        rec.Layer,
			}
			ev.RedactKeys = rec.AuditRedact
		}
		return res, err
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
				return assemble(c)
			}
			continue
		}
		versions = append(versions, c.Version)
		byVersion[c.Version] = c
	}
	if pin.Kind == version.PinContentHash {
		return nil, fmt.Errorf("%w: no version with content hash sha256:%s", ErrNotFound, pin.Hash)
	}

	// §4.7.6 session-consistent latest: the first latest lookup within
	// a session pins, subsequent same-id lookups in the session
	// resolve to the same version regardless of newer ingests.
	if pin.Kind == version.PinLatest && opts.SessionID != "" {
		if pinned, ok := r.lookupSessionPin(opts.SessionID, artifactID); ok {
			if rec, ok := byVersion[pinned]; ok {
				return assemble(rec)
			}
		}
	}

	var resolved string
	if pin.Kind == version.PinLatest {
		// §4.7.6 — `latest` is "the most recently ingested
		// non-deprecated version visible under the caller's effective
		// view." Order by ingest time (ties broken by higher semver),
		// not by semver, so a backported fix ingested after a newer
		// major line still wins. Fall back to the full candidate set
		// when every version is deprecated so callers still get bytes
		// (with the deprecation warning).
		cands := make([]version.Candidate, 0, len(versions))
		for _, v := range versions {
			if rec, ok := byVersion[v]; ok && !rec.Deprecated {
				cands = append(cands, version.Candidate{Version: v, IngestedAt: rec.IngestedAt})
			}
		}
		if len(cands) == 0 {
			for _, v := range versions {
				cands = append(cands, version.Candidate{Version: v, IngestedAt: byVersion[v].IngestedAt})
			}
		}
		resolved, err = version.ResolveLatest(cands)
	} else {
		resolved, err = version.Resolve(pin, versions)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	if pin.Kind == version.PinLatest && opts.SessionID != "" {
		r.recordSessionPin(opts.SessionID, artifactID, resolved)
	}
	rec := byVersion[resolved]
	return assemble(rec)
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
	result := resultFromRecord(merged)
	// This branch runs only for an extends artifact, and mergeChain always
	// re-serializes the frontmatter with the hidden parent stripped (§4.6),
	// so the served Frontmatter no longer reproduces the stored ContentHash.
	// Flag the result and deliver the leaf child's original ARTIFACT.md bytes
	// (rec is the leaf of the chain) as RawFrontmatter so the consumer can
	// still run the §6.6 step 2 content-hash match against the bytes the hash
	// was computed over rather than skipping it.
	result.Merged = true
	result.RawFrontmatter = append([]byte(nil), rec.Frontmatter...)
	return withDeprecationWarning(result), nil
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
		// Bundled resources belong to the concrete package: the child's
		// own files ship, not the hidden parent's (§4.6). The leaf record
		// is last in the chain, so its refs win.
		out.Resources = c.Resources
		// The verbatim SKILL.md follows the child's identity: a skill that
		// extends a parent still ships its own authored SKILL.md.
		out.SkillRaw = c.SkillRaw
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
		SkillRaw:     rec.SkillRaw,
		Layer:        rec.Layer,
		Sensitivity:  rec.Sensitivity,
		Resources:    rec.Resources,
		Deprecated:   rec.Deprecated,
		ReplacedBy:   rec.ReplacedBy,
		Signature:    rec.Signature,
	}
}

// ResolveResourceOwner returns the ID of a visible artifact that
// bundles a resource whose content hash matches key (the bare hex
// digest, no "sha256:" prefix), and ok=false when the caller can see no
// such artifact. The §7.2 data-plane /objects/{key} route uses it to
// re-check visibility on every fetch so a caller who has lost access to
// every artifact bundling the bytes can no longer follow a
// previously-issued URL. Bytes are deduplicated by content hash across
// artifacts (§4.4), so any one visible owner authorizes the read.
func (r *Registry) ResolveResourceOwner(ctx context.Context, id layer.Identity, key string) (string, bool) {
	visible, err := r.visibleManifests(ctx, id)
	if err != nil {
		return "", false
	}
	want := "sha256:" + key
	for _, m := range visible {
		for _, ref := range m.Resources {
			if ref.ContentHash == want {
				return m.ArtifactID, true
			}
		}
	}
	return "", false
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
	// §6.3.1 fine-grained scopes intersect with layer visibility; the
	// smaller surface wins. A public-mode caller bypasses layer filtering
	// but a presented scope still narrows the read surface.
	scopes := layer.ParseScopes(id.Scopes)
	// spec: §4.6 — resolve the caller's layer list (admin + runtime-registered)
	// per request rather than from a static boot-time slice (F-4.6.1).
	resolved := r.resolveLayers(ctx)
	if id.IsPublic || len(resolved) == 0 {
		return applyReadScope(all, scopes), nil
	}
	visible := layer.EffectiveLayersWith(resolved, id, r.resolveGroup)
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
	return applyReadScope(out, scopes), nil
}

// adminVisibleManifests returns every manifest in the tenant for a §4.7.2
// admin diagnostic read that overrides per-layer visibility. The caller
// must hold the admin role; a non-admin (or public-mode) identity is
// rejected with ErrForbidden so the bypass cannot be reached without the
// grant. The override is itself audited via admin.visibility_override
// before any record is returned, satisfying the spec's "the override is
// itself audited" clause. OAuth read scopes (§6.3.1) still narrow the
// surface, so a scoped admin token does not silently widen its read grant.
func (r *Registry) adminVisibleManifests(ctx context.Context, id layer.Identity, target string) ([]store.ManifestRecord, error) {
	if err := r.AdminAuthorize(ctx, id); err != nil {
		return nil, err
	}
	r.emit(ctx, AuditEvent{
		Type:    "admin.visibility_override",
		Caller:  callerOf(id),
		Target:  target,
		Context: map[string]string{"override": "visibility"},
	})
	all, err := r.store.ListManifests(ctx, r.tenantID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	return applyReadScope(all, layer.ParseScopes(id.Scopes)), nil
}

// applyReadScope narrows a visible-manifest set to the records the
// caller's OAuth read surface permits (§6.3.1). An inactive scope set is
// the identity transform.
func applyReadScope(in []store.ManifestRecord, scopes layer.ScopeSet) []store.ManifestRecord {
	if !scopes.Active() {
		return in
	}
	out := make([]store.ManifestRecord, 0, len(in))
	for _, m := range in {
		if scopes.AllowsRead(m.ArtifactID) {
			out = append(out, m)
		}
	}
	return out
}

// ----- helpers ------------------------------------------------------------

func descriptorOf(m store.ManifestRecord) ArtifactDescriptor {
	return ArtifactDescriptor{
		ID:          m.ArtifactID,
		Type:        m.Type,
		Version:     m.Version,
		Description: m.Description,
		Tags:        append([]string(nil), m.Tags...),
		// spec: §7.6.1 — search_artifacts results carry the artifact's
		// frontmatter for the read-CLI/SDK JSON schema ({..., frontmatter}).
		Frontmatter: string(m.Frontmatter),
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

// scoreBM25 ranks records by the shared bm25Rank implementation over
// manifest text (id segments, description, tags, body). Empty query
// returns records sorted alphabetically by ID with score 0.
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
	tiebreak := make([]string, len(records))
	for i, r := range records {
		docs[i] = tokensFor(r)
		tiebreak[i] = r.ArtifactID
	}
	ranked := bm25Rank(docs, tiebreak, query)
	out := make([]scoredRecord, 0, len(ranked))
	for _, ri := range ranked {
		out = append(out, scoredRecord{rec: records[ri.idx], score: ri.score})
	}
	return out
}

// scoredIndex is one document's BM25 score keyed by its position in the
// input slice. Callers map idx back to their own record type.
type scoredIndex struct {
	idx   int
	score float64
}

// bm25Rank scores each tokenized document against the query with Okapi
// BM25 weighting (k1=1.5, b=0.75), the shared lexical ranker behind both
// search_artifacts and search_domains (§4.7 hybrid retrieval). It returns
// only documents with a positive score, ordered by descending score then
// ascending tiebreak (the caller's stable identifier, e.g. artifact ID or
// domain path). An empty query or empty corpus returns nil.
func bm25Rank(docs [][]string, tiebreak []string, query string) []scoredIndex {
	queryTerms := tokenize(strings.ToLower(query))
	if len(docs) == 0 || len(queryTerms) == 0 {
		return nil
	}
	totalLen := 0
	for _, doc := range docs {
		totalLen += len(doc)
	}
	avgLen := float64(totalLen) / float64(max1(len(docs)))

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

	const (
		k1 = 1.5
		b  = 0.75
	)
	n := float64(len(docs))
	out := make([]scoredIndex, 0, len(docs))
	for i, doc := range docs {
		score := 0.0
		tf := termFreqs(doc)
		for _, qt := range queryTerms {
			f := float64(tf[qt])
			if f == 0 {
				continue
			}
			idf := 0.0
			if df[qt] > 0 {
				idf = logf((n-float64(df[qt])+0.5)/(float64(df[qt])+0.5) + 1)
			}
			dl := float64(len(doc))
			norm := f * (k1 + 1) / (f + k1*(1-b+b*dl/avgLen))
			score += idf * norm
		}
		if score > 0 {
			out = append(out, scoredIndex{idx: i, score: score})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return tiebreak[out[i].idx] < tiebreak[out[j].idx]
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
