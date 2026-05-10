package serverboot

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// yamlConfig mirrors the §13.10 `~/.podium/registry.yaml` shape.
// Each field is optional; loadConfig overlays a non-empty value on
// top of the env-derived default so env-var precedence remains
// (env beats yaml beats hardcoded default).
type yamlConfig struct {
	Bind             string        `yaml:"bind,omitempty"`
	PublicMode       *bool         `yaml:"public_mode,omitempty"`
	IdentityProvider string        `yaml:"identity_provider,omitempty"`
	Store            yamlStoreCfg  `yaml:"store,omitempty"`
	ObjectStore      yamlObjectCfg `yaml:"object_store,omitempty"`
	Vector           yamlVectorCfg `yaml:"vector_backend,omitempty"`
	Embedding        yamlEmbedCfg  `yaml:"embedding_provider,omitempty"`
	Discovery        yamlDiscovery `yaml:"discovery,omitempty"`
	Layers           yamlLayerCfg  `yaml:"layers,omitempty"`
	ReadOnly         yamlReadOnly  `yaml:"read_only,omitempty"`
}

type yamlStoreCfg struct {
	Type        string `yaml:"type,omitempty"`
	SQLitePath  string `yaml:"sqlite_path,omitempty"`
	PostgresDSN string `yaml:"postgres_dsn,omitempty"`
}

type yamlObjectCfg struct {
	Type           string `yaml:"type,omitempty"`
	FilesystemRoot string `yaml:"filesystem_root,omitempty"`
	S3Endpoint     string `yaml:"s3_endpoint,omitempty"`
	S3Bucket       string `yaml:"s3_bucket,omitempty"`
	S3Region       string `yaml:"s3_region,omitempty"`
}

type yamlVectorCfg struct {
	Type string `yaml:"type,omitempty"`
}

type yamlEmbedCfg struct {
	Provider string `yaml:"provider,omitempty"`
	Model    string `yaml:"model,omitempty"`
}

type yamlDiscovery struct {
	MaxDepth             int  `yaml:"max_depth,omitempty"`
	NotableCount         int  `yaml:"notable_count,omitempty"`
	FoldBelowArtifacts   int  `yaml:"fold_below_artifacts,omitempty"`
	FoldPassthroughChain bool `yaml:"fold_passthrough_chains,omitempty"`
	TargetResponseTokens int  `yaml:"target_response_tokens,omitempty"`
}

type yamlLayerCfg struct {
	DefaultVisibility string `yaml:"default_visibility,omitempty"`
}

type yamlReadOnly struct {
	ProbeFailures int `yaml:"probe_failures,omitempty"`
	ProbeInterval int `yaml:"probe_interval_seconds,omitempty"`
}

// readYAMLConfig loads ~/.podium/registry.yaml (or
// PODIUM_CONFIG_FILE override). Missing file returns (nil, nil).
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
	var out yamlConfig
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &out, nil
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
	if y.PublicMode != nil && os.Getenv("PODIUM_PUBLIC_MODE") == "" {
		c.publicMode = *y.PublicMode
	}
	if c.identityProvider == "" && y.IdentityProvider != "" {
		c.identityProvider = y.IdentityProvider
	}
	if c.storeType == "sqlite" && y.Store.Type != "" && os.Getenv("PODIUM_REGISTRY_STORE") == "" {
		c.storeType = y.Store.Type
	}
	if c.sqlitePath == "" && y.Store.SQLitePath != "" {
		c.sqlitePath = y.Store.SQLitePath
	}
	if c.postgresDSN == "" && y.Store.PostgresDSN != "" {
		c.postgresDSN = y.Store.PostgresDSN
	}
	if c.objectStore == "filesystem" && y.ObjectStore.Type != "" && os.Getenv("PODIUM_OBJECT_STORE") == "" {
		c.objectStore = y.ObjectStore.Type
	}
	if c.filesystemRoot == "" && y.ObjectStore.FilesystemRoot != "" {
		c.filesystemRoot = y.ObjectStore.FilesystemRoot
	}
	if c.s3Endpoint == "" && y.ObjectStore.S3Endpoint != "" {
		c.s3Endpoint = y.ObjectStore.S3Endpoint
	}
	if c.s3Bucket == "" && y.ObjectStore.S3Bucket != "" {
		c.s3Bucket = y.ObjectStore.S3Bucket
	}
	if y.ObjectStore.S3Region != "" && os.Getenv("PODIUM_S3_REGION") == "" {
		c.s3Region = y.ObjectStore.S3Region
	}
	if c.vectorBackend == "" && y.Vector.Type != "" {
		c.vectorBackend = y.Vector.Type
	}
	if c.embeddingProvider == "" && y.Embedding.Provider != "" {
		c.embeddingProvider = y.Embedding.Provider
	}
	if c.embeddingModel == "" && y.Embedding.Model != "" {
		c.embeddingModel = y.Embedding.Model
	}
	if c.defaultLayerVisibility == "" && y.Layers.DefaultVisibility != "" {
		c.defaultLayerVisibility = y.Layers.DefaultVisibility
	}
	if c.readOnlyProbeFailures == 0 && y.ReadOnly.ProbeFailures > 0 {
		c.readOnlyProbeFailures = y.ReadOnly.ProbeFailures
	}
	if c.readOnlyProbeInterval == 0 && y.ReadOnly.ProbeInterval > 0 {
		c.readOnlyProbeInterval = y.ReadOnly.ProbeInterval
	}
}
