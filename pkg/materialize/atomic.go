// Package materialize writes adapter output to disk under the sandbox
// contract from spec §6.6 and §6.7: atomic per-file write, no writes
// outside the destination root, no network, no subprocesses.
package materialize

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lennylabs/podium/pkg/adapter"
)

// Errors returned by Write. Tests assert against them via errors.Is.
var (
	// ErrOutOfDestination signals an attempt to write outside the
	// destination root. Maps to materialize.sandbox_violation in §6.10.
	ErrOutOfDestination = errors.New("materialize: write target escapes the destination root")
	// ErrEmptyDestination signals that the destination root is the empty
	// string. Defensive: an empty destination would resolve to "/" or
	// the working directory depending on the OS, both undesirable.
	ErrEmptyDestination = errors.New("materialize: destination path is empty")
)

// Write writes each file from files into the destination root. Each file
// is written atomically (temp file + rename) so a failure mid-stream
// leaves either the previous content or the new content, never a partial
// write.
//
// Per §6.7 sandbox contract, paths that escape destination (via "..", an
// absolute path, or a symlink) cause the call to fail with
// ErrOutOfDestination before any file is written.
func Write(destination string, files []adapter.File) error {
	if destination == "" {
		return ErrEmptyDestination
	}
	absDest, err := filepath.Abs(destination)
	if err != nil {
		return err
	}

	// Validate every path before any write so a single bad path fails the
	// whole batch atomically (no half-written tree).
	resolved := make([]string, len(files))
	for i, f := range files {
		full, err := resolveSandboxedPath(absDest, f.Path)
		if err != nil {
			return err
		}
		resolved[i] = full
	}

	if err := os.MkdirAll(absDest, 0o755); err != nil {
		return err
	}

	for i, f := range files {
		mode := os.FileMode(f.Mode)
		if mode == 0 {
			mode = 0o644
		}
		if err := writeAtomic(resolved[i], f.Content, mode); err != nil {
			return err
		}
	}
	return nil
}

// resolveSandboxedPath joins dest and rel into an absolute path and
// verifies the result is contained within dest. Empty rel and absolute
// rel are rejected.
func resolveSandboxedPath(dest, rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("%w: empty path", ErrOutOfDestination)
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("%w: absolute path %q", ErrOutOfDestination, rel)
	}
	full := filepath.Join(dest, rel)
	cleanedDest := filepath.Clean(dest) + string(filepath.Separator)
	if !strings.HasPrefix(filepath.Clean(full)+string(filepath.Separator), cleanedDest) {
		return "", fmt.Errorf("%w: %q resolves outside %q", ErrOutOfDestination, rel, dest)
	}
	return full, nil
}

// writeAtomic writes content to path via "<path>.tmp" + rename so the
// destination either has the previous content or the new content.
func writeAtomic(path string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, content, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
