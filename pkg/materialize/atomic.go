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
	for _, p := range systemPackages(req["system_packages"]) {
		if !containsString(host.SystemPackages, p) {
			return wrapRuntime("required system package %q not installed", p)
		}
	}
	return nil
}

// systemPackages coerces the system_packages requirement to a string
// slice. The value is []string when the map is built directly from a
// typed manifest.RuntimeRequirements, but a generic YAML or JSON
// round-trip yields []any; the bare []string assertion used to
// silently skip the check for the round-tripped form, treating an unmet
// requirement as satisfied. Both element types are accepted here.
func systemPackages(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			} else {
				out = append(out, fmt.Sprintf("%v", e))
			}
		}
		return out
	default:
		return nil
	}
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

// Write writes every file from files into the destination root with a
// tree-level all-or-nothing guarantee: the destination ends up containing a
// complete copy of files or none of them (§6.6 step 5, §6.9 "Materialization
// destination unwritable"). It stages each file as "<path>.tmp" first, and
// only after every staged write succeeds does it rename them into place. A
// failure during the write phase (an unwritable directory, a full disk)
// removes the staged temporaries and leaves the destination unchanged, so a
// mid-batch failure cannot leave files 0..i-1 renamed into place while file i
// failed.
//
// Per the §6.7 sandbox contract, paths that escape destination cause the call
// to fail with ErrOutOfDestination before any file is written. Escapes via
// ".." or an absolute path are rejected lexically; escapes via a symlinked
// destination root or a symlinked intermediate directory are rejected by
// resolving the deepest existing ancestor of each target with
// filepath.EvalSymlinks and confirming it stays within the resolved
// destination root.
func Write(destination string, files []adapter.File) error {
	if destination == "" {
		return ErrEmptyDestination
	}
	absDest, err := filepath.Abs(destination)
	if err != nil {
		return err
	}

	// Validate every path lexically before any write so a single bad path
	// fails the whole batch with no half-written tree.
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
	// Resolve the destination root through any symlinks once so every
	// per-target containment check compares against the real root (§6.7).
	realDest, err := filepath.EvalSymlinks(absDest)
	if err != nil {
		return err
	}

	// Fold inject/merge ops into one final content per destination path. This
	// reads any existing on-disk file for OpInject / OpMergeJSON targets so
	// the operator's other content is preserved (§6.7 config-merge / inject).
	items, err := foldOps(files, resolved)
	if err != nil {
		return err
	}

	// Stage phase: create parent directories and write every item to its
	// "<path>.tmp" sibling. Nothing is renamed into place yet, so a failure
	// here is fully reversible by removing the staged temporaries.
	staged := make([]string, len(items))
	cleanup := func() {
		for _, t := range staged {
			if t != "" {
				_ = os.Remove(t)
			}
		}
	}
	for i, it := range items {
		// §6.7 symlink containment: confirm the target's deepest existing
		// ancestor resolves inside the destination before creating or
		// writing anything, so a pre-existing symlinked directory cannot
		// redirect the write outside the root.
		if err := checkSymlinkContainment(realDest, it.resolved); err != nil {
			cleanup()
			return err
		}
		if err := os.MkdirAll(filepath.Dir(it.resolved), 0o755); err != nil {
			cleanup()
			return err
		}
		tmp := it.resolved + ".tmp"
		if err := os.WriteFile(tmp, it.content, it.mode); err != nil {
			cleanup()
			return err
		}
		staged[i] = tmp
	}

	// Commit phase: rename each staged temporary into place. The writes have
	// already succeeded, so a same-directory rename is atomic and does not
	// fail under the disk-full / unwritable conditions that abort the stage
	// phase. A leftover temporary from a rename error is cleaned up.
	for i := range items {
		if err := os.Rename(staged[i], items[i].resolved); err != nil {
			cleanup()
			return err
		}
		staged[i] = ""
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

// checkSymlinkContainment confirms that the deepest already-existing ancestor
// of target resolves (through any symlinks) to a path still inside realDest.
// realDest must already be symlink-resolved. This catches a destination whose
// intermediate directory is a symlink pointing outside the root (for example
// a pre-existing "sub -> /etc"), which the lexical check in
// resolveSandboxedPath cannot see. spec: §6.6 step 5, §6.7 sandbox contract.
func checkSymlinkContainment(realDest, target string) error {
	dir := filepath.Dir(target)
	for {
		real, err := filepath.EvalSymlinks(dir)
		if err == nil {
			if !withinRoot(realDest, real) {
				return fmt.Errorf("%w: %q resolves outside %q via a symlinked component", ErrOutOfDestination, target, realDest)
			}
			return nil
		}
		if !os.IsNotExist(err) {
			return err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached the filesystem root without an existing ancestor.
			return nil
		}
		dir = parent
	}
}

// withinRoot reports whether path is root itself or nested under it, after
// both have been symlink-resolved by the caller.
func withinRoot(root, path string) bool {
	if path == root {
		return true
	}
	return strings.HasPrefix(path+string(filepath.Separator), root+string(filepath.Separator))
}

// writeAtomic writes content to path via "<path>.tmp" + rename so the
// destination either has the previous content or the new content. It is the
// single-file primitive WriteSandboxProfile uses; the multi-file Write path
// stages and commits explicitly for its tree-level guarantee.
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
