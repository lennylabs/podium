package source

import (
	"context"
	"fmt"

	"github.com/go-git/go-git/v5/plumbing"
)

// Lineage answers a lineage query against the Git source: for each entry of
// commits, whether it is reference or an ancestor of it and, when base is not
// empty, whether it equals base or descends from it. The call clones the
// tracked ref at the starting depth and deepens it until every answer is
// decided, under the FetchTimeout deadline. It returns every answer or an
// ingest.source_unreachable error, never a partial response.
//
// Spec: §9.1 (Lineage query), §7.3.1 (Git fetch: Lineage query)
func (g Git) Lineage(ctx context.Context, cfg LayerConfig, reference, base string, commits []string) ([]LineageAnswer, error) {
	if err := requireRepoAndRef(cfg); err != nil {
		return nil, err
	}
	if err := ValidateLineageRequest(reference, base, commits); err != nil {
		return nil, err
	}
	q := newLineageQuery(base, commits)
	if q.decided() {
		// Every commit is a name that no Git history holds.
		return q.answers, nil
	}
	ref, ok := fullHexCommit(reference)
	if !ok {
		return nil, fmt.Errorf("%w: lineage reference %q is not a full hexadecimal commit name", ErrSourceUnreachable, reference)
	}
	return runCall(ctx, g, cfg, OperationLineageQuery, func(ctx context.Context, c *gitCall) ([]LineageAnswer, error) {
		if err := c.clone(ctx, g.fetchDepth()); err != nil {
			return nil, err
		}
		// The tip is fixed by the first fetch, as a snapshot's commit is, so
		// the progress of every round is measured on one walk.
		tip, err := resolveRef(c.repo, cfg.Ref)
		if err != nil {
			return nil, err
		}
		if err := c.settle(ctx, func(ctx context.Context, walks *walkCache) (bool, progress, error) {
			return q.evaluate(ctx, walks, tip, ref)
		}); err != nil {
			return nil, err
		}
		return q.answers, nil
	})
}

// lineageQuery holds the parsed commits of one request and the answers
// decided so far. A decided answer stays decided across rounds: a round only
// adds objects to the repository, so a walk that reached a commit still
// reaches it, and a complete walk stays complete.
type lineageQuery struct {
	base    plumbing.Hash
	hasBase bool
	// baseValid is false for a base that is not a full hexadecimal object
	// name, from which no commit of a Git history descends.
	baseValid bool
	commits   []plumbing.Hash
	// answers holds one entry for each commit; an empty entry is undecided.
	answers []LineageAnswer
}

// newLineageQuery parses the request. A commit that is not a full hexadecimal
// object name is not_in_history at once, with no fetch.
//
// Spec: §7.3.1 (Git fetch: Lineage query)
func newLineageQuery(base string, commits []string) *lineageQuery {
	q := &lineageQuery{
		hasBase: base != "",
		commits: make([]plumbing.Hash, len(commits)),
		answers: make([]LineageAnswer, len(commits)),
	}
	q.base, q.baseValid = fullHexCommit(base)
	for i, s := range commits {
		h, ok := fullHexCommit(s)
		if !ok {
			q.answers[i] = NotInHistory
			continue
		}
		q.commits[i] = h
	}
	return q
}

// decided reports whether every commit has its answer.
func (q *lineageQuery) decided() bool {
	for _, a := range q.answers {
		if a == "" {
			return false
		}
	}
	return true
}

// evaluate decides what it can from the repository as it stands. The walk
// from the fetched tip must reach ref first, and it measures the call's
// progress until it does; after that, the walk from ref, which is the starting
// commit of the query, measures it.
//
// Spec: §7.3.1 (Git fetch: Starting commit, Termination)
func (q *lineageQuery) evaluate(ctx context.Context, walks *walkCache, tip, ref plumbing.Hash) (bool, progress, error) {
	tipWalk, err := walks.from(ctx, tip)
	if err != nil {
		return false, progress{}, err
	}
	if !tipWalk.reaches(ref) {
		if !tipWalk.truncated {
			return false, progress{}, fmt.Errorf("%w: lineage reference %s is not in the history of %s", ErrSourceUnreachable, ref, tip)
		}
		return false, progressOf(tip, tipWalk), nil
	}
	refWalk, err := walks.from(ctx, ref)
	if err != nil {
		return false, progress{}, err
	}
	for i, c := range q.commits {
		if q.answers[i] != "" {
			continue
		}
		a, err := q.answer(ctx, walks, refWalk, c)
		if err != nil {
			return false, progress{}, err
		}
		q.answers[i] = a
	}
	return q.decided(), progressOf(ref, refWalk), nil
}

// answer decides one commit c of the request, or returns "" while the history
// the repository holds leaves it undecided.
//
// The descent question, whether c equals base or descends from it, is
// answered by the walk from c, and in the fallback by the walk from base. It
// is never answered by walking from the reference and stopping at base: that
// walk reaches older commits through the first parents of the signed ref, so
// it would place inside the lineage an ancestor of the reference that does not
// descend from base, such as the commit a re-run of an old signing run
// attests.
//
// Spec: §9.1 (Lineage query), §7.3.1 (Git fetch: Lineage query, Direction of the descent walk)
func (q *lineageQuery) answer(ctx context.Context, walks *walkCache, refWalk *commitWalk, c plumbing.Hash) (LineageAnswer, error) {
	switch refWalk.ancestorOrSelf(c) {
	case ancestryNo:
		return NotInHistory, nil
	case ancestryUndecided:
		return "", nil
	}
	if !q.hasBase {
		return InLineage, nil
	}
	if !q.baseValid {
		return NotDescendant, nil
	}
	if c == q.base {
		return InLineage, nil
	}
	cWalk, err := walks.from(ctx, c)
	if err != nil {
		return "", err
	}
	switch cWalk.ancestorOrSelf(q.base) {
	case ancestryYes:
		return InLineage, nil
	case ancestryNo:
		return NotDescendant, nil
	}
	// The walk from c is truncated before base. When base is in the history
	// of the reference, an ancestor of base cannot descend from it, because a
	// commit graph has no cycle, so the walk from base can decide the answer
	// with c's own history still truncated.
	if refWalk.reaches(q.base) {
		bWalk, err := walks.from(ctx, q.base)
		if err != nil {
			return "", err
		}
		if bWalk.ancestorOrSelf(c) == ancestryYes {
			return NotDescendant, nil
		}
	}
	return "", nil
}
