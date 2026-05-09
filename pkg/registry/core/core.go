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

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/store"
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
}

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

	// max_depth is the registry-side ceiling (§4.5.5); a caller-supplied
	// Depth above the ceiling is silently capped.
	maxDepth := DefaultMaxDepth
	notableCount := opts.NotableCount
	if notableCount == 0 {
		notableCount = DefaultNotableCount
	}

	res := &LoadDomainResult{Path: path}

	// Subdomains: gather every direct subdomain. When Depth > 1, also
	// build a flattened list so the response shape is stable regardless
	// of depth (subdomains is the immediate-children list; deeper
	// levels are reachable via subsequent load_domain calls per §4.5.5
	// "only the originally requested domain gets its body; expanded
	// subtrees get short descriptions only").
	seen := map[string]bool{}
	for _, m := range visible {
		if !inPrefix(m.ArtifactID, path) {
			continue
		}
		rest := stripPrefix(m.ArtifactID, path)
		if rest == "" {
			continue
		}
		first := strings.SplitN(rest, "/", 2)[0]
		if first == "" || !strings.Contains(rest, "/") {
			continue
		}
		domainPath := joinPath(path, first)
		if seen[domainPath] {
			continue
		}
		seen[domainPath] = true
		res.Subdomains = append(res.Subdomains, DomainDescriptor{
			Path: domainPath, Name: first,
		})
	}
	sort.Slice(res.Subdomains, func(i, j int) bool {
		return res.Subdomains[i].Path < res.Subdomains[j].Path
	})

	// Notable = artifacts directly under prefix.
	notable := make([]ArtifactDescriptor, 0, len(visible))
	for _, m := range visible {
		if !inPrefix(m.ArtifactID, path) {
			continue
		}
		rest := stripPrefix(m.ArtifactID, path)
		if strings.Contains(rest, "/") {
			continue
		}
		notable = append(notable, descriptorOf(m))
	}
	notable = orderNotable(notable, opts.Featured)
	if len(notable) > notableCount {
		notable = notable[:notableCount]
		res.Note = fmt.Sprintf("Notable list truncated to %d entries.", notableCount)
	}
	res.Notable = notable

	// §4.5.5 cap-and-note: a Depth above max_depth is silently capped
	// (max_depth = the registry default unless overridden via DOMAIN.md
	// in a future commit). The note surfaces the capping per the spec.
	if opts.Depth > maxDepth {
		if res.Note != "" {
			res.Note += " "
		}
		res.Note += fmt.Sprintf("Requested depth %d capped at the configured ceiling of %d.",
			opts.Depth, maxDepth)
	}

	if path != "" && len(res.Subdomains) == 0 && len(res.Notable) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrDomainNotFound, path)
	}
	return res, nil
}

// orderNotable surfaces featured IDs first (in author-supplied order),
// then the remaining notable artifacts in alphabetical ID order. Per
// §4.5.5 deduplication: an artifact appearing in both featured and the
// alphabetical list keeps its featured position.
func orderNotable(notable []ArtifactDescriptor, featured []string) []ArtifactDescriptor {
	if len(featured) == 0 {
		sort.Slice(notable, func(i, j int) bool { return notable[i].ID < notable[j].ID })
		return notable
	}
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
	for _, n := range notable {
		if !used[n.ID] {
			rest = append(rest, n)
		}
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i].ID < rest[j].ID })
	return append(out, rest...)
}

// ----- SearchArtifacts ----------------------------------------------------

// SearchArtifactsOptions captures the §5 search_artifacts arguments.
type SearchArtifactsOptions struct {
	Query string
	Type  string
	Scope string
	Tags  []string
	TopK  int
}

// SearchResult is the common envelope for both search functions.
type SearchResult struct {
	Query        string
	TotalMatched int
	Results      []ArtifactDescriptor
	Domains      []DomainDescriptor
}

// SearchArtifacts runs the §5 search over manifests visible to id.
//
// Phase 12 will introduce hybrid retrieval (BM25 + vectors via RRF).
// The current implementation is BM25-style scoring over manifest text
// (id, description, tags, body), tunable enough to give meaningful
// rankings without an external index.
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
		filtered = append(filtered, m)
	}

	// Score with BM25 against the query. Empty query returns matched
	// records with score 0 (alphabetical by id).
	scored := scoreBM25(filtered, opts.Query)

	res := &SearchResult{
		Query:        opts.Query,
		TotalMatched: len(scored),
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

	resolved, err := version.Resolve(pin, versions)
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
		return resultFromRecord(rec), nil
	}
	chain, err := r.resolveExtendsChain(ctx, rec, map[string]bool{})
	if err != nil {
		return nil, err
	}
	merged := mergeChain(chain)
	return resultFromRecord(merged), nil
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

// mergeChain folds the chain top-down (parent → child) per the §4.6
// field-semantics table. Scalar fields take the child's value when set;
// list fields union; sensitivity is most-restrictive-wins.
func mergeChain(chain []store.ManifestRecord) store.ManifestRecord {
	if len(chain) == 0 {
		return store.ManifestRecord{}
	}
	out := chain[0]
	for _, c := range chain[1:] {
		if c.Description != "" {
			out.Description = c.Description
		}
		if c.Type != "" {
			out.Type = c.Type
		}
		out.Tags = appendUniqueStrings(out.Tags, c.Tags)
		out.Sensitivity = mostRestrictiveSensitivity(out.Sensitivity, c.Sensitivity)
		// Identity fields preserve the child's coordinates so callers
		// see the child rather than the parent.
		out.ArtifactID = c.ArtifactID
		out.Version = c.Version
		out.ContentHash = c.ContentHash
		out.Layer = c.Layer
		out.IngestedAt = c.IngestedAt
		out.Deprecated = c.Deprecated
		// Body and Frontmatter take the child's; the parent's prose is
		// not concatenated — extends inherits structured fields, not
		// markdown body.
		if len(c.Body) > 0 {
			out.Body = c.Body
		}
		if len(c.Frontmatter) > 0 {
			out.Frontmatter = c.Frontmatter
		}
		out.ExtendsPin = c.ExtendsPin
	}
	return out
}

func appendUniqueStrings(a, b []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(a)+len(b))
	for _, s := range a {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, s := range b {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
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

func splitParentRef(ref string) (id, ver string) {
	if i := strings.LastIndex(ref, "@"); i >= 0 {
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
	}
}

// ----- Visibility ---------------------------------------------------------

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
	visible := layer.EffectiveLayers(r.layers, id)
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
				idf = logf((n - float64(df[qt]) + 0.5) / (float64(df[qt]) + 0.5) + 1)
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
