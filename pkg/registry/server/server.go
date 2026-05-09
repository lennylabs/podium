// Package server exposes the registry HTTP/JSON API (spec §2.2, §5, §6.10).
// Stage 3 ships the route surface plus the four meta-tools backed by a
// filesystem-source registry. Visibility filtering, identity, and full
// hybrid retrieval land in later phases (§4.6, §6.3, §4.7).
package server

import (
	"encoding/json"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// Server wraps a filesystem-source registry behind the HTTP/JSON API.
// Stage 3 reads the registry on each request; later phases add caching
// and a metadata store (§4.7).
type Server struct {
	registry *filesystem.Registry
}

// New returns a Server backed by the registry at registryPath.
func New(registryPath string) (*Server, error) {
	reg, err := filesystem.Open(registryPath)
	if err != nil {
		return nil, err
	}
	return &Server{registry: reg}, nil
}

// NewFromRegistry returns a Server wrapping an already-opened registry.
// Useful for tests that share fixtures across multiple servers.
func NewFromRegistry(reg *filesystem.Registry) *Server {
	return &Server{registry: reg}
}

// Handler returns an http.Handler with the meta-tool routes registered.
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

// HealthResponse describes /healthz output.
type HealthResponse struct {
	Mode  string `json:"mode"`
	Ready bool   `json:"ready"`
}

// LoadDomainResponse describes /v1/load_domain output (§5).
type LoadDomainResponse struct {
	Path        string                  `json:"path"`
	Description string                  `json:"description,omitempty"`
	Keywords    []string                `json:"keywords,omitempty"`
	Subdomains  []DomainDescriptor      `json:"subdomains"`
	Notable     []ArtifactDescriptor    `json:"notable"`
	Note        string                  `json:"note,omitempty"`
}

// DomainDescriptor is one subdomain entry in load_domain output.
type DomainDescriptor struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ArtifactDescriptor is one artifact entry in load_domain / search responses.
type ArtifactDescriptor struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	Version     string   `json:"version,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// SearchResponse is the common envelope for search_domains and
// search_artifacts (§5).
type SearchResponse struct {
	Query        string               `json:"query,omitempty"`
	TotalMatched int                  `json:"total_matched"`
	Results      []ArtifactDescriptor `json:"results,omitempty"`
	Domains      []DomainDescriptor   `json:"domains,omitempty"`
}

// LoadArtifactResponse is /v1/load_artifact output. Bundled resources are
// returned inline as a path -> base64 map for resources below the inline
// cutoff (§4.1: 256 KB) and as presigned URLs above it. Stage 3 returns
// inline only.
type LoadArtifactResponse struct {
	ID            string            `json:"id"`
	Type          string            `json:"type"`
	Version       string            `json:"version"`
	ManifestBody  string            `json:"manifest_body"`
	Frontmatter   string            `json:"frontmatter"`
	Resources     map[string]string `json:"resources,omitempty"`
	MaterializedAt string           `json:"materialized_at,omitempty"`
}

// ----- Handlers -------------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, HealthResponse{Mode: "ready", Ready: true})
}

func (s *Server) handleLoadDomain(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	pathArg := q.Get("path")
	depth := atoiOr(q.Get("depth"), 1)

	records, err := s.records()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
		return
	}

	resp := LoadDomainResponse{Path: pathArg}
	resp.Subdomains = subdomainsOf(records, pathArg, depth)
	resp.Notable = notableOf(records, pathArg)
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleSearchDomains(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := strings.ToLower(q.Get("query"))
	scope := q.Get("scope")
	topK := atoiOr(q.Get("top_k"), 10)

	if topK > 50 {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", "top_k > 50 is not allowed")
		return
	}

	records, err := s.records()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
		return
	}

	domains := domainsFromRecords(records, scope)
	matched := []DomainDescriptor{}
	for _, d := range domains {
		if query != "" && !strings.Contains(strings.ToLower(d.Name+" "+d.Description), query) {
			continue
		}
		matched = append(matched, d)
	}
	resp := SearchResponse{Query: q.Get("query"), TotalMatched: len(matched)}
	if len(matched) > topK {
		matched = matched[:topK]
	}
	resp.Domains = matched
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleSearchArtifacts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := strings.ToLower(q.Get("query"))
	typeFilter := q.Get("type")
	scope := q.Get("scope")
	topK := atoiOr(q.Get("top_k"), 10)

	if topK > 50 {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", "top_k > 50 is not allowed")
		return
	}

	records, err := s.records()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
		return
	}

	matched := []ArtifactDescriptor{}
	for _, rec := range records {
		if scope != "" && !strings.HasPrefix(rec.ID, scope) {
			continue
		}
		if typeFilter != "" && string(rec.Artifact.Type) != typeFilter {
			continue
		}
		text := strings.ToLower(rec.ID + " " + rec.Artifact.Description + " " + strings.Join(rec.Artifact.Tags, " "))
		if query != "" && !strings.Contains(text, query) {
			continue
		}
		matched = append(matched, descriptorOf(rec))
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].ID < matched[j].ID })

	resp := SearchResponse{Query: q.Get("query"), TotalMatched: len(matched)}
	if len(matched) > topK {
		matched = matched[:topK]
	}
	resp.Results = matched
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleLoadArtifact(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id := q.Get("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "registry.invalid_argument", "id is required")
		return
	}
	records, err := s.records()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
		return
	}
	for _, rec := range records {
		if rec.ID != id {
			continue
		}
		body := rec.Artifact.Body
		if rec.Artifact.Type == manifest.TypeSkill && rec.Skill != nil {
			body = rec.Skill.Body
		}
		resources := map[string]string{}
		for path, data := range rec.Resources {
			resources[path] = string(data)
		}
		writeJSON(w, http.StatusOK, LoadArtifactResponse{
			ID:           rec.ID,
			Type:         string(rec.Artifact.Type),
			Version:      rec.Artifact.Version,
			ManifestBody: body,
			Frontmatter:  string(rec.ArtifactBytes),
			Resources:    resources,
		})
		return
	}
	writeError(w, http.StatusNotFound, "registry.not_found",
		"artifact not found: "+id)
}

// ----- Helpers --------------------------------------------------------------

func (s *Server) records() ([]filesystem.ArtifactRecord, error) {
	return s.registry.Walk(filesystem.WalkOptions{
		CollisionPolicy: filesystem.CollisionPolicyHighestWins,
	})
}

func descriptorOf(rec filesystem.ArtifactRecord) ArtifactDescriptor {
	return ArtifactDescriptor{
		ID:          rec.ID,
		Type:        string(rec.Artifact.Type),
		Version:     rec.Artifact.Version,
		Description: rec.Artifact.Description,
		Tags:        rec.Artifact.Tags,
	}
}

// subdomainsOf returns immediate subdomain descriptors under prefix.
// "Immediate" means the next path segment from prefix; deeper paths fold
// into the same subdomain entry.
func subdomainsOf(records []filesystem.ArtifactRecord, prefix string, _ int) []DomainDescriptor {
	seen := map[string]bool{}
	out := []DomainDescriptor{}
	for _, rec := range records {
		if !inScope(rec.ID, prefix) {
			continue
		}
		rest := strings.TrimPrefix(rec.ID, prefix)
		rest = strings.TrimPrefix(rest, "/")
		first := strings.SplitN(rest, "/", 2)[0]
		if first == "" || strings.Contains(rest, "/") == false {
			// Either the root (rest empty) or the artifact is directly
			// under prefix; not a subdomain.
			continue
		}
		domainPath := path.Join(prefix, first)
		if seen[domainPath] {
			continue
		}
		seen[domainPath] = true
		out = append(out, DomainDescriptor{
			Path: domainPath,
			Name: first,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// notableOf returns artifacts directly under prefix (no further subpath).
// Phase 8 will replace this with the curated `featured:` + learn-from-usage
// resolution from §4.5.5.
func notableOf(records []filesystem.ArtifactRecord, prefix string) []ArtifactDescriptor {
	out := []ArtifactDescriptor{}
	for _, rec := range records {
		if !inScope(rec.ID, prefix) {
			continue
		}
		rest := strings.TrimPrefix(rec.ID, prefix)
		rest = strings.TrimPrefix(rest, "/")
		if strings.Contains(rest, "/") {
			continue
		}
		out = append(out, descriptorOf(rec))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func domainsFromRecords(records []filesystem.ArtifactRecord, scope string) []DomainDescriptor {
	seen := map[string]bool{}
	out := []DomainDescriptor{}
	for _, rec := range records {
		if scope != "" && !strings.HasPrefix(rec.ID, scope) {
			continue
		}
		dir := strings.TrimSuffix(rec.ID, "/"+lastSegment(rec.ID))
		if dir == rec.ID || dir == "" {
			continue
		}
		if seen[dir] {
			continue
		}
		seen[dir] = true
		out = append(out, DomainDescriptor{Path: dir, Name: lastSegment(dir)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func lastSegment(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func inScope(id, prefix string) bool {
	if prefix == "" {
		return true
	}
	return strings.HasPrefix(id, prefix)
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

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(body)
}

// ErrorResponse is the JSON envelope for structured errors per §6.10.
type ErrorResponse struct {
	Code            string `json:"code"`
	Message         string `json:"message"`
	Retryable       bool   `json:"retryable"`
	SuggestedAction string `json:"suggested_action,omitempty"`
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, ErrorResponse{Code: code, Message: msg})
}
