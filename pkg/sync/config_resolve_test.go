package sync_test

import (
	"path/filepath"
	"testing"

	"github.com/lennylabs/podium/pkg/sync"
)

// Spec: §13.11.2 / §7.5.2 — relative `defaults.registry`
// resolves against the workspace.
func TestResolveRegistryPath_RelativeUsesWorkspace(t *testing.T) {
	t.Parallel()
	got := sync.ResolveRegistryPath("/home/dev/project", "./.podium/registry/")
	want := "/home/dev/project/.podium/registry"
	if filepath.Clean(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Spec: §13.11.2 — absolute `defaults.registry` passes through.
func TestResolveRegistryPath_AbsolutePassesThrough(t *testing.T) {
	t.Parallel()
	got := sync.ResolveRegistryPath("/home/dev/project", "/opt/podium-artifacts/")
	if got != "/opt/podium-artifacts" {
		t.Errorf("got %q, want /opt/podium-artifacts", got)
	}
}

// Spec: §13.11.2 — http(s):// URLs pass through unchanged so the
// caller can distinguish filesystem from server source by
// inspecting the scheme.
func TestResolveRegistryPath_URLPassesThrough(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"https://podium.example/", "http://localhost:8080"} {
		if got := sync.ResolveRegistryPath("/ws", in); got != in {
			t.Errorf("got %q for %q, want passthrough", got, in)
		}
	}
}

// Spec: §13.11.2 — empty input returns empty (callers detect
// "not configured" and surface config.no_registry separately).
func TestResolveRegistryPath_EmptyReturnsEmpty(t *testing.T) {
	t.Parallel()
	if got := sync.ResolveRegistryPath("/ws", ""); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}
