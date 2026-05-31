// Package server exposes the registry HTTP/JSON API (spec §5, §6.10).
// The handlers translate HTTP requests into pkg/registry/core calls;
// all spec-defined logic (visibility, version resolution, search, etc.)
// lives in core, matching the §2.2 shared-library-code architecture.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/lint"
	"github.com/lennylabs/podium/pkg/manifest"
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
	core       *core.Registry
	publicMode bool
	resolveID  func(*http.Request) layer.Identity
	// idVerifier verifies the per-request caller token and maps it to an
	// Identity (§6.3.2 injected-session-token). When set, the meta-tool
	// routes verify on every call: a verification failure rejects the
	// request with the §6.10 auth.* envelope before the handler runs, and
	// a success carries the Identity to the handler via the request
	// context. Nil leaves the server on the resolveID path (anonymous
	// public by default), preserving standalone / public-mode behavior.
	idVerifier func(*http.Request) (layer.Identity, error)
	// events is the §7.6 in-process pub/sub for /v1/events.
	events *eventBus
	// objectStore is the §7.2 data-plane backend. load_artifact reads
	// small resources from it (when not inline) and presigns large ones;
	// the /objects/{key} route streams the bytes for the filesystem
	// backend. Nil when no object store is configured, in which case the
	// resources ingest left inline on the record still serve.
	objectStore   objectstore.Provider
	objectBaseURL string
	presignTTL    time.Duration
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
	// mode reports the §13.2.1 ready / read_only state. /healthz
	// and /readyz consult it. Optional; nil leaves both endpoints
	// reporting "ready".
	mode *ModeTracker
	// readiness holds the §13.9 dependency probes /readyz runs at
	// request time (metadata store, object storage). A failing probe
	// downgrades /readyz to not_ready (503). Empty leaves /readyz
	// reporting ready / read_only from the mode tracker alone.
	readiness []ReadinessCheck
	// lag reports the §13.2.1 observed replication lag in seconds for
	// /readyz and the X-Podium-Read-Only-Lag-Seconds header. Nil
	// reports 0 (the genuine value for a standalone deployment with
	// no read replica).
	lag LagReporter
	// auditSink records §8.1 events that originate at the HTTP boundary
	// (admin.granted). Read events flow through the core emitter; sharing
	// the same sink keeps both on one §8.6 hash chain. Nil is a no-op.
	auditSink *audit.FileSink
	// latency, when set, receives one §7.1 timing observation per served
	// request (operation name, status, elapsed) so a deployment can compare
	// observed latency against the SLO budgets. Nil disables timing with
	// zero per-request overhead. Wired by serverboot to a structured access
	// log; pluggable for a histogram exporter.
	latency LatencyObserver
}

// readyProbeTimeout bounds the §13.9 dependency probes so a hung
// metadata-store or object-store call can never block the /readyz
// handler past this deadline.
const readyProbeTimeout = 2 * time.Second

// ReadinessCheck probes one dependency for §13.9 readiness. It returns
// nil when the dependency is reachable and a non-nil error describing
// the outage otherwise. Checks must honor ctx cancellation so /readyz
// stays bounded.
type ReadinessCheck func(ctx context.Context) error

// LagReporter returns the §13.2.1 observed replication lag in seconds.
// A standalone deployment with no replica reports 0; a standard
// deployment wires a reporter backed by the replica's replay
// timestamp.
type LagReporter func(ctx context.Context) int

// WithMode installs the §13.2.1 mode tracker so /healthz and
// /readyz reflect the read-only / ready state.
func WithMode(m *ModeTracker) Option {
	return func(s *Server) { s.mode = m }
}

// WithReadinessChecks installs the §13.9 dependency probes consulted by
// /readyz. Each check pings one dependency (the metadata store, object
// storage); any failing check makes /readyz report not_ready and answer
// 503 so a load balancer pulls the registry out of rotation.
func WithReadinessChecks(checks ...ReadinessCheck) Option {
	return func(s *Server) { s.readiness = append(s.readiness, checks...) }
}

// WithLagReporter installs the §13.2.1 replication-lag source threaded
// into the /readyz body and the X-Podium-Read-Only-Lag-Seconds header.
func WithLagReporter(fn LagReporter) Option {
	return func(s *Server) { s.lag = fn }
}

// WithQuotaLimiter installs the §4.7.8 rate limiter for search
// QPS and materialize rate. Zero limits inside the limiter
// disable the check per dimension.
func WithQuotaLimiter(q *QuotaLimiter) Option {
	return func(s *Server) { s.quota = q }
}

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

// WithIdentityVerifier installs the §6.3.2 per-request token verifier used
// when the registry runs the injected-session-token provider. The function
// extracts and verifies the bearer token and returns the caller Identity,
// or an error (identity.ErrUntrustedRuntime / identity.ErrTokenExpired)
// that the meta-tool routes surface as the §6.10 auth.* envelope. Setting
// it makes the registry verify the signature on every meta-tool call;
// without it the server stays on the anonymous resolveID path.
func WithIdentityVerifier(fn func(*http.Request) (layer.Identity, error)) Option {
	return func(s *Server) { s.idVerifier = fn }
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
// request.
func WithTenant(t string) Option {
	return func(s *Server) { s.tenant = t }
}

// WithAudit installs the §8.3 audit sink used to record HTTP-boundary
// events (admin.granted). The same sink backs the core read-event emitter
// so both streams stay on one §8.6 hash chain.
func WithAudit(sink *audit.FileSink) Option {
	return func(s *Server) { s.auditSink = sink }
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
// every layer into a fresh in-memory store, and returns a Server
// wrapping the resulting core.Registry. Bundled resources persist at
// ingest (§7.2 data plane): when an object store is configured via
// WithObjectStore, each resource uploads keyed by its content hash and
// resources above the §4.2 cutoff serve via presigned URL; otherwise
// they stay inline on the manifest record. This is the standalone
// bootstrap helper used by tests and the standalone server.
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
	// Discover the configured object store ahead of ingest so the
	// data-plane upload runs in the ingest pipeline (the same path the
	// standalone server's bootstrapLayerPath uses). Applying the options
	// to a throwaway server reads objectStore without duplicating the
	// option wiring; New applies them again to the real server.
	probe := &Server{}
	for _, opt := range opts {
		opt(probe)
	}
	var resourcePut ingest.ResourcePutFunc
	if probe.objectStore != nil {
		resourcePut = probe.objectStore.Put
	}
	// §13.10/§13.2.2 public-mode sensitivity ceiling: when the server runs in
	// public mode, reject medium and high artifacts at ingest. Read from the
	// applied options so the standalone bootstrap and tests share one floor.
	var rejectAtOrAbove manifest.Sensitivity
	if probe.publicMode {
		rejectAtOrAbove = manifest.SensitivityMedium
	}
	layers := make([]layer.Layer, 0, len(reg.Layers))
	for i, l := range reg.Layers {
		layerFS := newDirFS(l.Path)
		if _, err := ingest.Ingest(context.Background(), st, ingest.Request{
			TenantID:        tenant,
			LayerID:         l.ID,
			Files:           layerFS,
			RejectAtOrAbove: rejectAtOrAbove,
			// §4.4: validate prose URL references with an HTTP HEAD by
			// default; PODIUM_INGEST_OFFLINE=true skips the network probe.
			Linter:      lint.NewIngestLinter(os.Getenv("PODIUM_INGEST_OFFLINE") == "true"),
			ResourcePut: resourcePut,
		}); err != nil {
			return nil, err
		}
		layers = append(layers, layer.Layer{
			ID:         l.ID,
			Precedence: i + 1,
			Visibility: bootstrapVisibility(l),
		})
	}

	registry := core.New(st, tenant, layers)
	// §3.3 / §12 learn-from-usage: the standalone server reranks search and
	// load_domain by access frequency, like the standard-topology registry.
	registry = registry.WithUsageSignals(core.NewMemoryUsageSignals())
	return New(registry, opts...), nil
}

// bootstrapVisibility resolves the runtime visibility of a filesystem-source
// layer. A layer that declares visibility via its .layer-config file (§4.6)
// uses that declaration; a layer without one defaults to public, the §13.10
// standalone bootstrap default. This lets a fixture or migrated directory
// express every visibility mode through the filesystem load path while
// preserving the all-public default for layers that say nothing.
func bootstrapVisibility(l filesystem.Layer) layer.Visibility {
	if !l.HasVisibility {
		return layer.Visibility{Public: true}
	}
	return layer.Visibility{
		Public:       l.Visibility.Public,
		Organization: l.Visibility.Organization,
		Groups:       l.Visibility.Groups,
		Users:        l.Visibility.Users,
	}
}

// Handler returns an http.Handler with every meta-tool route registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/readyz", s.handleReady)
	mux.HandleFunc("/v1/load_domain", s.handleLoadDomain)
	mux.HandleFunc("/v1/search_domains", s.handleSearchDomains)
	mux.HandleFunc("/v1/search_artifacts", s.handleSearchArtifacts)
	mux.HandleFunc("/v1/load_artifact", s.handleLoadArtifact)
	mux.HandleFunc("/v1/artifacts:batchLoad", s.handleBatchLoad)
	mux.HandleFunc("/v1/sync/manifest", s.handleSyncManifest)
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
	// §7.1: time the full request (outermost so the measured duration spans
	// identity verification, audit, and the handler) and report it to the
	// latency observer, keyed by operation name, for SLO comparison. Then
	// §6.3.2: verify the caller token on every meta-tool call (a rejected
	// request never reaches the audit emitter or a handler), then §8.1:
	// attach per-request audit metadata (trace id + structured caller
	// identity) to every request context so read events emitted by the core
	// and the HTTP write handlers carry it.
	return s.withLatencyObserver(s.withIdentityVerification(s.withAuditMetaMiddleware(s.withReadOnlyHeaders(mux))))
}

// withReadOnlyHeaders wraps the route mux so every response
// carries the §13.2.1 read-only signal headers when the mode
// tracker is flipped. Applied uniformly: callers don't need to
// remember to set them on each handler.
func (s *Server) withReadOnlyHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.mode != nil && s.mode.Get() == ModeReadOnly {
			w.Header().Set("X-Podium-Read-Only", "true")
			// Observed replication lag at response time (§13.2.1).
			// A standalone deployment with no replica reports 0;
			// the field stays a numeric string so clients parse it
			// uniformly.
			lag := s.replicationLagSeconds(r.Context())
			w.Header().Set("X-Podium-Read-Only-Lag-Seconds", strconv.Itoa(lag))
		}
		next.ServeHTTP(w, r)
	})
}

// ----- Response shapes ------------------------------------------------------

// HealthResponse describes /healthz output (§13.9). The endpoint is a
// liveness signal: it reports the mode string (§13.2.1 read_only,
// §13.2.2 public, or ready) and conveys liveness through the 200 status
// alone. Readiness lives on /readyz; /healthz carries no readiness
// boolean.
type HealthResponse struct {
	Mode string `json:"mode"`
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

// DomainDescriptor is one subdomain entry. Subdomains carries the
// nested child tree when load_domain expands more than one level
// (§4.5.5 depth); it is omitted for leaf entries and at the deepest
// rendered level.
type DomainDescriptor struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Keywords and Score populate the §3.2 Layer 1 search_domains
	// descriptor (path, name, description, keywords, score). Both are
	// omitted for load_domain subdomain entries, which carry
	// path/name/description (and the nested subtree) only.
	Keywords   []string           `json:"keywords,omitempty"`
	Score      float64            `json:"score,omitempty"`
	Subdomains []DomainDescriptor `json:"subdomains,omitempty"`
}

// ArtifactDescriptor is one artifact entry.
type ArtifactDescriptor struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	Version     string   `json:"version,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Score       float64  `json:"score,omitempty"`
	// Sensitivity is the classification label surfaced in search_artifacts
	// results (§4.7.4), resolved most-restrictive across an extends chain
	// (§4.6). Omitted when the artifact declares no sensitivity.
	Sensitivity string `json:"sensitivity,omitempty"`
	// FoldedFrom is the relative subpath a notable artifact was lifted
	// from when fold_below_artifacts collapsed its sparse subdomain
	// into this domain's leaf set (§4.5.5 folding mechanics). Empty for
	// a direct child of the requested domain.
	FoldedFrom string `json:"folded_from,omitempty"`
	// Source tags a load_domain notable entry with its §4.5.5
	// notable-selection source: "featured" for an author-curated entry,
	// "signal" otherwise. Omitted on search results, which carry no
	// notable source.
	Source string `json:"source,omitempty"`
	// Frontmatter carries the artifact's frontmatter on search_artifacts
	// results per the §7.6.1 read-CLI JSON schema. Omitted on load_domain
	// notable entries, which carry Summary instead.
	Frontmatter string `json:"frontmatter,omitempty"`
	// Summary carries the artifact's short summary on load_domain notable
	// entries per the §7.6.1 schema. Omitted on search results, which carry
	// Frontmatter instead.
	Summary string `json:"summary,omitempty"`
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
	ID           string `json:"id"`
	Type         string `json:"type"`
	Version      string `json:"version"`
	ContentHash  string `json:"content_hash"`
	ManifestBody string `json:"manifest_body"`
	Frontmatter  string `json:"frontmatter"`
	// SkillRaw is the verbatim SKILL.md for a type: skill artifact (§4.3.4),
	// so a server-source consumer materializes the authored skill file
	// byte-for-byte instead of reconstructing it from ARTIFACT.md frontmatter
	// plus body (§11 filesystem ↔ server equivalence). Empty for non-skills.
	SkillRaw       string                       `json:"skill_raw,omitempty"`
	Layer          string                       `json:"layer,omitempty"`
	Sensitivity    string                       `json:"sensitivity,omitempty"`
	Resources      map[string]string            `json:"resources,omitempty"`
	ResourcesB64   bool                         `json:"resources_base64,omitempty"`
	LargeResources map[string]LargeResourceLink `json:"large_resources,omitempty"`
	// ManifestMerged signals that Frontmatter is an extends-merged
	// re-serialization with the hidden parent stripped (§4.6), so its bytes
	// no longer reproduce ContentHash. The consumer skips local content-hash
	// verification for such manifests (§6.6 step 2).
	ManifestMerged bool `json:"manifest_merged,omitempty"`
	// Deprecated, ReplacedBy, and DeprecationWarning surface the
	// §4.7.4 lifecycle signal so consumers see the warning
	// alongside the served bytes and can route callers to the
	// upgrade target when set.
	Deprecated         bool   `json:"deprecated,omitempty"`
	ReplacedBy         string `json:"replaced_by,omitempty"`
	DeprecationWarning string `json:"deprecation_warning,omitempty"`
	// Signature is the §4.7.9 envelope produced at ingest by the
	// configured SignatureProvider. Empty when ingest had no
	// signer wired. Consumers verify against
	// PODIUM_VERIFY_SIGNATURES at materialize time.
	Signature string `json:"signature,omitempty"`
}

// LargeResourceLink describes one resource whose payload exceeded
// the inline cutoff. The presigned URL's auth model is backend-specific:
// the S3 backend embeds an AWS Signature V4 in the URL; the
// filesystem backend's URL points at the registry's authenticated
// /objects/{content_hash} route. ContentHash lets the consumer
// verify the bytes after fetching. The field is named presigned_url to
// match the §7.6.2 batch-load wire example, so both load paths use one
// name for the same reference.
type LargeResourceLink struct {
	URL         string `json:"presigned_url"`
	ContentHash string `json:"content_hash"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type,omitempty"`
}

// ErrorResponse is the JSON envelope for §6.10 structured errors. The
// field order mirrors the spec example: code, message, details,
// retryable, suggested_action. Details carries machine-readable context
// for codes whose spec example includes it (for example runtime_iss for
// auth.untrusted_runtime); it is omitted when empty.
type ErrorResponse struct {
	Code            string         `json:"code"`
	Message         string         `json:"message"`
	Details         map[string]any `json:"details,omitempty"`
	Retryable       bool           `json:"retryable"`
	SuggestedAction string         `json:"suggested_action,omitempty"`
}

// ----- Handlers -------------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, HealthResponse{Mode: s.modeBanner()})
}

// modeBanner returns the canonical mode string per §13.2.1:
// "ready" by default, "read_only" when the ModeTracker has flipped,
// "public" when the §13.10 public mode is engaged. Public-mode
// short-circuits read-only so a public-mode standalone with no
// metadata store still reports its serving mode clearly.
func (s *Server) modeBanner() string {
	if s.publicMode {
		return "public"
	}
	if s.mode != nil && s.mode.Get() == ModeReadOnly {
		return "read_only"
	}
	return "ready"
}

// ReadyResponse describes /readyz output (§13.9).
type ReadyResponse struct {
	Mode               string `json:"mode"`
	ReplicationLagSecs int    `json:"replication_lag_seconds"`
}

// handleReady answers /readyz per §13.9. The status code follows
// load-balancer conventions: ready / read_only return 200 (the
// registry stays in rotation), not_ready returns 503. The body
// always carries the observed replication lag in seconds (0 for a
// standalone deployment with no replica).
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readyProbeTimeout)
	defer cancel()
	mode := s.readyMode(ctx)
	resp := ReadyResponse{Mode: mode, ReplicationLagSecs: s.replicationLagSeconds(ctx)}
	status := http.StatusOK
	if mode == "not_ready" {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, resp)
}

// readyMode computes the §13.9 /readyz state, which the spec restricts
// to ready, read_only, or not_ready (public mode is a /healthz signal,
// not a readiness state, so it is intentionally absent here). The
// precedence is: not_ready when any §13.9 dependency probe fails (a hard
// outage takes the registry out of rotation), then read_only when the
// §13.2.1 tracker has flipped (degraded but still serving reads), then
// ready.
func (s *Server) readyMode(ctx context.Context) string {
	for _, check := range s.readiness {
		if check == nil {
			continue
		}
		if err := check(ctx); err != nil {
			return ModeNotReady.String()
		}
	}
	if s.mode != nil && s.mode.Get() == ModeReadOnly {
		return ModeReadOnly.String()
	}
	return ModeReady.String()
}

// replicationLagSeconds returns the §13.2.1 observed replication lag.
// Nil reporter (or a negative reading) reports 0, the genuine value for
// a standalone deployment with no read replica.
func (s *Server) replicationLagSeconds(ctx context.Context) int {
	if s.lag == nil {
		return 0
	}
	if n := s.lag(ctx); n > 0 {
		return n
	}
	return 0
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
		resp.Subdomains = append(resp.Subdomains, domainDescriptorOf(d))
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
			Keywords: d.Keywords, Score: d.Score,
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
		// spec: §7.6 — search_artifacts accepts session_id.
		SessionID: q.Get("session_id"),
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
	opts := core.ReembedOptions{OnlyIfMissing: q.Get("only_missing") == "true"}
	// spec: §4.7 `--since <timestamp>` — RFC3339 cutoff on IngestedAt.
	if since := q.Get("since"); since != "" {
		ts, perr := time.Parse(time.RFC3339, since)
		if perr != nil {
			writeError(w, http.StatusBadRequest, "registry.invalid_argument",
				"since must be an RFC3339 timestamp: "+perr.Error())
			return
		}
		opts.Since = ts
	}
	res, err := s.core.Reembed(r.Context(), opts)
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

// handleScopePreview serves §3.5 GET /v1/scope/preview. It rejects
// non-GET methods like the sibling read endpoints (F-3.5.9) and maps the
// §3.5 tenant gate (expose_scope_preview: false) to 403
// scope_preview_disabled (F-3.5.1).
func (s *Server) handleScopePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "registry.invalid_argument",
			"method not allowed: "+r.Method)
		return
	}
	preview, err := s.core.PreviewScope(r.Context(), s.identity(r))
	if err != nil {
		if errors.Is(err, core.ErrScopePreviewDisabled) {
			writeError(w, http.StatusForbidden, "scope_preview_disabled",
				"scope preview is disabled for this tenant")
			return
		}
		s.writeCoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) handleLoadArtifact(w http.ResponseWriter, r *http.Request) {
	// §6.5: GET materializes; HEAD revalidates the MCP resolution cache by
	// returning the resolved content hash in a header without a body.
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "registry.method_not_allowed", "method not allowed: "+r.Method)
		return
	}
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
		// §5 load_artifact "Optional session_id"; §4.7.6 — within a
		// session the first `latest` lookup pins, so a later same-id
		// lookup resolves to the same version even after a newer ingest.
		// The batch-load path already honors this; wiring it here lets
		// the single-artifact GET (the MCP bridge's load_artifact) carry
		// the same session consistency.
		SessionID: q.Get("session_id"),
	})
	if err != nil {
		s.writeCoreError(w, err)
		return
	}
	// §12 ETag caching of immutable artifact versions: the content hash is a
	// strong validator for a resolved (id, version) pair, so it is published
	// as the response ETag. A request that carries a matching If-None-Match is
	// served 304 Not Modified with no body, letting the MCP client read its
	// content-addressed cache instead of re-downloading the manifest body and
	// re-presigning resources. The check runs for GET and HEAD alike and
	// before any resource presigning so a revalidated hit avoids that work.
	if etag := contentHashETag(res.ContentHash); etag != "" {
		w.Header().Set("ETag", etag)
		if ifNoneMatchHit(r.Header.Get("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	// §6.5 HEAD revalidation: report the resolved content hash (and version)
	// in headers so the MCP cache confirms an unchanged artifact without
	// downloading the manifest body or presigning resources.
	if r.Method == http.MethodHead {
		w.Header().Set("X-Podium-Content-Hash", res.ContentHash)
		if res.Version != "" {
			w.Header().Set("X-Podium-Version", res.Version)
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	resp := LoadArtifactResponse{
		ID:                 res.ID,
		Type:               res.Type,
		Version:            res.Version,
		ContentHash:        res.ContentHash,
		ManifestBody:       res.ManifestBody,
		Frontmatter:        string(res.Frontmatter),
		SkillRaw:           string(res.SkillRaw),
		Layer:              res.Layer,
		Sensitivity:        res.Sensitivity,
		Deprecated:         res.Deprecated,
		ReplacedBy:         res.ReplacedBy,
		DeprecationWarning: res.DeprecationWarning,
		Signature:          res.Signature,
		ManifestMerged:     res.Merged,
	}
	// §7.2 data plane: resources at or below the inline cutoff return
	// inline; larger ones return as presigned URLs the consumer fetches
	// directly from object storage.
	if err := s.attachResources(r.Context(), &resp, res.Resources); err != nil {
		writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// contentHashETag formats a resolved content hash as a strong HTTP ETag
// (a quoted opaque string). spec: §12 ETag caching of immutable artifact
// versions. An empty hash yields an empty ETag so the caller skips the
// header rather than emitting `""`.
func contentHashETag(contentHash string) string {
	if contentHash == "" {
		return ""
	}
	return `"` + contentHash + `"`
}

// ifNoneMatchHit reports whether an If-None-Match request header matches the
// artifact's ETag. It accepts the wildcard `*`, a comma-separated list of
// entity tags, and weak validators (`W/"..."`), comparing on the opaque tag
// value per RFC 7232. The MCP client sends the single content-hash ETag it
// cached, so the common case is one entry.
func ifNoneMatchHit(ifNoneMatch, etag string) bool {
	if ifNoneMatch == "" {
		return false
	}
	if strings.TrimSpace(ifNoneMatch) == "*" {
		return true
	}
	want := strings.TrimPrefix(etag, "W/")
	for _, candidate := range strings.Split(ifNoneMatch, ",") {
		c := strings.TrimSpace(candidate)
		c = strings.TrimPrefix(c, "W/")
		if c == want {
			return true
		}
	}
	return false
}

// attachResources splits the §4.4 bundled resources of a load result
// into the §7.2 inline set (at or below objectstore.InlineCutoff) and
// the large set (above it). Small resources serve from the bytes ingest
// stored inline, falling back to an object-store read; large resources
// presign against the configured store.
func (s *Server) attachResources(ctx context.Context, resp *LoadArtifactResponse, refs []store.ResourceRef) error {
	for _, ref := range refs {
		if ref.Size > objectstore.InlineCutoff {
			link, err := s.presignResource(ctx, ref)
			if err != nil {
				return err
			}
			if resp.LargeResources == nil {
				resp.LargeResources = map[string]LargeResourceLink{}
			}
			resp.LargeResources[ref.Path] = link
			continue
		}
		body, err := s.inlineBytes(ctx, ref)
		if err != nil {
			return err
		}
		if resp.Resources == nil {
			resp.Resources = map[string]string{}
		}
		resp.Resources[ref.Path] = string(body)
	}
	return nil
}

// presignResource builds a §7.2 large-resource link, presigning the
// object-store key (the bare content hash) for the consumer to fetch.
func (s *Server) presignResource(ctx context.Context, ref store.ResourceRef) (LargeResourceLink, error) {
	if s.objectStore == nil {
		return LargeResourceLink{}, fmt.Errorf("no object store configured for large resource %s", ref.Path)
	}
	url, err := s.objectStore.Presign(ctx, resourceKey(ref), s.presignTTL)
	if err != nil {
		return LargeResourceLink{}, fmt.Errorf("presign large resource %s: %w", ref.Path, err)
	}
	return LargeResourceLink{
		URL:         url,
		ContentHash: ref.ContentHash,
		Size:        ref.Size,
		ContentType: ref.ContentType,
	}, nil
}

// inlineBytes returns a small resource's bytes for inline delivery: the
// copy ingest stored on the record when present, otherwise an
// object-store read by content hash.
func (s *Server) inlineBytes(ctx context.Context, ref store.ResourceRef) ([]byte, error) {
	if ref.Inline != nil {
		return ref.Inline, nil
	}
	if s.objectStore == nil {
		return nil, fmt.Errorf("no object store configured for resource %s", ref.Path)
	}
	body, err := s.objectStore.Get(ctx, resourceKey(ref))
	if err != nil {
		return nil, fmt.Errorf("read resource %s: %w", ref.Path, err)
	}
	return body, nil
}

// resourceKey is the object-store key for a resource: the content hash
// with the "sha256:" prefix stripped, which is what makes identical
// bytes deduplicate across artifact versions (§4.4).
func resourceKey(ref store.ResourceRef) string {
	return strings.TrimPrefix(ref.ContentHash, "sha256:")
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
	if s.objectStore == nil {
		writeError(w, http.StatusNotFound, "registry.not_found", "no such object")
		return
	}

	// Re-check visibility on every fetch: the caller must currently see
	// an artifact that bundles these bytes (§4.4 deduplicates by content
	// hash, so any one visible owner authorizes the read). A caller who
	// has lost access can no longer follow a previously-issued URL.
	if _, ok := s.core.ResolveResourceOwner(r.Context(), s.identity(r), key); !ok {
		writeError(w, http.StatusNotFound, "registry.not_found", "no such object")
		return
	}

	// HEAD reports size without reading the body (§7.2: the control plane
	// never streams large bytes it does not have to).
	if r.Method == http.MethodHead {
		info, err := s.objectStore.Stat(r.Context(), key)
		if err != nil {
			s.writeObjectStoreError(w, err)
			return
		}
		s.writeObjectHeaders(w, key, info)
		w.WriteHeader(http.StatusOK)
		return
	}

	// GET streams the bytes straight to the client instead of buffering
	// the whole resource in memory (§7.2 / F-7.2.4).
	reader, info, err := s.objectStore.GetStream(r.Context(), key)
	if err != nil {
		s.writeObjectStoreError(w, err)
		return
	}
	defer reader.Close()
	s.writeObjectHeaders(w, key, info)
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, reader)
}

// writeObjectHeaders sets the §7.2 data-plane response headers: the
// content type (from the stored metadata, falling back to a generic
// type), the content length, and the X-Content-Hash the consumer
// verifies against.
func (s *Server) writeObjectHeaders(w http.ResponseWriter, key string, info objectstore.ObjectInfo) {
	contentType := info.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
	w.Header().Set("X-Content-Hash", "sha256:"+key)
	// §13.7: the object key is the content hash, so these bytes are immutable
	// and safe to cache indefinitely. Emit the immutable Cache-Control header a
	// CDN honors at the origin.
	w.Header().Set("Cache-Control", objectstore.ImmutableCacheControl)
}

// writeObjectStoreError maps an object-store read error to the §6.10
// envelope: a missing key is a 404, anything else a 503.
func (s *Server) writeObjectStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, objectstore.ErrNotFound) {
		writeError(w, http.StatusNotFound, "registry.not_found", "object not found")
		return
	}
	writeError(w, http.StatusInternalServerError, "registry.unavailable", err.Error())
}

// ----- Helpers --------------------------------------------------------------

func (s *Server) identity(r *http.Request) layer.Identity {
	// §6.3.2: when a verifier is installed, the identity-verification
	// middleware has already verified the token and stored the caller
	// Identity on the context. Use it so handlers and the audit emitter
	// agree on one verified identity per request.
	if id, ok := verifiedIdentityFrom(r.Context()); ok {
		return id
	}
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
		Sensitivity: a.Sensitivity,
		FoldedFrom:  a.FoldedFrom,
		Source:      a.Source,
		// spec: §7.6.1 — Frontmatter on search results, Summary on
		// notable entries; each is omitempty so it appears only where set.
		Frontmatter: a.Frontmatter,
		Summary:     a.Summary,
	}
}

// domainDescriptorOf converts a core subdomain descriptor to the wire
// form, recursing so the §4.5.5 nested depth tree is preserved.
func domainDescriptorOf(d core.DomainDescriptor) DomainDescriptor {
	out := DomainDescriptor{Path: d.Path, Name: d.Name, Description: d.Description}
	for _, c := range d.Subdomains {
		out.Subdomains = append(out.Subdomains, domainDescriptorOf(c))
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeErrorDetails(w, status, code, msg, nil)
}

// writeErrorDetails emits the §6.10 envelope with machine-readable
// details. The retryable flag and suggested_action are filled from the
// per-code registry (see enrichEnvelope) so every emission path reports
// them consistently.
func writeErrorDetails(w http.ResponseWriter, status int, code, msg string, details map[string]any) {
	e := ErrorResponse{Code: code, Message: msg, Details: details}
	enrichEnvelope(&e)
	writeJSON(w, status, e)
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
