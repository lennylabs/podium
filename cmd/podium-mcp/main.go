// Command podium-mcp is the MCP server bridge described in spec §6.
//
// The bridge exposes the meta-tools (load_domain, search_domains,
// search_artifacts, load_artifact) over MCP's stdio transport. It
// forwards meta-tool calls to a Podium registry over HTTP, caches
// content-addressed responses, runs the configured HarnessAdapter and
// MaterializationHook chain, and writes adapter output atomically to
// the host's filesystem at load_artifact time (§6.6).
//
// The host configures the binary via env vars, command-line flags, or a
// YAML config file (§6.1 / §6.2; flag > config file > env, see cliconfig.go).
// The env-var forms:
//
//	PODIUM_REGISTRY            Registry URL (server source; required, §6.1).
//	PODIUM_HARNESS             Adapter ID (default: none).
//	PODIUM_CACHE_DIR           Content-addressed cache root.
//	PODIUM_MATERIALIZE_ROOT    Materialization destination.
//	PODIUM_SESSION_TOKEN       Injected JWT (§6.3.2).
//	PODIUM_SESSION_TOKEN_FILE  File path holding the JWT.
//	PODIUM_HOST_PYTHON         Host Python version, e.g. 3.11.4 (§4.4.1).
//	PODIUM_HOST_NODE           Host Node version (§4.4.1).
//	PODIUM_HOST_PACKAGES       CSV of installed system packages (§4.4.1).
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/podium/internal/buildinfo"
	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/hook"
	"github.com/lennylabs/podium/pkg/identity"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/materialize"
	"github.com/lennylabs/podium/pkg/metrics"
	"github.com/lennylabs/podium/pkg/overlay"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/sign"
	synccfg "github.com/lennylabs/podium/pkg/sync"
	"github.com/lennylabs/podium/pkg/tracing"
	"github.com/lennylabs/podium/pkg/version"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// protocolVersion is the maximum MCP wire-protocol version this binary
// speaks; supportedSince is the oldest version it still accepts. initialize
// negotiates within [supportedSince, protocolVersion] per §6.9 "MCP protocol
// version mismatch": it agrees on the lower of the host's request and this
// maximum, and returns mcp.unsupported_version when the host's request is
// older than supportedSince.
const (
	protocolVersion = "2024-11-05"
	supportedSince  = "2024-11-01"
)

// negotiateProtocol implements the §6.9 "MCP protocol version mismatch" row:
// negotiate down to the host's max supported version. The host advertises its
// maximum in the initialize protocolVersion field; the agreed version is the
// lower of that request and this binary's protocolVersion, so a host that
// tops out below this binary's max gets its own version echoed back rather
// than a version it cannot speak. A request older than supportedSince has no
// compatible version (ok=false), and the caller emits mcp.unsupported_version.
// An empty request (the host omitted the field) falls back to this binary's
// maximum. Comparison is lexical, which matches the chronological order of the
// YYYY-MM-DD MCP version scheme.
func negotiateProtocol(requested string) (agreed string, ok bool) {
	if requested == "" {
		return protocolVersion, true
	}
	if requested < supportedSince {
		return "", false
	}
	if requested < protocolVersion {
		return requested, true
	}
	return protocolVersion, true
}

// clientVersionRefusal implements the §6.9 "Binary version mismatch with
// host caller" row. It reports whether a host reporting clientVersion in its
// initialize clientInfo must be refused against the configured floor
// (config.minClientVersion), returning the mcp.client_too_old §6.10 message
// to emit. The check is opt-in: with no floor configured it never refuses,
// because host version strings are not portably comparable across MCP
// runtimes. A floor that is itself unparsable, or a host version that is
// absent or unparsable, also does not refuse: the binary cannot establish a
// mismatch, so it errs toward serving rather than locking the host out.
func (s *mcpServer) clientVersionRefusal(clientVersion string) (message string, refuse bool) {
	floor := s.cfg.minClientVersion
	if floor == "" || clientVersion == "" {
		return "", false
	}
	atLeast, err := version.AtLeast(clientVersion, floor)
	if err != nil {
		return "", false
	}
	if atLeast {
		return "", false
	}
	return "mcp.client_too_old: host caller version " + clientVersion +
		" is below the minimum " + floor + " this binary serves; update the host", true
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	// §13.8 OpenTelemetry: install the W3C propagator and, when PODIUM_TRACING
	// (or an OTLP endpoint) is configured, the trace exporter. Off by default;
	// the propagator is installed regardless so the bridge injects trace
	// context on its registry calls.
	shutdownTracing, terr := tracing.Init(context.Background(), "podium-mcp")
	if terr != nil {
		fmt.Fprintln(os.Stderr, terr)
		os.Exit(2)
	}
	defer func() { _ = shutdownTracing(context.Background()) }()
	srv, err := newServer(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	// §7.6.2 cache warm-up: when configured to prefetch, warm the §6.5 cache
	// from the batch-load endpoint before serving. Best-effort — a failure
	// logs and the bridge still starts, falling back to on-demand loads.
	if len(cfg.prefetchIDs) > 0 {
		if perr := srv.prefetch(cfg.prefetchIDs); perr != nil {
			fmt.Fprintf(os.Stderr, "WARN: prefetch warm-up failed: %v\n", perr)
		}
	}
	if err := srv.serve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// config captures every PODIUM_ env var the bridge consults.
type config struct {
	registry  string
	harness   string
	cacheDir  string
	cacheMode string
	// resolutionTTL bounds how long a cached `(id, "latest")` resolution is
	// served before it is treated as a miss (§6.5 "TTL 30s by default").
	// Pinned versions are immutable and ignore this. Sourced from
	// PODIUM_CACHE_RESOLUTION_TTL_SECONDS; 0 disables expiry.
	resolutionTTL   time.Duration
	materializeRoot string
	// prefetchIDs lists artifact IDs to warm into the §6.5 cache at startup
	// via the §7.6.2 batch-load endpoint. Sourced from PODIUM_PREFETCH (CSV)
	// or the `prefetch` config key. Empty disables warm-up.
	prefetchIDs      []string
	sessionToken     string
	sessionTokenFile string
	overlayPath      string
	// §9.1 LocalSearchProvider: optional semantic backing for the
	// workspace-overlay index. Both must name a backend (not "" / "none")
	// to activate; otherwise the overlay stays BM25-only. Selected by the
	// same env vars as the registry-side path.
	localVectorBackend     string
	localEmbeddingProvider string
	auditSink              string
	// auditSinkSet records whether PODIUM_AUDIT_SINK (or its flag /
	// config-file equivalent) was provided at all, so an explicit empty
	// value selects the §6.2 default (~/.podium/audit.log) while an
	// absent value leaves local auditing off (registry audit only).
	auditSinkSet bool
	// piiRedaction is the §8.2 query-text scrub toggle for the local audit
	// sink. Tri-state: nil (absent) means default-on, an explicit false
	// disables scrubbing. Sourced from PODIUM_PII_REDACTION.
	piiRedaction  *bool
	tenantID      string
	oauthAudience string
	// §6.2 / §6.3 identity provider selection and the oauth-device-code
	// options. identityProvider defaults to "oauth-device-code". The
	// device-code flow runs only when oauthAuthEndpoint is configured;
	// otherwise the bridge sends a cached or injected token, or no
	// credential, so a bridge pointed at an anonymous registry still works.
	identityProvider  string
	oauthAuthEndpoint string
	oauthTokenURL     string
	oauthClientID     string
	oauthScopes       string
	tokenKeychainName string
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
	// §4.4.1 runtime_requirements enforcement. The host advertises
	// what it can run so an artifact declaring runtime_requirements
	// the host cannot satisfy is refused at load time with
	// materialize.runtime_unavailable.
	hostPython   string
	hostNode     string
	hostPackages []string
	// enforceRuntime forces the runtime gate active even when the
	// host advertises no capability (fail-closed: any artifact with
	// runtime_requirements is refused). When false, the gate is
	// active only once the host advertises at least one capability.
	enforceRuntime bool
	// ignoreRuntime is the §4.4.1 escape hatch mirroring
	// ignoreSandbox: bypass the runtime gate with a loud warning.
	ignoreRuntime bool
	// metricsAddr is the §13.8 opt-in Prometheus listener bind address. The
	// bridge is a stdio process with no HTTP server of its own, so metrics are
	// off unless an operator names an address (PODIUM_MCP_METRICS_ADDR or
	// --metrics-addr), e.g. 127.0.0.1:9090. Empty disables the listener and the
	// per-call / cache recording.
	metricsAddr string
	// minClientVersion is the §6.9 "Binary version mismatch with host caller"
	// floor: the lowest host (MCP client) version this binary serves. The host
	// reports its version in the initialize clientInfo.version field; a request
	// below this floor is refused with mcp.client_too_old so the host can prompt
	// an update. Empty (the default) disables the check, since host version
	// strings are not portably comparable across runtimes; a distributor that
	// pairs a specific host with the binary sets the floor. Sourced from
	// PODIUM_MIN_CLIENT_VERSION or the min-client-version flag / config key.
	minClientVersion string
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
		cacheMode: envDefault("PODIUM_CACHE_MODE", "always-revalidate"),
		// §7.6.2: optional cache warm-up ID list (CSV).
		prefetchIDs: splitCSV(os.Getenv("PODIUM_PREFETCH")),
		// §6.5: resolution-cache TTL for `latest`, default 30s.
		resolutionTTL:          parseTTLSeconds(envDefault("PODIUM_CACHE_RESOLUTION_TTL_SECONDS", "30")),
		materializeRoot:        os.Getenv("PODIUM_MATERIALIZE_ROOT"),
		sessionToken:           os.Getenv(tokenSource),
		sessionTokenFile:       os.Getenv("PODIUM_SESSION_TOKEN_FILE"),
		overlayPath:            os.Getenv("PODIUM_OVERLAY_PATH"),
		localVectorBackend:     os.Getenv("PODIUM_VECTOR_BACKEND"),
		localEmbeddingProvider: os.Getenv("PODIUM_EMBEDDING_PROVIDER"),
		tenantID:               os.Getenv("PODIUM_TENANT_ID"),
		oauthAudience:          os.Getenv("PODIUM_OAUTH_AUDIENCE"),
		// §6.2: PODIUM_IDENTITY_PROVIDER defaults to oauth-device-code.
		identityProvider:  envDefault("PODIUM_IDENTITY_PROVIDER", "oauth-device-code"),
		oauthAuthEndpoint: os.Getenv("PODIUM_OAUTH_AUTHORIZATION_ENDPOINT"),
		oauthTokenURL:     os.Getenv("PODIUM_OAUTH_TOKEN_URL"),
		oauthClientID:     envDefault("PODIUM_OAUTH_CLIENT_ID", "podium-cli"),
		oauthScopes:       envDefault("PODIUM_OAUTH_SCOPES", "openid profile email groups"),
		tokenKeychainName: envDefault("PODIUM_TOKEN_KEYCHAIN_NAME", "podium"),
		// §4.7.9 / §6.2 / §13.10: never | medium-and-above | always. The
		// default is consumer-side and resolved after sync.yaml is known
		// (env/flag/config, then defaults.verify_signatures, then
		// medium-and-above); an empty value here means "env did not set it."
		verifyPolicy:      sign.VerificationPolicy(os.Getenv("PODIUM_VERIFY_SIGNATURES")),
		signatureProvider: envDefault("PODIUM_SIGNATURE_PROVIDER", "noop"),
		// §4.4.1 sandbox enforcement.
		enforceSandbox: os.Getenv("PODIUM_ENFORCE_SANDBOX_PROFILE") == "true",
		hostSandboxes:  splitCSV(envDefault("PODIUM_HOST_SANDBOXES", "unrestricted")),
		ignoreSandbox:  os.Getenv("PODIUM_IGNORE_SANDBOX") == "true",
		// §4.4.1 runtime_requirements enforcement.
		hostPython:     os.Getenv("PODIUM_HOST_PYTHON"),
		hostNode:       os.Getenv("PODIUM_HOST_NODE"),
		hostPackages:   splitCSV(os.Getenv("PODIUM_HOST_PACKAGES")),
		enforceRuntime: os.Getenv("PODIUM_ENFORCE_RUNTIME_REQUIREMENTS") == "true",
		ignoreRuntime:  os.Getenv("PODIUM_IGNORE_RUNTIME_REQUIREMENTS") == "true",
		// §13.8 opt-in Prometheus listener for the bridge.
		metricsAddr: os.Getenv("PODIUM_MCP_METRICS_ADDR"),
		// §6.9 host (MCP client) version floor; empty disables the check.
		minClientVersion: os.Getenv("PODIUM_MIN_CLIENT_VERSION"),
	}
	// §6.2 PODIUM_AUDIT_SINK: distinguish unset (registry audit only) from
	// set-but-empty (use the default ~/.podium/audit.log). os.Getenv cannot
	// tell the two apart, so use LookupEnv.
	if v, ok := os.LookupEnv("PODIUM_AUDIT_SINK"); ok {
		c.auditSink, c.auditSinkSet = v, true
	}
	// §8.2 query-text scrub: default-on for the local audit sink, disabled
	// only with PODIUM_PII_REDACTION=false.
	if v, ok := os.LookupEnv("PODIUM_PII_REDACTION"); ok {
		b := !piiDisabledValue(v)
		c.piiRedaction = &b
	}
	// §6.1 / §6.2: the host may configure the bridge via env vars,
	// command-line flags, or a config file. Flags and config-file values
	// overlay the env-derived defaults above (flag > config file > env).
	if err := applyFlagsAndConfig(c, os.Args[1:]); err != nil {
		return nil, err
	}
	// §6.2 / §7.5.2: when no registry is configured, fall back to
	// sync.yaml's defaults.registry (workspace overlay first, then the
	// home-global ~/.podium/sync.yaml the standalone recipe bootstraps).
	if c.registry == "" {
		c.registry = registryFromSyncYAML()
	}
	if c.registry == "" {
		// §6.10 / §7.5.2 / §13.10: the registry is unset across env, flags, and
		// every sync.yaml scope. Surface the canonical config.no_registry code
		// and point the user at `podium init`, matching pkg/sync ErrNoRegistry,
		// rather than a bare "required" message.
		return nil, fmt.Errorf("config.no_registry: no registry configured; set PODIUM_REGISTRY, pass --registry, or run `podium init` to write defaults.registry to .podium/sync.yaml")
	}
	// §6.1 / §7.5.2: the MCP server speaks HTTP and requires a server-source
	// registry. Under the §7.5.2 dispatch rule only an http:// or https://
	// prefix names a server; every other value (a bare path, a file:// URI)
	// is a filesystem source that only `podium sync` supports. Refuse to
	// start with a clear message rather than passing startup and failing
	// opaquely on the first tool call with an HTTP-client scheme error.
	if !synccfg.IsServerSource(c.registry) {
		return nil, fmt.Errorf("config.filesystem_registry_unsupported: PODIUM_REGISTRY %q is a filesystem-source registry; the MCP server speaks HTTP and requires a server source (http:// or https://). Use `podium sync` to consume a filesystem registry (§6.1, §7.5.2)", c.registry)
	}
	// §4.7.9 / §13.10: resolve the consumer-side signature-verification
	// default. Precedence: an explicit env/flag/config value (already applied
	// above) wins; otherwise honor defaults.verify_signatures from sync.yaml,
	// which a standalone deployment writes as `never` on first run; otherwise
	// fall back to the secure medium-and-above default.
	if c.verifyPolicy == "" {
		if v := verifySignaturesFromSyncYAML(); v != "" {
			c.verifyPolicy = sign.VerificationPolicy(v)
		} else {
			c.verifyPolicy = sign.PolicyMediumAndAbove
		}
	}
	if c.cacheDir == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			c.cacheDir = filepath.Join(home, ".podium", "cache")
		} else {
			// §6.2 default is ~/.podium/cache/. When the home directory
			// cannot be resolved the cache stays disabled; say so loudly
			// instead of degrading silently (§6.5 notes ephemeral-home
			// hosts should point PODIUM_CACHE_DIR at a volume).
			fmt.Fprintf(os.Stderr, "WARN: PODIUM_CACHE_DIR unset and home directory unresolved (%v); content cache disabled — set PODIUM_CACHE_DIR to enable it\n", err)
		}
	}
	switch c.cacheMode {
	case "always-revalidate", "offline-first", "offline-only":
		// known modes
	default:
		return nil, fmt.Errorf("PODIUM_CACHE_MODE must be always-revalidate | offline-first | offline-only, got %q", c.cacheMode)
	}
	// §6.2 / §4.7.9: PODIUM_VERIFY_SIGNATURES must be one of the recognized
	// policies. Reject an unknown value at startup so a typo cannot silently
	// disable signature enforcement on a security control.
	if !sign.ValidPolicy(c.verifyPolicy) {
		return nil, fmt.Errorf("PODIUM_VERIFY_SIGNATURES must be never | medium-and-above | always, got %q", c.verifyPolicy)
	}
	// §6.2: PODIUM_IDENTITY_PROVIDER selects a built-in provider. Reject an
	// unrecognized value at startup rather than silently treating it as the
	// injected-session-token path.
	switch c.identityProvider {
	case "oauth-device-code", "injected-session-token":
		// known providers
	default:
		return nil, fmt.Errorf("PODIUM_IDENTITY_PROVIDER must be oauth-device-code | injected-session-token, got %q", c.identityProvider)
	}
	// spec: §6.9 "Unknown PODIUM_HARNESS value" — refuse to start and list
	// the available adapter values, rather than detecting an unknown harness
	// lazily on the first load_artifact materialization. adapter.Registry.Get
	// already enumerates the registered IDs in its error (config.unknown_harness).
	if _, err := adapter.DefaultRegistry().Get(c.harness); err != nil {
		return nil, err
	}
	// spec: §6.7 "Versioning" — a profile or harness combination that needs a
	// newer adapter behavior pins a minimum binary version via
	// min_server_version in sync.yaml; an older binary refuses to start rather
	// than materializing with stale adapter behavior.
	if err := checkServerVersionFromSyncYAML(buildinfo.Version); err != nil {
		return nil, err
	}
	return c, nil
}

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envFirst returns the value of the first set, non-empty env var among keys,
// or "" when none is set. It mirrors the registry bootstrap's envFirst so the
// overlay vector backend reads the same fallback chains (for example
// PODIUM_PGVECTOR_DSN then PODIUM_POSTGRES_DSN).
func envFirst(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// parseTTLSeconds converts a §6.5 resolution-cache TTL expressed in seconds to
// a Duration. A non-numeric or negative value falls back to the 30s default; a
// value of 0 disables expiry.
func parseTTLSeconds(s string) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return 30 * time.Second
	}
	return time.Duration(n) * time.Second
}

// mcpServer holds the wiring for one bridge process.
type mcpServer struct {
	cfg         *config
	http        *http.Client
	cache       *contentCache
	resolutions *resolutionCache
	adapters    *adapter.Registry
	overlay     []filesystem.ArtifactRecord
	// overlayDomains is the §6.4 workspace-overlay DOMAIN.md set merged
	// across the overlay's layers, keyed by canonical domain path. The
	// load_domain merge composes it onto the registry result client-side
	// (§4.5.4). Swapped together with overlay by the
	// watcher and the roots/list reply.
	overlayDomains map[string]*manifest.Domain
	// overlayMu guards overlay, overlayDomains, and cfg.overlayPath. The
	// §6.4 overlay watcher (a separate goroutine, startOverlayWatch)
	// re-resolves the overlay records and swaps them on a filesystem
	// change, while request handlers read them on the serve goroutine; the
	// roots/list reply also writes them. All access goes through the
	// guarded accessors below.
	overlayMu sync.RWMutex
	// localSem is the §9.1 LocalSearchProvider semantic index over the
	// overlay. Nil when no overlay vector backend is configured, in which
	// case the overlay search stays BM25-only.
	localSem *localSemanticIndex
	// sessionID is generated once per bridge process (one agent session)
	// and threaded through every meta-tool call to the registry so the
	// session correlates across calls. It backs the sessionCorrelation
	// capability the bridge advertises (§5): the registry uses it for
	// `latest`-resolution consistency (§4.7.6) and as the learn-from-usage
	// correlation key (§3.3). A host that supplies its own session_id
	// argument overrides it per call.
	sessionID string
	// lastSuccess holds the Unix-nanosecond timestamp of the last
	// successful registry call, surfaced by the §13.9 health tool. Zero
	// means no successful call has happened yet.
	lastSuccess atomic.Int64
	// tokens persists oauth-device-code access tokens (§6.3): the OS
	// keychain in production, an in-memory store in tests. Consulted only
	// in oauth-device-code mode with an IdP configured.
	tokens identity.TokenStore
	// out is the JSON-RPC output encoder, shared by tool responses and
	// server-initiated messages (MCP elicitation for the device-code
	// flow, §6.3). outMu serializes writes since elicitation can be
	// emitted from a tool-call goroutine while a response is encoded.
	out      *json.Encoder
	outMu    sync.Mutex
	serverID atomic.Int64
	// §6.4 step 2 workspace-overlay resolution via MCP roots. When no
	// PODIUM_OVERLAY_PATH is set and the host advertised the roots
	// capability at initialize, the bridge asks the host for its
	// workspace roots (roots/list) and defaults the overlay to
	// <workspace>/.podium/overlay/ when that directory exists. The serve
	// loop is single-threaded, so these need no extra synchronization.
	hostSupportsRoots bool
	rootsRequested    bool
	// audit is the optional §6.2/§8.3 local audit sink (PODIUM_AUDIT_SINK),
	// the §9.1 LocalAuditSink seam. Nil when the var is unset, in which case
	// auditing is left to the registry. When set, meta-tool calls append a
	// local audit event.
	audit audit.LocalAuditSink
	// scrubber applies the §8.2 default-on query-text PII scrub before a
	// search event is written to the local audit sink. Nil when an operator
	// disabled scrubbing via PODIUM_PII_REDACTION=false.
	scrubber *audit.PIIScrubber
	// hooks is the §6.6 step 4 MaterializationHook chain, run over the
	// adapter output before the atomic write on every materialization path.
	// Empty by default (step 4 is a no-op when no hooks are configured); the
	// boot-time loading of configured hook plugins is the wire-serializable
	// SPI work is tracked separately. Tests inject hooks directly.
	hooks []hook.Hook
	// metrics is the §13.8 bridge metric set, non-nil only when an operator
	// configured the opt-in listener (PODIUM_MCP_METRICS_ADDR). When nil the
	// per-call and resolution-cache recording sites no-op.
	metrics *metrics.MCPRegistry
	// activeCtx carries the §13.8 trace context of the in-flight meta-tool call
	// so the outbound registry request and the local adapter-translation and
	// materialization spans attach to the call's root span. The serve loop
	// dispatches one call at a time (§6.8 host-owned lifecycle, single-threaded
	// stdio loop), so a single field needs no synchronization; it is set around
	// dispatchTool and is nil otherwise. reqCtx reads it with a Background
	// fallback for the off-call paths (prefetch, resource reads).
	activeCtx context.Context
}

// reqCtx returns the in-flight call's trace context, or context.Background()
// when no meta-tool call is active. Outbound registry requests build on it so
// the otelhttp transport injects W3C trace context and parents the round-trip
// span under the call's root span.
func (s *mcpServer) reqCtx() context.Context {
	if s.activeCtx != nil {
		return s.activeCtx
	}
	return context.Background()
}

// traceID returns the 32-hex W3C trace id of the in-flight meta-tool
// call's span, or "" when no recording span is active (tracing off). The
// registry derives its audit stream's trace id from the same span via the
// W3C `traceparent` header the otelhttp transport injects on the outbound
// call (pkg/registry/server/audit_context.go traceIDFromRequest), so
// stamping this id on the local audit event makes the registry and MCP
// local streams share one trace id per call, as §8.1 requires.
func (s *mcpServer) traceID() string {
	sc := oteltrace.SpanContextFromContext(s.reqCtx())
	if !sc.HasTraceID() {
		return ""
	}
	return sc.TraceID().String()
}

// recordSuccess stamps the time of a successful registry call for the
// §13.9 health tool's last-successful-call field.
func (s *mcpServer) recordSuccess(t time.Time) {
	s.lastSuccess.Store(t.UnixNano())
}

// lastSuccessTime returns the last successful registry call time and
// whether one has occurred.
func (s *mcpServer) lastSuccessTime() (time.Time, bool) {
	n := s.lastSuccess.Load()
	if n == 0 {
		return time.Time{}, false
	}
	return time.Unix(0, n), true
}

func newServer(cfg *config) (*mcpServer, error) {
	cache, err := newContentCache(cfg.cacheDir)
	if err != nil {
		return nil, err
	}
	srv := &mcpServer{
		cfg: cfg,
		// §13.8: wrap the transport so every registry call injects W3C trace
		// context and opens a "registry round-trip" client span under the
		// active meta-tool root span. With tracing off the global no-op tracer
		// makes this a passthrough.
		http:        &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)},
		cache:       cache,
		resolutions: newResolutionCache(cfg.cacheDir),
		adapters:    adapter.DefaultRegistry(),
		sessionID:   newSessionID(),
		// §6.3: oauth-device-code tokens cache in the OS keychain, keyed by
		// registry URL (matching `podium login`).
		tokens: identity.KeychainStore{Service: cfg.tokenKeychainName},
	}
	// §13.8 opt-in metrics: build the bridge metric set and wire the
	// resolution-cache hit/miss observer only when a listener address is
	// configured, so the default stdio bridge takes no recording overhead.
	if cfg.metricsAddr != "" {
		srv.metrics = metrics.NewMCP()
		srv.resolutions.observe = srv.metrics.ObserveCache
	}
	// §6.4 workspace overlay: load the initial records. The §6.4.1 watcher
	// (started in serve) re-resolves and swaps them on every filesystem
	// change so an edit, add, or remove is reflected without a restart.
	if cfg.overlayPath != "" {
		records, domains, err := resolveOverlayAll(cfg.overlayPath)
		if err == nil {
			srv.overlay = records
			srv.overlayDomains = domains
		} else {
			// spec: §6.9 "Workspace overlay path missing" — skip the
			// overlay but warn once, identifying the path, so a
			// developer whose drafts are invisible gets a diagnostic
			// rather than silence. The bridge still starts.
			fmt.Fprintf(os.Stderr, "WARN: workspace overlay path %q unavailable (%v); overlay disabled\n", cfg.overlayPath, err)
		}
	}
	// §9.1 LocalSearchProvider: wire the optional overlay semantic index.
	// A construction error (missing API key, unknown backend) disables the
	// semantic stream with a warning; the overlay stays BM25-searchable.
	if sem, err := buildLocalSemantic(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "WARN: overlay semantic search disabled: %v\n", err)
	} else {
		srv.localSem = sem
	}
	// §6.2 PODIUM_AUDIT_SINK: when configured, append meta-tool calls to a
	// local audit log in addition to the registry's audit stream.
	sink, err := newAuditSink(cfg)
	if err != nil {
		return nil, err
	}
	srv.audit = sink
	// §8.2 query-text scrub: default-on; a nil scrubber means an operator
	// disabled it with PODIUM_PII_REDACTION=false.
	if cfg.piiRedaction == nil || *cfg.piiRedaction {
		srv.scrubber = audit.NewPIIScrubber()
	}
	return srv, nil
}

// piiDisabledValue reports whether an env value turns the §8.2 query-text
// scrub off. Any other value leaves it on (default-on).
func piiDisabledValue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "false", "0", "no", "off":
		return true
	}
	return false
}

// newAuditSink builds the §6.2 local audit sink from PODIUM_AUDIT_SINK.
// An unset var leaves auditing to the registry (nil sink). A value of
// "default" (or an explicit empty value) writes to ~/.podium/audit.log;
// any other value is treated as a destination file path.
//
// spec: §6.2 — "When set without a value (or set to `default`), uses
// ~/.podium/audit.log".
func newAuditSink(cfg *config) (audit.LocalAuditSink, error) {
	if !cfg.auditSinkSet {
		return nil, nil
	}
	path := cfg.auditSink
	if path == "default" {
		path = ""
	}
	// §6.2 / §9: PODIUM_AUDIT_SINK may name an external endpoint instead of
	// a filesystem path ("Local audit destination (path or external
	// endpoint)"). An http(s) URL forwards meta-tool events to a SIEM / log
	// aggregator; any other value is a local JSON-Lines file.
	if isAuditEndpoint(path) {
		return audit.NewEndpointSink(path)
	}
	return audit.NewFileSink(path)
}

// isAuditEndpoint reports whether a PODIUM_AUDIT_SINK value selects the
// external-endpoint sink rather than a local file path.
func isAuditEndpoint(v string) bool {
	return strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://")
}

// auditMeta appends a local audit event for a meta-tool call when a sink
// is configured (§6.2). It is a no-op when auditing is registry-only.
// Failures are swallowed: a local-audit write must not break a tool call,
// and the registry audit stream remains the authoritative record. The
// in-flight call's trace id (§8.1) is stamped so the local event shares
// the registry stream's trace id.
func (s *mcpServer) auditMeta(t audit.EventType, target string) {
	if s.audit == nil {
		return
	}
	_ = s.audit.Append(context.Background(), audit.Event{
		Type:    t,
		Caller:  s.sessionID,
		Target:  target,
		TraceID: s.traceID(),
		Context: map[string]string{"source": "mcp"},
	})
}

// auditLoadArtifact appends the local artifact.loaded event, applying the
// §8.2 manifest-declared redaction directives before the event reaches the
// sink. The artifact's audit_redact directive names sensitive frontmatter
// fields (for example bank_account or ssn); auditLoadArtifact surfaces
// those field values into the event context and masks them to [redacted],
// the same directive the registry honors on its own stream. With no
// audit_redact directive the event carries only the structural source key.
// The in-flight trace id (§8.1) is stamped for cross-stream correlation.
func (s *mcpServer) auditLoadArtifact(id, frontmatter string) {
	if s.audit == nil {
		return
	}
	ev := audit.Event{
		Type:    audit.EventArtifactLoaded,
		Caller:  s.sessionID,
		Target:  id,
		TraceID: s.traceID(),
		Context: map[string]string{"source": "mcp"},
	}
	// §8.2 manifest-declared redaction: pull the author-named sensitive
	// fields into the context so the directive has a concrete target, then
	// mask them. RedactFields lower-cases keys for matching, so the value
	// reaches the sink only as [redacted].
	if redactKeys := manifestRedactKeys(frontmatter); len(redactKeys) > 0 {
		for k, v := range manifest.FrontmatterFields([]byte(frontmatter), redactKeys) {
			if _, exists := ev.Context[k]; !exists {
				ev.Context[k] = v
			}
		}
		ev.Context = audit.RedactFields(ev.Context, redactKeys)
	}
	_ = s.audit.Append(context.Background(), ev)
}

// manifestRedactKeys returns the audit_redact field names declared in an
// artifact's frontmatter, or nil when the frontmatter is absent, unparsable,
// or declares no directive. It is the value source for the §8.2
// manifest-declared redaction the MCP local sink applies.
func manifestRedactKeys(frontmatter string) []string {
	if frontmatter == "" {
		return nil
	}
	a, err := manifest.ParseArtifact([]byte(frontmatter))
	if err != nil {
		return nil
	}
	return a.AuditRedact
}

// auditSearch appends a local audit event for a search meta-tool call,
// applying the §8.2 default-on query-text scrub to the free-text query
// before it lands in the local sink. A nil sink is a no-op; a nil scrubber
// (operator-disabled) writes the query unredacted. The in-flight trace id
// (§8.1) is stamped so the local event shares the registry stream's id.
func (s *mcpServer) auditSearch(t audit.EventType, query string) {
	if s.audit == nil {
		return
	}
	ev := audit.Event{
		Type:    t,
		Caller:  s.sessionID,
		TraceID: s.traceID(),
		Context: map[string]string{"source": "mcp", "query": query},
	}
	ev = s.scrubber.ScrubEvent(ev)
	_ = s.audit.Append(context.Background(), ev)
}

// newSessionID returns a random RFC 4122 v4 UUID string. The bridge
// generates one per process (one agent session) and threads it through
// every meta-tool call (§5 "Optional session_id"); the registry uses it
// for `latest`-resolution consistency (§4.7.6) and as the learn-from-usage
// correlation key (§3.3). On the unlikely read failure it returns "" and
// the bridge forwards no session_id rather than aborting.
func newSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// overlayMatch returns the overlay record whose canonical ID
// matches id, or nil. Used by the load_artifact path to satisfy
// reads from the highest-precedence layer before talking to the
// registry.
func (s *mcpServer) overlayMatch(id string) *filesystem.ArtifactRecord {
	records := s.overlaySnapshot()
	for i := range records {
		if records[i].ID == id {
			return &records[i]
		}
	}
	return nil
}

// overlaySnapshot returns the current overlay records under the read
// lock. The slice is never mutated in place (the watcher replaces it
// wholesale), so callers may read the returned slice without holding the
// lock.
func (s *mcpServer) overlaySnapshot() []filesystem.ArtifactRecord {
	s.overlayMu.RLock()
	defer s.overlayMu.RUnlock()
	return s.overlay
}

// setOverlay swaps in a fresh set of overlay records (artifacts and the
// merged DOMAIN.md set) under the write lock. Called at startup, by the §6.4
// watcher on a filesystem change, and by the roots/list resolution. Both maps
// are replaced wholesale so request handlers always observe a consistent pair.
func (s *mcpServer) setOverlay(records []filesystem.ArtifactRecord, domains map[string]*manifest.Domain) {
	s.overlayMu.Lock()
	s.overlay = records
	s.overlayDomains = domains
	s.overlayMu.Unlock()
}

// overlayDomainsSnapshot returns the current overlay DOMAIN.md set under the
// read lock. The map is never mutated in place (setOverlay replaces it
// wholesale), so callers may read it without holding the lock.
func (s *mcpServer) overlayDomainsSnapshot() map[string]*manifest.Domain {
	s.overlayMu.RLock()
	defer s.overlayMu.RUnlock()
	return s.overlayDomains
}

// resolveOverlayAll resolves both the artifact records and the merged
// DOMAIN.md set at path. The artifact-resolution error (including ErrNoOverlay
// for a missing or unset path) propagates verbatim so the caller's §6.9
// warn-and-disable handling is unchanged. A DOMAIN.md-only ErrNoOverlay (the
// path vanished between the two reads) degrades to an empty domain map rather
// than failing the artifact load.
func resolveOverlayAll(path string) ([]filesystem.ArtifactRecord, map[string]*manifest.Domain, error) {
	prov := overlay.Filesystem{Path: path}
	records, err := prov.Resolve(nil)
	if err != nil {
		return nil, nil, err
	}
	domains, derr := prov.ResolveDomains(nil)
	if derr != nil && !errors.Is(derr, overlay.ErrNoOverlay) {
		return records, nil, derr
	}
	return records, domains, nil
}

// overlayPath returns the currently resolved overlay path under the read
// lock; empty means the layer is disabled.
func (s *mcpServer) overlayPath() string {
	s.overlayMu.RLock()
	defer s.overlayMu.RUnlock()
	return s.cfg.overlayPath
}

// setOverlayPath records the resolved overlay path under the write lock so
// the watcher picks up a path established after startup (roots/list).
func (s *mcpServer) setOverlayPath(path string) {
	s.overlayMu.Lock()
	s.cfg.overlayPath = path
	s.overlayMu.Unlock()
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

// maxFrameBytes caps the size of a single inbound JSON-RPC line. The §6.8
// process model is a long-lived stdio subprocess, so a single oversized frame
// must fail only that request rather than tearing the process down: when a
// frame exceeds this cap the serve loop emits a structured error response and
// keeps serving. The cap is generous enough for a tools/call whose
// arguments carry large inline data while still bounding memory.
const maxFrameBytes = 16 * 1024 * 1024

func (s *mcpServer) serve(r io.Reader, w io.Writer) error {
	// Read line-delimited frames with a bounded reader. A frame longer than
	// maxFrameBytes is reported (tooLong) and drained rather than buffered in
	// full, so memory stays bounded by the bufio buffer.
	reader := bufio.NewReaderSize(r, 64*1024)
	s.outMu.Lock()
	s.out = json.NewEncoder(w)
	s.outMu.Unlock()
	// §6.3.2.1 token rotation: honor SIGHUP (forced re-read) and a file
	// watch on PODIUM_SESSION_TOKEN_FILE in addition to the per-call fresh
	// read currentToken() already performs. Stops when serve returns.
	stop := s.startTokenWatch()
	defer stop()
	// §6.4 / §6.4.1: watch the resolved overlay path and re-index on
	// change so in-progress overlay edits are visible without restarting
	// the bridge subprocess.
	stopOverlay := s.startOverlayWatch()
	defer stopOverlay()
	// §13.8: opt-in Prometheus listener for the bridge. No-op unless an
	// operator named PODIUM_MCP_METRICS_ADDR; shuts down when serve returns.
	stopMetrics := s.startMetricsListener(s.cfg.metricsAddr)
	defer stopMetrics()
	// §6.8: the host owns the lifecycle. The loop ends only when stdin
	// reaches EOF (the host closing the pipe); there is no signal-driven
	// shutdown here. A read error other than EOF propagates.
	for {
		line, tooLong, err := readFrame(reader, maxFrameBytes)
		if tooLong {
			// fail only this request, keep serving. The frame's id
			// is unrecoverable, so the error carries a null id per JSON-RPC.
			if sendErr := s.send(rpcResponse{
				JSONRPC: "2.0",
				Error: &rpcError{
					Code:    -32600,
					Message: fmt.Sprintf("invalid request: inbound frame exceeds %d-byte limit", maxFrameBytes),
				},
			}); sendErr != nil {
				return sendErr
			}
		} else if len(bytes.TrimSpace(line)) > 0 {
			if derr := s.dispatchLine(line); derr != nil {
				return derr
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// dispatchLine routes one well-formed inbound frame. It returns a non-nil
// error only when writing a response to the host fails (a broken output pipe),
// in which case serve exits.
func (s *mcpServer) dispatchLine(line []byte) error {
	// §6.4 step 2: intercept the host's reply to our server-initiated
	// roots/list request and resolve the workspace overlay from it,
	// instead of mis-dispatching the reply as an inbound request.
	if s.applyRootsResponse(line) {
		return nil
	}
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return nil
	}
	// §6.8 / JSON-RPC 2.0: a notification is a request with no id and must
	// not receive a response. The host-driven lifecycle sends
	// notifications/initialized after initialize, and may send others such
	// as notifications/cancelled and notifications/roots/list_changed.
	// Dispatching them through handle would fall to the default branch and
	// emit a spurious -32601 error frame, which strict hosts treat as a
	// protocol error mid-handshake. An absent id leaves req.ID nil;
	// an explicit null id (a malformed request, not a notification) decodes
	// to the bytes "null" and is still answered.
	if req.ID == nil {
		return nil
	}
	resp := s.handle(req)
	if err := s.send(resp); err != nil {
		return err
	}
	// §6.4 step 2: once initialize is acknowledged, ask the host for
	// its workspace roots when no explicit PODIUM_OVERLAY_PATH was set.
	if req.Method == "initialize" {
		s.requestRootsIfNeeded()
	}
	return nil
}

// readFrame reads one '\n'-delimited frame from r, bounding buffered memory to
// max bytes. When a frame exceeds max it returns tooLong=true and discards the
// remainder of the line so the next call resumes at the following frame; the
// returned line is then empty. err is io.EOF once the stream is exhausted (with
// any trailing partial frame returned alongside it).
func readFrame(r *bufio.Reader, max int) (line []byte, tooLong bool, err error) {
	var buf []byte
	for {
		seg, e := r.ReadSlice('\n')
		if !tooLong {
			if len(buf)+len(seg) > max {
				// Stop accumulating; keep draining to the newline so the
				// oversized frame does not corrupt the next one.
				tooLong = true
				buf = nil
			} else {
				// seg aliases the reader's buffer; copy it out.
				buf = append(buf, seg...)
			}
		}
		switch e {
		case bufio.ErrBufferFull:
			continue
		case nil:
			return buf, tooLong, nil
		default:
			return buf, tooLong, e
		}
	}
}

// send writes one JSON-RPC message (a tool response or a server-initiated
// elicitation) to the output stream under outMu so concurrent writers do
// not interleave.
func (s *mcpServer) send(v any) error {
	s.outMu.Lock()
	defer s.outMu.Unlock()
	if s.out == nil {
		return nil
	}
	return s.out.Encode(v)
}

// rootsRequestID is the JSON-RPC id the bridge uses for its
// server-initiated roots/list request (§6.4 step 2). It is fixed so the
// serve loop recognizes the host's matching response.
const rootsRequestID = "podium-roots-1"

// requestRootsIfNeeded asks the host for its workspace roots so the bridge
// can default the workspace overlay to <workspace>/.podium/overlay/ per
// §6.4 step 2. It is a no-op when PODIUM_OVERLAY_PATH is set (§6.4 step 1
// wins), when the host did not advertise the roots capability, or when the
// request was already sent.
func (s *mcpServer) requestRootsIfNeeded() {
	if s.overlayPath() != "" || !s.hostSupportsRoots || s.rootsRequested {
		return
	}
	s.rootsRequested = true
	_ = s.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      rootsRequestID,
		"method":  "roots/list",
	})
}

// applyRootsResponse recognizes the host's reply to the bridge's
// roots/list request and resolves the workspace overlay from the first
// usable root (§6.4 step 2). It returns true when the line was that reply,
// so the serve loop consumes it instead of dispatching it as a request;
// any other message returns false.
func (s *mcpServer) applyRootsResponse(line []byte) bool {
	var msg struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Result struct {
			Roots []struct {
				URI  string `json:"uri"`
				Name string `json:"name"`
			} `json:"roots"`
		} `json:"result"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return false
	}
	// A JSON-RPC response carries no method; a request or notification does.
	if msg.Method != "" {
		return false
	}
	var id string
	_ = json.Unmarshal(msg.ID, &id)
	if id != rootsRequestID {
		return false
	}
	for _, r := range msg.Result.Roots {
		if s.resolveWorkspaceOverlay(workspaceFromURI(r.URI)) {
			break
		}
	}
	return true
}

// resolveWorkspaceOverlay applies §6.4 step 2 for one workspace directory:
// when no overlay is configured, default to <workspace>/.podium/overlay/
// if it exists and load its records. It returns true once an overlay has
// been resolved so the caller stops scanning further roots.
func (s *mcpServer) resolveWorkspaceOverlay(workspace string) bool {
	if workspace == "" || s.overlayPath() != "" {
		return false
	}
	path, err := overlay.ResolveWorkspaceOverlay(workspace, "")
	if err != nil {
		return false
	}
	records, domains, err := resolveOverlayAll(path)
	if err != nil {
		return false
	}
	// Set the path before the records so the watcher, which keys off the
	// resolved path, observes a consistent (path, records) pair.
	s.setOverlayPath(path)
	s.setOverlay(records, domains)
	return true
}

// workspaceFromURI converts an MCP root URI to a filesystem path. Roots
// are file:// URIs per the MCP spec; a bare absolute path is tolerated for
// hosts that send one.
func workspaceFromURI(uri string) string {
	if uri == "" {
		return ""
	}
	if strings.HasPrefix(uri, "file://") {
		u, err := url.Parse(uri)
		if err != nil {
			return ""
		}
		return u.Path
	}
	if strings.HasPrefix(uri, "/") {
		return uri
	}
	return ""
}

func (s *mcpServer) handle(req rpcRequest) rpcResponse {
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		// §6.9 "MCP protocol version mismatch": negotiate down to the lower
		// of the host's requested protocolVersion and this binary's maximum;
		// refuse with mcp.unsupported_version only when the request predates
		// supportedSince.
		var initParams struct {
			ProtocolVersion string `json:"protocolVersion"`
			Capabilities    struct {
				Roots json.RawMessage `json:"roots"`
			} `json:"capabilities"`
			ClientInfo struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"clientInfo"`
		}
		_ = json.Unmarshal(req.Params, &initParams)
		// §6.9 "Binary version mismatch with host caller": when this binary is
		// pinned to a minimum host version, refuse to start a session with a
		// host older than the floor so the host's CLI can prompt an update. The
		// refusal is an initialize error, the handshake-level signal a host
		// surfaces. An absent or unparsable clientInfo.version is not refused
		// (the mismatch cannot be established).
		if msg, refuse := s.clientVersionRefusal(initParams.ClientInfo.Version); refuse {
			resp.Error = &rpcError{Code: -32600, Message: msg}
			return resp
		}
		// §6.4 step 2: record whether the host can answer roots/list so
		// the serve loop knows it may resolve the workspace overlay from
		// the host's reported workspace root.
		s.hostSupportsRoots = len(initParams.Capabilities.Roots) > 0 &&
			string(initParams.Capabilities.Roots) != "null"
		agreedVersion, ok := negotiateProtocol(initParams.ProtocolVersion)
		if !ok {
			resp.Error = &rpcError{
				Code:    -32600,
				Message: "mcp.unsupported_version: host protocol " + initParams.ProtocolVersion + " predates supported " + supportedSince,
			}
			return resp
		}
		// §5 capability set: `{tools: {}, resources: {},
		// sessionCorrelation: true}`. tools/resources are MCP capability
		// objects ({} = present, no listChanged/subscribe sub-features);
		// sessionCorrelation is a Podium extension carried as a boolean. The
		// `resources` capability backs the §5.0 read-only mirror of
		// load_artifact. Command artifacts are delivered through
		// harness-native materialization (§6.7), not an MCP prompt
		// projection, so no `prompts` capability is advertised.
		caps := map[string]any{
			"tools":              map[string]any{},
			"sessionCorrelation": true,
			"resources":          map[string]any{},
		}
		resp.Result = map[string]any{
			// §6.9: the negotiated version, the lower of the host's request
			// and this binary's maximum.
			"protocolVersion": agreedVersion,
			"capabilities":    caps,
			"serverInfo":      map[string]any{"name": "podium-mcp", "version": buildinfo.Version},
			// §5.1 example system-prompt fragment, surfaced through the MCP
			// `instructions` field so a host can add it to the model's
			// system prompt verbatim.
			"instructions": systemPromptFragment,
		}
	case "tools/list":
		resp.Result = map[string]any{
			// §5.1 canonical descriptions emitted verbatim with an
			// inputSchema per meta-tool. See descriptions.go.
			"tools": metaToolDescriptors(),
		}
	case "tools/call":
		resp.Result = toolCallResult(s.callTool(req.Params))
	case "resources/list":
		// §5.0 — read-only mirror of load_artifact: artifact bodies are
		// also exposed through MCP's resource protocol.
		resp.Result = s.handleResourcesList()
	case "resources/read":
		resp.Result = s.handleResourcesRead(req.Params)
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
	// §13.8: open one root span per meta-tool call and carry its context so the
	// registry round-trip, adapter translation, and materialization spans
	// attach beneath it. The span is non-recording (negligible cost) when
	// tracing is off. activeCtx is restored after dispatch so off-call paths
	// fall back to context.Background().
	ctx, span := tracing.Tracer().Start(context.Background(), "mcp."+p.Name)
	prev := s.activeCtx
	s.activeCtx = ctx
	defer func() {
		s.activeCtx = prev
		span.End()
	}()

	// Record the per-tool request count, error count, and latency when the
	// opt-in metrics listener is active. The dispatch is unchanged; only the
	// timing and outcome are observed around it.
	if s.metrics == nil {
		return s.dispatchTool(p)
	}
	start := time.Now()
	result := s.dispatchTool(p)
	s.metrics.ObserveCall(p.Name, isErrorResult(result), time.Since(start))
	return result
}

// toolCallResult turns a meta-tool's domain result into an MCP CallToolResult
// (§6.1.1). The domain object is carried twice, as the MCP result format
// intends: `structuredContent` holds the typed object for programmatic
// consumers, and `content` holds the same JSON as a text block so MCP hosts
// (Claude Code, Claude Desktop, Cursor, VS Code) render the output. Without a
// `content` block the host renders an empty tool result and the model never
// sees the search or load output. `isError` is set for a §6.10 error envelope
// so the host marks the failure. The domain fields live under
// `structuredContent`; the result top level carries only the MCP envelope keys.
func toolCallResult(domain any) any {
	text, err := json.MarshalIndent(domain, "", "  ")
	if err != nil {
		// Unreachable for the map[string]any envelopes every meta-tool
		// returns; the fallback keeps a renderable block instead of panicking
		// if a future result carries a value json cannot marshal.
		text = []byte(fmt.Sprintf("%v", domain))
	}
	out := map[string]any{
		"content":           []map[string]any{{"type": "text", "text": string(text)}},
		"structuredContent": domain,
	}
	if isErrorResult(domain) {
		out["isError"] = true
	}
	return out
}

// isErrorResult reports whether a meta-tool result is a §6.10 error envelope,
// identified by the presence of an "error" key in the returned result map.
func isErrorResult(result any) bool {
	m, ok := result.(map[string]any)
	if !ok {
		return false
	}
	_, hasErr := m["error"]
	return hasErr
}

func (s *mcpServer) dispatchTool(p toolCallParams) any {
	switch p.Name {
	case "load_domain":
		// §4.5.4 / §6.4: the workspace overlay merges client-side. loadDomain
		// proxies the registry result and composes the overlay DOMAIN.md set
		// and overlay artifacts onto it. The audit event is
		// emitted inside loadDomain so it fires once on every path.
		return s.loadDomain(p.Arguments)
	case "search_domains":
		s.auditSearch(audit.EventDomainsSearched, argString(p.Arguments, "query"))
		return s.proxyGet("/v1/search_domains", p.Arguments, map[string]any{"results": []any{}})
	case "search_artifacts":
		s.auditSearch(audit.EventArtifactsSearched, argString(p.Arguments, "query"))
		return s.searchArtifacts(p.Arguments)
	case "load_artifact":
		// The artifact.loaded local audit event is emitted from the load
		// paths (deliverLoadArtifact / loadArtifactFromOverlay) where the
		// resolved manifest is available, so the §8.2 audit_redact directive
		// has its sensitive frontmatter fields to mask.
		return s.loadArtifact(p.Arguments)
	case "health":
		// §13.9 health tool: registry connectivity + observed mode +
		// cache size + last successful call timestamp.
		return s.healthTool()
	case "scope_preview":
		// §3.5 transparency affordance: aggregate counts for the caller's
		// effective view, no bodies or per-artifact metadata. Agents do not
		// use this as a discovery surface during a session; it answers
		// "what could this identity have loaded?" for an operator or
		// reviewer. Proxies GET /v1/scope/preview, returning the §3.5 403
		// config.scope_preview_disabled envelope verbatim when the tenant gate is off.
		return s.proxyGet("/v1/scope/preview", nil, nil)
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
	now := time.Now()
	ttl := s.cfg.resolutionTTL
	if (s.cfg.cacheMode == "offline-first" || s.cfg.cacheMode == "offline-only") && id != "" {
		// §6.5: both offline modes serve a present resolution rather than
		// revalidating it. offline-first is "use cached resolution and content
		// if present; only call the registry on miss" and offline-only "never
		// call the registry; cache only", so a present-but-stale `latest`
		// resolution is served without a registry call. The resolution TTL
		// governs HEAD revalidation, which the spec scopes to always-revalidate
		// ("Revalidated via HEAD on hit when PODIUM_CACHE_MODE=always-revalidate").
		// A genuine miss (no entry at all) still falls through to the registry
		// in offline-first. Pinned versions are immutable and never expire.
		if hash, ok := s.resolutions.Resolve(id, version, now, ttl, true); ok {
			if cached, cerr := s.loadArtifactFromCache(hash, id); cerr == nil {
				return s.deliverLoadArtifact(*cached, deliverOpts{harness: harnessFromArgs(s.cfg.harness, args), destination: destFromArgs(args)})
			}
		}
		if s.cfg.cacheMode == "offline-only" {
			return errorResult(errOfflineCacheMiss.Error())
		}
	}

	// §6.5 always-revalidate: HEAD-revalidate the cached resolution on a hit.
	// When the registry confirms the content hash is unchanged, serve the
	// cached content instead of downloading the full manifest + resources. A
	// changed hash or a HEAD failure falls through to a full fetch.
	if s.cfg.cacheMode == "always-revalidate" && id != "" {
		if hash, ok := s.resolutions.Resolve(id, version, now, ttl, true); ok && s.cache.has(hash) {
			if freshHash, herr := s.headContentHash("/v1/load_artifact", args); herr == nil && freshHash == hash {
				if cached, cerr := s.loadArtifactFromCache(hash, id); cerr == nil {
					if version == "" {
						// Revalidated: restart the `latest` TTL window.
						s.resolutions.RefreshLatest(id, now)
					}
					return s.deliverLoadArtifact(*cached, deliverOpts{harness: harnessFromArgs(s.cfg.harness, args), destination: destFromArgs(args)})
				}
			}
		}
	}

	// §12 ETag caching of immutable artifact versions: when the bridge
	// already holds the content for the resolved (id, version), send its
	// content-hash ETag as If-None-Match. A 304 lets the registry confirm the
	// artifact is unchanged without re-sending the manifest body or
	// re-presigning resources, and the bridge serves from cache. A cache miss
	// or any other (id, version) sends no validator and fetches normally.
	condHash := ""
	if id != "" {
		if h, ok := s.resolutions.Resolve(id, version, now, ttl, true); ok && s.cache.has(h) {
			condHash = h
		}
	}
	body, notModified, err := s.fetchJSONConditional("/v1/load_artifact", args, contentHashETag(condHash))
	if err == nil && notModified {
		if cached, cerr := s.loadArtifactFromCache(condHash, id); cerr == nil {
			if version == "" {
				// Revalidated: restart the `latest` TTL window.
				s.resolutions.RefreshLatest(id, now)
			}
			return s.deliverLoadArtifact(*cached, deliverOpts{harness: harnessFromArgs(s.cfg.harness, args), destination: destFromArgs(args)})
		}
		// The cache entry disappeared between the conditional request and the
		// read; refetch unconditionally so the host still gets the artifact.
		body, _, err = s.fetchJSONConditional("/v1/load_artifact", args, "")
	}
	if err != nil {
		// spec: §6.9 — distinguish a registry that could not be reached (a
		// transport-level failure) from one that answered and refused (403
		// visibility denial, 401 auth.token_expired, 409 quota.*, 403
		// auth.untrusted_runtime). Only a genuine transport failure is the
		// "Registry offline" row: fall back to cache and, on a miss, surface
		// network.registry_unreachable. A reachable-but-rejected response
		// carries the registry's structured §6.10 envelope, which must pass
		// through unchanged rather than being relabeled retryable.
		if isRegistryUnreachable(err) {
			// §7.4 degraded-network fallback: in always-revalidate mode, if a
			// fresh fetch fails, try to serve from cache before surfacing the
			// registry-unreachable error. Cache misses surface as
			// network.registry_unreachable.
			if s.cfg.cacheMode == "always-revalidate" && id != "" {
				if hash, ok := s.resolutions.Resolve(id, version, now, ttl, true); ok {
					if cached, cerr := s.loadArtifactFromCache(hash, id); cerr == nil {
						out := s.deliverLoadArtifact(*cached, deliverOpts{harness: harnessFromArgs(s.cfg.harness, args), destination: destFromArgs(args)})
						if m, ok := out.(map[string]any); ok {
							m["status"] = "offline"
							m["served_from_cache"] = true
						}
						return out
					}
				}
			}
			// §7.4 offline-first: "no error; serve cached results silently."
			// The content cache was already consulted above and missed, so
			// there is nothing to serve. Return a silent offline status with no
			// artifact rather than the registry-unreachable error, matching the
			// "no error" contract for this mode. offline-only never
			// reaches here: it short-circuits to errOfflineCacheMiss on the
			// earlier cache miss without calling the registry.
			if s.cfg.cacheMode == "offline-first" {
				return offlineResult(nil)
			}
			return errorResult("network.registry_unreachable: " + err.Error())
		}
		return errorResultFrom(err)
	}
	var resp loadArtifactResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return errorResult("decode load_artifact: " + err.Error())
	}
	if resp.ID == "" {
		// Either an error envelope or an empty result; pass through.
		return jsonAny(body)
	}
	// Update the resolution cache so future reads know the (id, version) →
	// content_hash mapping. A `latest` request records (id, "latest") → semver
	// and (id, semver) → content_hash (§6.5); a pinned request records the
	// version directly.
	if version == "" {
		s.resolutions.PutLatest(id, resp.Version, resp.ContentHash, now)
	} else {
		s.resolutions.PutVersion(id, version, resp.ContentHash, now)
	}

	return s.deliverLoadArtifact(resp, deliverOpts{
		harness:         harnessFromArgs(s.cfg.harness, args),
		destination:     destFromArgs(args),
		refresh:         s.largeResourceRefresher(args),
		manifestRefresh: s.manifestBodyRefresher(args),
	})
}

// manifestBodyRefresher returns a closure that re-requests /v1/load_artifact
// with the same arguments and yields a freshly presigned manifest_body_url,
// backing the §6.6 step 1 "retry with a fresh URL" contract for the
// manifest-body channel. Used only on the live-fetch path; cache and overlay
// deliveries pass nil.
func (s *mcpServer) manifestBodyRefresher(args map[string]any) resourceRefresher {
	return func() (map[string]largeResourceLink, error) {
		body, err := s.fetchJSON("/v1/load_artifact", args)
		if err != nil {
			return nil, err
		}
		var fresh loadArtifactResponse
		if err := json.Unmarshal(body, &fresh); err != nil {
			return nil, err
		}
		if fresh.ManifestBodyURL == nil {
			return nil, nil
		}
		return map[string]largeResourceLink{manifestBodyRefreshKey: *fresh.ManifestBodyURL}, nil
	}
}

// largeResourceRefresher returns a closure that re-requests /v1/load_artifact
// with the same arguments and yields a freshly presigned large_resources URL
// set, backing the §6.6 step 1 "retry with a fresh URL set" contract. It is
// used only on the live-fetch path; cache and overlay deliveries pass nil.
func (s *mcpServer) largeResourceRefresher(args map[string]any) resourceRefresher {
	return func() (map[string]largeResourceLink, error) {
		body, err := s.fetchJSON("/v1/load_artifact", args)
		if err != nil {
			return nil, err
		}
		var fresh loadArtifactResponse
		if err := json.Unmarshal(body, &fresh); err != nil {
			return nil, err
		}
		return fresh.LargeResources, nil
	}
}

// deliverOpts threads the per-call adjustments deliverLoadArtifact
// needs: the harness override and the per-call destination root.
type deliverOpts struct {
	harness string
	// destination is the §6.2 / §6.6 per-call materialization root. When
	// set it takes precedence over PODIUM_MATERIALIZE_ROOT, so a host can
	// supply the destination per load_artifact call instead of (or in
	// addition to) the process-wide env var.
	destination string
	// refresh re-requests a freshly presigned large_resources URL set so a
	// 403/expired URL is replaced rather than retried unchanged (§6.6 step
	// 1). Set only on the live-fetch path; nil on cache/overlay paths, which
	// cannot reach the registry.
	refresh resourceRefresher
	// manifestRefresh re-requests a freshly presigned manifest_body_url so a
	// 403/expired body URL is replaced rather than retried unchanged (§6.6
	// step 1). Set only on the live-fetch path; nil on cache/overlay paths.
	manifestRefresh resourceRefresher
}

// destFromArgs returns the per-call materialization destination from a
// load_artifact call's arguments (§6.2 / §6.6). The host may name it
// `destination`, `materialize_root`, or `path`; the first set wins.
func destFromArgs(args map[string]any) string {
	for _, key := range []string{"destination", "materialize_root", "path"} {
		if v, ok := args[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// absMaterializeRoot returns root as an absolute path so the materialized_at
// entries the bridge reports are absolute and ready to use (spec: §5.1 "The
// returned materialized_at paths are absolute and ready to use"). The
// destination may arrive relative via the per-call destination argument or
// PODIUM_MATERIALIZE_ROOT; absolutizing it once means every joined path is
// absolute regardless of the caller's working directory, so a host that
// resolves the paths against its own CWD (which can differ from the MCP server
// process CWD) lands on the correct location. The on-disk write absolutizes
// internally (materialize.Write -> filepath.Abs), so this only aligns the
// reported paths with where the files actually land. A filepath.Abs failure
// (it only fails when the working directory is unknowable) leaves root
// unchanged so materialization still proceeds.
func absMaterializeRoot(root string) string {
	if root == "" {
		return root
	}
	if abs, err := filepath.Abs(root); err == nil {
		return abs
	}
	return root
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
	// §6.6 step 1 — when the manifest body was delivered above the inline
	// cutoff as a presigned URL, fetch and reconstitute it before any policy
	// gate or the content-hash check reads the frontmatter. A no-op when the
	// body arrived inline (cache and overlay paths never presign).
	if err := s.fetchManifestBody(&resp, o.manifestRefresh); err != nil {
		return errorResult("materialize.fetch_failed: " + err.Error())
	}
	// §8.1 / §8.2: record the local artifact.loaded event with the in-flight
	// trace id and the manifest's audit_redact directive applied. Emitted here
	// (rather than at dispatch) so the resolved frontmatter supplies the
	// sensitive field values the directive masks.
	s.auditLoadArtifact(resp.ID, resp.Frontmatter)
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
	// §4.4.1 runtime_requirements enforcement: a host that advertises
	// its capabilities refuses an artifact it cannot satisfy. The error
	// already carries the materialize.runtime_unavailable code.
	if err := s.enforceRuntimePolicy(resp); err != nil {
		return errorResult(err.Error())
	}
	// §6.6 step 1 — normalize inline resources. When the registry flags them
	// base64, decode to raw bytes before the content-hash check and
	// materialization so the host receives the payload rather than base64 text.
	if err := decodeInlineResources(&resp); err != nil {
		return errorResult(err.Error())
	}
	// §6.6 step 1 — fetch every large_resource via its presigned URL into the
	// inline Resources map. Failures (network / 403 / hash mismatch) abort
	// materialization with a structured error; a 403/expired URL is refreshed.
	if err := s.fetchLargeResources(&resp, o.refresh); err != nil {
		return errorResult("materialize.fetch_failed: " + err.Error())
	}
	// §6.6 step 2 — content-hash match. Recompute the canonical hash over the
	// delivered manifest bytes and bundled resources and reject a mismatch
	// before anything is cached or written.
	if err := s.verifyContentHash(resp); err != nil {
		return errorResult(err.Error())
	}

	// Cache the canonical bytes (content cache is forever-immutable
	// per §6.5). Persist skill_raw / raw_frontmatter alongside so a
	// cache-served skill or extends-merged manifest reproduces the exact
	// bytes the §6.6 step 2 content hash was computed over.
	if err := s.cache.put(resp.ContentHash, resp.Frontmatter, resp.ManifestBody, resp.Resources); err != nil {
		return errorResult("cache: " + err.Error())
	}
	if err := s.cache.putExtras(resp.ContentHash, cacheExtras{
		SkillRaw:       resp.SkillRaw,
		RawFrontmatter: resp.RawFrontmatter,
		Sensitivity:    resp.Sensitivity,
		Signature:      resp.Signature,
	}); err != nil {
		return errorResult("cache: " + err.Error())
	}

	// Materialize to host filesystem when a destination is configured,
	// either per call (§6.2 / §6.6) or via PODIUM_MATERIALIZE_ROOT. The
	// per-call destination wins when both are present.
	materialized := []string{}
	var warnings []string
	root := o.destination
	if root == "" {
		root = s.cfg.materializeRoot
	}
	// §5.1: absolutize the root so every materialized_at entry is absolute,
	// matching the sandbox-profile entry (already absolute) and the actual
	// on-disk write location.
	root = absMaterializeRoot(root)
	if root != "" {
		harnessID := o.harness
		if harnessID == "" {
			harnessID = s.cfg.harness
		}
		a, err := s.adapters.Get(harnessID)
		if err != nil {
			return errorResult("config.unknown_harness: " + err.Error())
		}
		// §4.3 target_harnesses: suppress the on-disk write when the
		// artifact opts out of this harness. The manifest content is
		// still returned to the caller; only materialization is skipped.
		if materializeTargetsHarness(resp.Frontmatter, harnessID) {
			// §6.9 "Adapter cannot translate an artifact": when the
			// selected harness has no §6.7.1 equivalent for a field the
			// artifact uses (a ✗ cell), fail with a structured error
			// naming the field instead of writing an unannotated verbatim
			// copy.
			if art, perr := manifest.ParseArtifact([]byte(resp.Frontmatter)); perr == nil {
				if terr := adapter.TranslationError(harnessID, art); terr != nil {
					return errorResult(terr.Error())
				}
			}
			src := adapter.Source{
				ArtifactID:    resp.ID,
				ArtifactBytes: []byte(resp.Frontmatter),
				Resources:     resourcesAsBytes(resp.Resources),
			}
			if resp.Type == "skill" {
				src.SkillBytes = []byte(synthesizeSkillMD(resp))
			}
			// §13.8: child span for the §6.7 adapter translation stage.
			adaptCtx, adaptSpan := tracing.Tracer().Start(s.reqCtx(), "adapter.translate")
			out, err := a.Adapt(adaptCtx, src)
			adaptSpan.End()
			if err != nil {
				return errorResult("adapter: " + err.Error())
			}
			// §6.6 step 4 — run the configured MaterializationHook chain
			// over the adapter output before the atomic write. Hooks may
			// rewrite or drop files and emit warnings; the chain is a no-op
			// when none are configured and runs whether or not an adapter
			// translated (harness: none still produces the canonical layout).
			hookedOut, hookWarnings, herr := hook.Run(s.reqCtx(), s.hooks, manifestContext(resp.Frontmatter), out)
			if herr != nil {
				return errorResult("materialize.hook_failed: " + herr.Error())
			}
			warnings = append(warnings, hookWarnings...)
			out = hookedOut
			// §13.8: child span for the §6.6 materialization write stage.
			_, matSpan := tracing.Tracer().Start(s.reqCtx(), "materialize")
			werr := materialize.Write(root, out)
			matSpan.End()
			if werr != nil {
				return errorResult("materialize: " + werr.Error())
			}
			for _, f := range out {
				materialized = append(materialized, filepath.Join(root, filepath.FromSlash(f.Path)))
			}
		}
		// §4.4.1 — when the artifact requests a sandbox_profile whose
		// baseline Podium ships (seccomp-strict), deliver that baseline
		// alongside the materialized files so a host with sandbox
		// capability can honor it. The sandbox gate above has already
		// confirmed the host honors (or was told to ignore) the profile.
		if p, ok, err := materialize.WriteSandboxProfile(root, sandboxProfileOf(resp.Frontmatter)); err != nil {
			return errorResult("materialize: " + err.Error())
		} else if ok {
			materialized = append(materialized, p)
		}
	}

	result := map[string]any{
		"id":              resp.ID,
		"type":            resp.Type,
		"version":         resp.Version,
		"content_hash":    resp.ContentHash,
		"manifest_body":   resp.ManifestBody,
		"materialized_at": materialized,
	}
	// §6.6 step 4 — surface any warnings the hook chain emitted alongside the
	// materialized paths so the host sees them without failing the call.
	if len(warnings) > 0 {
		result["warnings"] = warnings
	}
	return result
}

// decodeInlineResources decodes base64-encoded inline resources in place when
// the registry set resources_base64. Large resources are fetched
// raw and are unaffected. A value that does not decode fails the call with a
// structured error rather than writing the base64 text to disk.
func decodeInlineResources(resp *loadArtifactResponse) error {
	if !resp.ResourcesB64 || len(resp.Resources) == 0 {
		return nil
	}
	decoded := make(map[string]string, len(resp.Resources))
	for k, v := range resp.Resources {
		raw, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return fmt.Errorf("materialize.invalid_base64: resource %s: %v", k, err)
		}
		decoded[k] = string(raw)
	}
	resp.Resources = decoded
	resp.ResourcesB64 = false
	return nil
}

// verifyContentHash recomputes the canonical content hash over the served
// manifest bytes and bundled resources and compares it to resp.ContentHash
// (§4.7.6 / §6.6 step 2). It binds the delivered artifact bytes, the verbatim
// SKILL.md, and the inline resources to the content_hash so a registry response
// (or a non-TLS hop) that tampered with the bytes while keeping a consistent
// (content_hash, signature) pair is rejected before materialization. For
// sub-threshold artifacts that carry no signature this is the only integrity
// gate the spec defines, so step 2 runs the match for every artifact type.
//
// The recomputation reproduces the registry's ingest canonicalization
// (contentHashOf over version.ContentHash): the original ARTIFACT.md bytes, the
// SKILL.md slot, then each bundled resource in sorted-path order.
//   - For a skill the SKILL.md slot carries the verbatim SKILL.md the registry
//     ships in skill_raw; the content_hash covers those bytes, which the prose
//     body alone could not reproduce.
//   - For an extends-merged manifest (resp.ManifestMerged) the served
//     frontmatter is a re-serialization with the hidden parent stripped (§4.6),
//     so the recomputation reads the leaf child's original ARTIFACT.md bytes
//     from raw_frontmatter, which is what the hash was computed over.
func (s *mcpServer) verifyContentHash(resp loadArtifactResponse) error {
	if resp.ContentHash == "" {
		return nil
	}
	// Slot 0: the original ARTIFACT.md bytes the hash was computed over. For a
	// merged manifest that is the pre-merge raw_frontmatter, not the served
	// (re-serialized) frontmatter.
	artifactBytes := []byte(resp.Frontmatter)
	if resp.ManifestMerged {
		artifactBytes = []byte(resp.RawFrontmatter)
	}
	// Slot 1: the verbatim SKILL.md for a skill (skill_raw), empty otherwise —
	// matching the registry's contentHashOf, which hashes rec.SkillBytes (nil
	// for non-skills) in this position.
	parts := [][]byte{artifactBytes, []byte(resp.SkillRaw)}
	keys := make([]string, 0, len(resp.Resources))
	for k := range resp.Resources {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, []byte(k), []byte(resp.Resources[k]))
	}
	got := "sha256:" + version.ContentHash(parts...)
	if got != resp.ContentHash {
		return fmt.Errorf("materialize.content_hash_mismatch: recomputed %s does not match served %s", got, resp.ContentHash)
	}
	return nil
}

// manifestContext parses the served frontmatter into the map[string]any the
// MaterializationHook chain receives for context (§6.6 step 4). A parse
// failure yields a nil map; the hooks still run over the file bytes.
func manifestContext(frontmatter string) map[string]any {
	fm, _, err := manifest.SplitFrontmatter([]byte(frontmatter))
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := yaml.Unmarshal(fm, &m); err != nil {
		return nil
	}
	return m
}

// materializeTargetsHarness reports whether the artifact whose full
// ARTIFACT.md is in frontmatter should materialize for harnessID, per
// §4.3 target_harnesses. Frontmatter that fails to parse (it already
// passed ingest) defaults to targeting every harness so a parse quirk
// never silently suppresses a write.
func materializeTargetsHarness(frontmatter, harnessID string) bool {
	a, err := manifest.ParseArtifact([]byte(frontmatter))
	if err != nil || a == nil {
		return true
	}
	return manifest.TargetsHarness(a.TargetHarnesses, harnessID)
}

// harnessFromArgs returns args["harness"] as a string when set,
// otherwise the deployment default.
func harnessFromArgs(defaultID string, args map[string]any) string {
	if h, ok := args["harness"].(string); ok && h != "" {
		return h
	}
	return defaultID
}

// argString returns args[key] as a string, or "" when absent or not a
// string. Used to record a meta-tool's target in the local audit event.
func argString(args map[string]any, key string) string {
	if v, ok := args[key].(string); ok {
		return v
	}
	return ""
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
	// §8.1 / §8.2: the overlay load path emits its own artifact.loaded event,
	// matching the registry-served path (deliverLoadArtifact), so an overlay
	// artifact's audit_redact directive is honored on the local sink too.
	s.auditLoadArtifact(resp.ID, resp.Frontmatter)
	if err := s.cache.put(contentHash, resp.Frontmatter, resp.ManifestBody, resp.Resources); err != nil {
		return errorResult("cache: " + err.Error())
	}
	materialized := []string{}
	var warnings []string
	root := destFromArgs(args)
	if root == "" {
		root = s.cfg.materializeRoot
	}
	// §5.1: absolutize the root so the overlay path returns absolute
	// materialized_at entries, matching the registry-served path.
	root = absMaterializeRoot(root)
	if root != "" {
		harnessID := s.cfg.harness
		if h, ok := args["harness"].(string); ok && h != "" {
			harnessID = h
		}
		a, err := s.adapters.Get(harnessID)
		if err != nil {
			return errorResult("config.unknown_harness: " + err.Error())
		}
		// §4.3 target_harnesses: an overlay artifact that opts out of
		// this harness is not written; the response still carries its
		// manifest body. A record without a parsed artifact targets all.
		targets := rec.Artifact == nil || manifest.TargetsHarness(rec.Artifact.TargetHarnesses, harnessID)
		if targets {
			// §6.9 guard: refuse to materialize a field the selected
			// harness cannot translate (a §6.7.1 ✗ cell).
			if rec.Artifact != nil {
				if terr := adapter.TranslationError(harnessID, rec.Artifact); terr != nil {
					return errorResult(terr.Error())
				}
			}
			src := adapter.Source{
				ArtifactID:    rec.ID,
				ArtifactBytes: rec.ArtifactBytes,
				SkillBytes:    rec.SkillBytes,
				Resources:     rec.Resources,
			}
			out, err := a.Adapt(context.Background(), src)
			if err != nil {
				return errorResult("adapter: " + err.Error())
			}
			// §6.6 step 4 — the hook chain runs on the overlay path too, so
			// the workspace-overlay layer is subject to the same per-file
			// rewrite/drop contract as a registry-served artifact.
			hookedOut, hookWarnings, herr := hook.Run(context.Background(), s.hooks, manifestContext(string(rec.ArtifactBytes)), out)
			if herr != nil {
				return errorResult("materialize.hook_failed: " + herr.Error())
			}
			warnings = append(warnings, hookWarnings...)
			out = hookedOut
			if err := materialize.Write(root, out); err != nil {
				return errorResult("materialize: " + err.Error())
			}
			for _, f := range out {
				materialized = append(materialized, filepath.Join(root, filepath.FromSlash(f.Path)))
			}
		}
	}
	result := map[string]any{
		"id":              resp.ID,
		"type":            resp.Type,
		"version":         resp.Version,
		"content_hash":    resp.ContentHash,
		"manifest_body":   resp.ManifestBody,
		"layer":           resp.Layer,
		"materialized_at": materialized,
	}
	if len(warnings) > 0 {
		result["warnings"] = warnings
	}
	return result
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
	ID           string `json:"id"`
	Type         string `json:"type"`
	Version      string `json:"version"`
	ContentHash  string `json:"content_hash"`
	ManifestBody string `json:"manifest_body"`
	Frontmatter  string `json:"frontmatter"`
	// SkillRaw is the verbatim SKILL.md for a type: skill artifact (§4.3.4),
	// delivered so materialization reproduces the authored skill file exactly
	// rather than reconstructing it from ARTIFACT.md frontmatter plus body.
	SkillRaw    string            `json:"skill_raw,omitempty"`
	Layer       string            `json:"layer,omitempty"`
	Sensitivity string            `json:"sensitivity,omitempty"`
	Resources   map[string]string `json:"resources,omitempty"`
	// ResourcesB64 mirrors the registry's resources_base64 flag: when true,
	// the inline Resources values are base64-encoded and must be decoded to
	// raw bytes before the content-hash check and materialization.
	ResourcesB64   bool                         `json:"resources_base64,omitempty"`
	LargeResources map[string]largeResourceLink `json:"large_resources,omitempty"`
	// ManifestBodyURL delivers the canonical manifest document via a
	// presigned object-store URL when it exceeds the §4.2 inline cutoff
	// (§6.6). When set, the inline ManifestBody and the canonical-document
	// field (Frontmatter, or SkillRaw for a skill) arrive empty;
	// deliverLoadArtifact fetches the URL in §6.6 step 1 and reconstitutes
	// them before any policy gate or the content-hash check. Nil for a
	// below-cutoff body delivered inline.
	ManifestBodyURL *largeResourceLink `json:"manifest_body_url,omitempty"`
	Signature       string             `json:"signature,omitempty"`
	// ManifestMerged signals that the served frontmatter is an extends-merged
	// re-serialization with the hidden parent stripped (§4.6) rather than the
	// original bytes the content_hash was computed over. The consumer
	// recomputes the §6.6 step 2 content hash over RawFrontmatter for such
	// manifests instead of over the served (merged) frontmatter.
	ManifestMerged bool `json:"manifest_merged,omitempty"`
	// RawFrontmatter is the leaf child's original pre-merge ARTIFACT.md bytes,
	// delivered when ManifestMerged is set so the bridge reproduces the §4.7.6
	// content hash for a merged manifest. Empty for a non-merged response.
	RawFrontmatter string `json:"raw_frontmatter,omitempty"`
}

// largeResourceLink mirrors the registry's per-resource link. The
// MCP server fetches the URL during materialization, retrying on
// 403/expired (§6.6 step 1) up to three times. The field is named
// presigned_url to match the §7.6.2 wire example and the registry's
// load_artifact response.
type largeResourceLink struct {
	URL         string `json:"presigned_url"`
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
	return sign.EnforceVerification(context.Background(),
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

// sandboxProfileOf returns the sandbox_profile declared in the frontmatter,
// defaulting to unrestricted when absent or unparseable. Used to decide
// whether a shipped baseline profile (seccomp-strict) must be delivered.
func sandboxProfileOf(frontmatter string) string {
	a, err := manifest.ParseArtifact([]byte(frontmatter))
	if err != nil || a == nil || a.SandboxProfile == "" {
		return string(manifest.SandboxUnrestricted)
	}
	return string(a.SandboxProfile)
}

// enforceRuntimePolicy applies the §4.4.1 runtime_requirements gate. The
// host advertises its capabilities via PODIUM_HOST_PYTHON,
// PODIUM_HOST_NODE, and PODIUM_HOST_PACKAGES; an artifact that declares a
// requirement the host cannot satisfy is refused at load time with
// materialize.runtime_unavailable. The returned error already carries
// that code (it wraps materialize.ErrRuntimeUnavailable).
//
// The gate is active only once the host opts in by advertising at least
// one capability, or by setting PODIUM_ENFORCE_RUNTIME_REQUIREMENTS. An
// unconfigured host surfaces runtime_requirements to the caller without
// refusing, matching the spec's "where supported" framing: a host that
// cannot evaluate its own capabilities does not gate. This mirrors the
// sandbox gate, whose host capabilities default to unrestricted.
func (s *mcpServer) enforceRuntimePolicy(resp loadArtifactResponse) error {
	a, err := manifest.ParseArtifact([]byte(resp.Frontmatter))
	if err != nil {
		// Fail closed: malformed frontmatter is a refusal.
		return fmt.Errorf("%w: parse frontmatter: %v", materialize.ErrRuntimeUnavailable, err)
	}
	if a.RuntimeRequirements == nil {
		return nil
	}
	if !s.runtimeGateActive() {
		return nil
	}
	if s.cfg.ignoreRuntime {
		// §4.4.1 — explicit override: log loudly so operators see the
		// bypass in the audit trail.
		fmt.Fprintf(os.Stderr,
			"WARN: PODIUM_IGNORE_RUNTIME_REQUIREMENTS bypassing runtime check — artifact %s declares runtime_requirements; host advertises python=%q node=%q packages=%v\n",
			resp.ID, s.cfg.hostPython, s.cfg.hostNode, s.cfg.hostPackages)
		return nil
	}
	return materialize.CheckRuntimeRequirements(
		runtimeRequirementsMap(a.RuntimeRequirements),
		materialize.HostCapabilities{
			Python:         s.cfg.hostPython,
			Node:           s.cfg.hostNode,
			SystemPackages: s.cfg.hostPackages,
		},
	)
}

// runtimeGateActive reports whether the host has opted into runtime
// capability gating, either by advertising a capability or by the
// explicit enforce flag.
func (s *mcpServer) runtimeGateActive() bool {
	return s.cfg.enforceRuntime ||
		s.cfg.hostPython != "" ||
		s.cfg.hostNode != "" ||
		len(s.cfg.hostPackages) > 0
}

// runtimeRequirementsMap converts the typed manifest requirements into
// the map[string]any CheckRuntimeRequirements consumes. system_packages
// is carried as []string, which the check handles directly (and now also
// when it arrives as []any).
func runtimeRequirementsMap(r *manifest.RuntimeRequirements) map[string]any {
	if r == nil {
		return nil
	}
	m := map[string]any{}
	if r.Python != "" {
		m["python"] = r.Python
	}
	if r.Node != "" {
		m["node"] = r.Node
	}
	if len(r.SystemPackages) > 0 {
		m["system_packages"] = r.SystemPackages
	}
	return m
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
// registry-managed for tenant-key deployments. The registry-managed
// verifier loads the registry's public key from
// PODIUM_SIGNATURE_VERIFY_KEY (base64 Ed25519) so the consumer can check
// the detached signature envelope (§4.7.9).
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
		// §4.7.9: verification runs in the consumer, so the MCP server holds
		// the registry's public key. PODIUM_SIGNATURE_VERIFY_KEY carries the
		// base64-encoded Ed25519 public key the registry publishes for its
		// signing keypair; PODIUM_SIGNATURE_KEY_ID, when set, pins the
		// expected key fingerprint so a signature from a rotated key is
		// refused. When the verify key is unset the provider has no public
		// key and Verify returns config.signature_provider_unavailable, which
		// surfaces as materialize.signature_invalid under an enforcing policy.
		k := sign.RegistryManagedKey{KeyID: os.Getenv("PODIUM_SIGNATURE_KEY_ID")}
		if raw := os.Getenv("PODIUM_SIGNATURE_VERIFY_KEY"); raw != "" {
			pub, err := sign.PublicKeyFromBase64(raw)
			if err != nil {
				return nil, fmt.Errorf("PODIUM_SIGNATURE_VERIFY_KEY: %w", err)
			}
			k.PublicKey = pub
		}
		return k, nil
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
	// spec: §4.3.4 / §11 — the registry delivers the verbatim SKILL.md in
	// skill_raw, so the materialized skill file is byte-identical to the
	// authored source. The authored SKILL.md frontmatter (name, description,
	// compatibility, allowed-tools, …) cannot be reconstructed from
	// ARTIFACT.md frontmatter plus body, so prefer the raw bytes.
	if r.SkillRaw != "" {
		return r.SkillRaw
	}
	return r.Frontmatter + r.ManifestBody
}

// newRegistryRequest builds an authenticated registry request for the given
// method and path, encoding args as query parameters and attaching the §6.3
// credential, tenant, and session-correlation headers. Shared by fetchJSON and
// the §6.5 HEAD revalidation path.
func (s *mcpServer) newRegistryRequest(method, path string, args map[string]any) (*http.Request, error) {
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
	// §3.3 / §4.7.6 — correlate every meta-tool call in this bridge
	// process under one session_id so the registry resolves `latest`
	// consistently and can observe search-to-load within the session.
	// This is what backs the advertised sessionCorrelation capability
	// (§5). A host that passed its own session_id argument keeps it.
	if q.Get("session_id") == "" && s.sessionID != "" {
		q.Set("session_id", s.sessionID)
	}
	u.RawQuery = q.Encode()
	// §13.8: build on the in-flight call's trace context so the otelhttp
	// transport injects W3C trace-context headers and the round-trip span
	// parents under the call's root span.
	req, err := http.NewRequestWithContext(s.reqCtx(), method, u.String(), nil)
	if err != nil {
		return nil, err
	}
	// §6.3: attach the caller credential per the selected identity
	// provider — the injected session token, or an oauth-device-code token
	// (cached in the keychain, acquired via the device flow on first use).
	tok, err := s.bearerToken()
	if err != nil {
		return nil, err
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	// §6.3 PODIUM_TENANT_ID: forwards the tenant context to the
	// registry on every request so multi-tenant deployments can
	// route without parsing the JWT.
	if s.cfg.tenantID != "" {
		req.Header.Set("X-Podium-Tenant", s.cfg.tenantID)
	}
	return req, nil
}

// contentHashETag formats a cached content hash as the strong HTTP ETag the
// registry publishes (a quoted opaque string) so it can be sent back as an
// If-None-Match validator. spec: §12. An empty hash yields an empty string so
// the caller omits the header.
func contentHashETag(contentHash string) string {
	if contentHash == "" {
		return ""
	}
	return `"` + contentHash + `"`
}

// fetchJSONConditional makes an authenticated GET against the registry,
// optionally sending an If-None-Match validator. spec: §12 ETag caching of
// immutable artifact versions — when the bridge already holds the content for
// a resolved (id, version) it sends the content-hash ETag as If-None-Match;
// the registry answers 304 Not Modified when the artifact is unchanged, which
// the bridge serves from its content-addressed cache. notModified is true on a
// 304 (body is nil); otherwise the decoded body and any error are returned as
// for fetchJSON.
func (s *mcpServer) fetchJSONConditional(path string, args map[string]any, ifNoneMatch string) (body []byte, notModified bool, err error) {
	req, err := s.newRegistryRequest("GET", path, args)
	if err != nil {
		return nil, false, err
	}
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	client := s.http
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		// §12: the registry confirmed the cached content hash is current.
		_, _ = io.Copy(io.Discard, resp.Body)
		s.recordSuccess(time.Now())
		return nil, true, nil
	}
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, false, err
	}
	if resp.StatusCode >= 400 {
		return body, false, parseRegistryError(resp.StatusCode, body)
	}
	s.recordSuccess(time.Now())
	return body, false, nil
}

// fetchJSON makes an authenticated GET against the registry and returns
// the response body.
func (s *mcpServer) fetchJSON(path string, args map[string]any) ([]byte, error) {
	req, err := s.newRegistryRequest("GET", path, args)
	if err != nil {
		return nil, err
	}
	client := s.http
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		// §6.10: decode the registry's structured envelope so the
		// namespaced code, details, retryable, and suggested_action
		// survive to the MCP client instead of collapsing into an
		// opaque "HTTP <status>: <body>" string.
		return body, parseRegistryError(resp.StatusCode, body)
	}
	// §13.9: a 2xx response is a successful registry call; stamp it so
	// the health tool can report the last-successful-call timestamp.
	s.recordSuccess(time.Now())
	return body, nil
}

// headContentHash issues an authenticated HEAD against the registry to
// revalidate a cached resolution without downloading the manifest body or
// presigning resources (§6.5 always-revalidate). It returns the registry's
// current content hash for the requested (id, version).
func (s *mcpServer) headContentHash(path string, args map[string]any) (string, error) {
	req, err := s.newRegistryRequest(http.MethodHead, path, args)
	if err != nil {
		return "", err
	}
	client := s.http
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HEAD %s: status %d", path, resp.StatusCode)
	}
	s.recordSuccess(time.Now())
	return resp.Header.Get("X-Podium-Content-Hash"), nil
}

// batchLoadEnvelope mirrors the registry's §7.6.2 per-item batch response,
// decoding only the fields prefetch needs to warm the cache. Bundled resources
// travel as presigned references the consumer fetches on demand, so prefetch
// warms the manifest body and resolution rather than resource bytes.
type batchLoadEnvelope struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	Version      string `json:"version"`
	ContentHash  string `json:"content_hash"`
	ManifestBody string `json:"manifest_body"`
	Frontmatter  string `json:"frontmatter"`
	// SkillRaw is the verbatim SKILL.md for a type: skill artifact. The
	// §7.6.2 batch endpoint emits it (§4.3.4); prefetch persists it so a
	// later cache-served load of a warmed skill reproduces the bytes its
	// content hash covers rather than failing content_hash_mismatch.
	SkillRaw string `json:"skill_raw"`
}

// prefetch warms the §6.5 content and resolution caches from the §7.6.2
// batch-load endpoint. spec: §7.6.2 — "The MCP server uses this endpoint
// internally for cache warm-up when configured to prefetch." It POSTs the
// configured IDs in batches of the §7.6.2 cap; for each ok item it stores the
// manifest in the content cache and records the (id, "latest") -> version ->
// content_hash resolution so a later load_artifact HEAD-revalidates and serves
// from cache instead of re-downloading the manifest.
func (s *mcpServer) prefetch(ids []string) error {
	const batchCap = 50 // §7.6.2 hard cap
	for start := 0; start < len(ids); start += batchCap {
		end := start + batchCap
		if end > len(ids) {
			end = len(ids)
		}
		if err := s.prefetchChunk(ids[start:end]); err != nil {
			return err
		}
	}
	return nil
}

// prefetchChunk warms one ≤50-ID batch. The request is bounded by a context
// deadline so an unreachable registry cannot block bridge startup.
func (s *mcpServer) prefetchChunk(chunk []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	reqBody, err := json.Marshal(map[string]any{"ids": chunk, "session_id": s.sessionID})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.cfg.registry+"/v1/artifacts:batchLoad", bytes.NewReader(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	// §6.3: warm-up runs as the bridge's own identity, like every other call.
	if tok, terr := s.bearerToken(); terr == nil && tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if s.cfg.tenantID != "" {
		req.Header.Set("X-Podium-Tenant", s.cfg.tenantID)
	}
	client := s.http
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("batchLoad: status %d", resp.StatusCode)
	}
	var envs []batchLoadEnvelope
	if err := json.Unmarshal(raw, &envs); err != nil {
		return err
	}
	now := time.Now()
	for _, e := range envs {
		if e.Status != "ok" || e.ContentHash == "" {
			continue
		}
		_ = s.cache.put(e.ContentHash, e.Frontmatter, e.ManifestBody, nil)
		// The §7.6.2 batch envelope carries skill_raw (so a warmed skill keeps
		// its content hash) but not sensitivity/signature, so a prefetched entry
		// records neither. A later cache-served load of such an entry that needs
		// signature verification re-fetches rather than serving the unverified
		// warmed bytes (see loadArtifactFromCache + always-revalidate).
		_ = s.cache.putExtras(e.ContentHash, cacheExtras{SkillRaw: e.SkillRaw})
		// (id, "latest") -> version -> content_hash so always-revalidate finds
		// the cached content on the next load.
		s.resolutions.PutLatest(e.ID, e.Version, e.ContentHash, now)
	}
	s.recordSuccess(now)
	return nil
}

// proxyGet forwards a GET meta-tool call to the registry. spec: §12 — the
// "Registry as a single point of failure for hosts" mitigation states that a
// fresh load_domain / search_domains / search_artifacts "returns an explicit
// 'offline' status that hosts can surface." When the registry is unreachable
// (a transport-level failure rather than a structured >=400 response), the
// bridge returns a result carrying status "offline" merged with `offline`
// (e.g. an empty `results` list for search_domains) instead of an error, so
// the host can distinguish a transient outage from a request rejection. A
// structured registry error still passes through as a §6.10 error envelope.
func (s *mcpServer) proxyGet(path string, args, offline map[string]any) any {
	// §7.4 offline-only: "never contact the registry; structured error if
	// cache miss." The discovery meta-tools keep no content cache (§6.5 caches
	// canonical artifact bytes for load_artifact, not domain trees or search
	// results), so an offline-only call has nothing local to serve and returns
	// the structured offline cache-miss error without opening a connection.
	if s.cfg.cacheMode == "offline-only" {
		return errorResult(errOfflineCacheMiss.Error())
	}
	body, err := s.fetchJSON(path, args)
	if err != nil {
		// §7.4 / §12 degraded network: a transport-level failure (registry
		// unreachable) returns the offline envelope rather than an error, so
		// the host can tell a transient outage from a request rejection.
		// always-revalidate surfaces the explicit "offline" status; offline-first
		// serves silently with no status field (offlineEnvelope honors the mode).
		// The discovery tools have no content cache to fall back to, so the
		// envelope carries no served_from_cache results. A structured >=400
		// response means the registry answered and refused; it passes through as
		// a §6.10 error.
		if isRegistryUnreachable(err) {
			return s.offlineEnvelope(offline)
		}
		return errorResultFrom(err)
	}
	return jsonAny(body)
}

// isRegistryUnreachable reports whether err is a transport-level failure
// reaching the registry (DNS, dial, timeout, connection reset) rather than a
// structured >=400 response. A *registryError means the registry answered, so
// it is reachable; anything else is treated as the registry being unreachable.
// spec: §12 — the offline status the discovery/search meta-tools surface
// during a transient outage.
func isRegistryUnreachable(err error) bool {
	if err == nil {
		return false
	}
	var re *registryError
	return !errors.As(err, &re)
}

// offlineResult builds the §12 offline status envelope, merging any
// tool-specific keys (e.g. an empty `results` list) over the base status.
func offlineResult(extra map[string]any) map[string]any {
	m := map[string]any{"status": "offline"}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// offlineEnvelope builds the degraded-network result for the discovery and
// search meta-tools, honoring the §7.4 per-mode distinction: always-revalidate
// surfaces the explicit "offline" status hosts can present, while offline-first
// serves cached results "silently" with no status field. The tool-specific
// keys in extra (for example an empty results list) are always carried.
// offline-only never reaches this path; it returns a structured cache-miss
// error before dialing the registry.
func (s *mcpServer) offlineEnvelope(extra map[string]any) map[string]any {
	if s.cfg.cacheMode == "offline-first" {
		m := map[string]any{}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	return offlineResult(extra)
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

// cacheExtras is the auxiliary per-artifact content a cache-served load must
// reproduce so the §6.6 verification + content-hash gates behave identically to
// a live fetch. The plain frontmatter/body the base put writes is not enough.
type cacheExtras struct {
	// SkillRaw is the verbatim SKILL.md for a skill, whose bytes the registry
	// folds into the canonical content hash (§4.3.4). Without it the §6.6 step 2
	// recompute hashes ARTIFACT.md with an empty slot 1 and fails.
	SkillRaw string
	// RawFrontmatter is the leaf child's pre-merge ARTIFACT.md for an
	// extends-merged manifest (§4.7.6), which the content hash covers in place
	// of the re-serialized frontmatter.
	RawFrontmatter string
	// Sensitivity and Signature drive the §4.7.9 signature policy. They are not
	// inputs to the content hash, but enforceSignaturePolicy needs them: a
	// cache-served high-sensitivity artifact that dropped its sensitivity would
	// skip verification entirely, and one that dropped its signature envelope
	// would fail a policy it should pass. Persisting both makes verification run
	// uniformly whether the bytes came from the registry or the cache.
	Sensitivity string
	Signature   string
}

// putExtras persists the cacheExtras side files next to the base
// frontmatter/body. Each file is written only when its field is non-empty, so a
// non-skill, non-merged, low-sensitivity, unsigned artifact leaves the bucket
// exactly as the base put left it. Call after put, which created the bucket.
func (c *contentCache) putExtras(hash string, ex cacheExtras) error {
	if c.dir == "" || hash == "" {
		return nil
	}
	bucket := filepath.Join(c.dir, sanitizeHash(hash))
	for name, content := range map[string]string{
		"skill_raw":       ex.SkillRaw,
		"raw_frontmatter": ex.RawFrontmatter,
		"sensitivity":     ex.Sensitivity,
		"signature":       ex.Signature,
	} {
		if content == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(bucket, name), []byte(content), 0o644); err != nil {
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

// sanitizeHash maps a content hash to its on-disk content-bucket name. The
// §6.5 disk-cache layout is `${PODIUM_CACHE_DIR}/<sha256>/`, so the bucket name
// is the bare hex digest with the `sha256:` algorithm prefix stripped.
func sanitizeHash(h string) string {
	out := strings.TrimPrefix(h, "sha256:")
	// Defense-in-depth: never let a separator escape the cache root for a
	// non-sha256 or malformed hash.
	out = strings.ReplaceAll(out, ":", "-")
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
