package source

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// descentState is what the walk from one commit tells about base: whether the
// walk reaches base, and whether it is truncated.
type descentState struct {
	reachesBase bool
	truncated   bool
}

// descentMemo answers, for any commit c, the question the walk from c
// answers about base: whether base is c or an ancestor of c. It computes the
// answer of every commit it visits once, in one depth-first pass over parent
// edges, so the descent questions of one round cost one pass over the history
// below the commits they name rather than one walk for each commit.
//
// The answer equals the answer of the walk from c. The walk from c reaches
// base exactly when c is base or a parent of a commit the walk reads reaches
// base, and the walk is truncated exactly when it reaches a commit the
// repository lacks, c included. Both properties are the union of the same
// properties over c's parents, which is what the pass memoises. The question
// keeps its direction: it is asked from c, never from the reference down to
// base.
//
// A round's deepening fetch changes the repository, so a memo lives for one
// evaluation only.
//
// Spec: §7.3.1 (Git fetch: Lineage query, Direction of the descent walk)
type descentMemo struct {
	repo  *git.Repository
	base  plumbing.Hash
	state map[plumbing.Hash]descentState
}

func newDescentMemo(repo *git.Repository, base plumbing.Hash) *descentMemo {
	return &descentMemo{repo: repo, base: base, state: map[plumbing.Hash]descentState{}}
}

// descentFrame is one commit on the pass's stack: its parents and the state
// merged from the parents visited so far.
type descentFrame struct {
	h       plumbing.Hash
	parents []plumbing.Hash
	next    int
	st      descentState
}

// ancestorOrSelf answers whether base is c or an ancestor of c.
func (m *descentMemo) ancestorOrSelf(ctx context.Context, c plumbing.Hash) (ancestry, error) {
	st, err := m.resolve(ctx, c)
	switch {
	case err != nil:
		return ancestryUndecided, err
	case st.reachesBase:
		return ancestryYes, nil
	case st.truncated:
		return ancestryUndecided, nil
	default:
		return ancestryNo, nil
	}
}

// open starts the visit of h. A commit whose state is already known, base
// itself, and a commit the repository lacks are settled at once and return no
// frame. A commit that reaches base needs no further parent, so base settles
// with no truncation: a descendant's answer is yes whatever lies below base.
func (m *descentMemo) open(h plumbing.Hash) (*descentFrame, error) {
	if _, ok := m.state[h]; ok {
		return nil, nil
	}
	if h == m.base {
		m.state[h] = descentState{reachesBase: true}
		return nil, nil
	}
	c, err := m.repo.CommitObject(h)
	if errors.Is(err, plumbing.ErrObjectNotFound) {
		m.state[h] = descentState{truncated: true}
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: read commit %s: %v", ErrSourceUnreachable, h, err)
	}
	return &descentFrame{h: h, parents: c.ParentHashes}, nil
}

// resolve returns the state of start, visiting the commits below it that the
// memo has not settled. The pass is iterative, so a long linear history needs
// no deep call stack, and it reads ctx as a walk does.
func (m *descentMemo) resolve(ctx context.Context, start plumbing.Hash) (descentState, error) {
	f, err := m.open(start)
	if err != nil || f == nil {
		return m.state[start], err
	}
	stack := []*descentFrame{f}
	onStack := map[plumbing.Hash]bool{start: true}
	for steps := 0; len(stack) > 0; steps++ {
		if steps%walkCheckEvery == 0 {
			if err := snapshotCtxErr(ctx); err != nil {
				return descentState{}, err
			}
		}
		top := stack[len(stack)-1]
		// A frame that reaches base needs no other parent.
		if top.next < len(top.parents) && !top.st.reachesBase {
			p := top.parents[top.next]
			top.next++
			if onStack[p] {
				// Unreachable: a commit's hash covers its parents' hashes, so a
				// commit graph has no cycle. The guard fails closed rather than
				// loop.
				return descentState{}, fmt.Errorf("%w: commit %s is its own ancestor", ErrSourceUnreachable, p)
			}
			nf, err := m.open(p)
			if err != nil {
				return descentState{}, err
			}
			if nf == nil {
				top.st = mergeDescent(top.st, m.state[p])
				continue
			}
			stack = append(stack, nf)
			onStack[p] = true
			continue
		}
		m.state[top.h] = top.st
		delete(onStack, top.h)
		stack = stack[:len(stack)-1]
		if len(stack) > 0 {
			parent := stack[len(stack)-1]
			parent.st = mergeDescent(parent.st, top.st)
		}
	}
	return m.state[start], nil
}

func mergeDescent(a, b descentState) descentState {
	return descentState{reachesBase: a.reachesBase || b.reachesBase, truncated: a.truncated || b.truncated}
}
