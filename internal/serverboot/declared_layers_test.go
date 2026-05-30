package serverboot

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
)

// declaredLocalTree writes a single-artifact local layer rooted at a fresh
// temp dir and returns the absolute path.
func declaredLocalTree(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	testharness.WriteTree(t, dir, testharness.WriteTreeOption{
		Path:    name + "/ARTIFACT.md",
		Content: artifactBody,
	})
	return dir
}

// Spec: §4.6 (F-4.6.8) — the declarative `layers:` list seeds a LayerConfig
// per entry, ingests local sources at startup, and feeds the in-memory layer
// list with the declared visibility.
func TestBootstrapDeclaredLayers_LocalIngestsAndSeeds(t *testing.T) {
	st := newMemoryStoreWithTenant(t)
	path := declaredLocalTree(t, "policy")
	cfg := &Config{
		identityProvider: "oidc",
		declaredLayers: []yamlLayerEntry{{
			ID:         "org-defaults",
			Source:     yamlLayerSource{Local: &yamlLocalSource{Path: path}},
			Visibility: yamlLayerVisibility{Organization: true},
		}},
	}
	layers, err := bootstrapDeclaredLayers(st, "default", cfg, nil)
	if err != nil {
		t.Fatalf("bootstrapDeclaredLayers: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("layers = %v, want 1", layers)
	}
	if layers[0].ID != "org-defaults" || layers[0].Precedence != 1 {
		t.Errorf("layer = %+v, want id=org-defaults precedence=1", layers[0])
	}
	if !layers[0].Visibility.Organization || layers[0].Visibility.Public {
		t.Errorf("Visibility = %+v, want organization-only", layers[0].Visibility)
	}

	lc, err := st.GetLayerConfig(context.Background(), "default", "org-defaults")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if lc.SourceType != "local" || lc.LocalPath != path {
		t.Errorf("LayerConfig source = %q/%q, want local/%q", lc.SourceType, lc.LocalPath, path)
	}
	if !lc.Organization || lc.Public {
		t.Errorf("LayerConfig visibility = %+v, want organization-only", lc)
	}

	mans, err := st.ListManifests(context.Background(), "default")
	if err != nil {
		t.Fatalf("ListManifests: %v", err)
	}
	if len(mans) == 0 {
		t.Errorf("declared local layer produced no manifests")
	}
}

// Spec: §4.6 / §13.10 (F-4.6.8) — a git source is seeded as a config row but
// not cloned at startup (a clone is unbounded network I/O that must not block
// boot); the §7.3.1 reingest/webhook path pulls it later.
func TestBootstrapDeclaredLayers_GitSeedsWithoutClone(t *testing.T) {
	st := newMemoryStoreWithTenant(t)
	cfg := &Config{
		declaredLayers: []yamlLayerEntry{{
			ID:         "team-finance",
			Source:     yamlLayerSource{Git: &yamlGitSource{Repo: "git@github.com:acme/x.git", Ref: "main", Root: "artifacts/"}},
			Visibility: yamlLayerVisibility{Groups: []string{"acme-finance"}},
		}},
	}
	layers, err := bootstrapDeclaredLayers(st, "default", cfg, nil)
	if err != nil {
		t.Fatalf("bootstrapDeclaredLayers: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("layers = %v, want 1", layers)
	}
	lc, err := st.GetLayerConfig(context.Background(), "default", "team-finance")
	if err != nil {
		t.Fatalf("GetLayerConfig: %v", err)
	}
	if lc.SourceType != "git" || lc.Repo != "git@github.com:acme/x.git" || lc.Ref != "main" || lc.Root != "artifacts/" {
		t.Errorf("git LayerConfig = %+v", lc)
	}
	if len(lc.Groups) != 1 || lc.Groups[0] != "acme-finance" {
		t.Errorf("git LayerConfig groups = %v, want [acme-finance]", lc.Groups)
	}
	mans, err := st.ListManifests(context.Background(), "default")
	if err != nil {
		t.Fatalf("ListManifests: %v", err)
	}
	if len(mans) != 0 {
		t.Errorf("git layer was ingested at boot (%d manifests); ingest must defer to reingest", len(mans))
	}
}

// Spec: §4.6 (F-4.6.8) — a declared layer with an empty visibility block falls
// back to the deployment default (IdP + private → no public grant).
func TestBootstrapDeclaredLayers_EmptyVisibilityUsesDefault(t *testing.T) {
	st := newMemoryStoreWithTenant(t)
	path := declaredLocalTree(t, "policy")
	cfg := &Config{
		identityProvider:       "oidc",
		defaultLayerVisibility: "private",
		declaredLayers: []yamlLayerEntry{{
			ID:     "no-vis",
			Source: yamlLayerSource{Local: &yamlLocalSource{Path: path}},
		}},
	}
	layers, err := bootstrapDeclaredLayers(st, "default", cfg, nil)
	if err != nil {
		t.Fatalf("bootstrapDeclaredLayers: %v", err)
	}
	if layers[0].Visibility.Public {
		t.Errorf("empty visibility under IdP+private must not be public: %+v", layers[0].Visibility)
	}
}

// Spec: §4.6 (F-4.6.8) — multiple entries take orders 1..N in config order
// (lowest precedence first).
func TestBootstrapDeclaredLayers_OrdersByListPosition(t *testing.T) {
	st := newMemoryStoreWithTenant(t)
	cfg := &Config{
		identityProvider: "",
		declaredLayers: []yamlLayerEntry{
			{ID: "first", Source: yamlLayerSource{Local: &yamlLocalSource{Path: declaredLocalTree(t, "a")}}},
			{ID: "second", Source: yamlLayerSource{Local: &yamlLocalSource{Path: declaredLocalTree(t, "b")}}},
		},
	}
	layers, err := bootstrapDeclaredLayers(st, "default", cfg, nil)
	if err != nil {
		t.Fatalf("bootstrapDeclaredLayers: %v", err)
	}
	if len(layers) != 2 || layers[0].ID != "first" || layers[0].Precedence != 1 ||
		layers[1].ID != "second" || layers[1].Precedence != 2 {
		t.Errorf("layers = %+v, want first@1 then second@2", layers)
	}
}

// Spec: §4.6 (F-4.6.8) — invalid declared entries abort startup so the
// operator notices the misconfiguration.
func TestBootstrapDeclaredLayers_ValidationErrors(t *testing.T) {
	cases := []struct {
		name  string
		entry yamlLayerEntry
	}{
		{"missing id", yamlLayerEntry{Source: yamlLayerSource{Local: &yamlLocalSource{Path: "/x"}}}},
		{"no source", yamlLayerEntry{ID: "x"}},
		{"both sources", yamlLayerEntry{ID: "x", Source: yamlLayerSource{Git: &yamlGitSource{Repo: "r"}, Local: &yamlLocalSource{Path: "/x"}}}},
		{"git no repo", yamlLayerEntry{ID: "x", Source: yamlLayerSource{Git: &yamlGitSource{Ref: "main"}}}},
		{"local no path", yamlLayerEntry{ID: "x", Source: yamlLayerSource{Local: &yamlLocalSource{}}}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			st := newMemoryStoreWithTenant(t)
			cfg := &Config{declaredLayers: []yamlLayerEntry{c.entry}}
			if _, err := bootstrapDeclaredLayers(st, "default", cfg, nil); err == nil {
				t.Errorf("err = nil, want a validation error for %s", c.name)
			}
		})
	}
}

// Spec: §4.6 (F-4.6.8) — readYAMLConfig parses the full layers: list with
// source (git/local) and visibility blocks per the §4.6 config schema.
func TestReadYAMLConfig_ParsesLayersList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.yaml")
	body := []byte(`registry:
  layers:
    - id: org-defaults
      source:
        git:
          repo: git@github.com:acme/org.git
          ref: main
          root: artifacts/
      visibility:
        organization: true
    - id: dev-finance
      source:
        local:
          path: /var/podium/dev/finance
      visibility:
        users: [alice@acme.com]
`)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("PODIUM_CONFIG_FILE", path)
	y, err := readYAMLConfig()
	if err != nil {
		t.Fatalf("readYAMLConfig: %v", err)
	}
	if y == nil || len(y.Layers) != 2 {
		t.Fatalf("Layers = %+v, want 2 entries", y)
	}
	if y.Layers[0].ID != "org-defaults" || y.Layers[0].Source.Git == nil ||
		y.Layers[0].Source.Git.Repo != "git@github.com:acme/org.git" || y.Layers[0].Source.Git.Root != "artifacts/" {
		t.Errorf("layer[0] = %+v", y.Layers[0])
	}
	if !y.Layers[0].Visibility.Organization {
		t.Errorf("layer[0] visibility = %+v, want organization", y.Layers[0].Visibility)
	}
	if y.Layers[1].Source.Local == nil || y.Layers[1].Source.Local.Path != "/var/podium/dev/finance" {
		t.Errorf("layer[1] source = %+v", y.Layers[1].Source)
	}
	if len(y.Layers[1].Visibility.Users) != 1 || y.Layers[1].Visibility.Users[0] != "alice@acme.com" {
		t.Errorf("layer[1] users = %v", y.Layers[1].Visibility.Users)
	}
}

// Spec: §4.6 (F-4.6.8) — applyYAML carries the parsed layers: list onto Config
// so Run() can seed them.
func TestApplyYAML_CarriesDeclaredLayers(t *testing.T) {
	c := &Config{}
	applyYAML(c, &yamlConfig{Layers: []yamlLayerEntry{
		{ID: "a", Source: yamlLayerSource{Local: &yamlLocalSource{Path: "/x"}}},
	}})
	if len(c.declaredLayers) != 1 || c.declaredLayers[0].ID != "a" {
		t.Errorf("declaredLayers = %+v, want 1 entry id=a", c.declaredLayers)
	}
}
