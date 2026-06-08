package serverboot

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/lennylabs/podium/pkg/audit"
	"github.com/lennylabs/podium/pkg/registry/ingest"
)

// fileConfig is the top-level registry.yaml document. §13.12 nests every
// server-side key under a single `registry:` mapping, so the parser reads
// into this wrapper and hands callers the unwrapped Registry block.
// spec: §13.12 (config file format example).
type fileConfig struct {
	Registry yamlConfig `yaml:"registry"`
}

// yamlConfig mirrors the §13.12 `registry:` block. Each field is optional;
// loadConfig overlays a non-empty value on top of the env-derived default so
// env-var precedence remains (env beats yaml beats hardcoded default).
type yamlConfig struct {
	// Endpoint is the §13.12 `endpoint:` public-facing registry URL; it maps
	// to PODIUM_PUBLIC_URL (the presigned-URL / advertised base).
	Endpoint    string          `yaml:"endpoint,omitempty"`
	Bind        string          `yaml:"bind,omitempty"`
	PublicMode  *bool           `yaml:"public_mode,omitempty"`
	Identity    yamlIdentityCfg `yaml:"identity_provider,omitempty"`
	Store       yamlStoreCfg    `yaml:"store,omitempty"`
	ObjectStore yamlObjectCfg   `yaml:"object_store,omitempty"`
	Vector      yamlVectorCfg   `yaml:"vector_backend,omitempty"`
	Embedding   yamlEmbedCfg    `yaml:"embedding_provider,omitempty"`
	Discovery   yamlDiscovery   `yaml:"discovery,omitempty"`
	// Layers is the §4.6 per-tenant admin-defined layer list. Each entry
	// declares an id, a single source (git or local), and a visibility
	// block. Seeded into store.LayerConfig rows at startup.
	Layers []yamlLayerEntry `yaml:"layers,omitempty"`
	// LayerPath is the §13.10 standalone bootstrap path (a single
	// filesystem-registry root). Mirrors the PODIUM_LAYER_PATH env var and
	// the --layer-path flag; env wins per the standard precedence.
	LayerPath string `yaml:"layer_path,omitempty"`
	// DefaultLayerVisibility is the §4.6 fallback visibility for a layer
	// registered or declared without an explicit visibility block. Mirrors
	// PODIUM_DEFAULT_LAYER_VISIBILITY.
	DefaultLayerVisibility string       `yaml:"default_layer_visibility,omitempty"`
	ReadOnly               yamlReadOnly `yaml:"read_only,omitempty"`
	// Tenant is the §3.5 per-tenant config block (currently the
	// scope-preview gate). Config-file-only with an env override.
	Tenant yamlTenant `yaml:"tenant,omitempty"`
	// PIIRedaction is the §8.2 query-text scrub config (enable toggle and
	// custom patterns). Default-on when absent; PODIUM_PII_REDACTION wins.
	PIIRedaction audit.PIIRedactionConfig `yaml:"pii_redaction,omitempty"`
	// FreezeWindows is the §4.7.2 org-level freeze list. Each window blocks
	// the named operations for [start, end); ingest in an active window is
	// rejected as ingest.frozen unless the manual reingest path passes
	// break-glass. Config-file-only.
	FreezeWindows []yamlFreezeWindow `yaml:"freeze_windows,omitempty"`
}

// yamlFreezeWindow is one §4.7.2 freeze window in the `freeze_windows:` list.
type yamlFreezeWindow struct {
	Name           string    `yaml:"name"`
	Start          time.Time `yaml:"start"`
	End            time.Time `yaml:"end"`
	Blocks         []string  `yaml:"blocks,omitempty"`
	BreakGlassRole string    `yaml:"break_glass_role,omitempty"`
}

// yamlTenant mirrors the §3.5 `tenant:` block. ExposeScopePreview is
// tri-state (nil = default true) so an explicit false survives.
type yamlTenant struct {
	ExposeScopePreview *bool `yaml:"expose_scope_preview,omitempty"`
}

// yamlIdentityCfg mirrors the §13.12 `identity_provider:` mapping. The spec
// example models it as an object with type / audience / authorization_endpoint
// , so a scalar would no longer unmarshal here.
type yamlIdentityCfg struct {
	Type                  string `yaml:"type,omitempty"`
	Audience              string `yaml:"audience,omitempty"`
	AuthorizationEndpoint string `yaml:"authorization_endpoint,omitempty"`
	// §6.3.3 / §13.12 oidc-jwt keys.
	Issuer              string `yaml:"issuer,omitempty"`
	TokenHeader         string `yaml:"token_header,omitempty"`
	JWKSCacheTTLSeconds int    `yaml:"jwks_cache_ttl_seconds,omitempty"`
}

// yamlStoreCfg mirrors the §13.12 `store:` block. The DSN key is `dsn`
// (section-relative, per the config example) rather than `postgres_dsn`.
type yamlStoreCfg struct {
	Type       string `yaml:"type,omitempty"`
	SQLitePath string `yaml:"sqlite_path,omitempty"`
	DSN        string `yaml:"dsn,omitempty"`
}

// yamlObjectCfg mirrors the §13.12 `object_store:` block. The keys are
// section-relative (`bucket`, `region`, `endpoint`) per the config example
// rather than the `s3_`-prefixed env-var forms.
type yamlObjectCfg struct {
	Type           string `yaml:"type,omitempty"`
	FilesystemRoot string `yaml:"filesystem_root,omitempty"`
	Endpoint       string `yaml:"endpoint,omitempty"`
	Bucket         string `yaml:"bucket,omitempty"`
	Region         string `yaml:"region,omitempty"`
	// ForcePathStyle mirrors §13.12 PODIUM_S3_FORCE_PATH_STYLE as a
	// config-file key (snake-cased under the section per spec line 343).
	ForcePathStyle bool `yaml:"force_path_style,omitempty"`
	// AccessKeyID / SecretAccessKey mirror §13.12 PODIUM_S3_ACCESS_KEY_ID /
	// PODIUM_S3_SECRET_ACCESS_KEY: static S3 credentials so an
	// operator can configure them entirely in registry.yaml (typically via
	// ${ENV_VAR} interpolation) rather than only as env vars.
	AccessKeyID     string `yaml:"access_key_id,omitempty"`
	SecretAccessKey string `yaml:"secret_access_key,omitempty"`
	// PresignTTLSeconds mirrors §13.12 PODIUM_PRESIGN_TTL_SECONDS.
	PresignTTLSeconds int `yaml:"presign_ttl_seconds,omitempty"`
}

// yamlVectorCfg mirrors the §13.12 `vector_backend:` block. Beyond `type`,
// the config example carries the per-backend sub-keys api_key / index /
// namespace / inference_model; applyYAML routes them to the
// selected backend's config fields.
type yamlVectorCfg struct {
	Type           string `yaml:"type,omitempty"`
	APIKey         string `yaml:"api_key,omitempty"`
	Index          string `yaml:"index,omitempty"`
	Namespace      string `yaml:"namespace,omitempty"`
	InferenceModel string `yaml:"inference_model,omitempty"`
	// Collection is the §13.12 weaviate-cloud / qdrant-cloud collection name,
	// required for those backends. Routed by applyVectorYAML.
	Collection string `yaml:"collection,omitempty"`
	// Host mirrors §13.12 PODIUM_PINECONE_HOST and URL mirrors
	// PODIUM_WEAVIATE_URL / PODIUM_QDRANT_URL, so a managed backend
	// that marks the host/URL required can be configured entirely in
	// registry.yaml. applyVectorYAML routes them to the selected backend.
	Host string `yaml:"host,omitempty"`
	URL  string `yaml:"url,omitempty"`
}

// yamlEmbedCfg mirrors the §13.12 `embedding_provider:` block. The selector
// key is `type` (matching the config example) rather than `provider`, and the
// block carries an `api_key`.
type yamlEmbedCfg struct {
	Type   string `yaml:"type,omitempty"`
	APIKey string `yaml:"api_key,omitempty"`
	Model  string `yaml:"model,omitempty"`
	// BaseURL mirrors §13.12 PODIUM_OPENAI_BASE_URL, Org mirrors
	// PODIUM_OPENAI_ORG, and URL mirrors PODIUM_OLLAMA_URL.
	// applyEmbeddingYAML routes them to the selected provider.
	BaseURL string `yaml:"base_url,omitempty"`
	Org     string `yaml:"org,omitempty"`
	URL     string `yaml:"url,omitempty"`
}

// yamlDiscovery mirrors the §13.12 tenant-scope `discovery:` block. The
// pointer fields distinguish "unset" (leave the package default) from an
// explicit value, including the explicit false the gates need.
type yamlDiscovery struct {
	MaxDepth                int   `yaml:"max_depth,omitempty"`
	NotableCount            int   `yaml:"notable_count,omitempty"`
	FoldBelowArtifacts      int   `yaml:"fold_below_artifacts,omitempty"`
	FoldPassthroughChains   *bool `yaml:"fold_passthrough_chains,omitempty"`
	TargetResponseTokens    int   `yaml:"target_response_tokens,omitempty"`
	AllowPerDomainOverrides *bool `yaml:"allow_per_domain_overrides,omitempty"`
}

// yamlLayerEntry is one admin-defined layer in the §4.6 `layers:` list.
type yamlLayerEntry struct {
	ID         string              `yaml:"id"`
	Source     yamlLayerSource     `yaml:"source"`
	Visibility yamlLayerVisibility `yaml:"visibility,omitempty"`
}

// yamlLayerSource is the §4.6 `source:` block. Exactly one of git or
// local is expected; bootstrap rejects an entry that sets neither or both.
type yamlLayerSource struct {
	Git   *yamlGitSource   `yaml:"git,omitempty"`
	Local *yamlLocalSource `yaml:"local,omitempty"`
}

type yamlGitSource struct {
	Repo string `yaml:"repo"`
	Ref  string `yaml:"ref,omitempty"`
	Root string `yaml:"root,omitempty"`
	// ForcePushPolicy is the §7.3.1 per-layer force-push handling:
	// "" / "tolerant" preserve prior commits and emit
	// layer.history_rewritten; "strict" rejects a rewritten history.
	ForcePushPolicy string `yaml:"force_push_policy,omitempty"`
}

type yamlLocalSource struct {
	Path string `yaml:"path"`
}

// yamlLayerVisibility is the §4.6 `visibility:` block.
type yamlLayerVisibility struct {
	Public       bool     `yaml:"public,omitempty"`
	Organization bool     `yaml:"organization,omitempty"`
	Groups       []string `yaml:"groups,omitempty"`
	Users        []string `yaml:"users,omitempty"`
}

type yamlReadOnly struct {
	ProbeFailures int `yaml:"probe_failures,omitempty"`
	ProbeInterval int `yaml:"probe_interval_seconds,omitempty"`
}

// envInterpolationRE matches the §13.12 `${ENV_VAR}` interpolation form. Only
// the brace form is recognized so a bare `$` in a DSN or password survives.
var envInterpolationRE = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expandEnvVars resolves `${ENV_VAR}` references in the raw config bytes
// before parsing, per §13.12 ("`${ENV_VAR}` interpolation supported" and
// "Secret values should use `${ENV_VAR}` interpolation"). An unset variable
// expands to the empty string, matching standard environment-substitution
// tools; the resulting empty required value is then named by validate
// (§13.12: "refuses to start when a backend is selected but its required
// values are missing"). spec: §13.12.
func expandEnvVars(data []byte) []byte {
	return envInterpolationRE.ReplaceAllFunc(data, func(match []byte) []byte {
		name := envInterpolationRE.FindSubmatch(match)[1]
		return []byte(os.Getenv(string(name)))
	})
}

// readYAMLConfig loads ~/.podium/registry.yaml (or PODIUM_CONFIG_FILE
// override), resolves §13.12 ${ENV_VAR} interpolation, and unwraps the
// top-level `registry:` block. Missing file returns (nil, nil).
func readYAMLConfig() (*yamlConfig, error) {
	path := os.Getenv("PODIUM_CONFIG_FILE")
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, nil
		}
		path = home + "/.podium/registry.yaml"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	// §13.12: resolve ${ENV_VAR} interpolation before parsing so a config
	// written with the documented secret-handling form connects with the
	// resolved value rather than the literal placeholder.
	data = expandEnvVars(data)
	var out fileConfig
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &out.Registry, nil
}

// applyYAML overlays values from the YAML config onto c. Env-set
// fields keep precedence; YAML fills in missing values only.
func applyYAML(c *Config, y *yamlConfig) {
	if y == nil {
		return
	}
	// §13.12 precedence (env beats config file): consult PODIUM_BIND directly
	// rather than comparing c.bind against the loopback default literal. An
	// operator who sets PODIUM_BIND=127.0.0.1:8080 explicitly (a value that
	// happens to equal the default) keeps it; the config-file bind only fills
	// an unset env var. The --bind flag also routes through
	// PODIUM_BIND (cmd/podium/serve.go), so the flag still wins over the file.
	if os.Getenv("PODIUM_BIND") == "" && y.Bind != "" {
		c.bind = y.Bind
	}
	// §13.12 endpoint: the advertised public URL (PODIUM_PUBLIC_URL). It is
	// defaulted to http://<bind> later in LoadConfig only when still empty.
	if c.publicURL == "" && y.Endpoint != "" && os.Getenv("PODIUM_PUBLIC_URL") == "" {
		c.publicURL = y.Endpoint
	}
	if y.PublicMode != nil && os.Getenv("PODIUM_PUBLIC_MODE") == "" {
		c.publicMode = *y.PublicMode
	}
	// §13.12 identity_provider object: type / audience / authorization_endpoint.
	if c.identityProvider == "" && y.Identity.Type != "" {
		c.identityProvider = y.Identity.Type
	}
	if c.oauthAudience == "" && y.Identity.Audience != "" {
		c.oauthAudience = y.Identity.Audience
	}
	if c.oauthAuthorizationEndpoint == "" && y.Identity.AuthorizationEndpoint != "" {
		c.oauthAuthorizationEndpoint = y.Identity.AuthorizationEndpoint
	}
	// §6.3.3 / §13.12 oidc-jwt keys (env wins; PODIUM_TRUSTED_PROXY_SECRET has no
	// config-file equivalent and is read from the environment only).
	if c.oauthIssuer == "" && y.Identity.Issuer != "" {
		c.oauthIssuer = y.Identity.Issuer
	}
	if c.oauthTokenHeader == "" && y.Identity.TokenHeader != "" {
		c.oauthTokenHeader = y.Identity.TokenHeader
	}
	if c.oauthJWKSCacheTTLSeconds == 0 && y.Identity.JWKSCacheTTLSeconds != 0 {
		c.oauthJWKSCacheTTLSeconds = y.Identity.JWKSCacheTTLSeconds
	}
	if c.storeType == "sqlite" && y.Store.Type != "" && os.Getenv("PODIUM_REGISTRY_STORE") == "" {
		c.storeType = y.Store.Type
	}
	if c.sqlitePath == "" && y.Store.SQLitePath != "" {
		c.sqlitePath = y.Store.SQLitePath
	}
	if c.postgresDSN == "" && y.Store.DSN != "" {
		c.postgresDSN = y.Store.DSN
	}
	if c.objectStore == "filesystem" && y.ObjectStore.Type != "" && os.Getenv("PODIUM_OBJECT_STORE") == "" {
		c.objectStore = y.ObjectStore.Type
	}
	if c.filesystemRoot == "" && y.ObjectStore.FilesystemRoot != "" {
		c.filesystemRoot = y.ObjectStore.FilesystemRoot
	}
	if c.s3Endpoint == "" && y.ObjectStore.Endpoint != "" {
		c.s3Endpoint = y.ObjectStore.Endpoint
	}
	if c.s3Bucket == "" && y.ObjectStore.Bucket != "" {
		c.s3Bucket = y.ObjectStore.Bucket
	}
	if y.ObjectStore.Region != "" && os.Getenv("PODIUM_S3_REGION") == "" {
		c.s3Region = y.ObjectStore.Region
	}
	if !c.s3ForcePathStyle && y.ObjectStore.ForcePathStyle && os.Getenv("PODIUM_S3_FORCE_PATH_STYLE") == "" {
		c.s3ForcePathStyle = true
	}
	// §13.12: static S3 credentials. The env-derived fields are
	// empty when PODIUM_S3_ACCESS_KEY_ID / PODIUM_S3_SECRET_ACCESS_KEY are
	// unset, so a non-empty target already encodes env precedence.
	if c.s3AccessKey == "" && y.ObjectStore.AccessKeyID != "" {
		c.s3AccessKey = y.ObjectStore.AccessKeyID
	}
	if c.s3SecretKey == "" && y.ObjectStore.SecretAccessKey != "" {
		c.s3SecretKey = y.ObjectStore.SecretAccessKey
	}
	// §13.12: presigned-URL TTL. LoadConfig reads the env var and
	// applies the package default after applyYAML, so set the duration here only
	// when the env var is unset; the later env read still overrides a file value.
	if os.Getenv("PODIUM_PRESIGN_TTL_SECONDS") == "" && y.ObjectStore.PresignTTLSeconds > 0 {
		c.presignTTL = time.Duration(y.ObjectStore.PresignTTLSeconds) * time.Second
	}
	if c.vectorBackend == "" && y.Vector.Type != "" {
		c.vectorBackend = y.Vector.Type
	}
	applyVectorYAML(c, y.Vector)
	if c.embeddingProvider == "" && y.Embedding.Type != "" {
		c.embeddingProvider = y.Embedding.Type
	}
	if c.embeddingModel == "" && y.Embedding.Model != "" {
		c.embeddingModel = y.Embedding.Model
	}
	applyEmbeddingYAML(c, y.Embedding)
	if c.defaultLayerVisibility == "" && y.DefaultLayerVisibility != "" {
		c.defaultLayerVisibility = y.DefaultLayerVisibility
	}
	if c.layerPath == "" && y.LayerPath != "" {
		c.layerPath = y.LayerPath
	}
	// §4.6 declarative layer list is config-file-only (no env equivalent),
	// so it overlays directly when present.
	if len(y.Layers) > 0 {
		c.declaredLayers = y.Layers
	}
	// §13.12 / §4.5.5 tenant discovery block is config-file-only, so it
	// overlays directly. AllowPerDomainOverrides is tri-state (nil =
	// default true); the others are zero-means-unset in resolveKnobs.
	c.discovery = y.Discovery
	c.allowPerDomainOverrides = y.Discovery.AllowPerDomainOverrides
	// §3.5 scope-preview gate: env (PODIUM_EXPOSE_SCOPE_PREVIEW) wins; the
	// yaml value fills in only when the env var left it unset.
	if c.exposeScopePreview == nil && y.Tenant.ExposeScopePreview != nil {
		c.exposeScopePreview = y.Tenant.ExposeScopePreview
	}
	// §8.2 pii_redaction: env PODIUM_PII_REDACTION wins for the enable
	// toggle; the yaml value fills it in only when the env left it unset.
	// Custom patterns are config-file-only, so they overlay directly.
	if c.piiRedaction.Enabled == nil && y.PIIRedaction.Enabled != nil {
		c.piiRedaction.Enabled = y.PIIRedaction.Enabled
	}
	if len(y.PIIRedaction.Patterns) > 0 {
		c.piiRedaction.Patterns = y.PIIRedaction.Patterns
	}
	// §13.2.1: env keeps precedence. A negative value is the "env unset"
	// sentinel (an explicit 0 from env disables the probe and is preserved),
	// so registry.yaml only fills in a still-unset threshold; the spec default
	// is applied later in LoadConfig.
	if c.readOnlyProbeFailures < 0 && y.ReadOnly.ProbeFailures > 0 {
		c.readOnlyProbeFailures = y.ReadOnly.ProbeFailures
	}
	if c.readOnlyProbeInterval <= 0 && y.ReadOnly.ProbeInterval > 0 {
		c.readOnlyProbeInterval = y.ReadOnly.ProbeInterval
	}
	// §4.7.2 freeze windows are config-file-only, so they overlay directly.
	if len(y.FreezeWindows) > 0 {
		c.freezeWindows = freezeWindowsFromYAML(y.FreezeWindows)
	}
}

// freezeWindowsFromYAML converts the parsed registry.yaml freeze list into the
// ingest pipeline's §4.7.2 FreezeWindow values. A window with no explicit
// `blocks` defaults to blocking ingest.
func freezeWindowsFromYAML(in []yamlFreezeWindow) []ingest.FreezeWindow {
	out := make([]ingest.FreezeWindow, 0, len(in))
	for _, w := range in {
		blocks := w.Blocks
		if len(blocks) == 0 {
			blocks = []string{"ingest"}
		}
		out = append(out, ingest.FreezeWindow{
			Name:   w.Name,
			Start:  w.Start.UTC(),
			End:    w.End.UTC(),
			Blocks: blocks,
		})
	}
	return out
}

// applyVectorYAML routes the §13.12 `vector_backend:` sub-keys to the selected
// backend's config fields. api_key maps to the backend's key;
// index / namespace / inference_model are the Pinecone-scoped keys the config
// example documents. The inference-model value is captured for the §13.12.6
// self-embedding path. Env values keep precedence.
func applyVectorYAML(c *Config, v yamlVectorCfg) {
	switch c.vectorBackend {
	case "pinecone":
		if c.pineconeKey == "" && v.APIKey != "" {
			c.pineconeKey = v.APIKey
		}
		if c.pineconeIndex == "" && v.Index != "" {
			c.pineconeIndex = v.Index
		}
		if c.pineconeNS == "" && v.Namespace != "" {
			c.pineconeNS = v.Namespace
		}
		// §13.12: PODIUM_PINECONE_HOST as a config-file key.
		if c.pineconeHost == "" && v.Host != "" {
			c.pineconeHost = v.Host
		}
	case "weaviate-cloud":
		if c.weaviateKey == "" && v.APIKey != "" {
			c.weaviateKey = v.APIKey
		}
		if c.weaviateColl == "" && v.Collection != "" {
			c.weaviateColl = v.Collection
		}
		// §13.12: PODIUM_WEAVIATE_URL as a config-file key, so a
		// weaviate-cloud deployment can be configured entirely in registry.yaml.
		if c.weaviateURL == "" && v.URL != "" {
			c.weaviateURL = v.URL
		}
	case "qdrant-cloud":
		if c.qdrantKey == "" && v.APIKey != "" {
			c.qdrantKey = v.APIKey
		}
		if c.qdrantColl == "" && v.Collection != "" {
			c.qdrantColl = v.Collection
		}
		// §13.12: PODIUM_QDRANT_URL as a config-file key.
		if c.qdrantURL == "" && v.URL != "" {
			c.qdrantURL = v.URL
		}
	}
	if c.vectorInferenceModel == "" && v.InferenceModel != "" {
		c.vectorInferenceModel = v.InferenceModel
	}
}

// applyEmbeddingYAML routes the §13.12 `embedding_provider` sub-keys to the
// selected provider's config fields: api_key for every
// provider, plus base_url / org for openai and url for ollama. Env values keep
// precedence (a non-empty target is left untouched; PODIUM_OLLAMA_URL is read
// directly because it carries a non-empty env default).
func applyEmbeddingYAML(c *Config, e yamlEmbedCfg) {
	switch c.embeddingProvider {
	case "openai":
		if c.openaiAPIKey == "" && e.APIKey != "" {
			c.openaiAPIKey = e.APIKey
		}
		if c.openaiBaseURL == "" && e.BaseURL != "" {
			c.openaiBaseURL = e.BaseURL
		}
		if c.openaiOrg == "" && e.Org != "" {
			c.openaiOrg = e.Org
		}
	case "voyage":
		if c.voyageAPIKey == "" && e.APIKey != "" {
			c.voyageAPIKey = e.APIKey
		}
	case "cohere":
		if c.cohereAPIKey == "" && e.APIKey != "" {
			c.cohereAPIKey = e.APIKey
		}
	case "ollama":
		if os.Getenv("PODIUM_OLLAMA_URL") == "" && e.URL != "" {
			c.ollamaURL = e.URL
		}
	}
}
