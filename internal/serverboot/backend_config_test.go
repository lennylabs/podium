package serverboot

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Spec: §13.12 (F-13.12.1) — `${ENV_VAR}` interpolation resolves the brace
// form only; an unset variable expands to empty; a bare `$` survives.
func TestExpandEnvVars(t *testing.T) {
	t.Setenv("PODIUM_TEST_DSN", "postgres://u:p@h/db")
	cases := []struct{ in, want string }{
		{"dsn: ${PODIUM_TEST_DSN}", "dsn: postgres://u:p@h/db"},
		{"a: ${PODIUM_TEST_UNSET_XYZ}", "a: "}, // unset → empty
		{"pw: p$ssw0rd", "pw: p$ssw0rd"},       // bare $ is not the brace form
		{"two: ${PODIUM_TEST_DSN}/${PODIUM_TEST_DSN}", "two: postgres://u:p@h/db/postgres://u:p@h/db"},
	}
	for _, c := range cases {
		if got := string(expandEnvVars([]byte(c.in))); got != c.want {
			t.Errorf("expandEnvVars(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Spec: §13.12 (F-13.12.1, F-13.12.2, F-13.12.4, F-13.12.5) — a registry.yaml
// written as the documented config-file example parses: the top-level
// `registry:` block, ${ENV_VAR} interpolation, section-relative store/object
// keys (`dsn` / `bucket` / `region` / `endpoint`), the vector_backend
// sub-keys, and the identity_provider object all reach the resolved Config.
func TestReadYAMLConfig_SpecExampleNestedBlock(t *testing.T) {
	// Interpolated secret + precedence guards (env unset → yaml is the source).
	t.Setenv("PODIUM_TEST_PG_DSN", "postgres://alice:pw@db/podium")
	t.Setenv("PINECONE_API_KEY", "pcn-secret")
	for _, k := range []string{
		"PODIUM_REGISTRY_STORE", "PODIUM_OBJECT_STORE", "PODIUM_VECTOR_BACKEND",
		"PODIUM_S3_REGION", "PODIUM_S3_BUCKET", "PODIUM_PINECONE_API_KEY",
		"PODIUM_PINECONE_INDEX", "PODIUM_PINECONE_NAMESPACE", "PODIUM_IDENTITY_PROVIDER",
		"PODIUM_OAUTH_AUDIENCE", "PODIUM_OAUTH_AUTHORIZATION_ENDPOINT", "PODIUM_PUBLIC_URL",
	} {
		t.Setenv(k, "")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.yaml")
	body := []byte(`registry:
  endpoint: https://podium.acme.com
  bind: 0.0.0.0:8080
  store:
    type: postgres
    dsn: ${PODIUM_TEST_PG_DSN}
  object_store:
    type: s3
    bucket: acme-podium
    region: us-west-2
    endpoint: https://minio.acme.com
  vector_backend:
    type: pinecone
    api_key: ${PINECONE_API_KEY}
    index: acme-prod
    namespace: tenant-acme
    inference_model: multilingual-e5-large
  identity_provider:
    type: oauth-device-code
    audience: https://podium.acme.com
    authorization_endpoint: https://acme.okta.com/oauth2/default
  discovery:
    notable_count: 9
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("PODIUM_CONFIG_FILE", path)
	y, err := readYAMLConfig()
	if err != nil {
		t.Fatalf("readYAMLConfig: %v", err)
	}
	if y == nil {
		t.Fatal("yamlConfig nil; the registry: block did not parse")
	}
	c := &Config{bind: "127.0.0.1:8080", storeType: "sqlite", objectStore: "filesystem"}
	applyYAML(c, y)

	// F-13.12.1: ${ENV_VAR} interpolation.
	if c.postgresDSN != "postgres://alice:pw@db/podium" {
		t.Errorf("postgresDSN = %q, want interpolated DSN", c.postgresDSN)
	}
	if c.pineconeKey != "pcn-secret" {
		t.Errorf("pineconeKey = %q, want interpolated PINECONE_API_KEY", c.pineconeKey)
	}
	// F-13.12.2 / F-13.12.5: section-relative store/object keys under registry:.
	if c.publicURL != "https://podium.acme.com" {
		t.Errorf("publicURL = %q, want endpoint value", c.publicURL)
	}
	if c.storeType != "postgres" {
		t.Errorf("storeType = %q, want postgres", c.storeType)
	}
	if c.objectStore != "s3" || c.s3Bucket != "acme-podium" || c.s3Region != "us-west-2" || c.s3Endpoint != "https://minio.acme.com" {
		t.Errorf("object store = {%q %q %q %q}, want s3/acme-podium/us-west-2/https://minio.acme.com",
			c.objectStore, c.s3Bucket, c.s3Region, c.s3Endpoint)
	}
	// F-13.12.4: vector + identity sub-keys.
	if c.vectorBackend != "pinecone" || c.pineconeIndex != "acme-prod" || c.pineconeNS != "tenant-acme" {
		t.Errorf("vector = {%q index=%q ns=%q}, want pinecone/acme-prod/tenant-acme", c.vectorBackend, c.pineconeIndex, c.pineconeNS)
	}
	if c.vectorInferenceModel != "multilingual-e5-large" {
		t.Errorf("vectorInferenceModel = %q, want multilingual-e5-large", c.vectorInferenceModel)
	}
	if c.identityProvider != "oauth-device-code" || c.oauthAudience != "https://podium.acme.com" ||
		c.oauthAuthorizationEndpoint != "https://acme.okta.com/oauth2/default" {
		t.Errorf("identity = {%q aud=%q authz=%q}", c.identityProvider, c.oauthAudience, c.oauthAuthorizationEndpoint)
	}
	// F-13.12.3: the discovery block under registry: still reaches the defaults.
	if c.discoveryDefaults().NotableCount != 9 {
		t.Errorf("discovery NotableCount = %d, want 9", c.discoveryDefaults().NotableCount)
	}
}

// Spec: §13.12 (F-13.12.4) — embedding_provider.api_key routes to the selected
// provider's key field.
func TestApplyEmbeddingYAML_RoutesAPIKey(t *testing.T) {
	c := &Config{embeddingProvider: "openai"}
	applyEmbeddingYAML(c, yamlEmbedCfg{Type: "openai", APIKey: "sk-from-yaml"})
	if c.openaiAPIKey != "sk-from-yaml" {
		t.Errorf("openaiAPIKey = %q, want sk-from-yaml", c.openaiAPIKey)
	}
	// Env-set key keeps precedence (non-empty target is not overwritten).
	c2 := &Config{embeddingProvider: "voyage", voyageAPIKey: "from-env"}
	applyEmbeddingYAML(c2, yamlEmbedCfg{APIKey: "from-yaml"})
	if c2.voyageAPIKey != "from-env" {
		t.Errorf("voyageAPIKey = %q, want from-env (env beats yaml)", c2.voyageAPIKey)
	}
}

// Spec: §13.12 line 347 (F-13.12.10) — a selected backend missing required
// values is a hard startup error naming the missing keys; an unset/none/empty
// selection, an unknown backend, and a fully-supplied backend are not.
func TestValidate_MissingBackendValues(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		wantErr bool
		wantKey string
	}{
		{"s3 without bucket", Config{objectStore: "s3"}, true, "PODIUM_S3_BUCKET"},
		{"s3 with bucket", Config{objectStore: "s3", s3Bucket: "b"}, false, ""},
		{"pinecone without key", Config{vectorBackend: "pinecone", pineconeHost: "h"}, true, "PODIUM_PINECONE_API_KEY"},
		{"pinecone key+host ok", Config{vectorBackend: "pinecone", pineconeKey: "k", pineconeHost: "h"}, false, ""},
		{"pinecone key+index ok", Config{vectorBackend: "pinecone", pineconeKey: "k", pineconeIndex: "i"}, false, ""},
		{"pinecone key but no host/index", Config{vectorBackend: "pinecone", pineconeKey: "k"}, true, "PODIUM_PINECONE_INDEX"},
		{"weaviate without url", Config{vectorBackend: "weaviate-cloud", weaviateKey: "k"}, true, "PODIUM_WEAVIATE_URL"},
		{"weaviate ok", Config{vectorBackend: "weaviate-cloud", weaviateURL: "u", weaviateKey: "k"}, false, ""},
		{"qdrant without key", Config{vectorBackend: "qdrant-cloud", qdrantURL: "u"}, true, "PODIUM_QDRANT_API_KEY"},
		{"qdrant ok", Config{vectorBackend: "qdrant-cloud", qdrantURL: "u", qdrantKey: "k"}, false, ""},
		{"openai without key", Config{embeddingProvider: "openai"}, true, "OPENAI_API_KEY"},
		{"openai ok", Config{embeddingProvider: "openai", openaiAPIKey: "sk"}, false, ""},
		{"unknown vector backend is not a missing-value error", Config{vectorBackend: "nope"}, false, ""},
		{"empty embedding provider is intentional disable", Config{embeddingProvider: ""}, false, ""},
		{"none selections", Config{vectorBackend: "none", objectStore: "none"}, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.validate()
			if tc.wantErr && err == nil {
				t.Fatalf("validate() = nil, want error naming %s", tc.wantKey)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validate() = %v, want nil", err)
			}
			if tc.wantErr && !strings.Contains(err.Error(), tc.wantKey) {
				t.Errorf("validate() error %q does not name %s", err, tc.wantKey)
			}
		})
	}
}

// Spec: §13.10 / §13.12 (F-13.12.15) — endpoint-registered layers default to
// `public` in a standalone deployment (no identity provider) and `private`
// once an identity provider gates access; an explicit value wins.
func TestLoadConfig_StandaloneDefaultVisibility(t *testing.T) {
	t.Setenv("PODIUM_CONFIG_FILE", filepath.Join(t.TempDir(), "missing.yaml"))

	t.Run("standalone (no idp) defaults public", func(t *testing.T) {
		t.Setenv("PODIUM_IDENTITY_PROVIDER", "")
		t.Setenv("PODIUM_DEFAULT_LAYER_VISIBILITY", "")
		if got := LoadConfig().defaultLayerVisibility; got != "public" {
			t.Errorf("defaultLayerVisibility = %q, want public", got)
		}
	})
	t.Run("with idp defaults private", func(t *testing.T) {
		t.Setenv("PODIUM_IDENTITY_PROVIDER", "injected-session-token")
		t.Setenv("PODIUM_DEFAULT_LAYER_VISIBILITY", "")
		if got := LoadConfig().defaultLayerVisibility; got != "private" {
			t.Errorf("defaultLayerVisibility = %q, want private", got)
		}
	})
	t.Run("explicit env wins in standalone", func(t *testing.T) {
		t.Setenv("PODIUM_IDENTITY_PROVIDER", "")
		t.Setenv("PODIUM_DEFAULT_LAYER_VISIBILITY", "organization")
		if got := LoadConfig().defaultLayerVisibility; got != "organization" {
			t.Errorf("defaultLayerVisibility = %q, want organization", got)
		}
	})
}

// Spec: §13.12 (F-13.12.14) — the undocumented `memory` store/vector backends
// stay accepted but log a non-durable warning so an operator is not surprised.
func TestOpenStore_MemoryWarns(t *testing.T) {
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)
	st, err := openStore(&Config{storeType: "memory"})
	if err != nil || st == nil {
		t.Fatalf("openStore(memory) = (%v, %v), want a store", st, err)
	}
	if !strings.Contains(buf.String(), "non-durable") {
		t.Errorf("expected a non-durable warning, got: %q", buf.String())
	}
}

func TestOpenVectorBackend_MemoryWarns(t *testing.T) {
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)
	v, err := openVectorBackend(&Config{vectorBackend: "memory"}, 8)
	if err != nil || v == nil {
		t.Fatalf("openVectorBackend(memory) = (%v, %v), want a backend", v, err)
	}
	if !strings.Contains(buf.String(), "non-durable") {
		t.Errorf("expected a non-durable warning, got: %q", buf.String())
	}
}
