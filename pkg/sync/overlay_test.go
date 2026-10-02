package sync_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/podium/pkg/sync"
	"github.com/lennylabs/podium/pkg/version"
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
// replaces the registry's contribution at the same canonical ID. The overlay
// is the §6.4 exception to the §4.6 collision rule, so the replacement needs
// no extends: and reports no drop.
func TestRun_OverlayOverridesRegistry(t *testing.T) {
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

// Spec: §6.4 — an overlay artifact that declares extends: replaces the
// same-ID registry-side artifact wholesale. The consumer resolves no extends:
// chain for an overlay artifact, so the registry copy's fields (tag a) never
// merge in, an unresolvable parent is not an error, and the §4.6 collision
// rule drops nothing.
func TestRun_OverlayWithExtendsReplacesWholesale(t *testing.T) {
	t.Parallel()
	registryBody := "---\ntype: context\nversion: 1.0.0\ndescription: base\nsensitivity: low\ntags: [a]\n---\n\nfrom registry\n"
	cases := map[string]string{
		"extends the registry artifact": "finance/intro",
		"extends a missing parent":      "missing/parent",
	}
	for name, parent := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			overlayBody := "---\ntype: context\nversion: 1.0.0\ndescription: overlay\nsensitivity: low\nextends: " +
				parent + "\ntags: [b]\n---\n\nfrom overlay\n"
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
			if len(res.Dropped) != 0 {
				t.Errorf("Dropped = %+v, want none for an overlay replacement", res.Dropped)
			}
			body, err := os.ReadFile(filepath.Join(target, "finance", "intro", "ARTIFACT.md"))
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			if string(body) != overlayBody {
				t.Errorf("ARTIFACT.md = %q, want the overlay's authored bytes %q", body, overlayBody)
			}
			if strings.Contains(string(body), "tags: [a]") || strings.Contains(string(body), "- a\n") {
				t.Errorf("registry tag a merged into the overlay artifact: %q", body)
			}
		})
	}
}

// Spec: §6.4 — overlay artifacts whose IDs are not in the registry
// are appended; nothing else is dropped.
func TestRun_OverlayAppendsNewArtifact(t *testing.T) {
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

// Spec: §4.7.6, §6.4, §7.5.3 — an overlay record's lock entry carries the
// canonical digest over the overlay's own ARTIFACT.md. applyOverlay builds its
// own record, so an implementation that drops the authored bytes there records
// the digest over an empty manifest instead.
func TestRun_OverlayRecordLockHashesItsAuthoredManifest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	registryBody := "---\ntype: context\nversion: 1.0.0\ndescription: registry\nsensitivity: low\n---\n\nfrom registry\n"
	overlayBody := "---\ntype: context\nversion: 2.0.0\ndescription: overlay\nsensitivity: low\n---\n\nfrom overlay\n"
	registry := makeRegistryWithBody(t, dir, registryBody)
	ovl := makeOverlayWithBody(t, dir, overlayBody)
	target := filepath.Join(dir, "out")
	if _, err := sync.Run(sync.Options{
		RegistryPath: registry,
		OverlayPath:  ovl,
		Target:       target,
		AdapterID:    "none",
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	lock, err := sync.ReadLock(target)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if lock == nil || len(lock.Artifacts) == 0 {
		t.Fatalf("lock missing artifacts: %+v", lock)
	}
	want := "sha256:" + version.CanonicalContentHash([]byte(overlayBody), nil, nil)
	empty := "sha256:" + version.CanonicalContentHash(nil, nil, nil)
	var seen int
	for _, la := range lock.Artifacts {
		if la.ID != "finance/intro" {
			continue
		}
		seen++
		if la.ContentHash == empty {
			t.Fatalf("lock %s content_hash is the digest over an empty manifest", la.ID)
		}
		if la.ContentHash != want {
			t.Errorf("lock %s content_hash = %q, want the overlay manifest digest %q", la.ID, la.ContentHash, want)
		}
	}
	if seen == 0 {
		t.Fatalf("lock has no finance/intro entry: %+v", lock.Artifacts)
	}
}
