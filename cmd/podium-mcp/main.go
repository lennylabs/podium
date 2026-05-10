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

	"github.com/lennylabs/podium/internal/buildinfo"
	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/materialize"
	"github.com/lennylabs/podium/pkg/overlay"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/sign"
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
	registry          string
	harness           string
	cacheDir          string
	cacheMode         string
	materializeRoot   string
	sessionToken      string
	sessionTokenFile  string
	overlayPath       string
	auditSink         string
	tenantID          string
	oauthAudience     string
	verifyPolicy      sign.VerificationPolicy
	signatureProvider string
	// §4.4.1 sandbox enforcement.
	enforceSandbox bool
	hostSandboxes  []string
	// ignoreSandbox is the §4.4.1 escape hatch: when true, a
	// non-unrestricted profile is materialized even on a host
	// that doesn't list it. The override is loud — surfaces in
	// the audit log and on stderr.
	ignoreSandbox bool
}

func loadConfig() (*config, error) {
	// §6.3.2 PODIUM_SESSION_TOKEN_ENV: hosts can name an env var
	// holding the JWT instead of putting the secret directly in
	// PODIUM_SESSION_TOKEN; the named var's value is read here.
	tokenSource := envDefault("PODIUM_SESSION_TOKEN_ENV", "PODIUM_SESSION_TOKEN")
	c := &config{
		registry: os.Getenv("PODIUM_REGISTRY"),
		harness:  envDefault("PODIUM_HARNESS", "none"),
		cacheDir: os.Getenv("PODIUM_CACHE_DIR"),
		// §6.5: always-revalidate (default) | offline-first | offline-only.
		cacheMode:        envDefault("PODIUM_CACHE_MODE", "always-revalidate"),
		materializeRoot:  os.Getenv("PODIUM_MATERIALIZE_ROOT"),
		sessionToken:     os.Getenv(tokenSource),
		sessionTokenFile: os.Getenv("PODIUM_SESSION_TOKEN_FILE"),
		overlayPath:      os.Getenv("PODIUM_OVERLAY_PATH"),
		auditSink:        os.Getenv("PODIUM_AUDIT_SINK"),
		tenantID:         os.Getenv("PODIUM_TENANT_ID"),
		oauthAudience:    os.Getenv("PODIUM_OAUTH_AUDIENCE"),
		// §4.7.9 / §6.2: never | medium-and-above (default) | always.
		verifyPolicy:      sign.VerificationPolicy(envDefault("PODIUM_VERIFY_SIGNATURES", string(sign.PolicyMediumAndAbove))),
		signatureProvider: envDefault("PODIUM_SIGNATURE_PROVIDER", "noop"),
		// §4.4.1 sandbox enforcement.
		enforceSandbox: os.Getenv("PODIUM_ENFORCE_SANDBOX_PROFILE") == "true",
		hostSandboxes:  splitCSV(envDefault("PODIUM_HOST_SANDBOXES", "unrestricted")),
		ignoreSandbox:  os.Getenv("PODIUM_IGNORE_SANDBOX") == "true",
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
	switch c.cacheMode {
	case "always-revalidate", "offline-first", "offline-only":
		// known modes
	default:
		return nil, fmt.Errorf("PODIUM_CACHE_MODE must be always-revalidate | offline-first | offline-only, got %q", c.cacheMode)
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
	cfg         *config
	http        *http.Client
	cache       *contentCache
	resolutions *resolutionCache
	adapters    *adapter.Registry
	overlay     []filesystem.ArtifactRecord
}

func newServer(cfg *config) (*mcpServer, error) {
	cache, err := newContentCache(cfg.cacheDir)
	if err != nil {
		return nil, err
	}
	srv := &mcpServer{
		cfg:         cfg,
		http:        &http.Client{},
		cache:       cache,
		resolutions: newResolutionCache(cfg.cacheDir),
		adapters:    adapter.DefaultRegistry(),
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
			"serverInfo": map[string]any{"name": "podium-mcp", "version": buildinfo.Version},
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
	case "prompts/list":
		// §5.2 — opt-in projection of `type: command` artifacts.
		resp.Result = s.handlePromptsList()
	case "prompts/get":
		resp.Result = s.handlePromptsGet(req.Params)
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
		return s.searchArtifacts(p.Arguments)
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

	// §6.5 cache modes: in offline-first / offline-only modes, try
	// the resolution + content cache before going to the network.
	id, version := argsIDAndVersion(args)
	if (s.cfg.cacheMode == "offline-first" || s.cfg.cacheMode == "offline-only") && id != "" {
		if hash, ok := s.resolutions.Get(id, version); ok {
			if cached, cerr := s.loadArtifactFromCache(hash, id); cerr == nil {
				return s.deliverLoadArtifact(*cached)
			}
		}
		if s.cfg.cacheMode == "offline-only" {
			return errorResult(errOfflineCacheMiss.Error())
		}
	}

	body, err := s.fetchJSON("/v1/load_artifact", args)
	if err != nil {
		// §7.4 degraded-network fallback: in always-revalidate
		// mode, if a fresh fetch fails, try to serve from cache
		// before surfacing the registry-unreachable error. Cache
		// misses surface as network.registry_unreachable.
		if s.cfg.cacheMode == "always-revalidate" && id != "" {
			if hash, ok := s.resolutions.Get(id, version); ok {
				if cached, cerr := s.loadArtifactFromCache(hash, id); cerr == nil {
					out := s.deliverLoadArtifact(*cached, deliverOpts{harness: harnessFromArgs(s.cfg.harness, args)})
					if m, ok := out.(map[string]any); ok {
						m["status"] = "offline"
						m["served_from_cache"] = true
					}
					return out
				}
			}
			return errorResult("network.registry_unreachable: " + err.Error())
		}
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
	// Update the resolution cache so future offline-first reads
	// know the (id, version) → content_hash mapping.
	s.resolutions.Put(id, version, resp.ContentHash)
	if resp.Version != "" && resp.Version != version {
		// Also memoize the explicit version for `version=""` (latest)
		// requests so a later pinned request can serve from cache.
		s.resolutions.Put(id, resp.Version, resp.ContentHash)
	}

	return s.deliverLoadArtifact(resp, deliverOpts{harness: harnessFromArgs(s.cfg.harness, args)})
}

// deliverOpts threads the per-call adjustments deliverLoadArtifact
// needs (currently only the harness override).
type deliverOpts struct {
	harness string
}

// deliverLoadArtifact runs §6.6 verification + materialization
// against an already-fetched (or cached) load_artifact response.
// Shared between the live-fetch and cache-served code paths so
// PODIUM_VERIFY_SIGNATURES and the sandbox profile enforcement
// run uniformly regardless of cache mode.
func (s *mcpServer) deliverLoadArtifact(resp loadArtifactResponse, opts ...deliverOpts) any {
	var o deliverOpts
	if len(opts) > 0 {
		o = opts[0]
	}
	// §4.7.9 / §6.2: enforce signature verification per
	// PODIUM_VERIFY_SIGNATURES before the artifact materializes
	// onto the host filesystem.
	if err := s.enforceSignaturePolicy(resp); err != nil {
		return errorResult("materialize.signature_invalid: " + err.Error())
	}
	// §4.4.1 sandbox profile enforcement.
	if err := s.enforceSandboxPolicy(resp); err != nil {
		return errorResult("materialize.sandbox_unsupported: " + err.Error())
	}
	// §6.6 step 1 — fetch every large_resource via its presigned
	// URL into the inline Resources map. Failures (network /
	// 403 / hash mismatch) abort materialization with a
	// structured error.
	if err := s.fetchLargeResources(&resp); err != nil {
		return errorResult("materialize.fetch_failed: " + err.Error())
	}

	// Cache the canonical bytes (content cache is forever-immutable
	// per §6.5).
	if err := s.cache.put(resp.ContentHash, resp.Frontmatter, resp.ManifestBody, resp.Resources); err != nil {
		return errorResult("cache: " + err.Error())
	}

	// Materialize to host filesystem if a destination is configured.
	materialized := []string{}
	if s.cfg.materializeRoot != "" {
		harnessID := o.harness
		if harnessID == "" {
			harnessID = s.cfg.harness
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
		if resp.Type == "skill" {
			src.SkillBytes = []byte(synthesizeSkillMD(resp))
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
		"materialized_at": materialized,
	}
}

// harnessFromArgs returns args["harness"] as a string when set,
// otherwise the deployment default.
func harnessFromArgs(defaultID string, args map[string]any) string {
	if h, ok := args["harness"].(string); ok && h != "" {
		return h
	}
	return defaultID
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
	ID             string                       `json:"id"`
	Type           string                       `json:"type"`
	Version        string                       `json:"version"`
	ContentHash    string                       `json:"content_hash"`
	ManifestBody   string                       `json:"manifest_body"`
	Frontmatter    string                       `json:"frontmatter"`
	Layer          string                       `json:"layer,omitempty"`
	Sensitivity    string                       `json:"sensitivity,omitempty"`
	Resources      map[string]string            `json:"resources,omitempty"`
	LargeResources map[string]largeResourceLink `json:"large_resources,omitempty"`
	Signature      string                       `json:"signature,omitempty"`
}

// largeResourceLink mirrors the registry's per-resource link. The
// MCP server fetches the URL during materialization, retrying on
// 403/expired (§6.6 step 1) up to three times.
type largeResourceLink struct {
	URL         string `json:"url"`
	ContentHash string `json:"content_hash"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type,omitempty"`
}

// enforceSignaturePolicy applies the configured §4.7.9 verification
// policy against the response. Returns nil when the policy is
// satisfied (either signature checks out or sensitivity falls below
// the threshold); returns the verification error otherwise.
func (s *mcpServer) enforceSignaturePolicy(resp loadArtifactResponse) error {
	provider, err := buildSignatureProvider(s.cfg.signatureProvider)
	if err != nil {
		return err
	}
	return sign.EnforceVerification(
		s.cfg.verifyPolicy,
		provider,
		manifest.Sensitivity(resp.Sensitivity),
		resp.ContentHash,
		resp.Signature,
	)
}

// enforceSandboxPolicy applies the §4.4.1 sandbox-profile gate.
// Default behavior:
//
//   - sandbox_profile=unrestricted (or omitted): always allow.
//   - sandbox_profile=other and host supports it: allow.
//   - sandbox_profile=other and host doesn't support it: refuse,
//     unless ignoreSandbox is set, in which case warn loudly and
//     allow.
//
// The legacy enforceSandbox flag is preserved for callers that
// want strict enforcement even for unrestricted profiles, but the
// spec says hosts MUST refuse a non-unrestricted profile by
// default — that's the new minimum.
func (s *mcpServer) enforceSandboxPolicy(resp loadArtifactResponse) error {
	a, err := manifest.ParseArtifact([]byte(resp.Frontmatter))
	if err != nil {
		// Fail closed: malformed frontmatter is a refusal.
		return fmt.Errorf("parse frontmatter: %v", err)
	}
	profile := string(a.SandboxProfile)
	if profile == "" {
		profile = string(manifest.SandboxUnrestricted)
	}
	if profile == string(manifest.SandboxUnrestricted) {
		return nil
	}
	for _, supported := range s.cfg.hostSandboxes {
		if supported == profile {
			return nil
		}
	}
	if s.cfg.ignoreSandbox {
		// §4.4.1 — explicit override: log loudly so operators see
		// the violation in the audit trail.
		fmt.Fprintf(os.Stderr,
			"WARN: PODIUM_IGNORE_SANDBOX bypassing sandbox check — artifact %s wants sandbox_profile=%s; host supports %v\n",
			resp.ID, profile, s.cfg.hostSandboxes)
		return nil
	}
	return fmt.Errorf("artifact requires sandbox_profile=%s; host supports %v",
		profile, s.cfg.hostSandboxes)
}

// splitCSV splits a comma-separated env-var value into trimmed
// non-empty entries.
func splitCSV(s string) []string {
	out := []string{}
	cur := ""
	flush := func() {
		t := strings.TrimSpace(cur)
		if t != "" {
			out = append(out, t)
		}
		cur = ""
	}
	for _, r := range s {
		if r == ',' {
			flush()
			continue
		}
		cur += string(r)
	}
	flush()
	return out
}

// buildSignatureProvider mirrors the CLI side: a Noop default,
// Sigstore-keyless when env vars supply Fulcio + Rekor, and
// registry-managed for tenant-key deployments.
func buildSignatureProvider(name string) (sign.Provider, error) {
	switch name {
	case "", "noop":
		return sign.Noop{}, nil
	case "sigstore-keyless":
		root, _ := os.ReadFile(os.Getenv("PODIUM_SIGSTORE_TRUST_ROOT_PEM_FILE"))
		return sign.SigstoreKeyless{
			FulcioURL: os.Getenv("PODIUM_SIGSTORE_FULCIO_URL"),
			RekorURL:  os.Getenv("PODIUM_SIGSTORE_REKOR_URL"),
			OIDCToken: os.Getenv("PODIUM_SIGSTORE_OIDC_TOKEN"),
			TrustRoot: root,
		}, nil
	case "registry-managed":
		return sign.RegistryManagedKey{}, nil
	}
	return nil, fmt.Errorf("unknown PODIUM_SIGNATURE_PROVIDER: %s", name)
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
	// §6.3 PODIUM_TENANT_ID: forwards the tenant context to the
	// registry on every request so multi-tenant deployments can
	// route without parsing the JWT.
	if s.cfg.tenantID != "" {
		req.Header.Set("X-Podium-Tenant", s.cfg.tenantID)
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

// currentToken reads the injected session token. §6.3.2.1 requires
// the MCP server to read fresh on every call so env-var or file
// rotations take effect at the next request without a restart or
// signal.
func (s *mcpServer) currentToken() string {
	// File source wins when configured; the file is the canonical
	// rotation surface for hosts that can write it with restrictive
	// permissions.
	if s.cfg.sessionTokenFile != "" {
		data, err := os.ReadFile(s.cfg.sessionTokenFile)
		if err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	// Env var: re-read at call time so rotations land on the next
	// registry call without requiring SIGHUP.
	tokenSource := os.Getenv("PODIUM_SESSION_TOKEN_ENV")
	if tokenSource == "" {
		tokenSource = "PODIUM_SESSION_TOKEN"
	}
	if v := os.Getenv(tokenSource); v != "" {
		return strings.TrimSpace(v)
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
