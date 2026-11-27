package materialize

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrUnsafeRecordedPath signals a path the §7.5.3 recorded-path rule refuses.
// Sync neither writes nor records such a path, skips a prior-lock entry that
// names one, and the stale-file cleanup refuses a path that resolves to one.
var ErrUnsafeRecordedPath = errors.New("materialize: unsafe recorded path")

// CheckRecordedPath applies the §7.5.3 recorded-path rule to a path that sync
// writes into a target and records in its lock, or reads back from a prior
// lock. The lock is not trusted input: a marketplace target commits it to the
// marketplace repository (§7.8) and a team can commit a workspace lock, so
// whoever can push to that repository chooses the paths the next run's
// cleanup removes or rewrites. One rule governs both directions, so a path
// sync records is a path a later run accepts.
//
// A path passes when it is a non-empty, slash-separated relative path with no
// empty, ".", or ".." segment, no segment that names .git, no backslash, no
// control character, and no volume name, and it names no sync state under
// .podium/. The only .podium/ subtree an adapter writes is the
// bundled-resource bucket .podium/resources/ (§6.7), so every other .podium/
// path (the lock, its temporary, sync.yaml, sync.local.yaml, the workspace
// overlay, the change summary) is refused. A segment names .git or .podium
// under the aliasing rule (AliasesDotGit, AliasesDotPodium): a filesystem
// resolves .GIT, a .git that carries a code point HFS+ ignores, .git. with
// the trailing period NTFS strips, and the short name GIT~1 to the real .git,
// so a byte comparison alone would let a write land there.
//
// Spec: §7.5.3
func CheckRecordedPath(p string) error {
	switch {
	case p == "":
		return fmt.Errorf("%w: empty path", ErrUnsafeRecordedPath)
	case strings.HasPrefix(p, "/"):
		return fmt.Errorf("%w: absolute path", ErrUnsafeRecordedPath)
	case strings.Contains(p, `\`):
		return fmt.Errorf("%w: backslash", ErrUnsafeRecordedPath)
	case strings.ContainsFunc(p, isControl):
		return fmt.Errorf("%w: control character", ErrUnsafeRecordedPath)
	case filepath.VolumeName(filepath.FromSlash(p)) != "":
		return fmt.Errorf("%w: volume name", ErrUnsafeRecordedPath)
	}
	segs := strings.Split(p, "/")
	for _, s := range segs {
		switch {
		case s == "" || s == "." || s == "..":
			return fmt.Errorf("%w: empty, \".\", or \"..\" segment", ErrUnsafeRecordedPath)
		case AliasesDotGit(s):
			return fmt.Errorf("%w: .git segment %q", ErrUnsafeRecordedPath, s)
		}
	}
	if AliasesDotPodium(segs[0]) && (len(segs) < 3 || segs[1] != "resources") {
		return fmt.Errorf("%w: sync state under .podium/", ErrUnsafeRecordedPath)
	}
	return nil
}

// isControl reports whether r is an ASCII or C1 control character.
func isControl(r rune) bool {
	return r < 0x20 || (r >= 0x7f && r <= 0x9f)
}
