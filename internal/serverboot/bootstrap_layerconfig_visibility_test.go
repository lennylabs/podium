package serverboot

import (
	"context"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/layer"
)

// Spec: §4.6 — a PODIUM_LAYER_PATH bootstrap layer that declares visibility
// in its .layer-config must boot with the declared visibility, not the
// bootstrap default. The filesystem registry parses the file into
// Layer.Visibility / Layer.HasVisibility; bootstrapLayerPath applies it.
// A sibling layer without a .layer-config keeps the bootstrap default, as
// the filesystem.Layer doc comment specifies.
func TestBootstrapLayerPath_HonorsLayerConfigVisibility(t *testing.T) {
	st := newMemoryStoreWithTenant(t)
	root := t.TempDir()
	// Multi-layer registry: "fin" declares groups:[finance]; "open" has no
	// .layer-config and inherits the bootstrap default (public here).
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{Path: ".registry-config", Content: "multi_layer: true\nlayer_order: [open, fin]\n"},
		testharness.WriteTreeOption{Path: "open/a/ARTIFACT.md", Content: artifactBody},
		testharness.WriteTreeOption{Path: "fin/b/ARTIFACT.md", Content: artifactBody},
		testharness.WriteTreeOption{Path: "fin/.layer-config", Content: "visibility:\n  groups:\n    - finance\n"},
	)

	// Bootstrap default is public (no-identity standalone). The declared
	// layer must override it; the undeclared layer must keep it.
	layers, err := bootstrapLayerPath(st, "default", root, layer.Visibility{Public: true}, 0, true, nil, "", nil, false, collocatedVectorIngest{}, false, nil)
	if err != nil {
		t.Fatalf("bootstrapLayerPath: %v", err)
	}

	byID := map[string]layer.Layer{}
	for _, l := range layers {
		byID[l.ID] = l
	}
	fin, ok := byID["fin"]
	if !ok {
		t.Fatalf("layer fin missing; got %v", layers)
	}
	if fin.Visibility.Public || fin.Visibility.Organization || len(fin.Visibility.Groups) != 1 || fin.Visibility.Groups[0] != "finance" {
		t.Errorf("fin in-memory visibility = %+v, want groups=[finance] only", fin.Visibility)
	}
	open, ok := byID["open"]
	if !ok {
		t.Fatalf("layer open missing; got %v", layers)
	}
	if !open.Visibility.Public {
		t.Errorf("open in-memory visibility = %+v, want public (bootstrap default)", open.Visibility)
	}

	// The persisted LayerConfig must match what was applied in memory.
	cfgs, err := st.ListLayerConfigs(context.Background(), "default")
	if err != nil {
		t.Fatalf("ListLayerConfigs: %v", err)
	}
	cfgByID := map[string]struct {
		public bool
		groups []string
	}{}
	for _, c := range cfgs {
		cfgByID[c.ID] = struct {
			public bool
			groups []string
		}{c.Public, c.Groups}
	}
	if got := cfgByID["fin"]; got.public || len(got.groups) != 1 || got.groups[0] != "finance" {
		t.Errorf("persisted fin config = %+v, want public=false groups=[finance]", got)
	}
	if got := cfgByID["open"]; !got.public {
		t.Errorf("persisted open config = %+v, want public=true", got)
	}
}

// Spec: §4.6 — a .layer-config that is present but declares an empty
// visibility block carries no filters, so the bootstrap treats it like a
// layer that declares nothing: it falls back to the deployment default
// rather than booting visible to no one. This keeps the filesystem bootstrap
// consistent with the declarative `visibility:` path (layerConfigFromEntry),
// which also defaults an empty block.
func TestBootstrapLayerPath_EmptyLayerConfigFallsBackToDefault(t *testing.T) {
	st := newMemoryStoreWithTenant(t)
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{Path: ".registry-config", Content: "multi_layer: true\n"},
		testharness.WriteTreeOption{Path: "lyr/a/ARTIFACT.md", Content: artifactBody},
		// File present, visibility block empty (no filters declared).
		testharness.WriteTreeOption{Path: "lyr/.layer-config", Content: "visibility:\n"},
	)

	layers, err := bootstrapLayerPath(st, "default", root, layer.Visibility{Public: true}, 0, true, nil, "", nil, false, collocatedVectorIngest{}, false, nil)
	if err != nil {
		t.Fatalf("bootstrapLayerPath: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("layers = %v, want 1", layers)
	}
	if !layers[0].Visibility.Public {
		t.Errorf("in-memory visibility = %+v, want public (bootstrap default for an empty .layer-config)", layers[0].Visibility)
	}

	cfgs, err := st.ListLayerConfigs(context.Background(), "default")
	if err != nil {
		t.Fatalf("ListLayerConfigs: %v", err)
	}
	if len(cfgs) != 1 || !cfgs[0].Public {
		t.Errorf("persisted config = %+v, want public=true", cfgs)
	}
}
