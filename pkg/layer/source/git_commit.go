package source

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// CommitTree is the tree of one commit of a local Git repository, below a
// path of that tree, as a git layer snapshot of the commit holds it.
type CommitTree struct {
	// Commit is the hash of the commit the revision resolved to.
	Commit string
	// Path is the slash path of the tree below the repository root, or ""
	// for the root. It is the root a git layer of this tree registers.
	Path string
	// Files reads the tree from the Git object database with the fs.FS a
	// git layer snapshot uses, so the ingest walk reads blob bytes and tree
	// paths: no line-ending conversion, no Git LFS smudge, and a symbolic
	// link read as its link text.
	Files fs.FS
}

// ErrNotWorkTreeTop reports a directory below the top of its working tree.
// A git layer's root is its path from the top of the repository, and the
// layer subject name is built from root alone, so reading the tree from a
// lower directory would digest one path and name another.
var ErrNotWorkTreeTop = errors.New("source: not the top of the working tree")

// OpenCommitTree opens the Git repository at dir, resolves rev to a commit,
// and returns that commit's tree below root. dir is the top of a working
// tree, which holds the .git directory or file, or a bare repository; a
// directory below the top of a working tree returns an error wrapping
// ErrNotWorkTreeTop. It reads the local object database alone and makes no
// network request.
//
// `podium attest --commit` reads a git layer through it, so the digests it
// writes are the ones registry ingest computes from a snapshot of the same
// commit rather than the ones of a checkout, whose filters and links can give
// other bytes.
//
// Spec: §4.7.6, §7.3.5
func OpenCommitTree(ctx context.Context, dir, rev, root string) (*CommitTree, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	repo, err := openRepository(dir)
	if err != nil {
		return nil, fmt.Errorf("source: open the Git repository that holds %s: %w", dir, err)
	}
	prefix, err := workTreePath(repo, dir)
	if err != nil {
		return nil, err
	}
	if prefix != "" && prefix != "." {
		return nil, fmt.Errorf("%w: %s lies at %q below the top of its working tree; run from the top and name the layer's path with --root", ErrNotWorkTreeTop, dir, prefix)
	}
	hash, err := repo.ResolveRevision(plumbing.Revision(rev))
	if err != nil {
		return nil, fmt.Errorf("source: resolve %q: %w", rev, err)
	}
	commit, err := repo.CommitObject(*hash)
	if err != nil {
		return nil, fmt.Errorf("source: %q names no commit: %w", rev, err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, fmt.Errorf("source: tree of commit %s: %w", hash, err)
	}
	sub := strings.Trim(root, "/")
	if sub != "" {
		if tree, err = tree.Tree(sub); err != nil {
			return nil, fmt.Errorf("source: commit %s holds no directory %q: %w", hash, sub, err)
		}
	}
	return &CommitTree{Commit: hash.String(), Path: sub, Files: &gitTreeFS{tree: tree}}, nil
}

// openRepository opens the repository whose working tree holds dir, finding
// its .git directory or file in dir or a parent, as Git does, and otherwise
// opens dir itself as a bare repository.
func openRepository(dir string) (*git.Repository, error) {
	repo, err := git.PlainOpenWithOptions(dir, &git.PlainOpenOptions{DetectDotGit: true, EnableDotGitCommonDir: true})
	if errors.Is(err, git.ErrRepositoryNotExists) {
		return git.PlainOpen(dir)
	}
	return repo, err
}

// workTreePath returns the slash path of dir relative to the top of repo's
// working tree, or "" for a bare repository. Both paths are compared with
// their symbolic links resolved, so a dir reached through a link, such as a
// temporary directory under /tmp on macOS, still lies inside the tree.
//
// A dir whose path passes through a link that leaves the working tree lies
// outside it, as Git reads it, and is refused rather than read as a path the
// tree does not hold.
func workTreePath(repo *git.Repository, dir string) (string, error) {
	wt, err := repo.Worktree()
	if err != nil {
		// go-git fails Worktree only for a bare repository, which has no
		// working tree for dir to lie in.
		return "", nil
	}
	top, topErr := realPath(wt.Filesystem.Root())
	here, hereErr := realPath(dir)
	if err := errors.Join(topErr, hereErr); err != nil {
		return "", fmt.Errorf("source: resolve %s in its working tree: %w", dir, err)
	}
	rel, err := filepath.Rel(top, here)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("source: %s lies outside the working tree %s", dir, top)
	}
	return filepath.ToSlash(rel), nil
}

// realPath returns the absolute path of p with every symbolic link resolved.
func realPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		// Abs fails only when the process cannot read its working
		// directory, which no test can arrange portably.
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// IsGitWorkTree reports whether dir lies inside the working tree of a Git
// repository. `podium attest` reads it to warn that a checkout can digest
// differently from the commit.
//
// Spec: §7.3.5
func IsGitWorkTree(dir string) bool {
	repo, err := openRepository(dir)
	if err != nil {
		return false
	}
	_, err = repo.Worktree()
	return err == nil
}
