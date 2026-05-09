package filesystem

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
)

// Spec: §13.11.1 Directory Layout — when .registry-config is absent, the
// path is a single local-source layer rooted at the path itself.
// Phase: 0
func TestOpen_NoConfig_SingleLayerMode(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{
			Path: "greetings/hello-world/SKILL.md",
			Content: `---
name: hello-world
description: Say hello.
---

Body.
`,
		},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if reg.Mode != ModeSingleLayer {
		t.Errorf("Mode = %s, want single-layer", reg.Mode)
	}
	if len(reg.Layers) != 1 {
		t.Fatalf("Layers length = %d, want 1", len(reg.Layers))
	}
	if reg.Layers[0].Path != root {
		t.Errorf("Layer path = %q, want %q", reg.Layers[0].Path, root)
	}
	if reg.Layers[0].ID != filepath.Base(root) {
		t.Errorf("Layer ID = %q, want %q", reg.Layers[0].ID, filepath.Base(root))
	}
}

// Spec: §13.11.1 — multi_layer: false in .registry-config is equivalent to
// no config: the path is a single layer.
// Phase: 0
func TestOpen_MultiLayerFalse_SingleLayerMode(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{
			Path:    ".registry-config",
			Content: "multi_layer: false\n",
		},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if reg.Mode != ModeSingleLayer {
		t.Errorf("Mode = %s, want single-layer", reg.Mode)
	}
}

// Spec: §13.11.1 — multi_layer: true treats each subdirectory as a layer;
// alphabetical order in the absence of layer_order.
// Phase: 0
func TestOpen_MultiLayer_AlphabeticalOrder(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{
			Path:    ".registry-config",
			Content: "multi_layer: true\n",
		},
		testharness.WriteTreeOption{
			Path:    "team-shared/greetings/hello/ARTIFACT.md",
			Content: "---\ntype: skill\nversion: 1.0.0\n---\nbody\n",
		},
		testharness.WriteTreeOption{
			Path:    "personal/notes/x/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 1.0.0\n---\nbody\n",
		},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if reg.Mode != ModeMultiLayer {
		t.Errorf("Mode = %s, want multi-layer", reg.Mode)
	}
	want := []string{"personal", "team-shared"}
	if len(reg.Layers) != len(want) {
		t.Fatalf("got %d layers, want %d", len(reg.Layers), len(want))
	}
	for i, w := range want {
		if reg.Layers[i].ID != w {
			t.Errorf("Layers[%d].ID = %q, want %q", i, reg.Layers[i].ID, w)
		}
	}
}

// Spec: §13.11.1 — layer_order: in .registry-config overrides alphabetical
// order; layers listed in layer_order: come first in declared order.
// Phase: 0
func TestOpen_MultiLayer_LayerOrderRespected(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{
			Path: ".registry-config",
			Content: `multi_layer: true
layer_order:
  - team-shared
  - personal
`,
		},
		testharness.WriteTreeOption{
			Path:    "team-shared/x/ARTIFACT.md",
			Content: "---\ntype: skill\nversion: 1.0.0\n---\nbody\n",
		},
		testharness.WriteTreeOption{
			Path:    "personal/y/ARTIFACT.md",
			Content: "---\ntype: skill\nversion: 1.0.0\n---\nbody\n",
		},
		testharness.WriteTreeOption{
			Path:    "extras/z/ARTIFACT.md",
			Content: "---\ntype: skill\nversion: 1.0.0\n---\nbody\n",
		},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	want := []string{"team-shared", "personal", "extras"}
	if len(reg.Layers) != len(want) {
		t.Fatalf("got %d layers, want %d", len(reg.Layers), len(want))
	}
	for i, w := range want {
		if reg.Layers[i].ID != w {
			t.Errorf("Layers[%d].ID = %q, want %q", i, reg.Layers[i].ID, w)
		}
	}
}

// Spec: §13.10 --layer-path modes — multi_layer: true with manifest files
// at the top level of <path> fails with config.layer_path_ambiguous.
// Phase: 0
func TestOpen_MultiLayer_AmbiguousFails(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{
			Path:    ".registry-config",
			Content: "multi_layer: true\n",
		},
		testharness.WriteTreeOption{
			Path:    "ARTIFACT.md",
			Content: "---\ntype: skill\nversion: 1.0.0\n---\nbody\n",
		},
	)
	_, err := Open(root)
	if !errors.Is(err, ErrLayerPathAmbiguous) {
		t.Fatalf("got %v, want ErrLayerPathAmbiguous", err)
	}
}

// Spec: §13.11.1 — invalid YAML in .registry-config fails with
// ErrConfigInvalid (config.invalid namespace from §6.10).
// Phase: 0
func TestOpen_InvalidConfig(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{
			Path:    ".registry-config",
			Content: "multi_layer: [not a bool",
		},
	)
	_, err := Open(root)
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("got %v, want ErrConfigInvalid", err)
	}
}

// Spec: §13.11 — a missing path returns ErrConfigMissing rather than a raw
// os error, so callers can map it to the structured error envelope.
// Phase: 0
func TestOpen_MissingPath(t *testing.T) {
	t.Parallel()
	_, err := Open(filepath.Join(t.TempDir(), "does-not-exist"))
	if !errors.Is(err, ErrConfigMissing) {
		t.Fatalf("got %v, want ErrConfigMissing", err)
	}
}

// Spec: §13.11.1 — dot-directories (.git, .ci) are not treated as layers
// in multi-layer mode.
// Phase: 0
func TestOpen_MultiLayer_SkipsDotDirs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{
			Path:    ".registry-config",
			Content: "multi_layer: true\n",
		},
		testharness.WriteTreeOption{
			Path:    ".git/HEAD",
			Content: "ref: refs/heads/main\n",
		},
		testharness.WriteTreeOption{
			Path:    "team-shared/x/ARTIFACT.md",
			Content: "---\ntype: skill\nversion: 1.0.0\n---\nbody\n",
		},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(reg.Layers) != 1 || reg.Layers[0].ID != "team-shared" {
		t.Errorf("got layers %+v, want one (team-shared)", reg.Layers)
	}
}
