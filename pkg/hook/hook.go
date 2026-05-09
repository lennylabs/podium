// Package hook defines the MaterializationHook SPI from spec §9.1 / §6.6.
// Hooks run between the HarnessAdapter and the atomic write, with read +
// rewrite access to per-file bytes plus the manifest for context.
//
// Sandbox contract (§6.7): no network, no subprocess, no out-of-destination
// writes. Phase 13 ships the SPI plus a chain runner; production
// implementations register at boot.
package hook

import (
	"errors"
	"fmt"

	"github.com/lennylabs/podium/pkg/adapter"
)

// Errors returned by hook functions.
var (
	// ErrSandboxViolation maps to materialize.sandbox_violation in §6.10.
	ErrSandboxViolation = errors.New("hook: sandbox_violation")
)

// File is one (path, content) pair flowing through the chain. Each hook
// can mutate Content, drop the file (return Drop: true), or emit warnings.
type File struct {
	Path    string
	Content []byte
	Mode    uint32
	Drop    bool
}

// Result is one hook's output for a single file.
type Result struct {
	File     File
	Warnings []string
}

// Hook is the SPI implementations satisfy.
type Hook interface {
	// ID returns the hook identifier.
	ID() string
	// Apply transforms one file. Returning a non-nil error aborts the
	// chain; the materialization fails before any write.
	Apply(manifest map[string]any, file File) (Result, error)
}

// Run runs hooks in order over files. Each hook receives the previous
// hook's output. Files where Drop=true are removed from subsequent
// stages. The returned warnings are concatenated.
func Run(hooks []Hook, manifest map[string]any, files []adapter.File) ([]adapter.File, []string, error) {
	if len(hooks) == 0 {
		return files, nil, nil
	}
	current := make([]File, len(files))
	for i, f := range files {
		current[i] = File{Path: f.Path, Content: f.Content, Mode: f.Mode}
	}
	var warnings []string
	for _, h := range hooks {
		next := make([]File, 0, len(current))
		for _, f := range current {
			res, err := h.Apply(manifest, f)
			if err != nil {
				return nil, nil, fmt.Errorf("hook %s: %w", h.ID(), err)
			}
			warnings = append(warnings, res.Warnings...)
			if res.File.Drop {
				continue
			}
			next = append(next, res.File)
		}
		current = next
	}
	out := make([]adapter.File, len(current))
	for i, f := range current {
		out[i] = adapter.File{Path: f.Path, Content: f.Content, Mode: f.Mode}
	}
	return out, warnings, nil
}
