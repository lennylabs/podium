package filesystem

import (
	"errors"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
)

// Spec: §4.6 / §11 — a layer's optional .layer-config declares visibility that
// a server bootstrap honors. A multi-layer registry reads each subdirectory's
// file and exposes the parsed visibility on the Layer.
func TestOpen_MultiLayer_ReadsLayerVisibility(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{Path: ".registry-config", Content: "multi_layer: true\n"},
		testharness.WriteTreeOption{Path: "pub/.layer-config", Content: "visibility:\n  public: true\n"},
		testharness.WriteTreeOption{Path: "pub/x/ARTIFACT.md", Content: "---\ntype: skill\nversion: 1.0.0\n---\nbody\n"},
		testharness.WriteTreeOption{Path: "org/.layer-config", Content: "visibility:\n  organization: true\n"},
		testharness.WriteTreeOption{Path: "org/y/ARTIFACT.md", Content: "---\ntype: skill\nversion: 1.0.0\n---\nbody\n"},
		testharness.WriteTreeOption{Path: "grp/.layer-config", Content: "visibility:\n  groups:\n    - acme-finance\n"},
		testharness.WriteTreeOption{Path: "grp/z/ARTIFACT.md", Content: "---\ntype: skill\nversion: 1.0.0\n---\nbody\n"},
		testharness.WriteTreeOption{Path: "usr/.layer-config", Content: "visibility:\n  users:\n    - alice\n"},
		testharness.WriteTreeOption{Path: "usr/w/ARTIFACT.md", Content: "---\ntype: skill\nversion: 1.0.0\n---\nbody\n"},
		// A layer with no .layer-config carries no explicit visibility.
		testharness.WriteTreeOption{Path: "bare/q/ARTIFACT.md", Content: "---\ntype: skill\nversion: 1.0.0\n---\nbody\n"},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	byID := map[string]Layer{}
	for _, l := range reg.Layers {
		byID[l.ID] = l
	}

	if got := byID["pub"]; !got.HasVisibility || !got.Visibility.Public {
		t.Errorf("pub layer: HasVisibility=%v Public=%v, want true/true", got.HasVisibility, got.Visibility.Public)
	}
	if got := byID["org"]; !got.HasVisibility || !got.Visibility.Organization {
		t.Errorf("org layer: HasVisibility=%v Organization=%v, want true/true", got.HasVisibility, got.Visibility.Organization)
	}
	if got := byID["grp"]; len(got.Visibility.Groups) != 1 || got.Visibility.Groups[0] != "acme-finance" {
		t.Errorf("grp layer: Groups=%v, want [acme-finance]", got.Visibility.Groups)
	}
	if got := byID["usr"]; len(got.Visibility.Users) != 1 || got.Visibility.Users[0] != "alice" {
		t.Errorf("usr layer: Users=%v, want [alice]", got.Visibility.Users)
	}
	if got := byID["bare"]; got.HasVisibility {
		t.Errorf("bare layer: HasVisibility=true, want false (no .layer-config)")
	}
}

// Spec: §13.11.1 — single-layer mode also reads a root .layer-config.
func TestOpen_SingleLayer_ReadsLayerVisibility(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{Path: ".layer-config", Content: "visibility:\n  organization: true\n"},
		testharness.WriteTreeOption{Path: "x/ARTIFACT.md", Content: "---\ntype: skill\nversion: 1.0.0\n---\nbody\n"},
	)
	reg, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if reg.Mode != ModeSingleLayer {
		t.Fatalf("Mode = %s, want single-layer", reg.Mode)
	}
	if len(reg.Layers) != 1 {
		t.Fatalf("got %d layers, want 1", len(reg.Layers))
	}
	if l := reg.Layers[0]; !l.HasVisibility || !l.Visibility.Organization {
		t.Errorf("single layer: HasVisibility=%v Organization=%v, want true/true", l.HasVisibility, l.Visibility.Organization)
	}
}

// Spec: §6.10 config.invalid — a malformed .layer-config fails Open.
func TestOpen_MalformedLayerConfig_Fails(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{Path: ".registry-config", Content: "multi_layer: true\n"},
		testharness.WriteTreeOption{Path: "bad/.layer-config", Content: "visibility: : not yaml\n  - broken\n"},
		testharness.WriteTreeOption{Path: "bad/x/ARTIFACT.md", Content: "---\ntype: skill\nversion: 1.0.0\n---\nbody\n"},
	)
	_, err := Open(root)
	if !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("Open err = %v, want ErrConfigInvalid", err)
	}
}
