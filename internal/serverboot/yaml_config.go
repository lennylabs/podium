package serverboot

import (
	"fmt"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

// fileConfig is the top-level registry.yaml document. §13.12 nests every
// server-side key under a single `registry:` mapping, so the parser reads
// into this wrapper and hands callers the unwrapped Registry block.
// spec: §13.12 (config file format example, F-13.12.2).
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
}

// yamlTenant mirrors the §3.5 `tenant:` block. ExposeScopePreview is
// tri-state (nil = default true) so an explicit false survives.
type yamlTenant struct {
	ExposeScopePreview *bool `yaml:"expose_scope_preview,omitempty"`
}

// yamlIdentityCfg mirrors the §13.12 `identity_provider:` mapping. The spec
// example models it as an object with type / audience / authorization_endpoint
// (F-13.12.4), so a scalar would no longer unmarshal here.
type yamlIdentityCfg struct {
	Type                  string `yaml:"type,omitempty"`
	Audience              string `yaml:"audience,omitempty"`
	AuthorizationEndpoint string `yaml:"authorization_endpoint,omitempty"`
}

// yamlStoreCfg mirrors the §13.12 `store:` block. The DSN key is `dsn`
// (section-relative, per the config example) rather than `postgres_dsn`
// (F-13.12.5).
type yamlStoreCfg struct {
	Type       string `yaml:"type,omitempty"`
	SQLitePath string `yaml:"sqlite_path,omitempty"`
	DSN        string `yaml:"dsn,omitempty"`
}

// yamlObjectCfg mirrors the §13.12 `object_store:` block. The keys are
// section-relative (`bucket`, `region`, `endpoint`) per the config example
// rather than the `s3_`-prefixed env-var forms (F-13.12.5).
type yamlObjectCfg struct {
	Type           string `yaml:"type,omitempty"`
	FilesystemRoot string `yaml:"filesystem_root,omitempty"`
	Endpoint       string `yaml:"endpoint,omitempty"`
	Bucket         string `yaml:"bucket,omitempty"`
	Region         string `yaml:"region,omitempty"`
}

// yamlVectorCfg mirrors the §13.12 `vector_backend:` block. Beyond `type`,
// the config example carries the per-backend sub-keys api_key / index /
// namespace / inference_model (F-13.12.4); applyYAML routes them to the
// selected backend's config fields.
type yamlVectorCfg struct {
	Type           string `yaml:"type,omitempty"`
	APIKey         string `yaml:"api_key,omitempty"`
	Index          string `yaml:"index,omitempty"`
	Namespace      string `yaml:"namespace,omitempty"`
	InferenceModel string `yaml:"inference_model,omitempty"`
}

// yamlEmbedCfg mirrors the §13.12 `embedding_provider:` block. The selector
// key is `type` (matching the config example) rather than `provider`, and the
// block carries an `api_key` (F-13.12.4).
type yamlEmbedCfg struct {
	Type   string `yaml:"type,omitempty"`
	APIKey string `yaml:"api_key,omitempty"`
	Model  string `yaml:"model,omitempty"`
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
// values are missing"). spec: §13.12 (F-13.12.1).
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
	// resolved value rather than the literal placeholder (F-13.12.1).
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
	if c.bind == "" || c.bind == "127.0.0.1:8080" {
		if y.Bind != "" {
			c.bind = y.Bind
		}
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
	if c.readOnlyProbeFailures == 0 && y.ReadOnly.ProbeFailures > 0 {
		c.readOnlyProbeFailures = y.ReadOnly.ProbeFailures
	}
	if c.readOnlyProbeInterval == 0 && y.ReadOnly.ProbeInterval > 0 {
		c.readOnlyProbeInterval = y.ReadOnly.ProbeInterval
	}
}

// applyVectorYAML routes the §13.12 `vector_backend:` sub-keys to the selected
// backend's config fields (F-13.12.4). api_key maps to the backend's key;
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
	case "weaviate-cloud":
		if c.weaviateKey == "" && v.APIKey != "" {
			c.weaviateKey = v.APIKey
		}
	case "qdrant-cloud":
		if c.qdrantKey == "" && v.APIKey != "" {
			c.qdrantKey = v.APIKey
		}
	}
	if c.vectorInferenceModel == "" && v.InferenceModel != "" {
		c.vectorInferenceModel = v.InferenceModel
	}
}

// applyEmbeddingYAML routes the §13.12 `embedding_provider.api_key` to the
// selected provider's key field (F-13.12.4). Env values keep precedence.
func applyEmbeddingYAML(c *Config, e yamlEmbedCfg) {
	if e.APIKey == "" {
		return
	}
	switch c.embeddingProvider {
	case "openai":
		if c.openaiAPIKey == "" {
			c.openaiAPIKey = e.APIKey
		}
	case "voyage":
		if c.voyageAPIKey == "" {
			c.voyageAPIKey = e.APIKey
		}
	case "cohere":
		if c.cohereAPIKey == "" {
			c.cohereAPIKey = e.APIKey
		}
	}
}
