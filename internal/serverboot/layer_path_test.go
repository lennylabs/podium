package serverboot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/store"
)

// publicVis is the §13.10 standalone default a no-identity-provider caller
// computes for bootstrap layers; the tests pass it explicitly.
var publicVis = layer.Visibility{Public: true}

// artifactBody is a minimal §4.5.1-shaped manifest the ingest
// pipeline accepts. type:context avoids the SKILL.md requirement
// that type:skill carries.
const artifactBody = "---\ntype: context\nversion: 1.0.0\ndescription: x\nsensitivity: low\n---\n\nbody\n"

func newMemoryStoreWithTenant(t testing.TB) store.Store {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateTenant(context.Background(), store.Tenant{ID: "default", Name: "default"}); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	return st
}

// Spec: §13.10 — PODIUM_LAYER_PATH is optional. When unset the
// bootstrap returns no layers and the registry boots empty, which
// preserves pre-PR behaviour.
func TestBootstrapLayerPath_EmptyReturnsEmpty(t *testing.T) {
	st := newMemoryStoreWithTenant(t)
	layers, err := bootstrapLayerPath(st, "default", "", publicVis, 0, true, nil, "", nil, false, false, nil)
	if err != nil {
		t.Fatalf("bootstrapLayerPath: %v", err)
	}
	if len(layers) != 0 {
		t.Errorf("layers = %v, want empty", layers)
	}
	cfgs, err := st.ListLayerConfigs(context.Background(), "default")
	if err != nil {
		t.Fatalf("ListLayerConfigs: %v", err)
	}
	if len(cfgs) != 0 {
		t.Errorf("LayerConfigs = %v, want empty", cfgs)
	}
}

// Spec: §13.10 — a missing path is a hard error so the operator
// notices the typo rather than booting with an empty registry.
func TestBootstrapLayerPath_MissingPathErrors(t *testing.T) {
	st := newMemoryStoreWithTenant(t)
	bogus := filepath.Join(t.TempDir(), "does-not-exist")
	_, err := bootstrapLayerPath(st, "default", bogus, publicVis, 0, true, nil, "", nil, false, false, nil)
	if err == nil {
		t.Fatal("err = nil, want filesystem.ErrConfigMissing")
	}
	if !errors.Is(err, filesystem.ErrConfigMissing) {
		t.Errorf("err = %v, want wrap of filesystem.ErrConfigMissing", err)
	}
}

// Spec: §13.10 / §13.11.1 — without .registry-config, the path is
// a single-layer directory. One LayerConfig persists with
// SourceType=local and LocalPath set to the resolved absolute path.
func TestBootstrapLayerPath_SingleLayerMode(t *testing.T) {
	st := newMemoryStoreWithTenant(t)
	root := t.TempDir()
	testharness.WriteTree(t, root, testharness.WriteTreeOption{
		Path:    "x/ARTIFACT.md",
		Content: artifactBody,
	})

	layers, err := bootstrapLayerPath(st, "default", root, publicVis, 0, true, nil, "", nil, false, false, nil)
	if err != nil {
		t.Fatalf("bootstrapLayerPath: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("layers = %v, want 1", layers)
	}
	if layers[0].Precedence != 1 {
		t.Errorf("Precedence = %d, want 1", layers[0].Precedence)
	}
	if !layers[0].Visibility.Public {
		t.Errorf("Visibility.Public = false, want true (§13.10 standalone default)")
	}

	cfgs, err := st.ListLayerConfigs(context.Background(), "default")
	if err != nil {
		t.Fatalf("ListLayerConfigs: %v", err)
	}
	if len(cfgs) != 1 {
		t.Fatalf("LayerConfigs = %v, want 1", cfgs)
	}
	got := cfgs[0]
	if got.SourceType != "local" {
		t.Errorf("SourceType = %q, want local", got.SourceType)
	}
	wantPath, _ := filepath.Abs(root)
	if got.LocalPath != wantPath {
		t.Errorf("LocalPath = %q, want %q", got.LocalPath, wantPath)
	}
	if got.Order != 1 {
		t.Errorf("Order = %d, want 1", got.Order)
	}
	if !got.Public {
		t.Errorf("Public = false, want true")
	}
	if got.ID == "" {
		t.Errorf("ID empty; want directory-name fallback")
	}

	// §7.3.1: GetLayerConfig finds the bootstrap layer by ID so a
	// downstream /v1/layers/reingest call can resolve it.
	resolved, err := st.GetLayerConfig(context.Background(), "default", got.ID)
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if resolved.LocalPath != wantPath {
		t.Errorf("GetLayerConfig.LocalPath = %q, want %q", resolved.LocalPath, wantPath)
	}

	// Sanity-check that ingest actually wrote manifests so the
	// /v1/search_artifacts path returns results out of the box.
	mans, err := st.ListManifests(context.Background(), "default")
	if err != nil {
		t.Fatalf("ListManifests: %v", err)
	}
	if len(mans) == 0 {
		t.Errorf("ingest produced no manifests")
	}
}

// Spec: §13.10 / §13.11.1 — with .registry-config (multi_layer: true),
// every subdirectory becomes a layer. Order is alphabetical when
// layer_order: is absent. Each layer persists a LayerConfig.
func TestBootstrapLayerPath_MultiLayerMode(t *testing.T) {
	st := newMemoryStoreWithTenant(t)
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{
			Path:    ".registry-config",
			Content: "multi_layer: true\n",
		},
		testharness.WriteTreeOption{
			Path:    "personal/x/ARTIFACT.md",
			Content: artifactBody,
		},
		testharness.WriteTreeOption{
			Path:    "team-shared/y/ARTIFACT.md",
			Content: artifactBody,
		},
	)

	layers, err := bootstrapLayerPath(st, "default", root, publicVis, 0, true, nil, "", nil, false, false, nil)
	if err != nil {
		t.Fatalf("bootstrapLayerPath: %v", err)
	}
	if len(layers) != 2 {
		t.Fatalf("layers = %v, want 2", layers)
	}
	// Alphabetical: personal, team-shared.
	wantIDs := []string{"personal", "team-shared"}
	for i, l := range layers {
		if l.ID != wantIDs[i] {
			t.Errorf("layers[%d].ID = %q, want %q", i, l.ID, wantIDs[i])
		}
		if l.Precedence != i+1 {
			t.Errorf("layers[%d].Precedence = %d, want %d", i, l.Precedence, i+1)
		}
	}

	cfgs, err := st.ListLayerConfigs(context.Background(), "default")
	if err != nil {
		t.Fatalf("ListLayerConfigs: %v", err)
	}
	if len(cfgs) != 2 {
		t.Fatalf("LayerConfigs len = %d, want 2", len(cfgs))
	}
	byID := map[string]store.LayerConfig{}
	for _, c := range cfgs {
		byID[c.ID] = c
	}
	for _, id := range wantIDs {
		c, ok := byID[id]
		if !ok {
			t.Errorf("LayerConfig %q missing", id)
			continue
		}
		if c.SourceType != "local" {
			t.Errorf("%q SourceType = %q, want local", id, c.SourceType)
		}
		wantPath, _ := filepath.Abs(filepath.Join(root, id))
		if c.LocalPath != wantPath {
			t.Errorf("%q LocalPath = %q, want %q", id, c.LocalPath, wantPath)
		}
	}
}

// Spec: §13.10 / §13.11.1 — layer_order: in .registry-config
// overrides alphabetical ordering. Precedence follows the listed
// order (lowest-precedence first).
func TestBootstrapLayerPath_RespectsLayerOrder(t *testing.T) {
	st := newMemoryStoreWithTenant(t)
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{
			Path:    ".registry-config",
			Content: "multi_layer: true\nlayer_order:\n  - team-shared\n  - personal\n",
		},
		testharness.WriteTreeOption{
			Path:    "personal/x/ARTIFACT.md",
			Content: artifactBody,
		},
		testharness.WriteTreeOption{
			Path:    "team-shared/y/ARTIFACT.md",
			Content: artifactBody,
		},
	)

	layers, err := bootstrapLayerPath(st, "default", root, publicVis, 0, true, nil, "", nil, false, false, nil)
	if err != nil {
		t.Fatalf("bootstrapLayerPath: %v", err)
	}
	want := []string{"team-shared", "personal"}
	for i, l := range layers {
		if l.ID != want[i] {
			t.Errorf("layers[%d].ID = %q, want %q", i, l.ID, want[i])
		}
	}
}

// Spec: §13.10 — multi_layer: true with manifests at the top level
// is config.layer_path_ambiguous. Bootstrap surfaces the error so
// the server refuses to start.
func TestBootstrapLayerPath_AmbiguousMultiLayerErrors(t *testing.T) {
	st := newMemoryStoreWithTenant(t)
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{
			Path:    ".registry-config",
			Content: "multi_layer: true\n",
		},
		testharness.WriteTreeOption{
			Path:    "ARTIFACT.md",
			Content: artifactBody,
		},
	)

	_, err := bootstrapLayerPath(st, "default", root, publicVis, 0, true, nil, "", nil, false, false, nil)
	if err == nil {
		t.Fatal("err = nil, want filesystem.ErrLayerPathAmbiguous")
	}
	if !errors.Is(err, filesystem.ErrLayerPathAmbiguous) {
		t.Errorf("err = %v, want wrap of filesystem.ErrLayerPathAmbiguous", err)
	}
	// spec: §13.10 (F-13.10.3) — the refusal surfaces the documented
	// config.layer_path_ambiguous code and names the conflicting manifest.
	if got := err.Error(); !strings.Contains(got, "config.layer_path_ambiguous") {
		t.Errorf("err %q missing config.layer_path_ambiguous code", got)
	}
	if got := err.Error(); !strings.Contains(got, "ARTIFACT.md") {
		t.Errorf("err %q does not name the conflicting manifest", got)
	}
}

// Spec: §13.12 — LoadConfig reads PODIUM_LAYER_PATH into
// Config.layerPath so Run() can pass it to bootstrapLayerPath.
func TestLoadConfig_ReadsLayerPathFromEnv(t *testing.T) {
	t.Setenv("PODIUM_LAYER_PATH", "/tmp/podium-test-layers")
	t.Setenv("PODIUM_CONFIG_FILE", filepath.Join(t.TempDir(), "missing.yaml"))
	c := LoadConfig()
	if c.layerPath != "/tmp/podium-test-layers" {
		t.Errorf("layerPath = %q, want /tmp/podium-test-layers", c.layerPath)
	}
}

// Spec: §13.12 — env beats YAML. PODIUM_LAYER_PATH wins when both
// are set.
func TestApplyYAML_LayerPathEnvWins(t *testing.T) {
	c := &Config{layerPath: "/from/env"}
	applyYAML(c, &yamlConfig{LayerPath: "/from/yaml"})
	if c.layerPath != "/from/env" {
		t.Errorf("layerPath = %q, want /from/env (env beats yaml)", c.layerPath)
	}
}

// Spec: §13.12 — YAML fills the layer path only when the env var
// left Config.layerPath empty.
func TestApplyYAML_LayerPathFillsWhenEmpty(t *testing.T) {
	c := &Config{layerPath: ""}
	applyYAML(c, &yamlConfig{LayerPath: "/from/yaml"})
	if c.layerPath != "/from/yaml" {
		t.Errorf("layerPath = %q, want /from/yaml", c.layerPath)
	}
}

// Spec: §13.12 — readYAMLConfig parses the top-level layer_path key so
// the standalone server can be configured via registry.yaml.
func TestReadYAMLConfig_ParsesLayerPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.yaml")
	body := []byte("registry:\n  layer_path: /var/podium/artifacts\n")
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
	if y.LayerPath != "/var/podium/artifacts" {
		t.Errorf("LayerPath = %q, want /var/podium/artifacts", y.LayerPath)
	}
}

// Spec: §13.12 — Config.Settings() surfaces layers.path so
// `podium config show` reveals the bootstrap path. Source is the
// env var name when set, otherwise registry.yaml.
func TestSettings_IncludesLayerPath(t *testing.T) {
	t.Setenv("PODIUM_LAYER_PATH", "/from/env")
	c := &Config{layerPath: "/from/env"}
	found := false
	for _, s := range c.Settings() {
		if s.Name == "layers.path" {
			found = true
			if s.Value != "/from/env" {
				t.Errorf("layers.path value = %q, want /from/env", s.Value)
			}
			if s.Source != "PODIUM_LAYER_PATH" {
				t.Errorf("layers.path source = %q, want PODIUM_LAYER_PATH", s.Source)
			}
		}
	}
	if !found {
		t.Error("Settings() missing layers.path entry")
	}
}

func TestSettings_LayerPathSourceYAMLWhenEnvUnset(t *testing.T) {
	t.Setenv("PODIUM_LAYER_PATH", "")
	c := &Config{layerPath: "/from/yaml"}
	for _, s := range c.Settings() {
		if s.Name == "layers.path" {
			if s.Source != "registry.yaml" {
				t.Errorf("source = %q, want registry.yaml", s.Source)
			}
			return
		}
	}
	t.Error("Settings() missing layers.path entry")
}
