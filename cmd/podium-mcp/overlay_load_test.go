package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/adapter"
	"github.com/lennylabs/podium/pkg/manifest"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/sign"
	"github.com/lennylabs/podium/pkg/version"
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
	// Spec: §4.7.6 — the overlay serves the canonical hash over the whole
	// package, the value the registry would store for the same bytes.
	want := "sha256:" + version.CanonicalContentHash(rec.ArtifactBytes, rec.SkillBytes, rec.Resources)
	if m["content_hash"] != want {
		t.Errorf("content_hash = %v, want %v", m["content_hash"], want)
	}
	// Spec: §6.5 — the overlay writes no content-cache bucket, so the
	// registry-served path is the single writer of that key.
	if s.cache.has(want) {
		t.Errorf("overlay wrote a content-cache bucket under %s", want)
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

// Spec: §6.4, §4.7.6 — a workspace overlay serves the canonical content hash
// over its whole package, so the served value moves when SKILL.md or a bundled
// resource changes, and the overlay writes no §6.5 bucket under it. The overlay
// response returns before the §6.6 pipeline, so nothing downstream re-derives
// this value.
func TestLoadArtifactFromOverlay_ServesTheCanonicalContentHash(t *testing.T) {
	t.Parallel()
	artifactBytes := []byte("---\ntype: skill\nversion: 1.0.0\nname: greet\ndescription: x\n---\n")
	skillBytes := []byte("---\nname: greet\ndescription: x\n---\n\nhello\n")
	resources := map[string][]byte{"references/notes.md": []byte("notes\n")}

	newRecord := func(skill []byte) *filesystem.ArtifactRecord {
		return &filesystem.ArtifactRecord{
			ID:            "personal/hello/greet",
			ArtifactBytes: artifactBytes,
			SkillBytes:    skill,
			Artifact: &manifest.Artifact{
				Type:    manifest.TypeSkill,
				Version: "1.0.0",
				Body:    "hello",
			},
			Resources: resources,
		}
	}
	load := func(t *testing.T, rec *filesystem.ArtifactRecord) (map[string]any, string) {
		t.Helper()
		dir := t.TempDir()
		cache, err := newContentCache(dir)
		if err != nil {
			t.Fatalf("newContentCache: %v", err)
		}
		s := &mcpServer{
			cfg: &config{
				harness:      "none",
				cacheDir:     dir,
				verifyPolicy: sign.PolicyNever,
			},
			cache:    cache,
			adapters: adapter.DefaultRegistry(),
		}
		got := s.loadArtifactFromOverlay(rec, map[string]any{})
		m, ok := got.(map[string]any)
		if !ok {
			t.Fatalf("loadArtifactFromOverlay = %T (%v), want map", got, got)
		}
		return m, dir
	}

	// The served hash is the canonical digest over the whole package.
	m, dir := load(t, newRecord(skillBytes))
	want := "sha256:" + version.CanonicalContentHash(artifactBytes, skillBytes, resources)
	if m["content_hash"] != want {
		t.Errorf("content_hash = %v, want %v", m["content_hash"], want)
	}

	// An edit to SKILL.md alone moves it. The pre-framing composition hashed
	// the manifest and the skill into one stream, so this is the regression no
	// existing test could see.
	edited := []byte("---\nname: greet\ndescription: x\n---\n\nhello again\n")
	m2, _ := load(t, newRecord(edited))
	if m2["content_hash"] == want {
		t.Errorf("content_hash did not move when SKILL.md changed: %v", m2["content_hash"])
	}

	// Spec: §6.5 — the overlay writes no content-cache bucket, so a promoted
	// overlay cannot clobber the bucket a registry-served load of the same
	// package writes under that key. Asserted on the directory: a clobbered
	// bucket still passes the §6.6 recomputation, so only its absence tells
	// the two writers apart.
	bucket := filepath.Join(dir, sanitizeHash(want))
	if _, err := os.Stat(bucket); !os.IsNotExist(err) {
		t.Errorf("os.Stat(%s) err = %v, want not-exist (the overlay wrote a §6.5 bucket)", bucket, err)
	}
}
