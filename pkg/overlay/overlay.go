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
	"sort"

	domainpkg "github.com/lennylabs/podium/pkg/domain"
	"github.com/lennylabs/podium/pkg/manifest"
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
	// Resolve returns the workspace overlay artifact records, or
	// ErrNoOverlay when no overlay path is configured.
	Resolve(ctx context.Context) ([]filesystem.ArtifactRecord, error)
	// ResolveDomains returns the workspace overlay DOMAIN.md set merged
	// across the overlay's layers per §4.5.4, keyed by canonical domain
	// path, or ErrNoOverlay when no overlay path is configured.
	ResolveDomains(ctx context.Context) (map[string]*manifest.Domain, error)
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

// ResolveDomains walks the overlay directory's DOMAIN.md files and merges
// the candidates for each domain path across the overlay's layers per §4.5.4
// (lowest precedence first). A workspace overlay is typically a single
// filesystem layer, so the merge is usually a passthrough; the cross-layer
// merge keeps a multi-layer overlay correct. A missing or unset path returns
// ErrNoOverlay, mirroring Resolve.
//
// spec: §6.4 — overlay DOMAIN.md files merge as the highest-precedence layer
// in the caller's effective view. The consumer that exposes load_domain
// applies the §4.5.4 merge of this set onto the registry result client-side
// (F-4.5.2, F-6.4.2).
func (f Filesystem) ResolveDomains(_ context.Context) (map[string]*manifest.Domain, error) {
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
	recs, err := reg.WalkDomains()
	if err != nil {
		return nil, err
	}
	byPath := map[string][]*manifest.Domain{}
	order := []string{}
	for _, rec := range recs {
		if _, seen := byPath[rec.Path]; !seen {
			order = append(order, rec.Path)
		}
		byPath[rec.Path] = append(byPath[rec.Path], rec.Domain)
	}
	sort.Strings(order)
	out := make(map[string]*manifest.Domain, len(order))
	for _, p := range order {
		out[p] = domainpkg.MergeAcrossLayers(byPath[p])
	}
	return out, nil
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
