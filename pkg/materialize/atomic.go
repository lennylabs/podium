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
	// ErrRuntimeUnavailable signals that the host cannot satisfy a
	// runtime_requirement declared by an artifact (§4.4.1). Maps to
	// materialize.runtime_unavailable in §6.10.
	ErrRuntimeUnavailable = errors.New("materialize.runtime_unavailable")
)

// HostCapabilities describes what the host can actually run. The
// MCP server checks an artifact's runtime_requirements against these
// before materializing per §4.4.1.
type HostCapabilities struct {
	// Python is the host-installed Python version (e.g., "3.11.4").
	Python string
	// Node is the host-installed Node version.
	Node string
	// SystemPackages is the set of installed system packages the
	// host advertises (e.g., "jq", "curl").
	SystemPackages []string
}

// CheckRuntimeRequirements reports whether the host can satisfy req.
// Returns nil when satisfied, ErrRuntimeUnavailable wrapping a
// description of the unmet requirement otherwise.
func CheckRuntimeRequirements(req map[string]any, host HostCapabilities) error {
	if len(req) == 0 {
		return nil
	}
	// Python requirement: minimum version. We compare lexicographically
	// after normalizing semver-style strings; for spec correctness this
	// is sufficient because version strings follow major.minor.patch.
	if want, ok := req["python"].(string); ok && want != "" {
		if host.Python == "" {
			return wrapRuntime("python required (%s) but host has none", want)
		}
		if !satisfiesVersion(host.Python, want) {
			return wrapRuntime("host python %s does not satisfy %s", host.Python, want)
		}
	}
	if want, ok := req["node"].(string); ok && want != "" {
		if host.Node == "" {
			return wrapRuntime("node required (%s) but host has none", want)
		}
		if !satisfiesVersion(host.Node, want) {
			return wrapRuntime("host node %s does not satisfy %s", host.Node, want)
		}
	}
	if pkgs, ok := req["system_packages"].([]string); ok {
		for _, p := range pkgs {
			if !containsString(host.SystemPackages, p) {
				return wrapRuntime("required system package %q not installed", p)
			}
		}
	}
	return nil
}

func wrapRuntime(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrRuntimeUnavailable}, args...)...)
}

// satisfiesVersion is a small >= check on semver-style version
// strings. The require string supports the ">=X.Y" form the spec uses
// for runtime_requirements (see §4.3 caller-interpreted fields). For
// any other shape, equality is required.
func satisfiesVersion(have, require string) bool {
	require = trimSpace(require)
	have = trimSpace(have)
	if require == "" {
		return true
	}
	if have == "" {
		return false
	}
	if hasPrefix(require, ">=") {
		min := trimSpace(require[2:])
		return compareVersions(have, min) >= 0
	}
	return have == require
}

func compareVersions(a, b string) int {
	aParts := splitVersion(a)
	bParts := splitVersion(b)
	for i := 0; i < len(aParts) || i < len(bParts); i++ {
		x := 0
		y := 0
		if i < len(aParts) {
			x = aParts[i]
		}
		if i < len(bParts) {
			y = bParts[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func splitVersion(s string) []int {
	out := []int{}
	cur := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			cur = cur*10 + int(r-'0')
		} else if r == '.' {
			out = append(out, cur)
			cur = 0
		} else {
			break
		}
	}
	return append(out, cur)
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

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
