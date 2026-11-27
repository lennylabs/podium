package source

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
)

// longHistoryCommits is the length of the linear history the walk tests use:
// long enough that walking all of it takes far longer than their deadlines.
const longHistoryCommits = 120000

var (
	longHistoryOnce sync.Once
	longHistoryRepo *git.Repository
	longHistoryHead plumbing.Hash
	longHistoryErr  error
)

// longHistory returns an in-memory repository whose main branch is a linear
// history of longHistoryCommits commits over one empty tree, and its head. It
// is built once per test binary and only read afterwards.
func longHistory(t testing.TB) (*git.Repository, plumbing.Hash) {
	t.Helper()
	longHistoryOnce.Do(func() {
		longHistoryRepo, longHistoryHead, longHistoryErr = buildLinearHistory(longHistoryCommits)
	})
	if longHistoryErr != nil {
		t.Fatalf("build history: %v", longHistoryErr)
	}
	return longHistoryRepo, longHistoryHead
}

func buildLinearHistory(n int) (*git.Repository, plumbing.Hash, error) {
	b, err := newHistoryBuilder()
	if err != nil {
		return nil, plumbing.ZeroHash, err
	}
	var head plumbing.Hash
	for i := 0; i < n; i++ {
		var parents []plumbing.Hash
		if i > 0 {
			parents = []plumbing.Hash{head}
		}
		if head, err = b.commit(true, parents...); err != nil {
			return nil, plumbing.ZeroHash, err
		}
	}
	repo, err := b.open(head)
	return repo, head, err
}

// historyBuilder writes commits over one empty tree into an in-memory
// repository, so a test can build any commit graph, including one whose
// clone lacks some of the commits its history names.
type historyBuilder struct {
	st   *memory.Storage
	tree plumbing.Hash
	seq  int
}

func newHistoryBuilder() (*historyBuilder, error) {
	st := memory.NewStorage()
	treeObj := st.NewEncodedObject()
	if err := (&object.Tree{}).Encode(treeObj); err != nil {
		return nil, err
	}
	tree, err := st.SetEncodedObject(treeObj)
	if err != nil {
		return nil, err
	}
	return &historyBuilder{st: st, tree: tree}, nil
}

// commit encodes a commit with the given parents and returns its hash. Each
// commit carries a distinct message, so no two commits share a hash. With
// stored false, the commit is hashed and left out of the repository, as a
// shallow or pruned clone leaves out the history beyond its boundary.
func (b *historyBuilder) commit(stored bool, parents ...plumbing.Hash) (plumbing.Hash, error) {
	sig := object.Signature{Name: "alice", Email: "alice@acme.com", When: time.Unix(0, 0).UTC()}
	c := &object.Commit{Author: sig, Committer: sig, Message: strconv.Itoa(b.seq), TreeHash: b.tree, ParentHashes: parents}
	b.seq++
	obj := b.st.NewEncodedObject()
	if err := c.Encode(obj); err != nil {
		return plumbing.ZeroHash, err
	}
	if !stored {
		return obj.Hash(), nil
	}
	return b.st.SetEncodedObject(obj)
}

// open points the main branch and HEAD at head and opens the repository.
func (b *historyBuilder) open(head plumbing.Hash) (*git.Repository, error) {
	if err := b.st.SetReference(plumbing.NewHashReference("refs/heads/main", head)); err != nil {
		return nil, err
	}
	if err := b.st.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, "refs/heads/main")); err != nil {
		return nil, err
	}
	return git.Open(b.st, nil)
}

// unreachablePrior names a commit the long history does not hold, so a walk
// that looks for it covers the whole history.
var unreachablePrior = plumbing.NewHash("0123456789012345678901234567890123456789")

// wantCallEnded fails the test unless err is the source-unreachable failure
// of a call whose context ended.
func wantCallEnded(t *testing.T, err error, what string) {
	t.Helper()
	if !errors.Is(err, ErrSourceUnreachable) || !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("%s = %v, want the source-unreachable failure of an ended context", what, err)
	}
}

// Spec: §7.3.1 (Source unreachable, Git fetch: Walk), §13.12 — a walk over a
// history that does not hold the commit it looks for stops when the call's
// context ends, rather than walk the whole history for a result no caller
// reads. A walk under a live context covers the history and is complete.
func TestWalkFrom_StopsWhenTheCallEnds(t *testing.T) {
	t.Parallel()
	repo, head := longHistory(t)

	w, err := walkFrom(context.Background(), repo, head)
	if err != nil || w.ancestorOrSelf(unreachablePrior) != ancestryNo || w.truncated {
		t.Fatalf("walkFrom under a live context = %+v, %v; want a complete walk without the commit", w, err)
	}

	ended, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = walkFrom(ended, repo, head)
	wantCallEnded(t, err, "walkFrom under an ended context")
}

// Spec: §7.3.1 (Source unreachable), §13.12 — reading the tree of a clone
// whose call context already ended stops before the first step.
func TestReadSnapshotTree_StopsWhenTheCallEnds(t *testing.T) {
	t.Parallel()
	repo, _ := longHistory(t)
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := readSnapshotTree(ended, repo, LayerConfig{Ref: "main"})
	wantCallEnded(t, err, "readSnapshotTree under an ended context")
}

// Spec: §7.3.1 (Source unreachable), §13.12 — a call whose deadline passes
// while it walks a long history for a commit the history does not hold ends
// its work promptly after the deadline, in well under the time the whole walk
// takes, so a walk that ignored the deadline would fail the test.
func TestWalkFrom_EndsPromptlyAfterTheDeadline(t *testing.T) {
	t.Parallel()
	repo, head := longHistory(t)

	start := time.Now()
	if _, err := walkFrom(context.Background(), repo, head); err != nil {
		t.Fatalf("walkFrom under a live context: %v", err)
	}
	full := time.Since(start)

	// The walk must stop within half the time a full walk takes, which
	// leaves the stopped walk 30ms or more of grace after its deadline.
	const deadline = 20 * time.Millisecond
	if full < 5*deadline {
		t.Skipf("the full walk took %v, too short to tell a stopped walk from a finished one", full)
	}
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	start = time.Now()
	_, err := walkFrom(ctx, repo, head)
	elapsed := time.Since(start)
	wantCallEnded(t, err, "walkFrom past its deadline")
	if elapsed > full/2 {
		t.Errorf("walkFrom ended %v after it started with a %v deadline, want under half the %v a full walk takes",
			elapsed, deadline, full)
	}
}

// Spec: §7.3.1 (Source unreachable, Git fetch: Truncation) — a clone that
// holds no commit at the configured ref or no directory at the configured
// root fails as ingest.source_unreachable, and a walk from a commit the clone
// does not hold is truncated, so it decides nothing but that the starting
// commit is itself.
func TestReadSnapshotTree_MissingRefRootAndCommit(t *testing.T) {
	t.Parallel()
	repo, _ := longHistory(t)
	for _, cfg := range []LayerConfig{{Ref: "no-such-branch"}, {Ref: "main", Root: "/no-such-dir"}} {
		if _, _, err := readSnapshotTree(context.Background(), repo, cfg); !errors.Is(err, ErrSourceUnreachable) {
			t.Errorf("readSnapshotTree(%+v) = %v, want ErrSourceUnreachable", cfg, err)
		}
	}
	w, err := walkFrom(context.Background(), repo, unreachablePrior)
	if err != nil || !w.truncated {
		t.Fatalf("walkFrom a missing commit = %+v, %v; want a truncated walk", w, err)
	}
	if got := w.ancestorOrSelf(plumbing.ZeroHash); got != ancestryUndecided {
		t.Errorf("answer from a missing commit = %v, want undecided", got)
	}
	if got := w.ancestorOrSelf(unreachablePrior); got != ancestryYes {
		t.Errorf("a missing commit against itself = %v, want yes", got)
	}
}

// mustCommit is commit for a test, failing it on an encoding error.
func (b *historyBuilder) mustCommit(t *testing.T, stored bool, parents ...plumbing.Hash) plumbing.Hash {
	t.Helper()
	h, err := b.commit(stored, parents...)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	return h
}

// ancestryCase is one ancestry question: whether x is y or an ancestor of y,
// and the answer the walk from y gives.
type ancestryCase struct {
	name string
	y    plumbing.Hash
	x    plumbing.Hash
	want ancestry
}

// checkAncestry asserts the answer of each case. For a decided answer it also
// checks the snapshot's force-push check, which reports a rewritten history
// exactly when the answer is no. An undecided answer makes the check deepen,
// and the repository has no remote to deepen from, so the check fails as an
// unreachable source rather than report a rewrite.
func checkAncestry(t *testing.T, repo *git.Repository, cases []ancestryCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, err := walkFrom(context.Background(), repo, tc.y)
			if err != nil {
				t.Fatalf("walkFrom: %v", err)
			}
			if got := w.ancestorOrSelf(tc.x); got != tc.want {
				t.Errorf("answer = %v, want %v", got, tc.want)
			}
			c := &gitCall{cfg: LayerConfig{PriorRef: tc.x.String()}, repo: repo, depth: DefaultFetchDepth}
			rewritten, err := priorRewritten(context.Background(), c, tc.y)
			switch tc.want {
			case ancestryUndecided:
				if !errors.Is(err, ErrSourceUnreachable) {
					t.Errorf("force-push check on an undecided answer = %v, %v; want ErrSourceUnreachable", rewritten, err)
				}
			default:
				if err != nil || rewritten != (tc.want == ancestryNo) {
					t.Errorf("force-push check = %v, %v; want rewritten %v", rewritten, err, tc.want == ancestryNo)
				}
			}
		})
	}
}

// Spec: §7.3.1 (Force-push detected, Git fetch: Ancestry answer) — on a
// complete history, a merge brings the history of every parent into the new
// head, so a commit reachable only through a merge commit's second parent is
// an ancestor. A commit on a branch no merge brought in is not, and a complete
// walk answers no, even though the repository holds the commit's object: the
// walk decides presence, not an object lookup.
func TestAncestry_CompleteWalks(t *testing.T) {
	t.Parallel()
	b, err := newHistoryBuilder()
	if err != nil {
		t.Fatalf("newHistoryBuilder: %v", err)
	}
	base := b.mustCommit(t, true)
	m1 := b.mustCommit(t, true, base)
	m2 := b.mustCommit(t, true, m1)
	f1 := b.mustCommit(t, true, base)
	f2 := b.mustCommit(t, true, f1)
	// merge joins two arms of equal length, so the walk reaches base along
	// both of them.
	merge := b.mustCommit(t, true, m2, f2)
	// noFF is a no-fast-forward merge of f1 into a main branch that has not
	// moved since base, so its first parent is the merge base.
	noFF := b.mustCommit(t, true, base, f1)
	abandoned := b.mustCommit(t, true, base)
	repo, err := b.open(merge)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	checkAncestry(t, repo, []ancestryCase{
		{name: "commit behind the second parent", y: merge, x: f1, want: ancestryYes},
		{name: "commit is the second parent", y: merge, x: f2, want: ancestryYes},
		{name: "commit is the merge base", y: merge, x: base, want: ancestryYes},
		{name: "commit is the starting commit", y: merge, x: merge, want: ancestryYes},
		{name: "commit on an unmerged branch the repository holds", y: merge, x: abandoned, want: ancestryNo},
		{name: "no-fast-forward merge of the commit", y: noFF, x: f1, want: ancestryYes},
		{name: "no-fast-forward merge without the commit", y: noFF, x: abandoned, want: ancestryNo},
		{name: "descendant is not an ancestor", y: base, x: merge, want: ancestryNo},
	})
}

// Spec: §7.3.1 (Force-push detected, Git fetch: Truncation, Ancestry answer)
// — in a shallow clone, a commit can name a parent the clone does not hold,
// and the walk is truncated there. A commit that names x as its parent proves
// x is an ancestor, whether or not the clone holds x's object, because a
// commit's hash covers its parent hashes, and a missing parent on one side of
// a merge does not hide a commit reachable through the other side. A commit
// only the missing history could reach is undecided rather than no, so the
// force-push check never reads a truncated history as a rewrite.
func TestAncestry_TruncatedWalks(t *testing.T) {
	t.Parallel()
	b, err := newHistoryBuilder()
	if err != nil {
		t.Fatalf("newHistoryBuilder: %v", err)
	}
	pruned := b.mustCommit(t, false)
	beyond := b.mustCommit(t, false, pruned)
	boundary := b.mustCommit(t, true, beyond)
	head := b.mustCommit(t, true, boundary)
	prior := b.mustCommit(t, true, boundary)
	sideHead := b.mustCommit(t, true, prior)
	missingSide := b.mustCommit(t, false, boundary)
	merge := b.mustCommit(t, true, missingSide, sideHead)
	// unrelated is held by the repository, as a tag fetch would leave it, and
	// no commit of head's history reaches it.
	unrelated := b.mustCommit(t, true)
	repo, err := b.open(head)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	checkAncestry(t, repo, []ancestryCase{
		{name: "commit is the absent parent of the shallow boundary", y: head, x: beyond, want: ancestryYes},
		{name: "commit beyond the absent parent", y: head, x: pruned, want: ancestryUndecided},
		{name: "commit held in the clone below the boundary", y: head, x: boundary, want: ancestryYes},
		{name: "held commit that the truncated walk does not reach", y: head, x: unrelated, want: ancestryUndecided},
		{name: "merge whose first parent is absent and whose second parent leads to the commit", y: merge, x: prior, want: ancestryYes},
		{name: "merge whose first parent is absent without the commit", y: merge, x: pruned, want: ancestryUndecided},
	})
}

// Spec: §7.3.1 (Git fetch: Snapshot) — the force-push check reports no
// rewrite for a layer with no last ingested reference and for the zero hash,
// and a rewrite with no deepening for a reference that is not a full
// hexadecimal commit name, which no history holds.
func TestPriorRewritten_ReferencesThatNeedNoWalk(t *testing.T) {
	t.Parallel()
	repo, head := longHistory(t)
	for _, tc := range []struct {
		prior string
		want  bool
	}{
		{prior: "", want: false},
		{prior: plumbing.ZeroHash.String(), want: false},
		{prior: "2026-11-26T00:00:00Z", want: true},
		{prior: head.String()[:12], want: true},
	} {
		c := &gitCall{cfg: LayerConfig{PriorRef: tc.prior}, repo: repo, depth: DefaultFetchDepth}
		if got, err := priorRewritten(context.Background(), c, head); err != nil || got != tc.want {
			t.Errorf("priorRewritten(%q) = %v, %v; want %v, nil", tc.prior, got, err, tc.want)
		}
	}
}
