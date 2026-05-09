package serverboot

import (
	"os"
	"path/filepath"
	"testing"
)

// Spec: §13.10 — registry.yaml supplies deployment defaults that env
// vars override. Phase 10 wires the new layers / read_only fields.
func TestApplyYAML_FillsMissingDefaults(t *testing.T) {
	c := &Config{
		// Mimic loadConfig's hardcoded defaults so applyYAML acts.
		bind:                   "127.0.0.1:8080",
		storeType:              "sqlite",
		objectStore:            "filesystem",
		defaultLayerVisibility: "private",
	}
	y := &yamlConfig{
		Bind:   "0.0.0.0:9090",
		Layers: yamlLayerCfg{DefaultVisibility: "organization"},
		ReadOnly: yamlReadOnly{
			ProbeFailures: 3,
			ProbeInterval: 60,
		},
		Store: yamlStoreCfg{Type: "postgres", PostgresDSN: "postgres://u:p@h/db"},
	}
	applyYAML(c, y)
	if c.bind != "0.0.0.0:9090" {
		t.Errorf("bind = %q, want 0.0.0.0:9090", c.bind)
	}
	if c.defaultLayerVisibility != "private" {
		t.Errorf("defaultLayerVisibility = %q, want private (env-derived value should win when set)", c.defaultLayerVisibility)
	}
	if c.readOnlyProbeFailures != 3 {
		t.Errorf("readOnlyProbeFailures = %d, want 3", c.readOnlyProbeFailures)
	}
	if c.readOnlyProbeInterval != 60 {
		t.Errorf("readOnlyProbeInterval = %d, want 60", c.readOnlyProbeInterval)
	}
	if c.storeType != "postgres" {
		t.Errorf("storeType = %q, want postgres (env unset → yaml fills)", c.storeType)
	}
	if c.postgresDSN != "postgres://u:p@h/db" {
		t.Errorf("postgresDSN = %q, want yaml value", c.postgresDSN)
	}
}

// Spec: §13.10 — yaml fills defaultLayerVisibility only when the
// config did not see PODIUM_DEFAULT_LAYER_VISIBILITY (left empty).
func TestApplyYAML_DefaultLayerVisibilityFillsWhenEmpty(t *testing.T) {
	c := &Config{defaultLayerVisibility: ""}
	applyYAML(c, &yamlConfig{Layers: yamlLayerCfg{DefaultVisibility: "public"}})
	if c.defaultLayerVisibility != "public" {
		t.Errorf("defaultLayerVisibility = %q, want public", c.defaultLayerVisibility)
	}
}

// Spec: §13.10 — readYAMLConfig returns (nil, nil) when the file is
// absent, so loadConfig falls through to env defaults.
func TestReadYAMLConfig_MissingFileIsNoOp(t *testing.T) {
	t.Setenv("PODIUM_CONFIG_FILE", filepath.Join(t.TempDir(), "registry.yaml"))
	y, err := readYAMLConfig()
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if y != nil {
		t.Errorf("yamlConfig = %+v, want nil", y)
	}
}

// Spec: §13.10 — readYAMLConfig parses the on-disk file, including
// nested store / object_store / read_only blocks.
func TestReadYAMLConfig_ParsesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.yaml")
	body := []byte(`bind: 127.0.0.1:9000
identity_provider: oidc
store:
  type: postgres
  postgres_dsn: postgres://localhost/db
object_store:
  type: filesystem
  filesystem_root: /var/podium/objects
layers:
  default_visibility: organization
read_only:
  probe_failures: 5
  probe_interval_seconds: 45
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
		t.Fatal("yamlConfig nil")
	}
	if y.Bind != "127.0.0.1:9000" {
		t.Errorf("Bind = %q", y.Bind)
	}
	if y.IdentityProvider != "oidc" {
		t.Errorf("IdentityProvider = %q", y.IdentityProvider)
	}
	if y.Store.Type != "postgres" || y.Store.PostgresDSN != "postgres://localhost/db" {
		t.Errorf("Store = %+v", y.Store)
	}
	if y.ObjectStore.FilesystemRoot != "/var/podium/objects" {
		t.Errorf("ObjectStore = %+v", y.ObjectStore)
	}
	if y.Layers.DefaultVisibility != "organization" {
		t.Errorf("Layers.DefaultVisibility = %q", y.Layers.DefaultVisibility)
	}
	if y.ReadOnly.ProbeFailures != 5 || y.ReadOnly.ProbeInterval != 45 {
		t.Errorf("ReadOnly = %+v", y.ReadOnly)
	}
}
