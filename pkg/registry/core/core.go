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
)

// Registry is the core registry type. Construct one per tenant; the
// caller passes Identity in per request to drive visibility filtering.
type Registry struct {
	store    store.Store
	tenantID string
	layers   []layer.Layer
}

// New returns a Registry backed by the given store, tenant, and layer
// list. The layer list governs visibility filtering (§4.6); empty means
// every record is visible.
func New(s store.Store, tenantID string, layers []layer.Layer) *Registry {
	return &Registry{store: s, tenantID: tenantID, layers: layers}
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
	// Depth caps the rendered subtree depth (§4.5.5). A value of 0
	// uses the configured default.
	Depth int
}

// LoadDomain returns the domain map for path. An empty path returns
// the registry root.
//
// Visibility: artifacts whose Layer is invisible to id are excluded.
// In standalone mode (no identity provider), pass an empty Identity
// with IsPublic=true to bypass visibility filtering, mirroring the
// §13.10 / §13.11 behavior.
func (r *Registry) LoadDomain(ctx context.Context, id layer.Identity, path string, opts LoadDomainOptions) (*LoadDomainResult, error) {
	visible, err := r.visibleManifests(ctx, id)
	if err != nil {
		return nil, err
	}

	res := &LoadDomainResult{Path: path}

	// Subdomains = the immediate path segments under prefix from any
	// matching manifest's canonical ID.
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
	for _, m := range visible {
		if !inPrefix(m.ArtifactID, path) {
			continue
		}
		rest := stripPrefix(m.ArtifactID, path)
		if strings.Contains(rest, "/") {
			continue
		}
		res.Notable = append(res.Notable, descriptorOf(m))
	}
	sort.Slice(res.Notable, func(i, j int) bool {
		return res.Notable[i].ID < res.Notable[j].ID
	})

	if path != "" && len(res.Subdomains) == 0 && len(res.Notable) == 0 {
		// Path resolves to nothing.
		return nil, fmt.Errorf("%w: %s", ErrDomainNotFound, path)
	}
	return res, nil
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
}

// LoadArtifact returns the manifest body. Resources are not fetched by
// the core (object storage / inline transit is the consumer's
// responsibility); the consumer reads them per the spec's data plane.
//
// Stage 6 keeps the simple case: for filesystem-source backends the
// store carries body + frontmatter + bundled resources. Phase 5+
// introduces presigned URL handling.
func (r *Registry) LoadArtifact(ctx context.Context, id layer.Identity, artifactID string, opts LoadArtifactOptions) (*LoadArtifactResult, error) {
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
	// Resolve version per §4.7.6.
	pin, err := version.ParsePin(opts.Version)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	versions := make([]string, 0, len(candidates))
	byVersion := map[string]store.ManifestRecord{}
	for _, c := range candidates {
		if pin.Kind == version.PinContentHash {
			if c.ContentHash == "sha256:"+pin.Hash {
				return resultFromRecord(c), nil
			}
			continue
		}
		versions = append(versions, c.Version)
		byVersion[c.Version] = c
	}
	if pin.Kind == version.PinContentHash {
		return nil, fmt.Errorf("%w: no version with content hash sha256:%s", ErrNotFound, pin.Hash)
	}
	resolved, err := version.Resolve(pin, versions)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	rec := byVersion[resolved]
	return resultFromRecord(rec), nil
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
		return nil, err
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
