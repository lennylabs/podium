// Command podium-mcp is the MCP server bridge described in spec §6.
//
// The bridge exposes the meta-tools (load_domain, search_domains,
// search_artifacts, load_artifact) over MCP's stdio transport. It
// forwards meta-tool calls to a Podium registry over HTTP, caches
// content-addressed responses, runs the configured HarnessAdapter and
// MaterializationHook chain, and writes adapter output atomically to
// the host's filesystem at load_artifact time (§6.6).
//
// Configuration (env vars; flags also supported by the launcher):
//
//	PODIUM_REGISTRY            Registry URL or filesystem path.
//	PODIUM_HARNESS             Adapter ID (default: none).
//	PODIUM_CACHE_DIR           Content-addressed cache root.
//	PODIUM_MATERIALIZE_ROOT    Materialization destination.
//	PODIUM_SESSION_TOKEN       Injected JWT (§6.3.2).
//	PODIUM_SESSION_TOKEN_FILE  File path holding the JWT.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/materialize"
	"github.com/lennylabs/podium/pkg/overlay"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// protocolVersion is the MCP wire-protocol version this binary speaks.
// initialize negotiates with the host: if the host's
// requested protocolVersion predates supportedSince, the server
// returns mcp.unsupported_version per §6.9.
const (
	protocolVersion = "2024-11-05"
	supportedSince  = "2024-11-01"
)

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	srv, err := newServer(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := srv.serve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// config captures every PODIUM_ env var the bridge consults.
type config struct {
	registry         string
	harness          string
	cacheDir         string
	materializeRoot  string
	sessionToken     string
	sessionTokenFile string
	overlayPath      string
}

func loadConfig() (*config, error) {
	c := &config{
		registry:         os.Getenv("PODIUM_REGISTRY"),
		harness:          envDefault("PODIUM_HARNESS", "none"),
		cacheDir:         os.Getenv("PODIUM_CACHE_DIR"),
		materializeRoot:  os.Getenv("PODIUM_MATERIALIZE_ROOT"),
		sessionToken:     os.Getenv("PODIUM_SESSION_TOKEN"),
		sessionTokenFile: os.Getenv("PODIUM_SESSION_TOKEN_FILE"),
		overlayPath:      os.Getenv("PODIUM_OVERLAY_PATH"),
	}
	if c.registry == "" {
		return nil, fmt.Errorf("PODIUM_REGISTRY is required")
	}
	if c.cacheDir == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			c.cacheDir = filepath.Join(home, ".podium", "cache")
		}
	}
	return c, nil
}

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// mcpServer holds the wiring for one bridge process.
type mcpServer struct {
	cfg      *config
	http     *http.Client
	cache    *contentCache
	adapters *adapter.Registry
	overlay  []filesystem.ArtifactRecord
}

func newServer(cfg *config) (*mcpServer, error) {
	cache, err := newContentCache(cfg.cacheDir)
	if err != nil {
		return nil, err
	}
	srv := &mcpServer{
		cfg:      cfg,
		http:     &http.Client{},
		cache:    cache,
		adapters: adapter.DefaultRegistry(),
	}
	// §6.4 workspace overlay: load now and reuse for the bridge
	// lifetime. An empty path or absent overlay disables the layer.
	if cfg.overlayPath != "" {
		records, err := overlay.Filesystem{Path: cfg.overlayPath}.Resolve(nil)
		if err == nil {
			srv.overlay = records
		}
		// Errors other than ErrNoOverlay are silently ignored: the
		// bridge runs without an overlay rather than refusing to
		// start.
	}
	return srv, nil
}

// overlayMatch returns the overlay record whose canonical ID
// matches id, or nil. Used by the load_artifact path to satisfy
// reads from the highest-precedence layer before talking to the
// registry.
func (s *mcpServer) overlayMatch(id string) *filesystem.ArtifactRecord {
	for i := range s.overlay {
		if s.overlay[i].ID == id {
			return &s.overlay[i]
		}
	}
	return nil
}

// rpcRequest is a JSON-RPC 2.0 request envelope.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// rpcResponse is the matching response envelope.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *mcpServer) serve(r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	enc := json.NewEncoder(w)
	for scanner.Scan() {
		var req rpcRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}
		resp := s.handle(req)
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (s *mcpServer) handle(req rpcRequest) rpcResponse {
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		// §6.9: refuse with mcp.unsupported_version when the host's
		// requested protocolVersion predates supportedSince.
		var initParams struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &initParams)
		if initParams.ProtocolVersion != "" && initParams.ProtocolVersion < supportedSince {
			resp.Error = &rpcError{
				Code:    -32600,
				Message: "mcp.unsupported_version: host protocol " + initParams.ProtocolVersion + " predates supported " + supportedSince,
			}
			return resp
		}
		resp.Result = map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities": map[string]any{
				"tools":              map[string]any{},
				"prompts":            map[string]any{},
				"sessionCorrelation": true,
			},
			"serverInfo": map[string]any{"name": "podium-mcp", "version": "0.0.0-dev"},
		}
	case "tools/list":
		resp.Result = map[string]any{
			"tools": []map[string]any{
				{"name": "load_domain", "description": "Browse the artifact catalog hierarchically."},
				{"name": "search_domains", "description": "Search the catalog for relevant domains."},
				{"name": "search_artifacts", "description": "Search or browse the artifact catalog."},
				{"name": "load_artifact", "description": "Load a specific artifact by ID."},
			},
		}
	case "tools/call":
		resp.Result = s.callTool(req.Params)
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
	return resp
}

type toolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

func (s *mcpServer) callTool(raw json.RawMessage) any {
	var p toolCallParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return errorResult(err.Error())
	}
	switch p.Name {
	case "load_domain":
		return s.proxyGet("/v1/load_domain", p.Arguments)
	case "search_domains":
		return s.proxyGet("/v1/search_domains", p.Arguments)
	case "search_artifacts":
		return s.proxyGet("/v1/search_artifacts", p.Arguments)
	case "load_artifact":
		return s.loadArtifact(p.Arguments)
	default:
		return errorResult("unknown tool: " + p.Name)
	}
}

// loadArtifact fetches the artifact from the registry, runs the
// §6.6 materialization pipeline, and returns the manifest body plus
// the materialized paths the host can read.
func (s *mcpServer) loadArtifact(args map[string]any) any {
	// §6.4 workspace overlay precedence: the overlay layer sits at the
	// highest precedence in the effective view. When an overlay record
	// matches the requested ID, return it directly without consulting
	// the registry.
	if id, ok := args["id"].(string); ok && id != "" {
		if rec := s.overlayMatch(id); rec != nil {
			return s.loadArtifactFromOverlay(rec, args)
		}
	}
	body, err := s.fetchJSON("/v1/load_artifact", args)
	if err != nil {
		return errorResult(err.Error())
	}
	var resp loadArtifactResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return errorResult("decode load_artifact: " + err.Error())
	}
	if resp.ID == "" {
		// Either an error envelope or an empty result; pass through.
		return jsonAny(body)
	}

	// Cache the canonical bytes (content cache is forever-immutable
	// per §6.5).
	if err := s.cache.put(resp.ContentHash, resp.Frontmatter, resp.ManifestBody, resp.Resources); err != nil {
		return errorResult("cache: " + err.Error())
	}

	// Materialize to host filesystem if a destination is configured.
	materialized := []string{}
	if s.cfg.materializeRoot != "" {
		// §6.6 step 3: HarnessAdapter translates canonical artifact.
		harnessID := s.cfg.harness
		if h, ok := args["harness"].(string); ok && h != "" {
			harnessID = h
		}
		a, err := s.adapters.Get(harnessID)
		if err != nil {
			return errorResult("config.unknown_harness: " + err.Error())
		}
		src := adapter.Source{
			ArtifactID:    resp.ID,
			ArtifactBytes: []byte(resp.Frontmatter),
			Resources:     resourcesAsBytes(resp.Resources),
		}
		// Skill artifacts carry their body in SkillBytes; the registry
		// returns the resolved manifest body, so synthesize the SKILL.md
		// from frontmatter + body when the type is skill.
		if resp.Type == "skill" {
			src.SkillBytes = []byte(synthesizeSkillMD(resp))
		}
		out, err := a.Adapt(src)
		if err != nil {
			return errorResult("adapter: " + err.Error())
		}
		// §6.6 step 5: atomic write.
		if err := materialize.Write(s.cfg.materializeRoot, out); err != nil {
			return errorResult("materialize: " + err.Error())
		}
		for _, f := range out {
			materialized = append(materialized, filepath.Join(s.cfg.materializeRoot, filepath.FromSlash(f.Path)))
		}
	}

	return map[string]any{
		"id":              resp.ID,
		"type":            resp.Type,
		"version":         resp.Version,
		"content_hash":    resp.ContentHash,
		"manifest_body":   resp.ManifestBody,
		"materialized_at": materialized,
	}
}

// loadArtifactFromOverlay produces a load_artifact response from a
// workspace overlay record, bypassing the registry per §6.4. The
// content hash is computed from the artifact bytes so the response
// shape matches the registry's.
func (s *mcpServer) loadArtifactFromOverlay(rec *filesystem.ArtifactRecord, args map[string]any) any {
	hash := sha256.Sum256(rec.ArtifactBytes)
	contentHash := "sha256:" + hex.EncodeToString(hash[:])

	resp := loadArtifactResponse{
		ID:          rec.ID,
		ContentHash: contentHash,
		Frontmatter: string(rec.ArtifactBytes),
		Layer:       "overlay",
		Resources:   resourcesAsStrings(rec.Resources),
	}
	if rec.Artifact != nil {
		resp.Type = string(rec.Artifact.Type)
		resp.Version = rec.Artifact.Version
		resp.ManifestBody = rec.Artifact.Body
	}
	if err := s.cache.put(contentHash, resp.Frontmatter, resp.ManifestBody, resp.Resources); err != nil {
		return errorResult("cache: " + err.Error())
	}
	materialized := []string{}
	if s.cfg.materializeRoot != "" {
		harnessID := s.cfg.harness
		if h, ok := args["harness"].(string); ok && h != "" {
			harnessID = h
		}
		a, err := s.adapters.Get(harnessID)
		if err != nil {
			return errorResult("config.unknown_harness: " + err.Error())
		}
		src := adapter.Source{
			ArtifactID:    rec.ID,
			ArtifactBytes: rec.ArtifactBytes,
			SkillBytes:    rec.SkillBytes,
			Resources:     rec.Resources,
		}
		out, err := a.Adapt(src)
		if err != nil {
			return errorResult("adapter: " + err.Error())
		}
		if err := materialize.Write(s.cfg.materializeRoot, out); err != nil {
			return errorResult("materialize: " + err.Error())
		}
		for _, f := range out {
			materialized = append(materialized, filepath.Join(s.cfg.materializeRoot, filepath.FromSlash(f.Path)))
		}
	}
	return map[string]any{
		"id":              resp.ID,
		"type":            resp.Type,
		"version":         resp.Version,
		"content_hash":    resp.ContentHash,
		"manifest_body":   resp.ManifestBody,
		"layer":           resp.Layer,
		"materialized_at": materialized,
	}
}

func resourcesAsStrings(in map[string][]byte) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = string(v)
	}
	return out
}

// loadArtifactResponse mirrors the registry server's
// LoadArtifactResponse so we can decode it without importing the server
// package.
type loadArtifactResponse struct {
	ID           string            `json:"id"`
	Type         string            `json:"type"`
	Version      string            `json:"version"`
	ContentHash  string            `json:"content_hash"`
	ManifestBody string            `json:"manifest_body"`
	Frontmatter  string            `json:"frontmatter"`
	Layer        string            `json:"layer,omitempty"`
	Resources    map[string]string `json:"resources,omitempty"`
}

func resourcesAsBytes(in map[string]string) map[string][]byte {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string][]byte, len(in))
	for k, v := range in {
		out[k] = []byte(v)
	}
	return out
}

func synthesizeSkillMD(r loadArtifactResponse) string {
	// The registry returns the merged manifest body. For skills the
	// agent expects a SKILL.md with the body content. The frontmatter
	// is whatever ARTIFACT.md carried; the body is the prose.
	return r.Frontmatter + r.ManifestBody
}

// fetchJSON makes an authenticated GET against the registry and returns
// the response body.
func (s *mcpServer) fetchJSON(path string, args map[string]any) ([]byte, error) {
	u, err := url.Parse(s.cfg.registry + path)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	for k, v := range args {
		// `harness` is a per-call override consumed locally; do not
		// forward to the registry since it is not a registry-side
		// argument.
		if k == "harness" {
			continue
		}
		q.Set(k, fmt.Sprintf("%v", v))
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return nil, err
	}
	if tok := s.currentToken(); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return body, fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}
	return body, nil
}

// proxyGet forwards a non-load_artifact tool call to the registry and
// returns the decoded JSON response. load_artifact gets its own
// handler because it has materialization side-effects.
func (s *mcpServer) proxyGet(path string, args map[string]any) any {
	body, err := s.fetchJSON(path, args)
	if err != nil {
		return errorResult(err.Error())
	}
	return jsonAny(body)
}

func jsonAny(b []byte) any {
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return errorResult("decode: " + err.Error())
	}
	return out
}

// currentToken reads the injected session token. §6.3.2 requires the
// MCP server to read fresh on every call so env-var or file rotations
// take effect at next request.
func (s *mcpServer) currentToken() string {
	if s.cfg.sessionTokenFile != "" {
		data, err := os.ReadFile(s.cfg.sessionTokenFile)
		if err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	return strings.TrimSpace(s.cfg.sessionToken)
}

func errorResult(msg string) map[string]any { return map[string]any{"error": msg} }

// ----- Content cache -------------------------------------------------------

// contentCache is the §6.5 content cache: maps content_hash -> manifest
// bytes + bundled resources. Content is forever-immutable by definition;
// we never expire entries.
type contentCache struct {
	dir string
}

func newContentCache(dir string) (*contentCache, error) {
	if dir == "" {
		// Cache disabled.
		return &contentCache{}, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("cache dir: %w", err)
	}
	return &contentCache{dir: dir}, nil
}

// put stores content under the cache. Returns nil when the cache is
// disabled.
func (c *contentCache) put(hash, frontmatter, body string, resources map[string]string) error {
	if c.dir == "" || hash == "" {
		return nil
	}
	bucket := filepath.Join(c.dir, sanitizeHash(hash))
	if err := os.MkdirAll(bucket, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(bucket, "frontmatter"), []byte(frontmatter), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(bucket, "body"), []byte(body), 0o644); err != nil {
		return err
	}
	for path, content := range resources {
		dest := filepath.Join(bucket, "resources", filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dest, []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// has reports whether the cache already holds the content_hash. Used
// by future cache-revalidation paths.
func (c *contentCache) has(hash string) bool {
	if c.dir == "" || hash == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(c.dir, sanitizeHash(hash), "frontmatter"))
	return err == nil
}

// sanitizeHash makes a content hash safe to use as a filesystem name.
// "sha256:abc..." becomes "sha256-abc...".
func sanitizeHash(h string) string {
	out := strings.ReplaceAll(h, ":", "-")
	// Defense-in-depth: never let a separator escape the cache root.
	out = strings.ReplaceAll(out, "/", "_")
	out = strings.ReplaceAll(out, "..", "_")
	if out == "" {
		// Pathologically empty: hash the empty string so we still
		// produce a stable bucket name.
		h := sha256.Sum256(nil)
		out = hex.EncodeToString(h[:])
	}
	return out
}
