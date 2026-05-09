package sync_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/sync"
)

// makeRegistryWithFile is a small variant of makeRegistry that lets
// tests substitute the artifact body so overlay-vs-registry collisions
// can be observed in the materialized output.
func makeRegistryWithBody(t *testing.T, dir, body string) string {
	t.Helper()
	root := filepath.Join(dir, "registry")
	if err := os.MkdirAll(filepath.Join(root, "finance", "intro"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "finance", "intro", "ARTIFACT.md"),
		[]byte(body), 0o644,
	); err != nil {
		t.Fatalf("WriteFile artifact: %v", err)
	}
	return root
}

// makeOverlayWithBody writes a single overlay artifact at the same
// canonical id as the registry so the merge can be observed.
func makeOverlayWithBody(t *testing.T, dir, body string) string {
	t.Helper()
	root := filepath.Join(dir, "overlay")
	if err := os.MkdirAll(filepath.Join(root, "finance", "intro"), 0o755); err != nil {
		t.Fatalf("MkdirAll overlay: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "finance", "intro", "ARTIFACT.md"),
		[]byte(body), 0o644,
	); err != nil {
		t.Fatalf("WriteFile overlay artifact: %v", err)
	}
	return root
}

// Spec: §6.4 — workspace overlay sits at the highest precedence and
// replaces the registry's contribution at the same canonical ID.
// Phase: 12
func TestRun_OverlayOverridesRegistry(t *testing.T) {
	testharness.RequirePhase(t, 12)
	t.Parallel()
	dir := t.TempDir()
	registryBody := "---\ntype: context\nversion: 1.0.0\ndescription: registry\nsensitivity: low\n---\n\nfrom registry\n"
	overlayBody := "---\ntype: context\nversion: 1.0.0\ndescription: overlay\nsensitivity: low\n---\n\nfrom overlay\n"
	registry := makeRegistryWithBody(t, dir, registryBody)
	ovl := makeOverlayWithBody(t, dir, overlayBody)
	target := filepath.Join(dir, "out")
	res, err := sync.Run(sync.Options{
		RegistryPath: registry,
		OverlayPath:  ovl,
		Target:       target,
		AdapterID:    "none",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Artifacts) != 1 {
		t.Fatalf("Artifacts = %d, want 1", len(res.Artifacts))
	}
	body, err := os.ReadFile(filepath.Join(target, "finance", "intro", "ARTIFACT.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(body), "from overlay") {
		t.Errorf("materialized body did not contain overlay content: %q", body)
	}
	if strings.Contains(string(body), "from registry") {
		t.Errorf("materialized body still contains registry content: %q", body)
	}
}

// Spec: §6.4 — overlay artifacts whose IDs are not in the registry
// are appended; nothing else is dropped.
// Phase: 12
func TestRun_OverlayAppendsNewArtifact(t *testing.T) {
	testharness.RequirePhase(t, 12)
	t.Parallel()
	dir := t.TempDir()
	registryBody := "---\ntype: context\nversion: 1.0.0\ndescription: r\nsensitivity: low\n---\n\nregistry-only\n"
	registry := makeRegistryWithBody(t, dir, registryBody)
	// Overlay adds a different id at marketing/deck.
	ovl := filepath.Join(dir, "overlay")
	if err := os.MkdirAll(filepath.Join(ovl, "marketing", "deck"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(ovl, "marketing", "deck", "ARTIFACT.md"),
		[]byte("---\ntype: context\nversion: 1.0.0\ndescription: ovl-only\nsensitivity: low\n---\n\nfrom overlay\n"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile artifact: %v", err)
	}
	target := filepath.Join(dir, "out")
	res, err := sync.Run(sync.Options{
		RegistryPath: registry,
		OverlayPath:  ovl,
		Target:       target,
		AdapterID:    "none",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	ids := map[string]bool{}
	for _, a := range res.Artifacts {
		ids[a.ID] = true
	}
	if !ids["finance/intro"] || !ids["marketing/deck"] {
		t.Errorf("expected both registry+overlay ids, got %v", ids)
	}
}
