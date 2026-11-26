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
	st := memory.NewStorage()
	treeObj := st.NewEncodedObject()
	if err := (&object.Tree{}).Encode(treeObj); err != nil {
		return nil, plumbing.ZeroHash, err
	}
	treeHash, err := st.SetEncodedObject(treeObj)
	if err != nil {
		return nil, plumbing.ZeroHash, err
	}
	sig := object.Signature{Name: "alice", Email: "alice@acme.com", When: time.Unix(0, 0).UTC()}
	var head plumbing.Hash
	for i := 0; i < n; i++ {
		c := &object.Commit{Author: sig, Committer: sig, Message: strconv.Itoa(i), TreeHash: treeHash}
		if i > 0 {
			c.ParentHashes = []plumbing.Hash{head}
		}
		obj := st.NewEncodedObject()
		if err := c.Encode(obj); err != nil {
			return nil, plumbing.ZeroHash, err
		}
		if head, err = st.SetEncodedObject(obj); err != nil {
			return nil, plumbing.ZeroHash, err
		}
	}
	if err := st.SetReference(plumbing.NewHashReference("refs/heads/main", head)); err != nil {
		return nil, plumbing.ZeroHash, err
	}
	if err := st.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, "refs/heads/main")); err != nil {
		return nil, plumbing.ZeroHash, err
	}
	repo, err := git.Open(st, nil)
	return repo, head, err
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
