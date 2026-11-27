// Package materialize writes adapter output to disk under the sandbox
// contract from spec §6.6 and §6.7: atomic per-file write, no writes
// outside the destination root, no network, no subprocesses.
package materialize

import (
	"errors"
	"fmt"
	"io/fs"
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
// destination root. The destination can be a repository checkout whose links
// another party commits (§7.8): every written path, before and after its
// directory links resolve, passes the §7.5.3 recorded-path rule; each
// temporary is created exclusively (stage); a config-merge or inject target
// that is a link is followed only to a file inside the destination that passes
// the rule (resolveMergeTarget); and a path under a .podium directory that is
// a link is refused (checkPodiumDir). The rename replaces a link at a
// standalone path rather than writing through it.
func Write(destination string, files []adapter.File) error {
	return WriteKeeping(destination, files, nil)
}

// WriteKeeping is Write with a set of artifacts whose prior contribution to a
// shared config-merge or inject file survives the §6.7 reconciliation. The
// fold strips every prior Podium-owned entry and block before this batch's
// fragments merge in, which removes the contribution of every artifact the
// batch does not carry. keep reports the artifact IDs exempt from that strip:
// their tagged JSON entries and their keyed inject blocks stay in the file as
// they are. A nil keep exempts nothing, which is Write.
//
// Spec: §6.6, §6.7, §13.11.3 — a filesystem-source sync keeps the previous
// output of an artifact refused under the unparseable-manifest rule, and that
// output can include an entry in a file other artifacts still write. A
// load_artifact writes one artifact and keeps every other artifact's entries
// and blocks.
func WriteKeeping(destination string, files []adapter.File, keep func(id string) bool) error {
	if destination == "" {
		return ErrEmptyDestination
	}
	absDest, err := filepath.Abs(destination)
	if err != nil {
		return err
	}

	// Validate every path lexically before any write so a single bad path
	// fails the whole batch with no half-written tree. Beyond containment,
	// every written path passes the §7.5.3 recorded-path rule, so no caller,
	// a podium-mcp MaterializationHook that rewrites paths included, writes
	// into .git or the sync state under .podium/; sync's own state goes
	// through WriteStateFile instead.
	resolved := make([]string, len(files))
	for i, f := range files {
		full, err := resolveSandboxedPath(absDest, f.Path)
		if err != nil {
			return err
		}
		if err := CheckRecordedPath(f.Path); err != nil {
			return ruleRefusal(fmt.Sprintf("%q", f.Path), err)
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

	// §6.7 symlink containment: confirm each target's deepest existing
	// ancestor resolves inside the destination before anything is read or
	// written, so a pre-existing symlinked directory can neither redirect a
	// write outside the root nor feed the merge fold a file outside it. A
	// .podium directory that is a link is refused outright (§7.8), and a
	// directory link that resolves into .git or the sync state, which a
	// repository checkout can commit, is refused before anything lands there.
	for i, f := range files {
		landing, err := checkSymlinkContainment(realDest, resolved[i])
		if err != nil {
			return err
		}
		if err := checkResolvedTarget(absDest, realDest, resolved[i], landing, f.Path); err != nil {
			return err
		}
		if err := checkPodiumDir(absDest, f.Path); err != nil {
			return err
		}
		// Every item is keyed by its landing path below the resolved root,
		// so one file reached under two spellings (AGENTS.md and GEMINI.md
		// linked to it, or a destination reached through a link) folds into
		// one item and stages one temporary.
		resolved[i] = landing
		// A config-merge or inject target that is a link resolving inside
		// the destination, such as GEMINI.md linked to AGENTS.md, is merged
		// into the file the link names, and the link stays (§6.6).
		if f.Op != adapter.OpWrite {
			target, err := resolveMergeTarget(realDest, landing, f.Path)
			if err != nil {
				return err
			}
			resolved[i] = target
		}
	}

	// Fold inject/merge ops into one final content per destination path. This
	// reads any existing on-disk file for OpInject / OpMergeJSON targets so
	// the operator's other content is preserved (§6.7 config-merge / inject).
	items, err := foldOps(files, resolved, keep)
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
		if err := os.MkdirAll(filepath.Dir(it.resolved), 0o755); err != nil {
			cleanup()
			return err
		}
		tmp := it.resolved + ".tmp"
		if err := stage(hostFS{}, tmp, it.content, it.mode); err != nil {
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
// of target resolves (through any symlinks) to a path still inside realDest,
// and returns target with that ancestor resolved: the path the write lands
// at. realDest must already be symlink-resolved. This catches a destination
// whose intermediate directory is a symlink pointing outside the root (for
// example a pre-existing "sub -> /etc"), which the lexical check in
// resolveSandboxedPath cannot see. spec: §6.6 step 5, §6.7 sandbox contract.
func checkSymlinkContainment(realDest, target string) (string, error) {
	dir := filepath.Dir(target)
	for {
		real, err := filepath.EvalSymlinks(dir)
		if err == nil {
			if !withinRoot(realDest, real) {
				return "", fmt.Errorf("%w: %q resolves outside %q via a symlinked component", ErrOutOfDestination, target, realDest)
			}
			rest, err := filepath.Rel(dir, target)
			if err != nil {
				// Unreachable: dir is an ancestor of target by construction.
				return "", err
			}
			return filepath.Join(real, rest), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached the filesystem root without an existing ancestor; the
			// destination root exists by now, so this needs a concurrent
			// removal of it.
			return target, nil
		}
		dir = parent
	}
}

// checkResolvedTarget refuses a write whose directories resolve, through a
// link inside the destination, to a path the §7.5.3 recorded-path rule
// refuses, such as one under .git or the sync state. full is the lexical
// target and landing the path checkSymlinkContainment resolved. A path that
// resolves to itself needs no second check: WriteKeeping already applied the
// rule to the lexical path.
//
// Spec: §6.6, §7.5.3, §7.8
func checkResolvedTarget(absDest, realDest, full, landing, rel string) error {
	lexical, err := filepath.Rel(absDest, full)
	if err != nil {
		// Unreachable: resolveSandboxedPath placed full under absDest.
		return err
	}
	resolved, err := filepath.Rel(realDest, landing)
	if err != nil {
		// Unreachable: checkSymlinkContainment placed landing under realDest.
		return err
	}
	if filepath.ToSlash(resolved) == filepath.ToSlash(lexical) {
		return nil
	}
	if err := CheckRecordedPath(filepath.ToSlash(resolved)); err != nil {
		return ruleRefusal(fmt.Sprintf("%q resolves to %q", rel, filepath.ToSlash(resolved)), err)
	}
	return nil
}

// checkPodiumDir refuses a path under .podium/ when the destination's .podium
// directory is a symbolic link. Sync keeps its own state there, and a
// repository checkout can commit .podium as a link (§7.8), so nothing is read
// or written through such a link even when it resolves inside the root.
func checkPodiumDir(absDest, rel string) error {
	first, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
	if !AliasesDotPodium(first) {
		return nil
	}
	info, err := os.Lstat(filepath.Join(absDest, first))
	if err == nil && info.Mode()&fs.ModeSymlink != 0 {
		return podiumDirError(rel)
	}
	return nil
}

// withinRoot reports whether path is root itself or nested under it, after
// both have been symlink-resolved by the caller.
func withinRoot(root, path string) bool {
	if path == root {
		return true
	}
	return strings.HasPrefix(path+string(filepath.Separator), root+string(filepath.Separator))
}
