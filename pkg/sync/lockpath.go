package sync

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// errUnsafeLockPath reports a materialized_path the recorded-path rule
// refuses. The stale-file cleanup skips such an entry and the next lock omits
// it.
var errUnsafeLockPath = errors.New("unsafe materialized_path")

// checkLockPath applies the §7.5.3 recorded-path rule to one materialized_path
// read from a prior lock. The lock is not trusted input: a marketplace target
// commits it to the marketplace repository (§7.8) and a team can commit a
// workspace lock, so whoever can push to that repository chooses the paths the
// next run's cleanup removes or rewrites. A path is usable only when it is a
// non-empty, slash-separated relative path with no empty, ".", "..", or ".git"
// segment, no backslash, no control character, and no volume name, and it does
// not name the sync's own state under .podium/. The only .podium/ subtree an
// adapter writes is the bundled-resource bucket .podium/resources/ (§6.7), so
// every other .podium/ path (the lock, sync.yaml, sync.local.yaml, the
// workspace overlay) is refused. The .git and .podium comparisons ignore case
// because a case-insensitive filesystem resolves .GIT to .git.
//
// Spec: §7.5.3
func checkLockPath(p string) error {
	switch {
	case p == "":
		return fmt.Errorf("%w: empty path", errUnsafeLockPath)
	case strings.HasPrefix(p, "/"):
		return fmt.Errorf("%w: absolute path", errUnsafeLockPath)
	case strings.Contains(p, `\`):
		return fmt.Errorf("%w: backslash", errUnsafeLockPath)
	case strings.ContainsFunc(p, isControl):
		return fmt.Errorf("%w: control character", errUnsafeLockPath)
	case filepath.VolumeName(filepath.FromSlash(p)) != "":
		return fmt.Errorf("%w: volume name", errUnsafeLockPath)
	}
	segs := strings.Split(p, "/")
	for _, s := range segs {
		switch {
		case s == "" || s == "." || s == "..":
			return fmt.Errorf("%w: empty, \".\", or \"..\" segment", errUnsafeLockPath)
		case strings.EqualFold(s, ".git"):
			return fmt.Errorf("%w: .git segment", errUnsafeLockPath)
		}
	}
	if strings.EqualFold(segs[0], ".podium") && (len(segs) < 3 || segs[1] != "resources") {
		return fmt.Errorf("%w: sync state under .podium/", errUnsafeLockPath)
	}
	return nil
}

// isControl reports whether r is an ASCII or C1 control character.
func isControl(r rune) bool {
	return r < 0x20 || (r >= 0x7f && r <= 0x9f)
}

// dropUnsafeLockPaths returns a copy of lock without the entries whose
// materialized_path fails checkLockPath, and names each dropped entry on
// standard error. The stale-file cleanup, the §7.5.2 comparison, and the
// §13.11.3 keep decision all read the returned lock, so a refused path is
// neither removed, rewritten, read, nor carried into the lock the run writes.
// An entry that records no path is kept as it is: it names no file to clean
// up. A nil lock returns nil.
//
// Spec: §7.5.3
func dropUnsafeLockPaths(lock *LockFile) *LockFile {
	if lock == nil {
		return nil
	}
	out := *lock
	out.Artifacts = make([]LockArtifact, 0, len(lock.Artifacts))
	for _, a := range lock.Artifacts {
		if a.MaterializedPath != "" {
			if err := checkLockPath(a.MaterializedPath); err != nil {
				fmt.Fprintf(os.Stderr, "sync: stale-file cleanup: skipping lock entry %q (id %q): %v\n", a.MaterializedPath, a.ID, err)
				continue
			}
		}
		out.Artifacts = append(out.Artifacts, a)
	}
	return &out
}
