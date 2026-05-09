// Package server exposes the registry HTTP/JSON API (spec §5, §6.10).
// The handlers translate HTTP requests into pkg/registry/core calls;
// all spec-defined logic (visibility, version resolution, search, etc.)
// lives in core, matching the §2.2 shared-library-code architecture.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/registry/ingest"
	"github.com/lennylabs/podium/pkg/store"
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

// New returns a Server backed by the given core.Registry.
func New(r *core.Registry, opts ...Option) *Server {
	s := &Server{core: r}
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
	server.resources = resources
	server.resourceFunc = filesystemResourceFunc(reg)
	return server, nil
}

// Handler returns an http.Handler with every meta-tool route registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/v1/load_domain", s.handleLoadDomain)
	mux.HandleFunc("/v1/search_domains", s.handleSearchDomains)
	mux.HandleFunc("/v1/search_artifacts", s.handleSearchArtifacts)
	mux.HandleFunc("/v1/load_artifact", s.handleLoadArtifact)
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

// LoadArtifactResponse is /v1/load_artifact output. Resources below the
// inline cutoff are returned base64-encoded; presigned URLs land in
// Phase 5+.
type LoadArtifactResponse struct {
	ID            string            `json:"id"`
	Type          string            `json:"type"`
	Version       string            `json:"version"`
	ContentHash   string            `json:"content_hash"`
	ManifestBody  string            `json:"manifest_body"`
	Frontmatter   string            `json:"frontmatter"`
	Layer         string            `json:"layer,omitempty"`
	Resources     map[string]string `json:"resources,omitempty"`
	ResourcesB64  bool              `json:"resources_base64,omitempty"`
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

func (s *Server) handleLoadArtifact(w http.ResponseWriter, r *http.Request) {
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
	}
	if cached, ok := s.resources[res.ID]; ok && len(cached) > 0 {
		resp.Resources = make(map[string]string, len(cached))
		for path, data := range cached {
			resp.Resources[path] = string(data)
		}
	}
	writeJSON(w, http.StatusOK, resp)
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

