package e2e

// End-to-end tests for §13.12 registry.yaml backend-configuration keys:
//   - F-13.12.4: env var beats the config-file `bind` even when the env value
//     equals the loopback default.
//   - F-13.12.2: a config-file embedding_provider.model is applied to a built-in
//     provider (observable as the embedder dimension in the startup log).
//   - F-13.12.1: previously env-only keys (here embedding_provider.url) are valid
//     config-file keys.
//
// These drive the real `podium` binary: `config show --server` for resolved
// values and a standalone `serve` for the startup dimension log.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// T-registry-config-1 (F-13.12.4): PODIUM_BIND set to the loopback default is
// still an explicit env value, so the config-file bind must not override it.
func TestRegistryConfig_BindEnvBeatsConfigFile(t *testing.T) {
	t.Parallel()
	cfgFile := filepath.Join(t.TempDir(), "registry.yaml")
	if err := os.WriteFile(cfgFile, []byte("registry:\n  bind: 0.0.0.0:9999\n"), 0o644); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}
	res := runPodium(t, "", []string{
		"HOME=" + t.TempDir(),
		"PODIUM_CONFIG_FILE=" + cfgFile,
		"PODIUM_BIND=127.0.0.1:8080",
		"PODIUM_REGISTRY=",
	}, "config", "show", "--server")
	if res.Exit != 0 {
		t.Fatalf("config show exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "127.0.0.1:8080") {
		t.Errorf("bind should be the explicit env value 127.0.0.1:8080:\n%s", res.Stdout)
	}
	if strings.Contains(res.Stdout, "0.0.0.0:9999") {
		t.Errorf("config-file bind must not override the explicit env value:\n%s", res.Stdout)
	}
}

// T-registry-config-2 (F-13.12.4): with PODIUM_BIND unset, the config-file bind
// is used (config file fills an absent env var).
func TestRegistryConfig_BindFromConfigFileWhenEnvUnset(t *testing.T) {
	t.Parallel()
	cfgFile := filepath.Join(t.TempDir(), "registry.yaml")
	if err := os.WriteFile(cfgFile, []byte("registry:\n  bind: 0.0.0.0:9090\n"), 0o644); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}
	res := runPodium(t, "", []string{
		"HOME=" + t.TempDir(),
		"PODIUM_CONFIG_FILE=" + cfgFile,
		"PODIUM_BIND=",
		"PODIUM_REGISTRY=",
	}, "config", "show", "--server")
	if res.Exit != 0 {
		t.Fatalf("config show exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "0.0.0.0:9090") {
		t.Errorf("bind should come from the config file when env is unset:\n%s", res.Stdout)
	}
}

// T-registry-config-3 (F-13.12.1): embedding_provider.url is a valid config-file
// key for ollama (the env var is PODIUM_OLLAMA_URL). It reaches the resolved
// ollama_url setting even though the embedding_provider block has no api_key.
func TestRegistryConfig_OllamaURLFromConfigFile(t *testing.T) {
	t.Parallel()
	cfgFile := filepath.Join(t.TempDir(), "registry.yaml")
	body := "registry:\n  embedding_provider:\n    type: ollama\n    url: http://ollama.test:1234\n"
	if err := os.WriteFile(cfgFile, []byte(body), 0o644); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}
	res := runPodium(t, "", []string{
		"HOME=" + t.TempDir(),
		"PODIUM_CONFIG_FILE=" + cfgFile,
		"PODIUM_EMBEDDING_PROVIDER=ollama",
		"PODIUM_OLLAMA_URL=",
		"PODIUM_REGISTRY=",
	}, "config", "show", "--server")
	if res.Exit != 0 {
		t.Fatalf("config show exit=%d stderr=%s", res.Exit, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "http://ollama.test:1234") {
		t.Errorf("ollama_url should come from embedding_provider.url:\n%s", res.Stdout)
	}
}

// T-registry-config-4 (F-13.12.2): a config-file embedding_provider.model for a
// built-in provider takes effect. text-embedding-3-large fixes the embedder
// dimension at 3072, which the startup hybrid-search log reports; the bug left
// the provider on its default model (1536). The server runs standalone with an
// empty registry so no embedding request is issued.
func TestRegistryConfig_EmbeddingModelFromConfigFile(t *testing.T) {
	t.Parallel()
	cfgFile := filepath.Join(t.TempDir(), "registry.yaml")
	body := "registry:\n  embedding_provider:\n    type: openai\n    api_key: sk-test\n    model: text-embedding-3-large\n"
	if err := os.WriteFile(cfgFile, []byte(body), 0o644); err != nil {
		t.Fatalf("write registry.yaml: %v", err)
	}
	srv := startServerArgs(t, []string{
		"HOME=" + t.TempDir(),
		"PODIUM_CONFIG_FILE=" + cfgFile,
		"PODIUM_VECTOR_BACKEND=sqlite-vec",
		"PODIUM_EMBEDDING_PROVIDER=openai",
		"PODIUM_OPENAI_MODEL=",
		"PODIUM_EMBEDDING_MODEL=",
	}, "serve", "--standalone")
	if st := getStatus(t, srv.BaseURL+"/healthz"); st != 200 {
		t.Fatalf("healthz = %d, want 200", st)
	}
	log := srv.log()
	if !strings.Contains(log, "embedder=openai") {
		t.Fatalf("expected embedder=openai in startup log:\n%s", log)
	}
	if !strings.Contains(log, "dim=3072") {
		t.Errorf("expected dim=3072 from the config-file model text-embedding-3-large:\n%s", log)
	}
}
