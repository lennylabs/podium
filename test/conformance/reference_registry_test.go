// Package conformance holds the cross-cutting suites that any built-in
// or community implementation runs against. Phase 19 ships the example
// artifact registry test that verifies the testdata/registries/reference
// fixture loads end-to-end through the production library code.
package conformance

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
	"github.com/lennylabs/podium/pkg/registry/filesystem"
	"github.com/lennylabs/podium/pkg/sync"
)

// referencePath returns the absolute path to testdata/registries/reference
// derived from this test file's location, so the test runs from any cwd.
func referencePath(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	return filepath.Join(root, "testdata", "registries", "reference")
}

// Spec: §11 Verification — the example artifact registry covers every
// first-class type, every visibility mode, and every adapter target;
// it loads end-to-end with no errors.
// Phase: 19
func TestReferenceRegistry_OpensAndWalks(t *testing.T) {
	testharness.RequirePhase(t, 19)
	t.Parallel()
	reg, err := filesystem.Open(referencePath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if reg.Mode != filesystem.ModeMultiLayer {
		t.Errorf("Mode = %s, want multi-layer", reg.Mode)
	}
	wantLayers := []string{"org-defaults", "_shared", "team-finance", "personal"}
	if len(reg.Layers) != len(wantLayers) {
		t.Fatalf("got %d layers, want %d", len(reg.Layers), len(wantLayers))
	}
	for i, want := range wantLayers {
		if reg.Layers[i].ID != want {
			t.Errorf("Layers[%d] = %q, want %q", i, reg.Layers[i].ID, want)
		}
	}

	records, err := reg.Walk(filesystem.WalkOptions{
		CollisionPolicy: filesystem.CollisionPolicyHighestWins,
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if len(records) == 0 {
		t.Fatalf("walked zero artifacts; reference registry should have content")
	}
}

// Spec: §11 — `podium sync` against the reference registry produces
// a valid materialized tree.
// Phase: 19
func TestReferenceRegistry_SyncsThroughNoneAdapter(t *testing.T) {
	testharness.RequirePhase(t, 19)
	t.Parallel()
	target := t.TempDir()
	res, err := sync.Run(sync.Options{
		RegistryPath: referencePath(t),
		Target:       target,
		AdapterID:    "none",
	})
	if err != nil {
		t.Fatalf("sync.Run: %v", err)
	}
	if len(res.Artifacts) == 0 {
		t.Errorf("no artifacts materialized")
	}
}

// Spec: §11 — claude-code adapter handles the reference registry's
// type-skill / type-context mix without errors.
// Phase: 19
func TestReferenceRegistry_SyncsThroughClaudeCode(t *testing.T) {
	testharness.RequirePhase(t, 19)
	t.Parallel()
	target := t.TempDir()
	res, err := sync.Run(sync.Options{
		RegistryPath: referencePath(t),
		Target:       target,
		AdapterID:    "claude-code",
	})
	if err != nil {
		t.Fatalf("sync.Run: %v", err)
	}
	if len(res.Artifacts) == 0 {
		t.Errorf("no artifacts materialized")
	}
	got := testharness.ReadTree(t, target)
	if _, ok := got[".claude/skills/welcome/SKILL.md"]; !ok {
		t.Errorf("expected .claude/skills/welcome/SKILL.md in target, got: %v", keys(got))
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
