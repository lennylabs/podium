package serverboot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadYAMLConfig_MissingFileReturnsNilNil(t *testing.T) {
	t.Setenv("PODIUM_CONFIG_FILE", filepath.Join(t.TempDir(), "absent.yaml"))
	cfg, err := readYAMLConfig()
	if err != nil {
		t.Errorf("err = %v", err)
	}
	if cfg != nil {
		t.Errorf("expected nil cfg, got %+v", cfg)
	}
}

func TestReadYAMLConfig_BadYAMLReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.yaml")
	if err := os.WriteFile(path, []byte("not: valid: yaml: ::"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("PODIUM_CONFIG_FILE", path)
	_, err := readYAMLConfig()
	if err == nil || !strings.Contains(err.Error(), "parse") {
		t.Errorf("err = %v, want parse error", err)
	}
}

func TestReadYAMLConfig_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.yaml")
	body := "bind: \"0.0.0.0:9090\"\nidentity_provider: \"oidc\"\nstore:\n  type: postgres\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("PODIUM_CONFIG_FILE", path)
	cfg, err := readYAMLConfig()
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if cfg == nil || cfg.Bind != "0.0.0.0:9090" || cfg.IdentityProvider != "oidc" {
		t.Errorf("got %+v", cfg)
	}
}

func TestApplyYAML_PopulatesEmptyFields(t *testing.T) {
	t.Setenv("PODIUM_PUBLIC_MODE", "")
	t.Setenv("PODIUM_REGISTRY_STORE", "")
	t.Setenv("PODIUM_OBJECT_STORE", "")
	publicTrue := true
	c := &Config{
		bind:           "127.0.0.1:8080", // default; should accept YAML override
		storeType:      "sqlite",
		objectStore:    "filesystem",
		filesystemRoot: "",
	}
	y := &yamlConfig{
		Bind:             "0.0.0.0:9090",
		IdentityProvider: "oidc",
		PublicMode:       &publicTrue,
	}
	y.Store.Type = "postgres"
	y.Store.PostgresDSN = "postgres://x"
	y.ObjectStore.Type = "s3"
	y.ObjectStore.S3Bucket = "my-bucket"
	applyYAML(c, y)
	if c.bind != "0.0.0.0:9090" {
		t.Errorf("bind = %q", c.bind)
	}
	if c.identityProvider != "oidc" {
		t.Errorf("identity = %q", c.identityProvider)
	}
	if !c.publicMode {
		t.Errorf("publicMode = false")
	}
	if c.storeType != "postgres" {
		t.Errorf("store = %q", c.storeType)
	}
	if c.postgresDSN != "postgres://x" {
		t.Errorf("DSN = %q", c.postgresDSN)
	}
	if c.objectStore != "s3" {
		t.Errorf("objectStore = %q", c.objectStore)
	}
}

func TestApplyYAML_NilNoop(t *testing.T) {
	c := &Config{}
	applyYAML(c, nil) // must not panic
}

func TestApplyYAML_RespectsEnvOverride(t *testing.T) {
	t.Setenv("PODIUM_PUBLIC_MODE", "true")
	c := &Config{publicMode: false}
	publicFromYAML := true
	y := &yamlConfig{PublicMode: &publicFromYAML}
	applyYAML(c, y)
	// Env was set so YAML should NOT have overridden — c.publicMode
	// stays at its initial value (false here, because we didn't run
	// LoadConfig). The branch we want is the env-presence check.
	if c.publicMode {
		t.Errorf("expected env precedence")
	}
}

func TestBoolStr(t *testing.T) {
	t.Parallel()
	if boolStr(true) != "true" || boolStr(false) != "false" {
		t.Errorf("boolStr")
	}
}
