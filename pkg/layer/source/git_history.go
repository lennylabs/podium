package source

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// ancestry is the answer to whether a commit X is a commit Y or an ancestor
// of Y, read from the walk from Y.
//
// Spec: §7.3.1 (Git fetch: Ancestry answer)
type ancestry int

const (
	// ancestryUndecided: the walk from Y is truncated and did not reach X. A
	// deeper history can turn it into either answer, so it is never reported
	// as one.
	ancestryUndecided ancestry = iota
	// ancestryYes: the walk from Y reached X.
	ancestryYes
	// ancestryNo: the walk from Y is complete and did not reach X.
	ancestryNo
)

// walkCheckEvery is how many commits a walk reads between two reads of its
// context. Reading a commit decodes it from the in-memory clone, which takes
// microseconds, so a walk stops within about a millisecond of its context's
// end and the check costs nothing measurable.
const walkCheckEvery = 256

// commitWalk is the result of one walk over parent edges from a starting
// commit.
//
// Spec: §7.3.1 (Git fetch: Walk, Truncation)
type commitWalk struct {
	// reached holds the starting commit and every parent that a commit the
	// walk read names. A parent named by a commit is reached even when the
	// repository lacks its object, because a commit's hash covers its parent
	// hashes, so the name alone proves the ancestry.
	reached map[plumbing.Hash]struct{}
	// truncated is true when a commit the walk reached is absent from the
	// repository, so its own parents are unknown. It is read from the walk
	// rather than from the storer's shallow list, which go-git only appends to
	// and which is therefore stale after a deepening fetch.
	truncated bool
}

// walkFrom walks parent edges from start over the commits repo holds,
// following every parent of a merge commit. A commit counts as present only
// when the walk reaches it: the walk looks up only the objects it reaches, so
// an object a tag or an earlier round brought in that the starting commit does
// not reach takes no part in an answer. It stops with the source-unreachable
// failure when ctx ends.
//
// Spec: §7.3.1 (Git fetch: Walk, Truncation)
func walkFrom(ctx context.Context, repo *git.Repository, start plumbing.Hash) (*commitWalk, error) {
	w := &commitWalk{reached: map[plumbing.Hash]struct{}{start: {}}}
	queue := []plumbing.Hash{start}
	for read := 0; len(queue) > 0; read++ {
		if read%walkCheckEvery == 0 {
			if err := snapshotCtxErr(ctx); err != nil {
				return nil, err
			}
		}
		h := queue[0]
		queue = queue[1:]
		c, err := repo.CommitObject(h)
		if errors.Is(err, plumbing.ErrObjectNotFound) {
			w.truncated = true
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%w: read commit %s: %v", ErrSourceUnreachable, h, err)
		}
		for _, ph := range c.ParentHashes {
			if _, seen := w.reached[ph]; seen {
				continue
			}
			w.reached[ph] = struct{}{}
			queue = append(queue, ph)
		}
	}
	return w, nil
}

// reaches reports whether the walk reached h.
func (w *commitWalk) reaches(h plumbing.Hash) bool {
	_, ok := w.reached[h]
	return ok
}

// ancestorOrSelf answers whether x is the walk's starting commit or an
// ancestor of it.
//
// Spec: §7.3.1 (Git fetch: Ancestry answer)
func (w *commitWalk) ancestorOrSelf(x plumbing.Hash) ancestry {
	switch {
	case w.reaches(x):
		return ancestryYes
	case w.truncated:
		return ancestryUndecided
	default:
		return ancestryNo
	}
}

// walkCache holds the walks of one round, keyed by their starting commit, so
// the answers of one round that share a starting commit read the history
// once. A round's deepening fetch changes the repository, so a cache lives for
// one evaluation only.
type walkCache struct {
	repo  *git.Repository
	walks map[plumbing.Hash]*commitWalk
}

func newWalkCache(repo *git.Repository) *walkCache {
	return &walkCache{repo: repo, walks: map[plumbing.Hash]*commitWalk{}}
}

// from returns the walk from start, walking the history on the first request.
func (c *walkCache) from(ctx context.Context, start plumbing.Hash) (*commitWalk, error) {
	if w, ok := c.walks[start]; ok {
		return w, nil
	}
	w, err := walkFrom(ctx, c.repo, start)
	if err != nil {
		return nil, err
	}
	c.walks[start] = w
	return w, nil
}

// hexCommitLen is the length of a full hexadecimal SHA-1 object name, the only
// object format go-git v5 reads.
const hexCommitLen = 40

// fullHexCommit parses a full hexadecimal object name. A value of any other
// form names no commit of a Git history, so the source answers for it without
// a fetch.
func fullHexCommit(s string) (plumbing.Hash, bool) {
	if len(s) != hexCommitLen {
		return plumbing.ZeroHash, false
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return plumbing.ZeroHash, false
		}
	}
	return plumbing.NewHash(s), true
}
