package source

import (
	"errors"
	"fmt"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"
)

// ErrNoOrigin reports a repository with no origin remote URL.
var ErrNoOrigin = errors.New("source: the repository has no origin remote")

// Origin is the origin remote of a local working tree, as `podium attest
// init` reads it.
type Origin struct {
	// URL is the first URL of the origin remote.
	URL string
	// DefaultBranch is the branch refs/remotes/origin/HEAD names, which a
	// clone records as the remote's default branch, or "" when the
	// repository holds no such reference.
	DefaultBranch string
}

// ReadOrigin reads the origin remote of the working tree whose top is dir.
// A dir below the top of its working tree returns an error wrapping
// ErrNotWorkTreeTop, and a bare repository an error, because the workflow
// `podium attest init` writes belongs in a working tree. It reads the
// repository's configuration and references alone and makes no network
// request.
//
// Spec: §7.3.5 (Writing the signing workflow)
func ReadOrigin(dir string) (Origin, error) {
	repo, err := openRepository(dir)
	if err != nil {
		return Origin{}, fmt.Errorf("source: open the Git repository that holds %s: %w", dir, err)
	}
	if _, err := repo.Worktree(); err != nil {
		return Origin{}, fmt.Errorf("source: %s is a bare repository, which holds no working tree to write a workflow into", dir)
	}
	prefix, err := workTreePath(repo, dir)
	if err != nil {
		return Origin{}, err
	}
	if prefix != "." {
		return Origin{}, fmt.Errorf("%w: %s lies at %q below the top of its working tree; run from the top", ErrNotWorkTreeTop, dir, prefix)
	}
	remote, err := repo.Remote("origin")
	if err != nil || len(remote.Config().URLs) == 0 {
		return Origin{}, ErrNoOrigin
	}
	o := Origin{URL: remote.Config().URLs[0]}
	head, err := repo.Reference(plumbing.NewRemoteHEADReferenceName("origin"), false)
	if err == nil && head.Type() == plumbing.SymbolicReference {
		if branch, ok := strings.CutPrefix(head.Target().String(), "refs/remotes/origin/"); ok {
			o.DefaultBranch = branch
		}
	}
	return o, nil
}
