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
		// Mimic loadConfig's hardcoded defaults so applyYAML acts. The
		// §13.2.1 probe fields use -1 as the "env unset" sentinel so the
		// registry.yaml overlay can distinguish absent from an explicit 0.
		bind:                   "127.0.0.1:8080",
		storeType:              "sqlite",
		objectStore:            "filesystem",
		defaultLayerVisibility: "private",
		readOnlyProbeFailures:  -1,
		readOnlyProbeInterval:  -1,
	}
	y := &yamlConfig{
		Bind:                   "0.0.0.0:9090",
		DefaultLayerVisibility: "organization",
		ReadOnly: yamlReadOnly{
			ProbeFailures: 3,
			ProbeInterval: 60,
		},
		Store: yamlStoreCfg{Type: "postgres", DSN: "postgres://u:p@h/db"},
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

// Spec: §13.12 / §4.5.5 — the registry.yaml discovery block
// parses into the config and resolves to core.DiscoveryDefaults plus the
// allow_per_domain_overrides gate.
func TestApplyYAML_DiscoveryBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.yaml")
	body := []byte(`registry:
  discovery:
    max_depth: 4
    notable_count: 7
    fold_below_artifacts: 2
    fold_passthrough_chains: false
    target_response_tokens: 3000
    allow_per_domain_overrides: false
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("PODIUM_CONFIG_FILE", path)
	y, err := readYAMLConfig()
	if err != nil {
		t.Fatalf("readYAMLConfig: %v", err)
	}
	c := &Config{}
	applyYAML(c, y)

	if c.allowPerDomain() {
		t.Error("allowPerDomain() = true, want false (allow_per_domain_overrides: false)")
	}
	d := c.discoveryDefaults()
	if d.MaxDepth != 4 || d.NotableCount != 7 || d.FoldBelowArtifacts != 2 || d.TargetResponseTokens != 3000 {
		t.Errorf("discoveryDefaults = %+v, want max_depth 4 / notable 7 / fold_below 2 / budget 3000", d)
	}
	if d.FoldPassthroughChains == nil || *d.FoldPassthroughChains {
		t.Errorf("FoldPassthroughChains = %v, want explicit false", d.FoldPassthroughChains)
	}
}

// Spec: §4.5.5 — when registry.yaml omits the discovery
// block, allow_per_domain_overrides defaults to true (per-domain
// overrides allowed).
func TestApplyYAML_DiscoveryDefaultsAllowOverrides(t *testing.T) {
	c := &Config{}
	applyYAML(c, &yamlConfig{})
	if !c.allowPerDomain() {
		t.Error("allowPerDomain() = false, want true by default")
	}
}

// Spec: §13.10 — yaml fills defaultLayerVisibility only when the
// config did not see PODIUM_DEFAULT_LAYER_VISIBILITY (left empty).
func TestApplyYAML_DefaultLayerVisibilityFillsWhenEmpty(t *testing.T) {
	c := &Config{defaultLayerVisibility: ""}
	applyYAML(c, &yamlConfig{DefaultLayerVisibility: "public"})
	if c.defaultLayerVisibility != "public" {
		t.Errorf("defaultLayerVisibility = %q, want public", c.defaultLayerVisibility)
	}
}

// Spec: §3.5 — registry.yaml's tenant.expose_scope_preview
// parses as a tri-state and overlays into the config; an absent block
// leaves it nil (default true).
func TestApplyYAML_ExposeScopePreview(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.yaml")
	if err := os.WriteFile(path, []byte("registry:\n  tenant:\n    expose_scope_preview: false\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("PODIUM_CONFIG_FILE", path)
	y, err := readYAMLConfig()
	if err != nil {
		t.Fatalf("readYAMLConfig: %v", err)
	}
	c := &Config{}
	applyYAML(c, y)
	if c.exposeScopePreview == nil || *c.exposeScopePreview {
		t.Errorf("exposeScopePreview = %v, want explicit false from yaml", c.exposeScopePreview)
	}

	// Omitting the tenant block leaves the gate unset (default true).
	c2 := &Config{}
	applyYAML(c2, &yamlConfig{})
	if c2.exposeScopePreview != nil {
		t.Errorf("exposeScopePreview = %v, want nil when tenant block absent", *c2.exposeScopePreview)
	}
}

// Spec: §3.5 — the env var PODIUM_EXPOSE_SCOPE_PREVIEW wins over
// registry.yaml, matching the standard env-beats-yaml precedence.
func TestApplyYAML_ExposeScopePreviewEnvWins(t *testing.T) {
	c := &Config{exposeScopePreview: envBoolPtr("PODIUM_EXPOSE_SCOPE_PREVIEW")} // unset → nil
	if c.exposeScopePreview != nil {
		t.Fatalf("precondition: env unset should yield nil, got %v", *c.exposeScopePreview)
	}
	yesEnv := true
	c.exposeScopePreview = &yesEnv // simulate env-derived true
	applyYAML(c, &yamlConfig{Tenant: yamlTenant{ExposeScopePreview: boolPtr(false)}})
	if c.exposeScopePreview == nil || !*c.exposeScopePreview {
		t.Errorf("exposeScopePreview = %v, want env true to win over yaml false", c.exposeScopePreview)
	}
}

// Spec: §3.5 — envBoolPtr is the tri-state reader behind the gate.
func TestEnvBoolPtr_TriState(t *testing.T) {
	if envBoolPtr("PODIUM_TEST_UNSET_VAR_XYZ") != nil {
		t.Error("unset var should yield nil")
	}
	t.Setenv("PODIUM_TEST_BOOL_VAR", "false")
	if p := envBoolPtr("PODIUM_TEST_BOOL_VAR"); p == nil || *p {
		t.Errorf("'false' should yield &false, got %v", p)
	}
	t.Setenv("PODIUM_TEST_BOOL_VAR", "true")
	if p := envBoolPtr("PODIUM_TEST_BOOL_VAR"); p == nil || !*p {
		t.Errorf("'true' should yield &true, got %v", p)
	}
}

func boolPtr(b bool) *bool { return &b }

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
	body := []byte(`registry:
  bind: 127.0.0.1:9000
  identity_provider:
    type: oidc
  store:
    type: postgres
    dsn: postgres://localhost/db
  object_store:
    type: filesystem
    filesystem_root: /var/podium/objects
  default_layer_visibility: organization
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
	if y.Identity.Type != "oidc" {
		t.Errorf("Identity.Type = %q", y.Identity.Type)
	}
	if y.Store.Type != "postgres" || y.Store.DSN != "postgres://localhost/db" {
		t.Errorf("Store = %+v", y.Store)
	}
	if y.ObjectStore.FilesystemRoot != "/var/podium/objects" {
		t.Errorf("ObjectStore = %+v", y.ObjectStore)
	}
	if y.DefaultLayerVisibility != "organization" {
		t.Errorf("DefaultLayerVisibility = %q", y.DefaultLayerVisibility)
	}
	if y.ReadOnly.ProbeFailures != 5 || y.ReadOnly.ProbeInterval != 45 {
		t.Errorf("ReadOnly = %+v", y.ReadOnly)
	}
}
