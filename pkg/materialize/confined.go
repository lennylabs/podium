package materialize

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ErrUnconfinedPath signals a path the stale-file cleanup, a recorded-path
// read, or a sync state access refuses: it does not name a regular file
// reached below the destination root under the rule the operation applies. A
// directory or another non-regular file at the path is refused, and so is a
// symbolic link that resolves outside the root.
var ErrUnconfinedPath = errors.New("materialize: path is not a regular file confined to the destination")

// ErrPodiumNotDirectory signals that the destination's .podium is a symbolic
// link or is not a directory. Sync keeps its own state there and never reads
// or writes it through a link, because a repository checkout can commit
// .podium as one (§7.8). It is reported together with ErrUnconfinedPath, so
// a caller can tell this unusable state directory from a refused file and
// fail with an error that names the remedy.
var ErrPodiumNotDirectory = errors.New("materialize: .podium is a symbolic link or not a directory")

// RemoveStale deletes the regular file at rel below destination and then
// prunes the directories that held it while they are empty. rel is the
// slash-separated path a sync lock recorded, and the lock can come from a
// repository another party pushes to.
//
// The removal accepts the links the writer accepts (§6.7): a directory
// component may be a symbolic link that resolves inside the root, so the
// output the writer put behind such a link is removed when its artifact
// leaves the view. A link that resolves outside the root, a link at the path
// itself, a .podium directory that is a link, and a path whose resolved form
// fails CheckRecordedPath are refused. The writer replaces a standalone path
// by rename, so a link at a stale path is not one sync wrote, and following it
// would delete the file it names. A directory is never removed recursively.
// The pruning removes only the empty directories the path reaches below its
// last link, deepest first, so it stops at the first directory that is not
// empty, at the directory a link names, or at the root. A path that is already
// absent prunes the same way and returns nil, so a file the operator deleted
// by hand still lets its empty parents go.
//
// Spec: §6.7, §7.5.3
func RemoveStale(destination, rel string) error {
	sp, err := openResolved(destination, rel, resolveOpts{recorded: true})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer sp.close()
	err = checkConfinedFile(sp.root, rel, sp.segs)
	switch {
	case err == nil:
		if rerr := sp.root.Remove(filepath.Join(sp.segs...)); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
			return fmt.Errorf("materialize: remove %s: %w", rel, rerr)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	pruneEmptyDirs(sp.root, sp.segs[:len(sp.segs)-1], sp.keep)
	return nil
}

// ReadRecorded returns the bytes of the regular file a recorded path names
// below destination. Directory links and a link at the path itself are
// followed when they resolve inside the root, as the writer follows them, and
// the resolved path must pass CheckRecordedPath. An absent path, including a
// link that names an absent file, returns an error that wraps fs.ErrNotExist;
// a refused path returns an error that wraps ErrUnconfinedPath or
// ErrUnsafeRecordedPath. A recorded path therefore cannot direct a read at a
// device, a FIFO, or a file outside the destination.
//
// Spec: §7.5.2, §7.5.3
func ReadRecorded(destination, rel string) ([]byte, error) {
	sp, err := openResolved(destination, rel, resolveOpts{followLeaf: true, recorded: true})
	if err != nil {
		return nil, err
	}
	defer sp.close()
	return sp.read(rel)
}

// RewriteStale replaces the content of the regular file at rel below
// destination with rewrite applied to its current content. It is the in-place
// reconciliation of a §6.7 config-merge or inject file whose last contributing
// artifact is gone, and it resolves rel as ReadRecorded does: a link at the
// path that resolves inside the root, such as GEMINI.md linked to AGENTS.md,
// is followed, so the file it names is rewritten and the link stays. An absent
// path is a no-op that returns nil.
//
// Spec: §6.7, §7.5.3
func RewriteStale(destination, rel string, rewrite func([]byte) []byte) error {
	sp, err := openResolved(destination, rel, resolveOpts{followLeaf: true, recorded: true})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer sp.close()
	data, err := sp.read(rel)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := sp.root.WriteFile(filepath.Join(sp.segs...), rewrite(data), 0o644); err != nil {
		return fmt.Errorf("materialize: write %s: %w", rel, err)
	}
	return nil
}

// resolveOpts selects how openResolved treats a path.
type resolveOpts struct {
	// followLeaf follows a symbolic link at the path itself when it resolves
	// inside the root.
	followLeaf bool
	// recorded applies CheckRecordedPath to the path and to its resolved
	// form. The sync state reader leaves it unset, because its own paths
	// under .podium/ are what the rule refuses.
	recorded bool
}

// resolvedPath is a path resolved below an open root.
type resolvedPath struct {
	// root is the destination opened as an os.Root. Every operation goes
	// through it, so a component swapped for a link that leaves the root
	// after the resolution cannot carry the operation outside the
	// destination. os.Root follows links that stay inside the root, so a
	// concurrent local writer of the target that swaps a component between
	// the check and the operation can still redirect it inside the root; the
	// lock's concurrent-writer rule (§7.5.3) puts that writer out of scope.
	// Content committed to a repository is static during the run, so it
	// cannot.
	root *os.Root
	// segs is the path below the root with every followed link resolved. The
	// operations check it again with checkConfinedFile, which refuses any
	// link on that path as the check sees it.
	segs []string
	// keep is the number of leading directories of segs the pruning leaves in
	// place: the directories up to and including the one the last link names.
	keep int
}

func (rp *resolvedPath) close() { _ = rp.root.Close() }

// read returns the bytes of the resolved path after checking it again.
func (rp *resolvedPath) read(rel string) ([]byte, error) {
	if err := checkConfinedFile(rp.root, rel, rp.segs); err != nil {
		return nil, err
	}
	data, err := rp.root.ReadFile(filepath.Join(rp.segs...))
	if err != nil {
		return nil, fmt.Errorf("materialize: read %s: %w", rel, err)
	}
	return data, nil
}

// openResolved opens destination as a root and resolves rel through the links
// that stay inside it: every directory link, and a link at rel itself when
// o.followLeaf is set. A .podium directory that is a link is refused. The
// resolution uses filepath.EvalSymlinks against the symlink-resolved root, as
// the writer's checkSymlinkContainment does, so the writer and the readers
// accept the same links. A missing directory returns an error wrapping
// fs.ErrNotExist.
func openResolved(destination, rel string, o resolveOpts) (*resolvedPath, error) {
	if o.recorded {
		if err := CheckRecordedPath(rel); err != nil {
			return nil, err
		}
	}
	root, segs, err := openConfinedRoot(destination, rel)
	if err != nil {
		return nil, err
	}
	rp, err := resolveIn(root, destination, rel, segs, o)
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	return rp, nil
}

// resolveIn resolves rel below root. See openResolved.
func resolveIn(root *os.Root, destination, rel string, segs []string, o resolveOpts) (*resolvedPath, error) {
	// filepath.Abs fails only when the working directory cannot be read, and
	// openConfinedRoot already resolved the same destination.
	absDest, err := filepath.Abs(destination)
	if err != nil {
		return nil, err
	}
	realDest, err := filepath.EvalSymlinks(absDest)
	if err != nil {
		return nil, fmt.Errorf("materialize: resolve %s: %w", absDest, err)
	}
	dirs := segs[:len(segs)-1]
	lastLink := -1
	for i := range dirs {
		info, err := root.Lstat(filepath.Join(dirs[:i+1]...))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("materialize: inspect %s: %w", rel, err)
			}
			return nil, fmt.Errorf("%w: %q: %v", ErrUnconfinedPath, rel, err)
		}
		isLink := info.Mode()&fs.ModeSymlink != 0
		if strings.EqualFold(dirs[i], ".podium") && (isLink || !info.IsDir()) {
			return nil, podiumDirError(rel)
		}
		if isLink {
			lastLink = i
		}
	}
	relDir, err := resolveWithin(realDest, filepath.Join(realDest, filepath.Join(dirs...)), rel)
	if err != nil {
		return nil, err
	}
	resolved := segs[len(segs)-1]
	if relDir != "." {
		resolved = relDir + "/" + resolved
	}
	if o.followLeaf {
		info, err := root.Lstat(filepath.FromSlash(resolved))
		if err == nil && info.Mode()&fs.ModeSymlink != 0 {
			if resolved, err = resolveWithin(realDest, filepath.Join(realDest, filepath.FromSlash(resolved)), rel); err != nil {
				return nil, err
			}
		}
	}
	// A recorded path passes the rule in its resolved form. A state path such
	// as .podium/sync.lock fails the rule by design, so it is checked only
	// when a link moved it (checkStateLink): a lock link into .git is refused
	// like any other.
	var ruleErr error
	switch {
	case o.recorded:
		ruleErr = CheckRecordedPath(resolved)
	case resolved != rel:
		ruleErr = checkStateLink(resolved)
	}
	if ruleErr != nil {
		return nil, ruleRefusal(fmt.Sprintf("%q resolves to %q", rel, resolved), ruleErr)
	}
	rsegs := strings.Split(resolved, "/")
	// The directories after the last link are real directories the path
	// itself created; the pruning stops above them.
	realDirs := len(dirs) - (lastLink + 1)
	return &resolvedPath{root: root, segs: rsegs, keep: max(len(rsegs)-1-realDirs, 0)}, nil
}

// checkStateLink applies the recorded-path rule to the resolved path of a
// state file reached through a link. A link to another regular file inside
// .podium/, such as .podium/sync.lock linked to .podium/real.lock, reaches
// none of the target's other files, so the part below .podium/ is checked as
// a path of its own; any other resolved path must pass the rule.
//
// Spec: §7.5.3
func checkStateLink(resolved string) error {
	if first, rest, ok := strings.Cut(resolved, "/"); ok && strings.EqualFold(first, ".podium") {
		return CheckRecordedPath(rest)
	}
	return CheckRecordedPath(resolved)
}

// refusal is an error that matches each of its sentinels and reads as msg.
// One refusal can match two sentinels, such as ErrUnconfinedPath and
// ErrPodiumNotDirectory, and the text of each sentinel starts with
// "materialize:", so wrapping both with %w would print the package prefix
// twice.
type refusal struct {
	msg string
	is  []error
}

func (r *refusal) Error() string   { return r.msg }
func (r *refusal) Unwrap() []error { return r.is }

// podiumDirError reports that the .podium directory on rel's path is a
// symbolic link or not a directory. The error matches ErrUnconfinedPath and
// ErrPodiumNotDirectory.
func podiumDirError(rel string) error {
	return &refusal{
		msg: fmt.Sprintf("%v: %q", ErrPodiumNotDirectory, rel),
		is:  []error{ErrUnconfinedPath, ErrPodiumNotDirectory},
	}
}

// ruleRefusal reports a path the §7.5.3 recorded-path rule refuses: ruleErr
// is the CheckRecordedPath result and detail names the path. The error
// matches ErrUnconfinedPath and ErrUnsafeRecordedPath, and its message
// carries the "materialize:" prefix once.
func ruleRefusal(detail string, ruleErr error) error {
	return &refusal{
		msg: fmt.Sprintf("%v: %s: %s", ErrUnconfinedPath, detail, strings.TrimPrefix(ruleErr.Error(), "materialize: ")),
		is:  []error{ErrUnconfinedPath, ruleErr},
	}
}

// resolveWithin resolves every symbolic link in the existing path full and
// returns the result as a slash-separated path relative to realDest, or "."
// for realDest itself. A path that resolves outside realDest is refused with
// ErrUnconfinedPath; a path that names an absent file, a dangling link
// included, returns an error wrapping fs.ErrNotExist.
func resolveWithin(realDest, full, rel string) (string, error) {
	target, err := filepath.EvalSymlinks(full)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("materialize: resolve %s: %w", rel, err)
		}
		return "", fmt.Errorf("%w: %q: %v", ErrUnconfinedPath, rel, err)
	}
	if !withinRoot(realDest, target) {
		return "", fmt.Errorf("%w: %q resolves outside the destination", ErrUnconfinedPath, rel)
	}
	r, err := filepath.Rel(realDest, target)
	if err != nil {
		// Unreachable: both paths are absolute and withinRoot placed target
		// under realDest. Kept so a future change fails closed.
		return "", fmt.Errorf("%w: %q: %v", ErrUnconfinedPath, rel, err)
	}
	return filepath.ToSlash(r), nil
}

// openConfinedRoot splits rel into its segments, checks it lexically with the
// writer's resolveSandboxedPath, and opens destination as an os.Root. The root
// confines every later operation to destination, including when a component
// is replaced by a symbolic link between a check and the operation. A
// destination that does not exist returns an error wrapping fs.ErrNotExist.
func openConfinedRoot(destination, rel string) (*os.Root, []string, error) {
	if destination == "" {
		return nil, nil, ErrEmptyDestination
	}
	segs, err := confinedSegments(rel)
	if err != nil {
		return nil, nil, err
	}
	// filepath.Abs fails only when the working directory cannot be read. The
	// writer's lexical check cannot fail after confinedSegments refused every
	// "..", "", and absolute form; it is kept so the two checks cannot drift.
	absDest, err := filepath.Abs(destination)
	if err != nil {
		return nil, nil, err
	}
	if _, err := resolveSandboxedPath(absDest, filepath.Join(segs...)); err != nil {
		return nil, nil, err
	}
	root, err := os.OpenRoot(absDest)
	if err != nil {
		return nil, nil, fmt.Errorf("materialize: open %s: %w", absDest, err)
	}
	return root, segs, nil
}

// confinedSegments splits a slash-separated relative path into its segments
// and refuses an empty path, an absolute path, and an empty, ".", or ".."
// segment.
func confinedSegments(rel string) ([]string, error) {
	if rel == "" || strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) {
		return nil, fmt.Errorf("%w: %q is not a relative path", ErrOutOfDestination, rel)
	}
	segs := strings.Split(rel, "/")
	for _, s := range segs {
		if s == "" || s == "." || s == ".." {
			return nil, fmt.Errorf("%w: %q has an empty, \".\", or \"..\" segment", ErrOutOfDestination, rel)
		}
	}
	return segs, nil
}

// checkConfinedFile confirms that each component of rel below root is a
// directory that is not a symbolic link and that rel itself is a regular file.
// It inspects the components from the root down with Lstat, so no component
// is resolved through a link the walk has not already refused. A missing
// component returns an error wrapping fs.ErrNotExist.
func checkConfinedFile(root *os.Root, rel string, segs []string) error {
	for i := range segs {
		name := filepath.Join(segs[:i+1]...)
		info, err := root.Lstat(name)
		if err != nil {
			return fmt.Errorf("materialize: inspect %s: %w", rel, err)
		}
		last := i == len(segs)-1
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			return fmt.Errorf("%w: %q: %s is a symbolic link", ErrUnconfinedPath, rel, filepath.ToSlash(name))
		case !last && !info.IsDir():
			return fmt.Errorf("%w: %q: %s is not a directory", ErrUnconfinedPath, rel, filepath.ToSlash(name))
		case last && !info.Mode().IsRegular():
			return fmt.Errorf("%w: %q is not a regular file", ErrUnconfinedPath, rel)
		}
	}
	return nil
}

// pruneEmptyDirs removes the directory named by dirs and each ancestor above
// it, deepest first, leaving the first keep directories and the root in
// place. Root.Remove deletes only an empty directory, so the walk halts at the
// first ancestor that still holds an operator file or a sibling artifact's
// output, and a component that is missing or is a symbolic link also halts it.
func pruneEmptyDirs(root *os.Root, dirs []string, keep int) {
	for n := len(dirs); n > keep; n-- {
		name := filepath.Join(dirs[:n]...)
		info, err := root.Lstat(name)
		if err != nil || !info.IsDir() {
			return
		}
		if err := root.Remove(name); err != nil {
			return
		}
	}
}
