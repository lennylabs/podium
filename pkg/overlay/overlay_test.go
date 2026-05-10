package overlay

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/internal/testharness"
)

// Spec: §6.4 — Filesystem provider with a populated overlay returns
// records via the filesystem registry walker.
func TestFilesystem_PopulatedOverlay(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testharness.WriteTree(t, root,
		testharness.WriteTreeOption{
			Path:    "x/ARTIFACT.md",
			Content: "---\ntype: context\nversion: 1.0.0\n---\n\nbody\n",
		},
	)
	got, err := (Filesystem{Path: root}).Resolve(context.Background())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) != 1 || got[0].ID != "x" {
		t.Errorf("got %v", got)
	}
}

// Spec: §6.4 — empty path returns ErrNoOverlay.
func TestFilesystem_EmptyPath(t *testing.T) {
	t.Parallel()
	_, err := (Filesystem{}).Resolve(context.Background())
	if !errors.Is(err, ErrNoOverlay) {
		t.Errorf("got %v, want ErrNoOverlay", err)
	}
}

// Spec: §6.4 path resolution — env var wins over workspace fallback.
func TestResolveWorkspaceOverlay_EnvPrecedence(t *testing.T) {
	t.Parallel()
	got, err := ResolveWorkspaceOverlay("/workspace", "/explicit/env")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "/explicit/env" {
		t.Errorf("got %q, want /explicit/env", got)
	}
}

// Spec: §6.4 — fallback to <workspace>/.podium/overlay when it exists.
func TestResolveWorkspaceOverlay_FallbackExists(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	testharness.WriteTree(t, ws, testharness.WriteTreeOption{
		Path:    ".podium/overlay/dummy.txt",
		Content: "x",
	})
	got, err := ResolveWorkspaceOverlay(ws, "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != filepath.Join(ws, ".podium", "overlay") {
		t.Errorf("got %q", got)
	}
}

// Spec: §6.4 — workspace without an overlay directory returns ErrNoOverlay.
func TestResolveWorkspaceOverlay_NoFallback(t *testing.T) {
	t.Parallel()
	_, err := ResolveWorkspaceOverlay(t.TempDir(), "")
	if !errors.Is(err, ErrNoOverlay) {
		t.Errorf("got %v, want ErrNoOverlay", err)
	}
}
