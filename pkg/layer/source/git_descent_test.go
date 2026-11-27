package source

import (
	"context"
	"runtime"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// Spec: §7.3.1 (Git fetch: Lineage query, Direction of the descent walk) —
// the memoised descent pass gives, for every commit c and base, the answer of
// the walk from c: yes, no, or undecided, over a graph with merges, absent
// parents, an absent starting commit, and commits the repository holds that
// no walk reaches. One memo answers every c of one base, as a round does.
func TestDescentMemo_EqualsTheWalkFromTheCommit(t *testing.T) {
	t.Parallel()
	b, err := newHistoryBuilder()
	if err != nil {
		t.Fatalf("newHistoryBuilder: %v", err)
	}
	pruned := b.mustCommit(t, false)
	beyond := b.mustCommit(t, false, pruned)
	boundary := b.mustCommit(t, true, beyond)
	m1 := b.mustCommit(t, true, boundary)
	m2 := b.mustCommit(t, true, m1)
	f1 := b.mustCommit(t, true, boundary)
	missingSide := b.mustCommit(t, false, f1)
	merge := b.mustCommit(t, true, m2, missingSide)
	completeRoot := b.mustCommit(t, true)
	c1 := b.mustCommit(t, true, completeRoot)
	c2 := b.mustCommit(t, true, completeRoot)
	diamond := b.mustCommit(t, true, c1, c2)
	top := b.mustCommit(t, true, merge, diamond)
	all := []plumbing.Hash{pruned, beyond, boundary, m1, m2, f1, missingSide, merge, completeRoot, c1, c2, diamond, top}
	repo, err := b.open(top)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	ctx := context.Background()
	for _, base := range all {
		memo := newDescentMemo(repo, base)
		for _, c := range all {
			w, err := walkFrom(ctx, repo, c)
			if err != nil {
				t.Fatalf("walkFrom: %v", err)
			}
			got, err := memo.ancestorOrSelf(ctx, c)
			if err != nil {
				t.Fatalf("memo: %v", err)
			}
			if want := w.ancestorOrSelf(base); got != want {
				t.Errorf("base %s, commit %s: memo %v, walk %v", base, c, got, want)
			}
		}
	}

	ended, cancel := context.WithCancel(ctx)
	cancel()
	_, err = newDescentMemo(repo, completeRoot).ancestorOrSelf(ended, top)
	wantCallEnded(t, err, "the descent pass under an ended context")
}

// firstParentChain returns the first-parent history of head, oldest first.
func firstParentChain(t testing.TB, repo *git.Repository, head plumbing.Hash) []plumbing.Hash {
	t.Helper()
	var chain []plumbing.Hash
	for h := head; ; {
		chain = append(chain, h)
		c, err := repo.CommitObject(h)
		if err != nil {
			t.Fatalf("CommitObject: %v", err)
		}
		if len(c.ParentHashes) == 0 {
			break
		}
		h = c.ParentHashes[0]
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

// spreadQuery is a lineage query of 64 commits spread over chain, every one a
// descendant of the base near the root, so a walk from each commit would read
// the history down to the base.
func spreadQuery(chain []plumbing.Hash) (*lineageQuery, plumbing.Hash) {
	base := chain[10]
	commits := make([]string, MaxLineageCommits)
	for i := range commits {
		commits[i] = chain[11+i*(len(chain)-12)/(MaxLineageCommits-1)].String()
	}
	return newLineageQuery(base.String(), commits), chain[len(chain)-1]
}

// allocated returns the bytes fn allocates.
func allocated(fn func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// Spec: §7.3.1 (Git fetch: Lineage query) — the evaluation of a lineage query
// costs a bounded number of passes over the history, whatever the number of
// commits it names: 64 commits spread over a linear history of 20,000 commits
// allocate less than six walks of the whole history. A walk from each commit
// would allocate about 32. The test does not run in parallel, because the
// allocation count is process-wide.
func TestLineageEvaluate_AllocationIsBoundedByTheHistory(t *testing.T) {
	repo, head, err := buildLinearHistory(20000)
	if err != nil {
		t.Fatalf("build history: %v", err)
	}
	chain := firstParentChain(t, repo, head)
	ctx := context.Background()
	oneWalk := allocated(func() {
		if _, err := walkFrom(ctx, repo, head); err != nil {
			t.Fatalf("walkFrom: %v", err)
		}
	})
	q, ref := spreadQuery(chain)
	var decided bool
	evaluation := allocated(func() {
		decided, _, err = q.evaluate(ctx, newWalkCache(repo), ref, ref)
	})
	if err != nil || !decided {
		t.Fatalf("evaluate = %v, %v; want every answer decided", decided, err)
	}
	for i, a := range q.answers {
		if a != InLineage {
			t.Fatalf("answer %d = %s, want in_lineage", i, a)
		}
	}
	if evaluation > 6*oneWalk {
		t.Errorf("evaluation allocated %d bytes, want under six walks of %d bytes", evaluation, oneWalk)
	}
}

// BenchmarkLineageEvaluate measures one evaluation of a 64-commit lineage
// query over the 120,000-commit linear history.
func BenchmarkLineageEvaluate(b *testing.B) {
	repo, head := longHistory(b)
	chain := firstParentChain(b, repo, head)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q, ref := spreadQuery(chain)
		if decided, _, err := q.evaluate(ctx, newWalkCache(repo), ref, ref); err != nil || !decided {
			b.Fatalf("evaluate = %v, %v", decided, err)
		}
	}
}
