// Package serverboot is the shared bootstrap for the registry HTTP
// server. The podium-server binary and the `podium serve`
// subcommand both call Run, so a single deployment ships only the
// `podium` binary and still has the standalone server available
// in-process.
//
// Configuration comes from PODIUM_* environment variables (§13.12)
// with `~/.podium/registry.yaml` filling in any unset values.
// Default behavior matches §13.10's zero-flag standalone
// deployment: SQLite metadata, filesystem object store, no auth,
// bound on 127.0.0.1:8080.
package serverboot

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lennylabs/podium/pkg/embedding"
	"github.com/lennylabs/podium/pkg/identity"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/notification"
	"github.com/lennylabs/podium/pkg/objectstore"
	"github.com/lennylabs/podium/pkg/registry/core"
	"github.com/lennylabs/podium/pkg/registry/server"
	"github.com/lennylabs/podium/pkg/scim"
	"github.com/lennylabs/podium/pkg/store"
	"github.com/lennylabs/podium/pkg/vector"
	"github.com/lennylabs/podium/pkg/webhook"
	"github.com/lennylabs/podium/web"
)

// envFirst returns the value of the first non-empty env var.
func envFirst(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// buildSCIMHandler returns a SCIM handler when at least one
// bearer token is configured via PODIUM_SCIM_TOKENS (comma-
// separated). Returns nil otherwise; the registry then runs
// without an IdP push interface and the visibility evaluator
// matches groups via JWT claims only.
func buildSCIMHandler(store *scim.Memory) *scim.Handler {
	raw := os.Getenv("PODIUM_SCIM_TOKENS")
	if raw == "" {
		return nil
	}
	tokens := map[string]bool{}
	for _, t := range strings.Split(raw, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			tokens[t] = true
		}
	}
	if len(tokens) == 0 {
		return nil
	}
	return &scim.Handler{Store: store, Tokens: tokens}
}

// openNotifier returns the §9 notification provider per
// PODIUM_NOTIFICATION_PROVIDER. Returns nil when unset or when the
// provider name resolves to "noop".
func openNotifier() notification.Provider {
	switch os.Getenv("PODIUM_NOTIFICATION_PROVIDER") {
	case "", "noop":
		return nil
	case "log":
		return notification.LogProvider{}
	case "webhook":
		url := os.Getenv("PODIUM_NOTIFICATION_WEBHOOK_URL")
		if url == "" {
			log.Printf("warning: PODIUM_NOTIFICATION_WEBHOOK_URL is required for webhook notifier")
			return nil
		}
		return notification.Webhook{
			URL:    url,
			Secret: os.Getenv("PODIUM_NOTIFICATION_WEBHOOK_SECRET"),
		}
	case "multi":
		// "multi" combines the log provider with the webhook provider
		// when the URL is set; useful for "alert + record" deployments.
		out := []notification.Provider{notification.LogProvider{}}
		if url := os.Getenv("PODIUM_NOTIFICATION_WEBHOOK_URL"); url != "" {
			out = append(out, notification.Webhook{
				URL:    url,
				Secret: os.Getenv("PODIUM_NOTIFICATION_WEBHOOK_SECRET"),
			})
		}
		return notification.MultiProvider{Providers: out}
	}
	log.Printf("warning: unknown PODIUM_NOTIFICATION_PROVIDER=%q",
		os.Getenv("PODIUM_NOTIFICATION_PROVIDER"))
	return nil
}

// adaptNotifier turns a notification.Provider into the
// core.NotificationFunc shape, swallowing errors (the registry
// keeps running on outage; the audit log records what happened).
func adaptNotifier(p notification.Provider) core.NotificationFunc {
	return func(ctx context.Context, severity, title, body string, tags map[string]string) {
		err := p.Notify(ctx, notification.Notification{
			Severity: notification.Severity(severity),
			Title:    title,
			Body:     body,
			Tags:     tags,
			Time:     time.Now().UTC(),
		})
		if err != nil {
			log.Printf("notification (%s): %v", p.ID(), err)
		}
	}
}

// envInt returns the integer value of an env var, or def when the
// var is unset or invalid.
func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return def
	}
	return n
}

// Run loads configuration, opens the configured backends, mounts
// every endpoint, and blocks on the HTTP listener. Returns the
// http.Server's error (always non-nil — at minimum
// http.ErrServerClosed when the listener exits cleanly).
func Run() error {
	cfg := LoadConfig()
	if err := cfg.validate(); err != nil {
		return err
	}

	st, err := openStore(cfg)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}

	// Standalone bootstrap: ensure the default tenant exists so
	// initial requests don't fail on missing-tenant lookups (§13.10
	// auto-bootstrap).
	const tenantID = "default"
	_ = st.CreateTenant(context.Background(), store.Tenant{ID: tenantID, Name: tenantID})

	registry := core.New(st, tenantID, []layer.Layer{})
	if v, e, err := openVectorAndEmbedder(cfg); err != nil {
		log.Printf("warning: vector search disabled: %v", err)
	} else if v != nil && e != nil {
		registry = registry.WithVectorSearch(v, e)
		log.Printf("hybrid search: vector=%s embedder=%s dim=%d", v.ID(), e.ID(), e.Dimensions())
	}

	// §9 NotificationProvider: chosen via PODIUM_NOTIFICATION_PROVIDER
	// (one of "noop", "log", "webhook", or "multi"). Wraps the
	// notifier in core.NotificationFunc so the registry can fire
	// operational notifications without depending on this package.
	if np := openNotifier(); np != nil {
		registry = registry.WithNotifier(adaptNotifier(np))
		log.Printf("notification provider: %s", np.ID())
	}

	// §6.3.1 SCIM 2.0: when at least one bearer token is configured,
	// the SCIM IdP receiver is mounted at /scim/v2/. The same
	// in-memory store also feeds the §4.6 visibility evaluator's
	// `groups:` expander so layer filters resolve against
	// IdP-pushed group membership.
	scimStore := scim.NewMemory()
	scimHandler := buildSCIMHandler(scimStore)
	if scimHandler != nil {
		registry = registry.WithGroupResolver(func(g string) []string {
			members, err := scimStore.MembersOf(context.Background(), g)
			if err != nil {
				return nil
			}
			return members
		})
	}

	mode := server.NewModeTracker()
	// §7.3.2 outbound webhook worker: in-memory receiver store
	// fans change events out to subscribers. Receivers do not
	// survive a server restart in this configuration; persistent
	// storage is on the configuration roadmap.
	webhookStore := webhook.NewMemoryStore()
	webhookWorker := &webhook.Worker{Store: webhookStore}

	bootOpts := bootstrapOptions(cfg)
	bootOpts = append(bootOpts, server.WithWebhooks(webhookWorker))
	if scimHandler != nil {
		bootOpts = append(bootOpts, server.WithSCIM(scimHandler))
		log.Printf("SCIM 2.0 receiver mounted at /scim/v2/")
	}
	srv := server.New(registry, bootOpts...)

	// §7.3.1 layer-management endpoint: mounted alongside the meta-
	// tools so admin operators can register/list/unregister layers
	// over HTTP. The endpoint shares the ModeTracker with the
	// read-only probe so config writes refuse during outage.
	layers := server.NewLayerEndpoint(st, tenantID, mode).
		WithDefaultVisibility(cfg.defaultLayerVisibility)

	// §6.3.2 runtime trust keys: an in-memory registry that accepts
	// PEM-encoded public keys via POST /v1/admin/runtime. Keys live
	// for the lifetime of the process; persistence is on the
	// configuration roadmap.
	runtimeKeys := identity.NewRuntimeKeyRegistry()
	runtimeEndpoint := server.NewRuntimeKeyEndpoint(runtimeKeys, mode)

	mux := http.NewServeMux()
	mux.Handle("/v1/layers", layers.Handler())
	mux.Handle("/v1/layers/", layers.Handler())
	mux.Handle("/v1/admin/runtime", runtimeEndpoint.Handler())
	if isTrue(os.Getenv("PODIUM_WEB_UI")) {
		mux.Handle("/ui/", http.StripPrefix("/ui/", http.FileServer(http.FS(web.Assets()))))
		log.Printf("web UI mounted at /ui/")
	}
	mux.Handle("/", srv.Handler())

	// §8.3 audit sink: file-backed, hash-chained, shared by the
	// anchor scheduler, the retention scheduler, and the read-only
	// probe transition events. Nil when the path can't be resolved
	// (probes still log; downstream features that need the sink
	// gracefully no-op).
	auditSink := openAuditSink(cfg)

	// §8.6 transparency anchoring: when the operator enables
	// PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS, a goroutine periodically
	// anchors new entries via the registry-managed signing key.
	// Operators monitor audit.anchored / audit.anchor_failed events.
	if cfg.auditAnchorInterval > 0 {
		startAnchorScheduler(cfg, auditSink)
	}

	// §8.5 retention enforcement: when
	// PODIUM_AUDIT_RETENTION_INTERVAL_SECONDS > 0, a goroutine
	// truncates the audit log on a cadence using the configured
	// retention policies (defaulting to the §8.5 standard set).
	if cfg.auditRetentionInterval > 0 {
		startRetentionScheduler(cfg, auditSink)
	}

	// §13.2.1 read-only probe: ping the metadata store on a tick
	// and flip the shared mode tracker after Failures consecutive
	// errors. Disabled when failures threshold is 0. Mode
	// transitions write registry.read_only_entered /
	// registry.read_only_exited events to the audit sink.
	if cfg.readOnlyProbeFailures > 0 {
		auditEnter := readOnlyEnterCallback(auditSink, tenantID, "store_probe_failed")
		auditExit := readOnlyExitCallback(auditSink, tenantID)
		probe := &server.ReadOnlyProbe{
			Store:    st,
			Tracker:  mode,
			TenantID: tenantID,
			Interval: time.Duration(cfg.readOnlyProbeInterval) * time.Second,
			Failures: cfg.readOnlyProbeFailures,
			OnEnter: func() {
				log.Printf("registry entered read_only mode after %d probe failures", cfg.readOnlyProbeFailures)
				auditEnter()
			},
			OnExit: func() {
				log.Printf("registry exited read_only mode")
				auditExit()
			},
		}
		go func() {
			if err := probe.Run(context.Background()); err != nil && err != context.Canceled {
				log.Printf("read-only probe stopped: %v", err)
			}
		}()
	}

	httpServer := &http.Server{
		Addr:              cfg.bind,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("podium-server listening on %s (mode=%s)", cfg.bind, cfg.modeBanner())
	return httpServer.ListenAndServe()
}

type Config struct {
	bind             string
	publicMode       bool
	identityProvider string
	storeType        string
	sqlitePath       string
	postgresDSN      string
	objectStore      string
	filesystemRoot   string
	publicURL        string
	presignTTL       time.Duration
	s3Endpoint       string
	s3Region         string
	s3Bucket         string
	s3AccessKey      string
	s3SecretKey      string
	s3UseSSL         bool
	// Vector + embedding (§4.7).
	vectorBackend    string
	embeddingProvider string
	embeddingModel   string
	openaiAPIKey     string
	voyageAPIKey     string
	cohereAPIKey     string
	ollamaURL        string
	pgvectorDSN      string
	pineconeKey      string
	pineconeHost     string
	pineconeNS       string
	weaviateURL      string
	weaviateKey      string
	weaviateColl     string
	qdrantURL        string
	qdrantKey        string
	qdrantColl       string
	// §4.6 default visibility for newly-registered layers when no
	// explicit visibility is supplied. One of "public" |
	// "organization" | "private". Defaults to "private" so
	// admin-defined layers don't leak by accident.
	defaultLayerVisibility string
	// §13.2.1 read-only mode probe.
	readOnlyProbeFailures int
	readOnlyProbeInterval int
	// §8.6 transparency anchoring.
	auditLogPath        string
	auditSigningKeyPath string
	auditAnchorInterval int
	// §8.5 retention enforcement.
	auditRetentionInterval int
	auditRetentionMaxAgeDays int
}

// Setting names one resolved field together with the env var (or
// fallback) it came from. `podium config show` consumes this.
type Setting struct {
	Name   string
	Value  string
	Source string
}

// Settings returns a deterministic view of the resolved
// configuration with the source of each value (env var, yaml
// file, or hardcoded default). Secrets are redacted.
func (c *Config) Settings() []Setting {
	const yamlSrc = "registry.yaml"
	const defaultSrc = "default"
	envOrSrc := func(env, src string) string {
		if os.Getenv(env) != "" {
			return env
		}
		return src
	}
	redact := func(s string) string {
		if s == "" {
			return ""
		}
		return "<redacted>"
	}
	return []Setting{
		{"bind", c.bind, envOrSrc("PODIUM_BIND", defaultSrc)},
		{"public_mode", boolStr(c.publicMode), envOrSrc("PODIUM_PUBLIC_MODE", defaultSrc)},
		{"identity_provider", c.identityProvider, envOrSrc("PODIUM_IDENTITY_PROVIDER", yamlSrc)},
		{"store.type", c.storeType, envOrSrc("PODIUM_REGISTRY_STORE", defaultSrc)},
		{"store.sqlite_path", c.sqlitePath, envOrSrc("PODIUM_SQLITE_PATH", defaultSrc)},
		{"store.postgres_dsn", redact(c.postgresDSN), envOrSrc("PODIUM_POSTGRES_DSN", yamlSrc)},
		{"object_store.type", c.objectStore, envOrSrc("PODIUM_OBJECT_STORE", defaultSrc)},
		{"object_store.filesystem_root", c.filesystemRoot, envOrSrc("PODIUM_FILESYSTEM_ROOT", defaultSrc)},
		{"object_store.s3_endpoint", c.s3Endpoint, envOrSrc("PODIUM_S3_ENDPOINT", yamlSrc)},
		{"object_store.s3_bucket", c.s3Bucket, envOrSrc("PODIUM_S3_BUCKET", yamlSrc)},
		{"object_store.s3_region", c.s3Region, envOrSrc("PODIUM_S3_REGION", defaultSrc)},
		{"vector_backend", c.vectorBackend, envOrSrc("PODIUM_VECTOR_BACKEND", yamlSrc)},
		{"embedding_provider", c.embeddingProvider, envOrSrc("PODIUM_EMBEDDING_PROVIDER", yamlSrc)},
		{"embedding_model", c.embeddingModel, envOrSrc("PODIUM_EMBEDDING_MODEL", yamlSrc)},
		{"layers.default_visibility", c.defaultLayerVisibility, envOrSrc("PODIUM_DEFAULT_LAYER_VISIBILITY", defaultSrc)},
		{"read_only.probe_failures", intStr(c.readOnlyProbeFailures), envOrSrc("PODIUM_READONLY_PROBE_FAILURES", defaultSrc)},
		{"read_only.probe_interval_seconds", intStr(c.readOnlyProbeInterval), envOrSrc("PODIUM_READONLY_PROBE_INTERVAL", defaultSrc)},
		{"openai_api_key", redact(c.openaiAPIKey), envOrSrc("OPENAI_API_KEY", "")},
		{"voyage_api_key", redact(c.voyageAPIKey), envOrSrc("VOYAGE_API_KEY", "")},
		{"cohere_api_key", redact(c.cohereAPIKey), envOrSrc("COHERE_API_KEY", "")},
		{"ollama_url", c.ollamaURL, envOrSrc("PODIUM_OLLAMA_URL", defaultSrc)},
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func intStr(n int) string {
	return strconv.Itoa(n)
}

func LoadConfig() *Config {
	c := &Config{
		bind:             envDefault("PODIUM_BIND", "127.0.0.1:8080"),
		publicMode:       isTrue(os.Getenv("PODIUM_PUBLIC_MODE")),
		identityProvider: os.Getenv("PODIUM_IDENTITY_PROVIDER"),
		storeType:        envDefault("PODIUM_REGISTRY_STORE", "sqlite"),
		sqlitePath:       os.Getenv("PODIUM_SQLITE_PATH"),
		postgresDSN:      os.Getenv("PODIUM_POSTGRES_DSN"),
		objectStore:      envDefault("PODIUM_OBJECT_STORE", "filesystem"),
		filesystemRoot:   os.Getenv("PODIUM_FILESYSTEM_ROOT"),
		publicURL:        os.Getenv("PODIUM_PUBLIC_URL"),
		s3Endpoint:       os.Getenv("PODIUM_S3_ENDPOINT"),
		s3Region:         envDefault("PODIUM_S3_REGION", "us-east-1"),
		s3Bucket:         os.Getenv("PODIUM_S3_BUCKET"),
		s3AccessKey:      os.Getenv("PODIUM_S3_ACCESS_KEY_ID"),
		s3SecretKey:      os.Getenv("PODIUM_S3_SECRET_ACCESS_KEY"),
		s3UseSSL:         os.Getenv("PODIUM_S3_USE_SSL") != "false",
		// §4.7 vector + embedding.
		vectorBackend:     os.Getenv("PODIUM_VECTOR_BACKEND"),
		embeddingProvider: os.Getenv("PODIUM_EMBEDDING_PROVIDER"),
		embeddingModel:    os.Getenv("PODIUM_EMBEDDING_MODEL"),
		openaiAPIKey:      os.Getenv("OPENAI_API_KEY"),
		voyageAPIKey:      os.Getenv("VOYAGE_API_KEY"),
		cohereAPIKey:      os.Getenv("COHERE_API_KEY"),
		ollamaURL:         envDefault("PODIUM_OLLAMA_URL", "http://localhost:11434"),
		pgvectorDSN:       envFirst("PODIUM_PGVECTOR_DSN", "PODIUM_POSTGRES_DSN"),
		pineconeKey:       os.Getenv("PODIUM_PINECONE_API_KEY"),
		pineconeHost:      os.Getenv("PODIUM_PINECONE_HOST"),
		pineconeNS:        os.Getenv("PODIUM_PINECONE_NAMESPACE"),
		weaviateURL:       os.Getenv("PODIUM_WEAVIATE_URL"),
		weaviateKey:       os.Getenv("PODIUM_WEAVIATE_API_KEY"),
		weaviateColl:      envDefault("PODIUM_WEAVIATE_COLLECTION", "PodiumArtifacts"),
		qdrantURL:         os.Getenv("PODIUM_QDRANT_URL"),
		qdrantKey:         os.Getenv("PODIUM_QDRANT_API_KEY"),
		qdrantColl:        envDefault("PODIUM_QDRANT_COLLECTION", "podium_artifacts"),
		// §4.6 + §13.2.1.
		defaultLayerVisibility: envDefault("PODIUM_DEFAULT_LAYER_VISIBILITY", "private"),
		readOnlyProbeFailures:  envInt("PODIUM_READONLY_PROBE_FAILURES", 0),
		readOnlyProbeInterval:  envInt("PODIUM_READONLY_PROBE_INTERVAL", 30),
		// §8.6 audit anchoring.
		auditLogPath:        os.Getenv("PODIUM_AUDIT_LOG_PATH"),
		auditSigningKeyPath: os.Getenv("PODIUM_AUDIT_SIGNING_KEY_PATH"),
		auditAnchorInterval: envInt("PODIUM_AUDIT_ANCHOR_INTERVAL_SECONDS", 0),
		// §8.5 retention enforcement.
		auditRetentionInterval:   envInt("PODIUM_AUDIT_RETENTION_INTERVAL_SECONDS", 0),
		auditRetentionMaxAgeDays: envInt("PODIUM_AUDIT_RETENTION_MAX_AGE_DAYS", 365),
	}
	// §13.10 ~/.podium/registry.yaml: load and overlay onto env-
	// derived defaults. Env values keep precedence per applyYAML.
	if y, err := readYAMLConfig(); err != nil {
		log.Printf("warning: ignored registry.yaml: %v", err)
	} else {
		applyYAML(c, y)
	}
	if c.sqlitePath == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			c.sqlitePath = filepath.Join(home, ".podium", "standalone", "podium.db")
		}
	}
	if c.filesystemRoot == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			c.filesystemRoot = filepath.Join(home, ".podium", "standalone", "objects")
		}
	}
	if v := os.Getenv("PODIUM_PRESIGN_TTL_SECONDS"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			c.presignTTL = time.Duration(secs) * time.Second
		}
	}
	if c.presignTTL <= 0 {
		c.presignTTL = objectstore.DefaultPresignTTL
	}
	if c.publicURL == "" {
		c.publicURL = "http://" + c.bind
	}
	return c
}

func (c *Config) validate() error {
	startup := server.StartupConfig{
		PublicMode:       c.publicMode,
		IdentityProvider: c.identityProvider,
	}
	if err := startup.Validate(); err != nil {
		return err
	}
	if c.storeType == "postgres" && c.postgresDSN == "" {
		return fmt.Errorf("PODIUM_POSTGRES_DSN is required when PODIUM_REGISTRY_STORE=postgres")
	}
	return nil
}

func (c *Config) modeBanner() string {
	if c.publicMode {
		return "public"
	}
	if c.identityProvider != "" {
		return c.identityProvider
	}
	return "standalone"
}

func openStore(c *Config) (store.Store, error) {
	switch c.storeType {
	case "sqlite":
		dir := filepath.Dir(c.sqlitePath)
		_ = os.MkdirAll(dir, 0o755)
		return store.OpenSQLite(c.sqlitePath)
	case "memory":
		return store.NewMemory(), nil
	case "postgres":
		return store.OpenPostgres(c.postgresDSN)
	}
	return nil, fmt.Errorf("unknown PODIUM_REGISTRY_STORE: %s", c.storeType)
}

func bootstrapOptions(c *Config) []server.Option {
	out := []server.Option{}
	if c.publicMode {
		out = append(out, server.WithPublicMode())
	}
	if store, err := openObjectStore(c); err != nil {
		log.Printf("warning: object store disabled: %v", err)
	} else if store != nil {
		out = append(out, server.WithObjectStore(store, c.publicURL, c.presignTTL))
	}
	return out
}

// openVectorAndEmbedder returns the configured §4.7 hybrid-search
// pieces. Returns (nil, nil, nil) when vector search is disabled
// (operator left PODIUM_VECTOR_BACKEND unset / set to "none").
func openVectorAndEmbedder(c *Config) (vector.Provider, embedding.Provider, error) {
	emb, err := openEmbedder(c)
	if err != nil {
		return nil, nil, err
	}
	if emb == nil {
		return nil, nil, nil
	}
	v, err := openVectorBackend(c, emb.Dimensions())
	if err != nil {
		return nil, nil, err
	}
	return v, emb, nil
}

// openEmbedder honors §13 per-provider model env vars
// (PODIUM_OPENAI_MODEL, PODIUM_VOYAGE_MODEL, PODIUM_COHERE_MODEL,
// PODIUM_OLLAMA_MODEL). The generic PODIUM_EMBEDDING_MODEL acts as
// a cross-provider fallback when the per-provider variable isn't
// set.
func openEmbedder(c *Config) (embedding.Provider, error) {
	switch c.embeddingProvider {
	case "", "none":
		return nil, nil
	case "openai":
		if c.openaiAPIKey == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY required for openai embedder")
		}
		return embedding.OpenAI{
			APIKey:  c.openaiAPIKey,
			Model_:  envFirst("PODIUM_OPENAI_MODEL", "PODIUM_EMBEDDING_MODEL"),
			BaseURL: os.Getenv("PODIUM_OPENAI_BASE_URL"),
			Org:     os.Getenv("PODIUM_OPENAI_ORG"),
		}, nil
	case "voyage":
		if c.voyageAPIKey == "" {
			return nil, fmt.Errorf("VOYAGE_API_KEY required for voyage embedder")
		}
		return embedding.Voyage{
			APIKey: c.voyageAPIKey,
			Model_: envFirst("PODIUM_VOYAGE_MODEL", "PODIUM_EMBEDDING_MODEL"),
		}, nil
	case "cohere":
		if c.cohereAPIKey == "" {
			return nil, fmt.Errorf("COHERE_API_KEY required for cohere embedder")
		}
		return embedding.Cohere{
			APIKey: c.cohereAPIKey,
			Model_: envFirst("PODIUM_COHERE_MODEL", "PODIUM_EMBEDDING_MODEL"),
		}, nil
	case "ollama":
		return embedding.Ollama{
			BaseURL: c.ollamaURL,
			Model_:  envFirst("PODIUM_OLLAMA_MODEL", "PODIUM_EMBEDDING_MODEL"),
		}, nil
	}
	return nil, fmt.Errorf("unknown PODIUM_EMBEDDING_PROVIDER: %s", c.embeddingProvider)
}

func openVectorBackend(c *Config, dim int) (vector.Provider, error) {
	switch c.vectorBackend {
	case "", "none":
		return nil, nil
	case "memory":
		return vector.NewMemory(dim), nil
	case "pgvector":
		if c.pgvectorDSN == "" {
			return nil, fmt.Errorf("PODIUM_PGVECTOR_DSN or PODIUM_POSTGRES_DSN required for pgvector")
		}
		return vector.OpenPgVector(vector.PgVectorConfig{DSN: c.pgvectorDSN, Dimensions: dim})
	case "sqlite-vec":
		path := c.sqlitePath
		if path == "" {
			path = ":memory:"
		}
		return vector.OpenSQLiteVec(vector.SQLiteVecConfig{Path: path, Dimensions: dim})
	case "pinecone":
		host := c.pineconeHost
		if host == "" {
			// §13: PODIUM_PINECONE_INDEX is auto-resolved to a host
			// for serverless. Ship a clear error pointing at Host
			// for now; an SDK call to the Pinecone control plane
			// would resolve it but adds dep weight.
			if idx := os.Getenv("PODIUM_PINECONE_INDEX"); idx != "" {
				return nil, fmt.Errorf(
					"PODIUM_PINECONE_INDEX=%q set but PODIUM_PINECONE_HOST is required for serverless; supply the index host URL", idx)
			}
		}
		return vector.NewPinecone(vector.PineconeConfig{
			APIKey: c.pineconeKey, Host: host,
			Namespace: c.pineconeNS, Dimensions: dim,
		})
	case "weaviate-cloud":
		return vector.NewWeaviate(vector.WeaviateConfig{
			URL: c.weaviateURL, APIKey: c.weaviateKey,
			Collection: c.weaviateColl, Dimensions: dim,
		})
	case "qdrant-cloud":
		return vector.NewQdrant(vector.QdrantConfig{
			URL: c.qdrantURL, APIKey: c.qdrantKey,
			Collection: c.qdrantColl, Dimensions: dim,
		})
	}
	return nil, fmt.Errorf("unknown PODIUM_VECTOR_BACKEND: %s", c.vectorBackend)
}

// openObjectStore returns the configured §13.10 object-storage
// backend, or (nil, nil) when the standalone deployment runs without
// one (resources stay inline regardless of size).
func openObjectStore(c *Config) (objectstore.Provider, error) {
	switch c.objectStore {
	case "", "filesystem":
		_ = os.MkdirAll(c.filesystemRoot, 0o755)
		return objectstore.Open(c.filesystemRoot)
	case "s3":
		if c.s3Bucket == "" {
			return nil, fmt.Errorf("PODIUM_S3_BUCKET is required when PODIUM_OBJECT_STORE=s3")
		}
		if c.s3Endpoint == "" {
			c.s3Endpoint = "s3.amazonaws.com"
		}
		return objectstore.NewS3(objectstore.S3Config{
			Endpoint:        c.s3Endpoint,
			Bucket:          c.s3Bucket,
			Region:          c.s3Region,
			AccessKeyID:     c.s3AccessKey,
			SecretAccessKey: c.s3SecretKey,
			UseSSL:          c.s3UseSSL,
		})
	case "none":
		return nil, nil
	}
	return nil, fmt.Errorf("unknown PODIUM_OBJECT_STORE: %s", c.objectStore)
}

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func isTrue(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}
