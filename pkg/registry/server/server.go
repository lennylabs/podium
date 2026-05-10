// Package server exposes the registry HTTP/JSON API (spec §5, §6.10).
// The handlers translate HTTP requests into pkg/registry/core calls;
// all spec-defined logic (visibility, version resolution, search, etc.)
// lives in core, matching the §2.2 shared-library-code architecture.
package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/scim"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/webhook"
)

// Server is a thin HTTP wrapper over a core.Registry.
type Server struct {
	core         *core.Registry
	publicMode   bool
	resolveID    func(*http.Request) layer.Identity
	resourceFunc ResourceFunc
	// resources caches bundled resources keyed by artifact ID.
	// Populated by NewFromFilesystem; empty for callers that
	// construct via New (the meta-tool API still returns the manifest
	// body, just without bundled bytes inline).
	resources map[string]map[string][]byte
	// events is the §7.6 in-process pub/sub for /v1/events.
	events *eventBus
	// largeResources maps (artifactID, resourcePath) → object key for
	// resources whose payload exceeded objectstore.InlineCutoff at
	// ingest. The HTTP handler presigns a URL per key on read; the
	// /objects/{key} route serves the bytes for filesystem-backed
	// stores. Empty when no objectstore is configured.
	largeResources map[string]map[string]largeRef
	objectStore    objectstore.Provider
	objectBaseURL  string
	presignTTL     time.Duration
	// webhooks is the §7.3.2 outbound delivery worker. When set,
	// PublishEvent fans the event out to every matching receiver.
	webhooks *webhook.Worker
	// scim is the §6.3.1 SCIM 2.0 receiver. When set, the registry
	// mounts /scim/v2/ to accept user/group push from an IdP.
	scim *scim.Handler
	// tenant is the tenant identifier used for outbound webhook
	// receiver lookup. Single-tenant deployments leave this as
	// "default"; multi-tenant deployments resolve it per-request
	// once tenant routing is wired.
	tenant string
	// quota is the §4.7.8 rate limiter. When non-nil, search /
	// load_artifact handlers consult it before doing real work.
	quota *QuotaLimiter
}

// WithQuotaLimiter installs the §4.7.8 rate limiter for search
// QPS and materialize rate. Zero limits inside the limiter
// disable the check per dimension.
func WithQuotaLimiter(q *QuotaLimiter) Option {
	return func(s *Server) { s.quota = q }
}

// largeRef is the per-resource metadata the server keeps so it can
// presign URLs and validate visibility on /objects/{key} reads.
type largeRef struct {
	Key         string
	Size        int64
	ContentType string
	ContentHash string
}

// ResourceFunc returns the bytes of one bundled resource for an
// artifact load. The default implementation looks up the resource on
// disk under a configured root; tests can swap it for an in-memory
// fixture. Returning (nil, false) signals "not present" — the response
// then omits Resources for that artifact.
type ResourceFunc func(ctx context.Context, artifactID, resourcePath string) ([]byte, bool)

// Option mutates the Server during construction.
type Option func(*Server)

// WithPublicMode runs the server in §13.10 public mode: the visibility
// evaluator short-circuits to "every layer visible," identity is
// recorded as system:public, and ingest of medium / high sensitivity
// is rejected (the server doesn't ingest; the bootstrap configures
// that).
func WithPublicMode() Option { return func(s *Server) { s.publicMode = true } }

// WithIdentityResolver swaps the default anonymous-public resolver for
// one that maps an HTTP request to an Identity (e.g., decoded JWT).
func WithIdentityResolver(fn func(*http.Request) layer.Identity) Option {
	return func(s *Server) { s.resolveID = fn }
}

// WithResources installs a function that returns bundled resource bytes
// for load_artifact responses. Empty → no Resources are attached.
func WithResources(fn ResourceFunc) Option {
	return func(s *Server) { s.resourceFunc = fn }
}

// WithObjectStore configures the §4.1 large-resource path. Resources
// larger than objectstore.InlineCutoff at ingest are uploaded to
// store and surfaced in load_artifact responses as URLs the consumer
// follows. baseURL is the prefix the registry will serve large
// resources under (filesystem backend uses <baseURL>/objects/<key>;
// S3 backend ignores baseURL and returns its own presigned URLs).
// ttl <= 0 falls back to objectstore.DefaultPresignTTL.
func WithObjectStore(store objectstore.Provider, baseURL string, ttl time.Duration) Option {
	return func(s *Server) {
		s.objectStore = store
		s.objectBaseURL = baseURL
		if ttl <= 0 {
			ttl = objectstore.DefaultPresignTTL
		}
		s.presignTTL = ttl
		// Attach the same baseURL to a Filesystem provider so its
		// Presign returns absolute URLs without the caller wiring
		// it twice.
		if fs, ok := store.(*objectstore.Filesystem); ok && fs.BaseURL == "" {
			fs.BaseURL = baseURL
		}
	}
}

// WithSCIM mounts the §6.3.1 SCIM 2.0 receiver at /scim/v2/. The
// IdP pushes Users + Groups through this endpoint; the visibility
// evaluator resolves `groups:` filters via scim.Store.MembersOf.
func WithSCIM(h *scim.Handler) Option {
	return func(s *Server) { s.scim = h }
}

// WithWebhooks attaches a §7.3.2 outbound webhook worker. Every
// call to PublishEvent fans the event out to every matching
// receiver in the configured store. Without this option, change
// events still flow to /v1/events subscribers but no outbound
// HTTP POSTs are issued.
func WithWebhooks(w *webhook.Worker) Option {
	return func(s *Server) { s.webhooks = w }
}

// WithTenant sets the tenant identifier used for outbound webhook
// receiver lookup. Single-tenant deployments leave this at the
// default ("default"); multi-tenant deployments resolve it per
// request (Phase 5+).
func WithTenant(t string) Option {
	return func(s *Server) { s.tenant = t }
}

// New returns a Server backed by the given core.Registry.
func New(r *core.Registry, opts ...Option) *Server {
	s := &Server{core: r, events: newEventBus(), tenant: "default"}
	for _, opt := range opts {
		opt(s)
	}
	if s.resolveID == nil {
		// Default: anonymous identity. Public-mode bypass is applied
		// at the core level via Identity.IsPublic.
		s.resolveID = func(*http.Request) layer.Identity {
			return layer.Identity{IsPublic: true}
		}
	}
	return s
}

// NewFromFilesystem opens the filesystem registry at path, ingests
// every layer into a fresh in-memory store, captures bundled
// resources for inline delivery, and returns a Server wrapping the
// resulting core.Registry. This is the standalone bootstrap helper
// used by tests and the standalone server.
func NewFromFilesystem(path string, opts ...Option) (*Server, error) {
	reg, err := filesystem.Open(path)
	if err != nil {
		return nil, err
	}
	st := store.NewMemory()
	const tenant = "default"
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: tenant, Name: tenant}); err != nil {
		return nil, err
	}
	resources := map[string]map[string][]byte{}
	layers := make([]layer.Layer, 0, len(reg.Layers))
	for i, l := range reg.Layers {
		layerFS := newDirFS(l.Path)
		if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
			TenantID: tenant,
			LayerID:  l.ID,
			Files:    layerFS,
		}); err != nil {
			return nil, err
		}
		// Walk the layer once more to capture bundled resources for
		// inline delivery via load_artifact responses (§4.1 inline
		// cutoff). Higher-precedence layers overwrite lower ones for
		// collision-free artifact IDs (sync's effective view).
		records, err := reg.Walk(filesystem.WalkOptions{
			CollisionPolicy: filesystem.CollisionPolicyHighestWins,
		})
		if err == nil {
			for _, rec := range records {
				if rec.Layer.ID != l.ID {
					continue
				}
				resources[rec.ID] = rec.Resources
			}
		}
		layers = append(layers, layer.Layer{
			ID:         l.ID,
			Precedence: i + 1,
			Visibility: layer.Visibility{Public: true},
		})
	}

	registry := core.New(st, tenant, layers)
	server := New(registry, opts...)
	server.resourceFunc = filesystemResourceFunc(reg)
	if server.objectStore != nil {
		// Split bundled resources by §4.1 inline cutoff: small ones
		// stay in s.resources for inline embedding; large ones go to
		// the objectstore and are referenced via presigned URLs.
		large, err := uploadLargeResources(context.Background(), server.objectStore, resources)
		if err != nil {
			return nil, fmt.Errorf("upload large resources: %w", err)
		}
		server.largeResources = large
		// Strip large resources from the inline cache so handleLoadArtifact
		// doesn't double-deliver them.
		for artifactID, paths := range large {
			for path := range paths {
				delete(resources[artifactID], path)
			}
		}
	}
	server.resources = resources
	return server, nil
}

// uploadLargeResources walks resources, uploads any blob above
// objectstore.InlineCutoff to store, and returns the (artifactID →
// path → ref) mapping for the server to use on /v1/load_artifact.
func uploadLargeResources(ctx context.Context, store objectstore.Provider, resources map[string]map[string][]byte) (map[string]map[string]largeRef, error) {
	out := map[string]map[string]largeRef{}
	for artifactID, byPath := range resources {
		for path, body := range byPath {
			if int64(len(body)) <= objectstore.InlineCutoff {
				continue
			}
			h := sha256.Sum256(body)
			contentHash := "sha256:" + hex.EncodeToString(h[:])
			key := contentHash[len("sha256:"):]
			contentType := guessContentType(path)
			if err := store.Put(ctx, key, body, contentType); err != nil {
				return nil, fmt.Errorf("put %s/%s: %w", artifactID, path, err)
			}
			if out[artifactID] == nil {
				out[artifactID] = map[string]largeRef{}
			}
			out[artifactID][path] = largeRef{
				Key:         key,
				Size:        int64(len(body)),
				ContentType: contentType,
				ContentHash: contentHash,
			}
		}
	}
	return out, nil
}

// guessContentType picks a Content-Type from path extension. Falls
// back to application/octet-stream so a missing match is benign.
func guessContentType(path string) string {
	switch {
	case strings.HasSuffix(path, ".json"):
		return "application/json"
	case strings.HasSuffix(path, ".md"):
		return "text/markdown"
	case strings.HasSuffix(path, ".txt"):
		return "text/plain"
	case strings.HasSuffix(path, ".yaml"), strings.HasSuffix(path, ".yml"):
		return "application/yaml"
	case strings.HasSuffix(path, ".png"):
		return "image/png"
	case strings.HasSuffix(path, ".jpg"), strings.HasSuffix(path, ".jpeg"):
		return "image/jpeg"
	}
	return "application/octet-stream"
}

// Handler returns an http.Handler with every meta-tool route registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/v1/load_domain", s.handleLoadDomain)
	mux.HandleFunc("/v1/search_domains", s.handleSearchDomains)
	mux.HandleFunc("/v1/search_artifacts", s.handleSearchArtifacts)
	mux.HandleFunc("/v1/load_artifact", s.handleLoadArtifact)
	mux.HandleFunc("/v1/dependents", s.handleDependents)
	mux.HandleFunc("/v1/scope/preview", s.handleScopePreview)
	mux.HandleFunc("/v1/domain/analyze", s.handleDomainAnalyze)
	mux.HandleFunc("/v1/admin/reembed", s.handleReembed)
	mux.HandleFunc("/v1/admin/grants", s.handleAdminGrants)
	mux.HandleFunc("/v1/admin/show-effective", s.handleAdminShowEffective)
	mux.HandleFunc("/v1/quota", s.handleQuota)
	mux.HandleFunc("/v1/events", s.handleEvents)
	if s.webhooks != nil {
		mux.HandleFunc("/v1/webhooks", s.handleWebhooksList)
		mux.HandleFunc("/v1/webhooks/", s.handleWebhookOne)
	}
	if s.scim != nil {
		mux.Handle("/scim/v2/", s.scim)
	}
	if s.objectStore != nil {
		mux.HandleFunc("/objects/", s.handleObjectsRoute)
	}
	return mux
}

// ----- Response shapes ------------------------------------------------------

// HealthResponse describes /healthz output (§13.9).
type HealthResponse struct {
	Mode  string `json:"mode"`
	Ready bool   `json:"ready"`
}

// LoadDomainResponse describes /v1/load_domain output (§5).
type LoadDomainResponse struct {
	Path        string               `json:"path"`
	Description string               `json:"description,omitempty"`
	Keywords    []string             `json:"keywords,omitempty"`
	Subdomains  []DomainDescriptor   `json:"subdomains"`
	Notable     []ArtifactDescriptor `json:"notable"`
	Note        string               `json:"note,omitempty"`
}

// DomainDescriptor is one subdomain entry.
type DomainDescriptor struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ArtifactDescriptor is one artifact entry.
type ArtifactDescriptor struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	Version     string   `json:"version,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Score       float64  `json:"score,omitempty"`
}

// SearchResponse is the common envelope for both search endpoints.
type SearchResponse struct {
	Query        string               `json:"query,omitempty"`
	TotalMatched int                  `json:"total_matched"`
	Results      []ArtifactDescriptor `json:"results,omitempty"`
	Domains      []DomainDescriptor   `json:"domains,omitempty"`
}

// LoadArtifactResponse is /v1/load_artifact output. Resources below
// the §4.1 256 KB inline cutoff are returned inline as text;
// resources above the cutoff are returned in LargeResources as
// follow-the-URL references the consumer fetches separately.
type LoadArtifactResponse struct {
	ID             string                       `json:"id"`
	Type           string                       `json:"type"`
	Version        string                       `json:"version"`
	ContentHash    string                       `json:"content_hash"`
	ManifestBody   string                       `json:"manifest_body"`
	Frontmatter    string                       `json:"frontmatter"`
	Layer          string                       `json:"layer,omitempty"`
	Sensitivity    string                       `json:"sensitivity,omitempty"`
	Resources      map[string]string            `json:"resources,omitempty"`
	ResourcesB64   bool                         `json:"resources_base64,omitempty"`
	LargeResources map[string]LargeResourceLink `json:"large_resources,omitempty"`
	// Signature is the §4.7.9 envelope produced at ingest by the
	// configured SignatureProvider. Empty when ingest had no
	// signer wired. Consumers verify against
	// PODIUM_VERIFY_SIGNATURES at materialize time.
	Signature string `json:"signature,omitempty"`
}

// LargeResourceLink describes one resource whose payload exceeded
// the inline cutoff. The URL's auth model is backend-specific:
// the S3 backend embeds an AWS Signature V4 in the URL; the
// filesystem backend's URL points at the registry's authenticated
// /objects/{content_hash} route. ContentHash lets the consumer
// verify the bytes after fetching.
type LargeResourceLink struct {
	URL         string `json:"url"`
	ContentHash string `json:"content_hash"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type,omitempty"`
}

// ErrorResponse is the JSON envelope for §6.10 structured errors.
type ErrorResponse struct {
	Code            string `json:"code"`
	Message         string `json:"message"`
	Retryable       bool   `json:"retryable"`
	SuggestedAction string `json:"suggested_action,omitempty"`
}

// ----- Handlers -------------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	mode := "ready"
	if s.publicMode {
		mode = "public"
	}
	writeJSON(w, http.StatusOK, HealthResponse{Mode: mode, Ready: true})
}

func (s *Server) handleLoadDomain(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	res, err := s.core.LoadDomain(r.Context(), s.identity(r), q.Get("path"),
		core.LoadDomainOptions{Depth: atoiOr(q.Get("depth"), 0)})
	if err != nil {
		s.writeCoreError(w, err)
		return
	}
	resp := LoadDomainResponse{
		Path:        res.Path,
		Description: res.Description,
		Keywords:    res.Keywords,
		Note:        res.Note,
	}
	for _, d := range res.Subdomains {
		resp.Subdomains = append(resp.Subdomains, DomainDescriptor{
			Path: d.Path, Name: d.Name, Description: d.Description,
		})
	}
	for _, a := range res.Notable {
		resp.Notable = append(resp.Notable, descriptorOf(a))
	}
	if resp.Subdomains == nil {
		resp.Subdomains = []DomainDescriptor{}
	}
	if resp.Notable == nil {
		resp.Notable = []ArtifactDescriptor{}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleSearchDomains(w http.ResponseWriter, r *http.Request) {
	if !s.quota.AllowSearch(s.tenant) {
		writeQuotaError(w, "quota.search_qps_exceeded", "tenant search QPS budget exhausted")
		return
	}
	q := r.URL.Query()
	res, err := s.core.SearchDomains(r.Context(), s.identity(r), core.SearchDomainsOptions{
		Query: q.Get("query"),
		Scope: q.Get("scope"),
		TopK:  atoiOr(q.Get("top_k"), 10),
	})
	if err != nil {
		s.writeCoreError(w, err)
		return
	}
	resp := SearchResponse{Query: res.Query, TotalMatched: res.TotalMatched}
	for _, d := range res.Domains {
		resp.Domains = append(resp.Domains, DomainDescriptor{
			Path: d.Path, Name: d.Name, Description: d.Description,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleSearchArtifacts(w http.ResponseWriter, r *http.Request) {
	if !s.quota.AllowSearch(s.tenant) {
		writeQuotaError(w, "quota.search_qps_exceeded", "tenant search QPS budget exhausted")
		return
	}
	q := r.URL.Query()
	tags := []string{}
	if t := q.Get("tags"); t != "" {
		tags = splitCSV(t)
	}
	res, err := s.core.SearchArtifacts(r.Context(), s.identity(r), core.SearchArtifactsOptions{
		Query: q.Get("query"),
		Type:  q.Get("type"),
		Scope: q.Get("scope"),
		Tags:  tags,
		TopK:  atoiOr(q.Get("top_k"), 10),
	})
	if err != nil {
		s.writeCoreError(w, err)
		return
	}
	resp := SearchResponse{Query: res.Query, TotalMatched: res.TotalMatched}
	for _, a := range res.Results {
		resp.Results = append(resp.Results, descriptorOf(a))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleDependents(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", "id is required")
		return
	}
	edges, err := s.core.DependentsOf(r.Context(), s.identity(r), id)
	if err != nil {
		s.writeCoreError(w, err)
		return
	}
	out := []map[string]string{}
	for _, e := range edges {
		out = append(out, map[string]string{
			"from": e.From, "to": e.To, "kind": e.Kind,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"edges": out})
}

// handleReembed runs the §4.7 reembed flow over the tenant. POST
// /v1/admin/reembed?artifact=<id>&version=<v>&only_missing=true to
// scope to one artifact or limit to artifacts without a current
// embedding. Admin-only in production deployments.
func (s *Server) handleReembed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "registry.invalid_argument",
			"method not allowed: "+r.Method)
		return
	}
	q := r.URL.Query()
	if id := q.Get("artifact"); id != "" {
		ver := q.Get("version")
		if ver == "" {
			writeError(w, http.StatusBadRequest, "registry.invalid_argument",
				"version is required when artifact is set")
			return
		}
		if err := s.core.ReembedOne(r.Context(), id, ver); err != nil {
			writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"reembedded": map[string]string{"id": id, "version": ver},
		})
		return
	}
	onlyMissing := q.Get("only_missing") == "true"
	res, err := s.core.Reembed(r.Context(), onlyMissing)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleDomainAnalyze serves §4.5.5 GET /v1/domain/analyze?path=<>
// returning the per-subtree analysis report.
func (s *Server) handleDomainAnalyze(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "registry.invalid_argument",
			"method not allowed: "+r.Method)
		return
	}
	path := r.URL.Query().Get("path")
	report, err := s.core.AnalyzeDomain(r.Context(), s.identity(r), path)
	if err != nil {
		s.writeCoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleScopePreview(w http.ResponseWriter, r *http.Request) {
	preview, err := s.core.PreviewScope(r.Context(), s.identity(r))
	if err != nil {
		s.writeCoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) handleLoadArtifact(w http.ResponseWriter, r *http.Request) {
	if !s.quota.AllowMaterialize(s.tenant) {
		writeQuotaError(w, "quota.materialize_rate_exceeded", "tenant materialize budget exhausted")
		return
	}
	q := r.URL.Query()
	id := q.Get("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", "id is required")
		return
	}
	res, err := s.core.LoadArtifact(r.Context(), s.identity(r), id, core.LoadArtifactOptions{
		Version: q.Get("version"),
	})
	if err != nil {
		s.writeCoreError(w, err)
		return
	}
	resp := LoadArtifactResponse{
		ID:           res.ID,
		Type:         res.Type,
		Version:      res.Version,
		ContentHash:  res.ContentHash,
		ManifestBody: res.ManifestBody,
		Frontmatter:  string(res.Frontmatter),
		Layer:        res.Layer,
		Sensitivity:  res.Sensitivity,
		Signature:    res.Signature,
	}
	if cached, ok := s.resources[res.ID]; ok && len(cached) > 0 {
		resp.Resources = make(map[string]string, len(cached))
		for path, data := range cached {
			resp.Resources[path] = string(data)
		}
	}
	if largeMap, ok := s.largeResources[res.ID]; ok && len(largeMap) > 0 {
		resp.LargeResources = make(map[string]LargeResourceLink, len(largeMap))
		for path, ref := range largeMap {
			url, perr := s.objectStore.Presign(r.Context(), ref.Key, s.presignTTL)
			if perr != nil {
				writeError(w, http.StatusInternalServerError, "registry.unavailable",
					"presign large resource: "+perr.Error())
				return
			}
			resp.LargeResources[path] = LargeResourceLink{
				URL:         url,
				ContentHash: ref.ContentHash,
				Size:        ref.Size,
				ContentType: ref.ContentType,
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleObjectsRoute serves bytes for the filesystem objectstore via
// /objects/{key}. The §13.10 spec clarifies this route's auth model:
// the URL bears no embedded signature; the consumer sends the same
// session token it used for /v1/load_artifact. Visibility is
// re-checked here so a caller cannot follow a previously-issued URL
// after losing access to the artifact.
func (s *Server) handleObjectsRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "registry.invalid_argument",
			"method not allowed: "+r.Method)
		return
	}
	const prefix = "/objects/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		writeError(w, http.StatusNotFound, "registry.not_found", "no such object")
		return
	}
	key := r.URL.Path[len(prefix):]
	if key == "" || strings.Contains(key, "..") {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", "invalid object key")
		return
	}

	// Find the artifact owning this key so we can re-check visibility.
	artifactID, _, ok := s.findLargeRef(key)
	if !ok {
		writeError(w, http.StatusNotFound, "registry.not_found", "no such object")
		return
	}
	id := s.identity(r)
	visible, err := s.core.LoadArtifact(r.Context(), id, artifactID, core.LoadArtifactOptions{})
	if err != nil || visible == nil {
		writeError(w, http.StatusForbidden, "auth.forbidden",
			"caller is not authorized for this object")
		return
	}

	body, err := s.objectStore.Get(r.Context(), key)
	if err != nil {
		if errors.Is(err, objectstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "registry.not_found", "object not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
		return
	}
	contentType := contentTypeFromStore(s.objectStore, key)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Hash", "sha256:"+key)
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(body)
	}
}

// findLargeRef returns (artifactID, path, ok) for the resource whose
// stored key matches; ok=false when no artifact owns the key.
func (s *Server) findLargeRef(key string) (string, string, bool) {
	for artifactID, paths := range s.largeResources {
		for path, ref := range paths {
			if ref.Key == key {
				return artifactID, path, true
			}
		}
	}
	return "", "", false
}

// contentTypeFromStore extracts a content type from the configured
// store when the backend supports it. Filesystem records the type
// alongside the body; S3 returns it on the GetObject response (we
// don't query that here for the redirect path).
func contentTypeFromStore(store objectstore.Provider, key string) string {
	if fs, ok := store.(*objectstore.Filesystem); ok {
		return fs.ContentTypeOf(key)
	}
	return ""
}

// ----- Helpers --------------------------------------------------------------

func (s *Server) identity(r *http.Request) layer.Identity {
	id := s.resolveID(r)
	if s.publicMode {
		id.IsPublic = true
	}
	return id
}

func (s *Server) writeCoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, core.ErrInvalidArgument):
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", err.Error())
	case errors.Is(err, core.ErrDomainNotFound):
		writeError(w, http.StatusNotFound, "domain.not_found", err.Error())
	case errors.Is(err, core.ErrNotFound):
		writeError(w, http.StatusNotFound, "registry.not_found", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
	}
}

func descriptorOf(a core.ArtifactDescriptor) ArtifactDescriptor {
	return ArtifactDescriptor{
		ID:          a.ID,
		Type:        a.Type,
		Version:     a.Version,
		Description: a.Description,
		Tags:        append([]string(nil), a.Tags...),
		Score:       a.Score,
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, ErrorResponse{Code: code, Message: msg})
}

func atoiOr(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func splitCSV(s string) []string {
	out := []string{}
	cur := ""
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			if cur != "" {
				out = append(out, cur)
			}
			cur = ""
			continue
		}
		cur += string(s[i])
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

