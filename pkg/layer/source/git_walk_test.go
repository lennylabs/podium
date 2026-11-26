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
func longHistory(t *testing.T) (*git.Repository, plumbing.Hash) {
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

// unreachablePrior names a commit the long history does not hold, so the
// force-push walk covers the whole history.
var unreachablePrior = plumbing.NewHash("0123456789012345678901234567890123456789")

// wantSnapshotEnded fails the test unless err is the source-unreachable
// failure of a snapshot whose context ended.
func wantSnapshotEnded(t *testing.T, err error, what string) {
	t.Helper()
	if !errors.Is(err, ErrSourceUnreachable) || !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("%s = %v, want the source-unreachable failure of an ended context", what, err)
	}
}

// Spec: §7.3.1 (Source unreachable), §13.12 — the force-push walk over a
// history that does not hold the prior reference stops when the snapshot's
// context ends, rather than walk the whole history for a result no caller
// reads. A walk under a live context still covers the history.
func TestIsAncestor_StopsWhenTheSnapshotEnds(t *testing.T) {
	t.Parallel()
	repo, head := longHistory(t)

	reachable, err := isAncestor(context.Background(), repo, unreachablePrior, head)
	if err != nil || reachable {
		t.Fatalf("isAncestor under a live context = %v, %v; want false, nil", reachable, err)
	}

	ended, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = isAncestor(ended, repo, unreachablePrior, head)
	wantSnapshotEnded(t, err, "isAncestor under an ended context")
}

// Spec: §7.3.1 (Source unreachable), §13.12 — reading a clone whose snapshot
// context already ended stops before the first step.
func TestReadSnapshot_StopsWhenTheSnapshotEnds(t *testing.T) {
	t.Parallel()
	repo, _ := longHistory(t)
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := readSnapshot(ended, repo, LayerConfig{Ref: "main", PriorRef: unreachablePrior.String()})
	wantSnapshotEnded(t, err, "readSnapshot under an ended context")
}

// Spec: §7.3.1 (Source unreachable), §13.12 — a snapshot whose deadline passes
// while it walks a long history for a prior reference the history does not
// hold ends its work promptly after the deadline, in well under the time the
// whole walk takes, so a walk that ignored the deadline would fail the test.
func TestReadSnapshot_WalkEndsPromptlyAfterTheDeadline(t *testing.T) {
	t.Parallel()
	repo, head := longHistory(t)
	cfg := LayerConfig{Ref: "main", PriorRef: unreachablePrior.String()}

	start := time.Now()
	if snap, err := readSnapshot(context.Background(), repo, cfg); err != nil || !snap.HistoryRewritten || snap.Reference != head.String() {
		t.Fatalf("readSnapshot under a live context = %+v, %v; want the head with a rewritten history", snap, err)
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
	_, err := readSnapshot(ctx, repo, cfg)
	elapsed := time.Since(start)
	wantSnapshotEnded(t, err, "readSnapshot past its deadline")
	if elapsed > full/2 {
		t.Errorf("readSnapshot ended %v after it started with a %v deadline, want under half the %v a full walk takes",
			elapsed, deadline, full)
	}
}

// Spec: §7.3.1 (Source unreachable) — a clone that holds no commit at the
// configured ref or no directory at the configured root fails as
// ingest.source_unreachable, and a force-push walk from a commit the clone
// does not hold reports the prior reference unreachable.
func TestReadSnapshot_MissingRefRootAndCommit(t *testing.T) {
	t.Parallel()
	repo, _ := longHistory(t)
	for _, cfg := range []LayerConfig{{Ref: "no-such-branch"}, {Ref: "main", Root: "/no-such-dir"}} {
		if _, err := readSnapshot(context.Background(), repo, cfg); !errors.Is(err, ErrSourceUnreachable) {
			t.Errorf("readSnapshot(%+v) = %v, want ErrSourceUnreachable", cfg, err)
		}
	}
	if reachable, err := isAncestor(context.Background(), repo, plumbing.ZeroHash, unreachablePrior); reachable || err != nil {
		t.Errorf("isAncestor from a missing commit = %v, %v; want false, nil", reachable, err)
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

// ancestryCase is one force-push check: the head a snapshot reads, the prior
// reference the layer recorded, and whether the prior is in the head's history.
type ancestryCase struct {
	name      string
	head      plumbing.Hash
	prior     plumbing.Hash
	reachable bool
}

// checkAncestry asserts isAncestor's answer for each case and that
// readSnapshot reports a rewritten history exactly when the prior is not
// reachable.
func checkAncestry(t *testing.T, repo *git.Repository, cases []ancestryCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := isAncestor(context.Background(), repo, tc.prior, tc.head)
			if err != nil || got != tc.reachable {
				t.Errorf("isAncestor = %v, %v; want %v, nil", got, err, tc.reachable)
			}
			snap, err := readSnapshot(context.Background(), repo, LayerConfig{Ref: tc.head.String(), PriorRef: tc.prior.String()})
			if err != nil {
				t.Fatalf("readSnapshot: %v", err)
			}
			if snap.HistoryRewritten != !tc.reachable {
				t.Errorf("HistoryRewritten = %v, want %v", snap.HistoryRewritten, !tc.reachable)
			}
		})
	}
}

// Spec: §7.3.1 (Force-push detected) — a merge brings the history of every
// parent into the new head, so a prior reference reachable only through a
// merge commit's second parent is in the history and the snapshot reports no
// rewrite. A prior reference on a branch no merge brought in is reported as a
// rewrite after the walk covers both sides of the merge.
func TestIsAncestor_MergeCommits(t *testing.T) {
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
		{name: "prior behind the second parent", head: merge, prior: f1, reachable: true},
		{name: "prior is the second parent", head: merge, prior: f2, reachable: true},
		{name: "prior is the merge base", head: merge, prior: base, reachable: true},
		{name: "prior is the head", head: merge, prior: merge, reachable: true},
		{name: "prior on an unmerged branch", head: merge, prior: abandoned, reachable: false},
		{name: "no-fast-forward merge of the prior", head: noFF, prior: f1, reachable: true},
		{name: "no-fast-forward merge without the prior", head: noFF, prior: abandoned, reachable: false},
	})
}

// Spec: §7.3.1 (Force-push detected) — in a shallow or pruned clone, a commit
// can name a parent the clone does not hold. A commit that names the prior
// reference as its parent proves the prior is in the history, whether or not
// the clone holds the prior's object, and a missing parent on one side of a
// merge does not hide a prior reachable through the other side. A prior that
// only the missing history could reach cannot be shown reachable, so the
// snapshot reports a rewrite, which the strict policy refuses.
func TestIsAncestor_MissingParentCommits(t *testing.T) {
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
	repo, err := b.open(head)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	checkAncestry(t, repo, []ancestryCase{
		{name: "prior is the absent parent of the shallow boundary", head: head, prior: beyond, reachable: true},
		{name: "prior beyond the absent parent", head: head, prior: pruned, reachable: false},
		{name: "prior held in the clone below the boundary", head: head, prior: boundary, reachable: true},
		{name: "merge whose first parent is absent and whose second parent leads to the prior", head: merge, prior: prior, reachable: true},
		{name: "merge whose first parent is absent without the prior", head: merge, prior: pruned, reachable: false},
	})
}
