package main

import (
	"testing"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/sign"
)

// loadArtifactFromOverlay should return a load_artifact-shaped map
// directly from a filesystem overlay record, without consulting the
// registry.
func TestLoadArtifactFromOverlay_ReturnsLayerOverlay(t *testing.T) {
	t.Parallel()
	cache, _ := newContentCache(t.TempDir())
	s := &mcpServer{
		cfg: &config{
			harness:      "none",
			verifyPolicy: sign.PolicyNever,
		},
		cache:    cache,
		adapters: adapter.DefaultRegistry(),
	}
	rec := &filesystem.ArtifactRecord{
		ID:            "personal/hello/greet",
		ArtifactBytes: []byte("---\ntype: skill\nversion: 1.0.0\n---\n"),
		SkillBytes:    []byte("---\nname: greet\n---\nbody\n"),
		Artifact: &manifest.Artifact{
			Type:    manifest.TypeSkill,
			Version: "1.0.0",
			Body:    "body content",
		},
		Resources: map[string][]byte{"r.md": []byte("data")},
	}
	got := s.loadArtifactFromOverlay(rec, map[string]any{})
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("type = %T (%v)", got, got)
	}
	if m["id"] != "personal/hello/greet" {
		t.Errorf("id = %v", m["id"])
	}
	if m["layer"] != "overlay" {
		t.Errorf("layer = %v, want overlay", m["layer"])
	}
	if m["content_hash"] == "" {
		t.Errorf("content_hash empty")
	}
}

// When the deployment has a materialize root, loadArtifactFromOverlay
// adapts the artifact and writes harness-native files.
func TestLoadArtifactFromOverlay_MaterializesWhenConfigured(t *testing.T) {
	t.Parallel()
	cache, _ := newContentCache(t.TempDir())
	out := t.TempDir()
	s := &mcpServer{
		cfg: &config{
			harness:         "claude-code",
			materializeRoot: out,
			verifyPolicy:    sign.PolicyNever,
		},
		cache:    cache,
		adapters: adapter.DefaultRegistry(),
	}
	rec := &filesystem.ArtifactRecord{
		ID:            "personal/hello/greet",
		ArtifactBytes: []byte("---\ntype: skill\nversion: 1.0.0\nname: greet\ndescription: x\n---\n"),
		SkillBytes:    []byte("---\nname: greet\ndescription: x\n---\nhello\n"),
		Artifact: &manifest.Artifact{
			Type:        manifest.TypeSkill,
			Version:     "1.0.0",
			Name:        "greet",
			Description: "x",
		},
	}
	got := s.loadArtifactFromOverlay(rec, map[string]any{})
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("type = %T", got)
	}
	mats, _ := m["materialized_at"].([]string)
	if len(mats) == 0 {
		t.Errorf("expected at least one materialized path, got %v", m)
	}
}

// Unknown harness flag returns a config.unknown_harness error result.
func TestLoadArtifactFromOverlay_UnknownHarnessReturnsError(t *testing.T) {
	t.Parallel()
	cache, _ := newContentCache(t.TempDir())
	s := &mcpServer{
		cfg: &config{
			harness:         "none",
			materializeRoot: t.TempDir(),
			verifyPolicy:    sign.PolicyNever,
		},
		cache:    cache,
		adapters: adapter.DefaultRegistry(),
	}
	rec := &filesystem.ArtifactRecord{
		ID:            "x",
		ArtifactBytes: []byte("---\ntype: skill\n---\n"),
		Artifact:      &manifest.Artifact{Type: manifest.TypeSkill},
	}
	got := s.loadArtifactFromOverlay(rec, map[string]any{"harness": "definitely-not-real"})
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("type = %T", got)
	}
	if _, has := m["error"]; !has {
		t.Errorf("expected error key in %v", m)
	}
}

// harnessFromArgs picks the per-call override when set; falls back otherwise.
func TestHarnessFromArgs(t *testing.T) {
	t.Parallel()
	if got := harnessFromArgs("default", nil); got != "default" {
		t.Errorf("nil args: %q", got)
	}
	if got := harnessFromArgs("default", map[string]any{"harness": "claude-code"}); got != "claude-code" {
		t.Errorf("override: %q", got)
	}
	if got := harnessFromArgs("default", map[string]any{"harness": ""}); got != "default" {
		t.Errorf("empty: %q", got)
	}
	if got := harnessFromArgs("default", map[string]any{"harness": 7}); got != "default" {
		t.Errorf("non-string: %q", got)
	}
}
