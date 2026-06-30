package serverboot

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/podium/pkg/embedding"
	"github.com/lennylabs/podium/pkg/webhook"
)

// Spec: §13.12 — "Server-side precedence: CLI flag > env var >
// config file." An explicit PODIUM_BIND that happens to equal the loopback
// default 127.0.0.1:8080 is still an env value, so the registry.yaml bind must
// not override it.
func TestLoadConfig_BindEnvBeatsYAMLEvenAtDefaultValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.yaml")
	if err := os.WriteFile(path, []byte("registry:\n  bind: 0.0.0.0:9999\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("PODIUM_CONFIG_FILE", path)
	t.Setenv("PODIUM_BIND", "127.0.0.1:8080") // explicit, equal to the default literal
	if got := LoadConfig().bind; got != "127.0.0.1:8080" {
		t.Errorf("bind = %q, want 127.0.0.1:8080 (explicit env beats config file)", got)
	}
}

// Spec: §13.12 — when PODIUM_BIND is unset, the registry.yaml bind
// is used (config file fills an absent env var).
func TestLoadConfig_BindFromYAMLWhenEnvUnset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.yaml")
	if err := os.WriteFile(path, []byte("registry:\n  bind: 0.0.0.0:9999\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("PODIUM_CONFIG_FILE", path)
	t.Setenv("PODIUM_BIND", "")
	if got := LoadConfig().bind; got != "0.0.0.0:9999" {
		t.Errorf("bind = %q, want 0.0.0.0:9999 (config file used when env unset)", got)
	}
}

// Spec: §13.12 — PODIUM_S3_ACCESS_KEY_ID / PODIUM_S3_SECRET_ACCESS_KEY
// / PODIUM_PRESIGN_TTL_SECONDS are valid config-file keys under object_store.
func TestApplyYAML_ObjectStoreCredentialsAndPresign(t *testing.T) {
	t.Setenv("PODIUM_PRESIGN_TTL_SECONDS", "") // ensure the env guard sees unset
	c := &Config{objectStore: "s3"}
	applyYAML(c, &yamlConfig{ObjectStore: yamlObjectCfg{
		AccessKeyID:       "AKIAEXAMPLE",
		SecretAccessKey:   "secret-from-yaml",
		PresignTTLSeconds: 120,
	}})
	if c.s3AccessKey != "AKIAEXAMPLE" {
		t.Errorf("s3AccessKey = %q, want AKIAEXAMPLE", c.s3AccessKey)
	}
	if c.s3SecretKey != "secret-from-yaml" {
		t.Errorf("s3SecretKey = %q, want secret-from-yaml", c.s3SecretKey)
	}
	if c.presignTTL != 120*time.Second {
		t.Errorf("presignTTL = %v, want 120s", c.presignTTL)
	}
}

// Spec: §13.12 — PODIUM_PRESIGN_TTL_SECONDS env var keeps precedence
// over the config-file presign_ttl_seconds key.
func TestLoadConfig_PresignTTLEnvBeatsYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.yaml")
	if err := os.WriteFile(path, []byte("registry:\n  object_store:\n    presign_ttl_seconds: 120\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("PODIUM_CONFIG_FILE", path)
	t.Setenv("PODIUM_PRESIGN_TTL_SECONDS", "600")
	if got := LoadConfig().presignTTL; got != 600*time.Second {
		t.Errorf("presignTTL = %v, want 600s (env beats config file)", got)
	}
}

// Spec: §13.12 — vector_backend.host (Pinecone) and vector_backend.url
// (Weaviate / Qdrant) are valid config-file keys routed to the selected backend.
func TestApplyVectorYAML_HostAndURLKeys(t *testing.T) {
	cp := &Config{vectorBackend: "pinecone"}
	applyVectorYAML(cp, yamlVectorCfg{Host: "https://acme-idx.svc.pinecone.io"})
	if cp.pineconeHost != "https://acme-idx.svc.pinecone.io" {
		t.Errorf("pineconeHost = %q, want the yaml host", cp.pineconeHost)
	}

	cw := &Config{vectorBackend: "weaviate-cloud"}
	applyVectorYAML(cw, yamlVectorCfg{URL: "https://w.weaviate.cloud"})
	if cw.weaviateURL != "https://w.weaviate.cloud" {
		t.Errorf("weaviateURL = %q, want the yaml url", cw.weaviateURL)
	}

	cq := &Config{vectorBackend: "qdrant-cloud"}
	applyVectorYAML(cq, yamlVectorCfg{URL: "https://q.qdrant.cloud"})
	if cq.qdrantURL != "https://q.qdrant.cloud" {
		t.Errorf("qdrantURL = %q, want the yaml url", cq.qdrantURL)
	}
}

// Spec: §13.12 — embedding_provider.base_url / org (openai) and
// embedding_provider.url (ollama) are valid config-file keys.
func TestApplyEmbeddingYAML_BaseURLOrgAndOllamaURL(t *testing.T) {
	t.Setenv("PODIUM_OLLAMA_URL", "") // ensure the env guard sees unset

	co := &Config{embeddingProvider: "openai"}
	applyEmbeddingYAML(co, yamlEmbedCfg{BaseURL: "https://azure.example.com/v1", Org: "org-123"})
	if co.openaiBaseURL != "https://azure.example.com/v1" || co.openaiOrg != "org-123" {
		t.Errorf("openai base_url/org = %q/%q, want the yaml values", co.openaiBaseURL, co.openaiOrg)
	}

	cl := &Config{embeddingProvider: "ollama"}
	applyEmbeddingYAML(cl, yamlEmbedCfg{URL: "http://ollama.local:11434"})
	if cl.ollamaURL != "http://ollama.local:11434" {
		t.Errorf("ollamaURL = %q, want the yaml url", cl.ollamaURL)
	}
}

// Spec: §13.12 — the registry.yaml embedding_provider.model key
// (captured in c.embeddingModel) is applied to the built-in providers, not only
// the per-provider PODIUM_*_MODEL env vars.
func TestOpenEmbedder_ModelFromConfigFile(t *testing.T) {
	for _, env := range []string{
		"PODIUM_OPENAI_MODEL", "PODIUM_EMBEDDING_MODEL", "PODIUM_VOYAGE_MODEL",
		"PODIUM_COHERE_MODEL", "PODIUM_OLLAMA_MODEL",
	} {
		t.Setenv(env, "")
	}
	c := &Config{embeddingProvider: "openai", openaiAPIKey: "sk-test", embeddingModel: "text-embedding-3-large"}
	emb, err := openEmbedder(c)
	if err != nil {
		t.Fatalf("openEmbedder: %v", err)
	}
	oa, ok := emb.(embedding.OpenAI)
	if !ok {
		t.Fatalf("embedder type = %T, want embedding.OpenAI", emb)
	}
	if oa.Model() != "text-embedding-3-large" {
		t.Errorf("Model() = %q, want text-embedding-3-large (from the config-file model)", oa.Model())
	}
	// The model determines the dimension, which is the observable downstream effect.
	if oa.Dimensions() != 3072 {
		t.Errorf("Dimensions() = %d, want 3072 for text-embedding-3-large", oa.Dimensions())
	}
}

// Spec: §13.12 — the per-provider PODIUM_*_MODEL env var keeps
// precedence over the config-file model.
func TestOpenEmbedder_PerProviderEnvBeatsConfigModel(t *testing.T) {
	t.Setenv("PODIUM_EMBEDDING_MODEL", "")
	t.Setenv("PODIUM_OPENAI_MODEL", "text-embedding-3-large")
	c := &Config{embeddingProvider: "openai", openaiAPIKey: "sk-test", embeddingModel: "ignored-yaml-model"}
	emb, err := openEmbedder(c)
	if err != nil {
		t.Fatalf("openEmbedder: %v", err)
	}
	if got := emb.(embedding.OpenAI).Model(); got != "text-embedding-3-large" {
		t.Errorf("Model() = %q, want the per-provider env value", got)
	}
}

// Spec: §13.12 — embedding_provider.base_url / org reach the openai
// embedder via the Config fields rather than being read only from env vars.
func TestOpenEmbedder_OpenAIBaseURLAndOrgFromConfig(t *testing.T) {
	t.Setenv("PODIUM_OPENAI_MODEL", "")
	t.Setenv("PODIUM_EMBEDDING_MODEL", "")
	c := &Config{
		embeddingProvider: "openai",
		openaiAPIKey:      "sk-test",
		openaiBaseURL:     "https://proxy.example.com/v1",
		openaiOrg:         "org-xyz",
	}
	emb, err := openEmbedder(c)
	if err != nil {
		t.Fatalf("openEmbedder: %v", err)
	}
	oa := emb.(embedding.OpenAI)
	if oa.BaseURL != "https://proxy.example.com/v1" || oa.Org != "org-xyz" {
		t.Errorf("openai BaseURL/Org = %q/%q, want the config values", oa.BaseURL, oa.Org)
	}
}

// Spec: §13.12 — the serverboot config path resolves an index-only
// Pinecone deployment's data-plane host from the control plane. openVectorBackend
// flows the control-plane override (PODIUM_PINECONE_CONTROL_PLANE) through
// backendConfig() into the shared OpenBuiltin factory.
func TestOpenVectorBackend_PineconeResolvesHostFromIndex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/indexes/acme-prod" {
			t.Errorf("control-plane path = %q, want /indexes/acme-prod", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"host":"https://acme-prod.svc.pinecone.io"}`))
	}))
	defer srv.Close()

	c := &Config{
		vectorBackend:        "pinecone",
		pineconeKey:          "k",
		pineconeIndex:        "acme-prod",
		pineconeControlPlane: srv.URL,
	}
	v, err := openVectorBackend(c, 4)
	if err != nil {
		t.Fatalf("openVectorBackend(index-only pinecone) = %v, want a resolved backend", err)
	}
	if v == nil || v.ID() != "pinecone" {
		t.Fatalf("backend = %v, want pinecone", v)
	}
}

// Spec: §13.12, §7.3.2 — PODIUM_WEBHOOK_ALLOWED_TARGETS populates the
// receiver-URL SSRF policy with the comma-separated hosts and CIDRs, which
// the policy then permits in addition to public addresses. The boot wires
// this policy onto the webhook worker.
func TestWebhookURLPolicy_AllowedTargetsPopulatesPolicy(t *testing.T) {
	p, err := webhookURLPolicy("relay.internal, 10.0.0.0/8")
	if err != nil {
		t.Fatalf("webhookURLPolicy: %v", err)
	}
	if got := p.AllowedTargets(); len(got) != 2 || got[0] != "relay.internal" || got[1] != "10.0.0.0/8" {
		t.Fatalf("AllowedTargets() = %v, want [relay.internal 10.0.0.0/8]", got)
	}
	// The allowlisted bare host short-circuits resolution, so the policy
	// permits a target that the strict default would reject.
	if err := p.Validate(context.Background(), "https://relay.internal/ci"); err != nil {
		t.Errorf("allowlisted host should pass, got %v", err)
	}
}

// Spec: §13.12, §7.3.2 — an empty PODIUM_WEBHOOK_ALLOWED_TARGETS leaves
// the strict default: no allowlist overrides, https required, and private
// targets rejected.
func TestWebhookURLPolicy_EmptyKeepsStrictDefault(t *testing.T) {
	p, err := webhookURLPolicy("")
	if err != nil {
		t.Fatalf("webhookURLPolicy: %v", err)
	}
	if got := p.AllowedTargets(); len(got) != 0 {
		t.Fatalf("AllowedTargets() = %v, want empty (strict default)", got)
	}
	// A plain-http target fails the scheme check the strict default enforces.
	if err := p.Validate(context.Background(), "http://hooks.acme.com/ci"); !errors.Is(err, webhook.ErrDisallowedTarget) {
		t.Errorf("plain http should be rejected by the strict default, got %v", err)
	}
	// A private literal target is rejected without an allowlist override.
	if err := p.Validate(context.Background(), "https://127.0.0.1/ci"); !errors.Is(err, webhook.ErrDisallowedTarget) {
		t.Errorf("loopback target should be rejected by the strict default, got %v", err)
	}
}

// Spec: §13.12 — a malformed PODIUM_WEBHOOK_ALLOWED_TARGETS entry aborts
// boot rather than silently widening the policy.
func TestWebhookURLPolicy_MalformedEntryFailsBoot(t *testing.T) {
	_, err := webhookURLPolicy("relay.internal, 10.0.0.0/bad")
	if err == nil {
		t.Fatal("webhookURLPolicy(malformed CIDR) = nil, want a boot error")
	}
}
