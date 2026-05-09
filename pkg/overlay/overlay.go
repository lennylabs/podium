// Package overlay implements the LocalOverlayProvider SPI from spec §6.4.
// The workspace local overlay sits as the highest-precedence layer in
// the caller's effective view and is merged client-side by the MCP
// server, podium sync, and the SDK.
package overlay

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/lennylabs/podium/pkg/registry/filesystem"
)

// Errors returned by overlay functions.
var (
	// ErrNoOverlay signals that no overlay path was configured; the
	// caller should treat this as the layer being disabled per §6.4
	// path resolution.
	ErrNoOverlay = errors.New("overlay: no path configured")
)

// Provider is the SPI implementations satisfy.
type Provider interface {
	// Resolve returns the workspace overlay records, or ErrNoOverlay
	// when no overlay path is configured.
	Resolve(ctx context.Context) ([]filesystem.ArtifactRecord, error)
}

// Filesystem is the built-in overlay provider. Path is the workspace
// overlay directory, typically "<workspace>/.podium/overlay/".
type Filesystem struct {
	Path string
}

// Resolve opens the overlay directory as a single-layer filesystem
// registry and walks its artifacts. A missing or unset path returns
// ErrNoOverlay.
func (f Filesystem) Resolve(_ context.Context) ([]filesystem.ArtifactRecord, error) {
	if f.Path == "" {
		return nil, ErrNoOverlay
	}
	if _, err := os.Stat(f.Path); err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoOverlay
		}
		return nil, err
	}
	reg, err := filesystem.Open(f.Path)
	if err != nil {
		return nil, err
	}
	return reg.Walk(filesystem.WalkOptions{
		CollisionPolicy: filesystem.CollisionPolicyHighestWins,
	})
}

// ResolveWorkspaceOverlay applies the §6.4 path resolution rules:
//
//  1. PODIUM_OVERLAY_PATH if set.
//  2. <workspace>/.podium/overlay/ if it exists.
//  3. Otherwise: ErrNoOverlay (layer disabled).
func ResolveWorkspaceOverlay(workspace, env string) (string, error) {
	if env != "" {
		return env, nil
	}
	if workspace == "" {
		return "", ErrNoOverlay
	}
	candidate := filepath.Join(workspace, ".podium", "overlay")
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	return "", ErrNoOverlay
}
